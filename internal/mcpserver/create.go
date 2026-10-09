package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/cluster"
	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/job"
	"github.com/virtbase/proxbase/internal/progress"
	"github.com/virtbase/proxbase/internal/state"
)

const createDescription = `Create a Proxmox VE cluster (or resume a failed create of the same name).
Without config, it creates 1 node with 4 vCPUs, 4G memory, a 32G ZFS root disk and one 32G data disk
holding the ZFS pool "tank". Set nodes: 3 for a real cluster, storage: ceph for Ceph (3+ nodes).
The create runs in the background on this host and survives this connection; the tool reports
progress and returns when the cluster is ready, or with running=true after timeoutSeconds
(then call cluster_wait). Cancelling the call cancels the create (re-run to resume).
Needs free memory for all nodes; the first create downloads a ~1.5 GB ISO.`

type createIn struct {
	Name           string `json:"name" jsonschema:"cluster name: lowercase letters, digits and dashes, starting with a letter, at most 15 characters"`
	Config         string `json:"config,omitempty" jsonschema:"cluster file (YAML, apiVersion proxbase.virtbase.com/v1alpha1, kind Cluster); the other parameters override it"`
	Nodes          int    `json:"nodes,omitempty" jsonschema:"number of nodes (default 1)"`
	CPUs           int    `json:"cpus,omitempty" jsonschema:"vCPUs per node (default 4)"`
	Memory         string `json:"memory,omitempty" jsonschema:"memory per node, e.g. 4G (default 4G, 6G with Ceph)"`
	RootDisk       string `json:"rootDisk,omitempty" jsonschema:"root disk size, e.g. 32G"`
	DataDisks      string `json:"dataDisks,omitempty" jsonschema:"data disks per node: 2x32G, 32G,64G or none"`
	Storage        string `json:"storage,omitempty" jsonschema:"storage on the data disks: zfs, ceph or none"`
	PVEVersion     string `json:"pveVersion,omitempty" jsonschema:"Proxmox VE ISO version, e.g. 9.2 or 9.2-1 (default: newest supported)"`
	Golden         *bool  `json:"golden,omitempty" jsonschema:"clone nodes from a cached installed base image instead of installing each (default true; the first create builds the image)"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"return after this many seconds while the create keeps running (default 600, max 3600)"`
}

type waitIn struct {
	Cluster        string `json:"cluster" jsonschema:"cluster name"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"return after this many seconds if the create is still running (default 600, max 3600)"`
}

// jobOut is the state of a create in the background.
type jobOut struct {
	Cluster string          `json:"cluster"`
	Running bool            `json:"running" jsonschema:"the create is still running; call cluster_wait"`
	Error   string          `json:"error,omitempty"`
	Hint    string          `json:"hint,omitempty"`
	Log     string          `json:"log" jsonschema:"progress log (JSON lines); node install logs are in the cluster's logs directory"`
	Steps   []string        `json:"steps" jsonschema:"latest progress messages"`
	Status  *cluster.Status `json:"status,omitempty"`
}

// stepsShown is how many progress messages results carry.
const stepsShown = 10

// resolve builds the cluster file from the tool parameters.
func (in createIn) resolve() (*config.Cluster, error) {
	cfg := &config.Cluster{}
	if in.Config != "" {
		var err error
		if cfg, err = config.Decode(strings.NewReader(in.Config)); err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
	}
	golden := in.Golden == nil || *in.Golden
	cfg.Proxmox.Golden = cfg.Proxmox.Golden && golden
	ov := config.Overrides{Name: in.Name, Golden: golden}
	if in.Nodes != 0 {
		ov.Nodes = &in.Nodes
	}
	if in.CPUs != 0 {
		ov.CPUs = &in.CPUs
	}
	for _, f := range []struct {
		dst **string
		v   *string
	}{{&ov.Memory, &in.Memory}, {&ov.Disk, &in.RootDisk}, {&ov.DataDisks, &in.DataDisks}, {&ov.Storage, &in.Storage}, {&ov.Version, &in.PVEVersion}} {
		if *f.v != "" {
			*f.dst = f.v
		}
	}
	return cfg, ov.Apply(cfg)
}

