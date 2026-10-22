package mcpserver

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/cluster"
	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/job"
	"github.com/virtbase/proxbase/internal/state"
)

const createDescription = `Create a Proxmox VE cluster (or resume a failed create of the same name).
Without config, it creates 1 node with 4 vCPUs, 4G memory, a 32G ZFS root disk and one 32G data disk
holding the ZFS pool "tank". Set nodes: 3 for a real cluster, storage: ceph for Ceph (3+ nodes).
The create runs in the background on this host and survives this connection; the tool reports
progress and returns when the cluster is ready, or with running=true after timeoutSeconds
(then call cluster_wait). Cancelling the call cancels the create (re-run to resume).
With snapshot="baseline" it saves a live snapshot when ready, which cluster_reset returns to in seconds.
templates in config provide VM templates from cloud images and container templates as fixtures.
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
	Snapshot       string `json:"snapshot,omitempty" jsonschema:"save a live snapshot with this name when the cluster is ready, e.g. baseline; cluster_reset returns to it in seconds"`
	Owner          string `json:"owner,omitempty" jsonschema:"who owns the cluster, e.g. the agent and its task (default: the client's name)"`
	TTLMinutes     int    `json:"ttlMinutes,omitempty" jsonschema:"lifetime: cluster_gc deletes the cluster this many minutes after the create (default: no expiry)"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"return after this many seconds while the create keeps running (default 600, max 3600; at most 50 for clients that cancel tool calls after a minute)"`
}

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
	args := []string{"create", "--owner", cmp.Or(in.Owner, clientName(req), "mcp")}
	if in.TTLMinutes > 0 {
		args = append(args, "--ttl", (time.Duration(in.TTLMinutes) * time.Minute).String())
	}
	if in.Snapshot != "" {
		if err := cluster.CheckSnapshotName(in.Snapshot); err != nil {
			return nil, jobOut{}, err
		}
		args = append(args, "--snapshot", in.Snapshot)
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
	out, err := h.runJob(ctx, req, j, append(args, "-f", file), in.TimeoutSeconds)
	return nil, out, err
}