func (h *handlers) create(ctx context.Context, req *mcp.CallToolRequest, in createIn) (*mcp.CallToolResult, jobOut, error) {
	cfg, err := in.resolve()
	if err != nil {
		return nil, jobOut{}, err
	}
	if d := state.ForCluster(cfg.Name); d.Exists() {
		if st, err := d.Load(); err == nil && st.Phase == state.PhaseReady {
			return nil, jobOut{}, fmt.Errorf("cluster %q already exists and is ready; use it, or cluster_destroy it first", cfg.Name)
		}
	}
	b, err := cfg.YAML()
	if err != nil {
		return nil, jobOut{}, err
	}
	j, err := job.New(cfg.Name)
	if err != nil {
		return nil, jobOut{}, err
	}
	file, err := j.WriteInput(b)
	if err != nil {
		return nil, jobOut{}, err
	}
	exited, err := j.Start("create", "-f", file)
	if err != nil {
		return nil, jobOut{}, err
	}
	out, err := h.follow(ctx, req, j, exited, seconds(in.TimeoutSeconds, 600, 3600))
	if ctx.Err() != nil {
		// The create saves its state and exits; calling cluster_create again resumes it.
		if serr := j.Signal(syscall.SIGTERM); serr != nil {
			h.log.Logf("cancel create of %s: %v", cfg.Name, serr)
		}
	}
	return nil, out, err
}

func (h *handlers) wait(ctx context.Context, req *mcp.CallToolRequest, in waitIn) (*mcp.CallToolResult, jobOut, error) {
	j, err := job.New(in.Cluster)
	if err != nil {
		return nil, jobOut{}, err
	}
	if _, err := os.Stat(j.Log()); err != nil {
		return nil, jobOut{}, fmt.Errorf("no create was started for cluster %q from MCP; see cluster_status", in.Cluster)
	}
	out, err := h.follow(ctx, req, j, nil, seconds(in.TimeoutSeconds, 600, 3600))
	return nil, out, err
}

// follow forwards the job's progress until it ends or timeout passes. exited,
// if not nil, is closed when the job (a child of this process) ends.
func (h *handlers) follow(ctx context.Context, req *mcp.CallToolRequest, j job.Job, exited <-chan struct{}, timeout time.Duration) (jobOut, error) {
	p := h.sink(ctx, req)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var offset int64
	var last progress.Event
	forward := func() error {
		events, next, err := j.Events(offset)
		if err != nil {
			return fmt.Errorf("read progress of %s: %w", j.Cluster(), err)
		}
		offset = next
		for _, e := range events {
			p(e)
			last = e
		}
		return nil
	}
	for exited != nil || j.PID() != 0 {
		if err := forward(); err != nil {
			return jobOut{}, err
		}
		select {
		case <-exited:
			exited = nil
		case <-tick.C:
		case <-ctx.Done():
			return jobOut{}, ctx.Err()
		case <-deadline.C:
			out, err := summarize(j)
			out.Running = true
			return out, err
		}
	}
	if err := forward(); err != nil {
		return jobOut{}, err
	}
	out, err := summarize(j)
	if err != nil {
		return out, err
	}
	switch last.Type {
	case progress.TypeError:
		out.Error, out.Hint = last.Error, last.Hint
	case progress.TypeDone:
	default:
		out.Error = "the create ended without a result; see the log"
	}
	if c, err := cluster.Open(j.Cluster(), nil); err == nil {
		out.Status = c.Status(ctx)
	} else if out.Error == "" {
		return out, err
	}
	if out.Error != "" {
		return out, errors.New(out.Error + toolHint(out.Hint))
	}
	return out, nil
}

// toolHint rewrites a CLI hint in terms of the tools.
func toolHint(hint string) string {
	if hint == "" {
		return ""
	}
	r := strings.NewReplacer("`proxbase create ", "`cluster_create name=", "`proxbase destroy ", "`cluster_destroy cluster=")
	return "\n" + r.Replace(hint)
}

// summarize returns the latest progress messages of a job.
func summarize(j job.Job) (jobOut, error) {
	out := jobOut{Cluster: j.Cluster(), Log: j.Log(), Running: j.PID() != 0, Steps: []string{}}
	events, _, err := j.Events(0)
	if err != nil {
		return out, fmt.Errorf("read progress of %s: %w", j.Cluster(), err)
	}
	for _, e := range events {
		switch {
		case e.Type == progress.TypeStep:
			out.Steps = append(out.Steps, fmt.Sprintf("[%4.0fs] %s", e.Elapsed, e.Message))
		case e.Type == progress.TypeLog && e.Message != "":
			out.Steps = append(out.Steps, e.Message)
		}
	}
	out.Steps = out.Steps[max(0, len(out.Steps)-stepsShown):]
	return out, nil
}

// stopJob ends a running create: SIGTERM, then SIGKILL after 30 seconds.
func stopJob(ctx context.Context, j job.Job) error {
	if j.PID() == 0 {
		return nil
	}
	if err := j.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop create of %s: %w", j.Cluster(), err)
	}
	deadline := time.Now().Add(30 * time.Second)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	killed := false
	for j.PID() != 0 {
		if !killed && time.Now().After(deadline) {
			if err := j.Signal(syscall.SIGKILL); err != nil {
				return fmt.Errorf("kill create of %s: %w", j.Cluster(), err)
			}
			killed = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
	return nil
}
