package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/cluster"
	"github.com/virtbase/proxbase/internal/config"
	"github.com/virtbase/proxbase/internal/job"
	"github.com/virtbase/proxbase/internal/state"
)

type clusterIn struct {
	Cluster string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster, else \"default\""`
}

type listOut struct {
	Clusters []clusterSummary `json:"clusters"`
}

type clusterSummary struct {
	Name    string `json:"name"`
	Phase   string `json:"phase"`
	Nodes   int    `json:"nodes"`
	Running int    `json:"running"`
	Job     bool   `json:"createRunning,omitempty"`
}

type statusOut struct {
	Healthy bool            `json:"healthy"`
	Problem string          `json:"problem,omitempty"`
	Status  *cluster.Status `json:"status"`
	Job     *jobOut         `json:"create,omitempty" jsonschema:"progress of a create still running in the background"`
}

type messageOut struct {
	Message string `json:"message"`
}

type stopIn struct {
	Cluster        string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"time for a clean shutdown before powering off hard (default 180; nodes shut their guests down first)"`
}

type destroyIn struct {
	Cluster string `json:"cluster" jsonschema:"cluster name"`
	Confirm string `json:"confirm" jsonschema:"must equal the cluster name; destroying deletes all disks and cannot be undone"`
}

type execIn struct {
	Cluster        string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	Node           string `json:"node,omitempty" jsonschema:"node name, e.g. pve1 (default: the first node)"`
	Command        string `json:"command" jsonschema:"shell command line, run as root through the login shell"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"kill the command after this many seconds (default 120, max 3600)"`
}

type execOut struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  int    `json:"exitCode" jsonschema:"exit status; -1 if the command was killed"`
	Truncated bool   `json:"truncated,omitempty" jsonschema:"stdout or stderr was cut to the last 64 KiB"`
}

type envIn struct {
	Cluster             string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	IncludeRootPassword bool   `json:"includeRootPassword,omitempty" jsonschema:"also return the root password of the nodes (only if needed, e.g. for root@pam API logins)"`
}

type envOut struct {
	Env       *cluster.Env `json:"env"`
	CACertPEM string       `json:"caCertPem" jsonschema:"the cluster CA; API certificates are signed by it"`
}

type snapshotIn struct {
	Cluster string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	Name    string `json:"name" jsonschema:"snapshot name"`
}

type snapshotsOut struct {
	Snapshots []state.Snapshot `json:"snapshots"`
}

type faultIn struct {
	Cluster     string     `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	Kind        string     `json:"kind" jsonschema:"one of: kill (power the node off hard; cluster_start boots it), freeze (stop its vCPUs), thaw, link-down, link-up (the node's cable on a network), partition (split a network into groups), degrade (latency and frame loss on a network)"`
	Node        string     `json:"node,omitempty" jsonschema:"node for kill, freeze, thaw, link-down, link-up"`
	Network     string     `json:"network,omitempty" jsonschema:"network for link-*, partition and degrade (default: corosync link0)"`
	Groups      [][]string `json:"groups,omitempty" jsonschema:"partition: lists of node names; unlisted nodes form one more group"`
	DelayMs     int        `json:"delayMs,omitempty" jsonschema:"degrade: delay per frame and direction in milliseconds"`
	LossPercent float64    `json:"lossPercent,omitempty" jsonschema:"degrade: percentage of frames dropped per direction"`
}

type faultsOut struct {
	Faults  []string `json:"faults" jsonschema:"active faults"`
	Cleared []string `json:"cleared,omitempty"`
}

type validateIn struct {
	Config string `json:"config" jsonschema:"cluster file (YAML)"`
}

type validateOut struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Resolved string   `json:"resolved,omitempty" jsonschema:"the cluster file with all defaults, as create would use it"`
}

const maxOutput = 64 << 10

func (h *handlers) register(s *mcp.Server) {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)}
	local := func(destructive, idempotent bool) *mcp.ToolAnnotations {
		return &mcp.ToolAnnotations{DestructiveHint: &destructive, IdempotentHint: idempotent, OpenWorldHint: ptr(false)}
	}

	mcp.AddTool(s, &mcp.Tool{Name: "cluster_list", Description: "List all proxbase clusters on this host with phase and running nodes.", Annotations: readOnly}, h.list)
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_status", Description: "Show a cluster: phase, quorum, nodes (running/online, IP, web UI URL, SSH port), Ceph health, active faults, and the progress of a create running in the background. healthy=true means ready, quorate, all nodes online and Ceph HEALTH_OK.", Annotations: readOnly}, h.status)
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_create", Description: createDescription, Annotations: local(false, true)}, h.create)
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_wait", Description: "Wait for a create running in the background (after cluster_create returned running=true), reporting its progress. Returns running=false with the status when it finished, or running=true again after timeoutSeconds.", Annotations: readOnly}, h.wait)
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_start", Description: "Boot a stopped cluster and wait for quorum, the API and storage. Also boots nodes killed by fault_apply kill.", Annotations: local(false, true)}, h.start)
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_stop", Description: "Shut all nodes of a cluster down (cleanly, then hard after the timeout). Disks are kept; cluster_start boots it again. Clears all faults.", Annotations: local(false, true)}, h.stop)
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_destroy", Description: "Delete a cluster: kill its VMs and remove all its disks, snapshots and credentials. Also stops a create running in the background. Set confirm to the cluster name.", Annotations: local(true, true)}, h.destroy)
	mcp.AddTool(s, &mcp.Tool{Name: "node_exec", Description: "Run a shell command as root on a running node over SSH and return stdout, stderr and the exit code. Use it for pvesh, qm, pct, pvecm, ceph and the like. Non-interactive; output is capped at 64 KiB per stream.", Annotations: local(true, false)}, h.exec)
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_env", Description: "Return what API and SSH clients need: API endpoint, API token (proxbase@pve!api, full privileges), the cluster CA certificate (PEM and path), SSH user, key path and ports per node. Root password only if includeRootPassword is set.", Annotations: readOnly}, h.env)
	mcp.AddTool(s, &mcp.Tool{Name: "snapshot_save", Description: "Snapshot all disks of a stopped cluster (call cluster_stop first). Restoring is much faster than creating again.", Annotations: local(false, false)}, h.snapshotOp((*cluster.Cluster).SnapshotSave, "saved"))
	mcp.AddTool(s, &mcp.Tool{Name: "snapshot_restore", Description: "Reset all disks of a stopped cluster to a snapshot (call cluster_stop first, cluster_start afterwards). Changes since the snapshot are lost.", Annotations: local(true, true)}, h.snapshotOp((*cluster.Cluster).SnapshotRestore, "restored"))
	mcp.AddTool(s, &mcp.Tool{Name: "snapshot_delete", Description: "Delete a snapshot of a cluster.", Annotations: local(true, true)}, h.snapshotOp((*cluster.Cluster).SnapshotDelete, "deleted"))
	mcp.AddTool(s, &mcp.Tool{Name: "snapshot_list", Description: "List the snapshots of a cluster.", Annotations: readOnly}, h.snapshots)
	mcp.AddTool(s, &mcp.Tool{Name: "fault_apply", Description: "Inject a failure into a running cluster to test HA, fencing, Ceph or applications: kill, freeze, thaw, link-down, link-up, partition or degrade. fault_clear undoes them.", Annotations: local(true, false)}, h.fault)
	mcp.AddTool(s, &mcp.Tool{Name: "fault_clear", Description: "Undo all injected faults (thaw, plug cables in, heal partitions and degradations). Killed nodes need cluster_start.", Annotations: local(false, true)}, h.clearFaults)
	mcp.AddTool(s, &mcp.Tool{Name: "config_validate", Description: "Check a cluster file (YAML) without creating anything; returns errors, warnings and the file with all defaults. The schema is the resource " + SchemaURI + ".", Annotations: readOnly}, h.validate)
}

func (h *handlers) list(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listOut, error) {
	names, err := state.List()
	if err != nil {
		return nil, listOut{}, err
	}
	out := listOut{Clusters: []clusterSummary{}}
	for _, name := range names {
		c, err := cluster.Open(name, nil)
		if err != nil {
			out.Clusters = append(out.Clusters, clusterSummary{Name: name, Phase: "broken: " + err.Error()})
			continue
		}
		s := c.Status(ctx)
		r := clusterSummary{Name: name, Phase: s.Phase, Nodes: len(s.Nodes)}
		if j, err := job.New(name); err == nil {
			r.Job = j.PID() != 0
		}
		for _, n := range s.Nodes {
			if n.Running {
				r.Running++
			}
		}
		out.Clusters = append(out.Clusters, r)
	}
	return nil, out, nil
}

func (h *handlers) status(ctx context.Context, _ *mcp.CallToolRequest, in clusterIn) (*mcp.CallToolResult, statusOut, error) {
	name := clusterName(in.Cluster)
	j, err := job.New(name)
	if err != nil {
		return nil, statusOut{}, err
	}
	var out statusOut
	if j.PID() != 0 {
		js, err := summarize(j)
		if err != nil {
			return nil, out, err
		}
		out.Job = &js
	}
	c, err := cluster.Open(name, nil)
	if err != nil {
		if out.Job != nil { // create started, cluster not initialized yet
			return nil, out, nil
		}
		return nil, out, err
	}
	out.Status = c.Status(ctx)
	out.Problem = out.Status.Problem()
	out.Healthy = out.Problem == ""
	return nil, out, nil
}

func (h *handlers) start(ctx context.Context, req *mcp.CallToolRequest, in clusterIn) (*mcp.CallToolResult, statusOut, error) {
	c, err := h.open(in.Cluster, h.sink(ctx, req))
	if err != nil {
		return nil, statusOut{}, err
	}
	if err := c.Start(ctx); err != nil {
		return nil, statusOut{}, err
	}
	s := c.Status(ctx)
	return nil, statusOut{Status: s, Problem: s.Problem(), Healthy: s.Problem() == ""}, nil
}

func (h *handlers) stop(ctx context.Context, req *mcp.CallToolRequest, in stopIn) (*mcp.CallToolResult, messageOut, error) {
	c, err := h.open(in.Cluster, h.sink(ctx, req))
	if err != nil {
		return nil, messageOut{}, err
	}
	if err := c.Stop(ctx, seconds(in.TimeoutSeconds, 180, 1800)); err != nil {
		return nil, messageOut{}, err
	}
	return nil, messageOut{Message: "cluster " + c.Cfg.Name + " stopped"}, nil
}

func (h *handlers) destroy(ctx context.Context, req *mcp.CallToolRequest, in destroyIn) (*mcp.CallToolResult, messageOut, error) {
	if in.Cluster == "" || in.Confirm != in.Cluster {
		return nil, messageOut{}, errors.New("set confirm to the cluster name to destroy it")
	}
	j, err := job.New(in.Cluster)
	if err != nil {
		return nil, messageOut{}, err
	}
	if err := stopJob(ctx, j); err != nil {
		return nil, messageOut{}, err
	}
	if _, err := os.Stat(string(state.ForCluster(in.Cluster))); err == nil {
		if err := cluster.Destroy(in.Cluster, h.sink(ctx, req)); err != nil {
			return nil, messageOut{}, err
		}
	} else if _, err := os.Stat(j.Log()); err != nil {
		return nil, messageOut{}, fmt.Errorf("cluster %q does not exist", in.Cluster)
	}
	j.Remove()
	return nil, messageOut{Message: "cluster " + in.Cluster + " destroyed"}, nil
}

func (h *handlers) exec(ctx context.Context, _ *mcp.CallToolRequest, in execIn) (*mcp.CallToolResult, execOut, error) {
	c, err := h.open(in.Cluster, nil)
	if err != nil {
		return nil, execOut{}, err
	}
	node := in.Node
	if node == "" {
		node = c.St.Nodes[0].Name
	}
	timeout := seconds(in.TimeoutSeconds, 120, 3600)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	r, err := c.Exec(ctx, node, in.Command)
	if errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("%s: command killed after %s", node, timeout)
	}
	if err != nil {
		return nil, execOut{}, err
	}
	out := execOut{ExitCode: r.ExitCode}
	out.Stdout, out.Truncated = tail(r.Stdout)
	var cut bool
	out.Stderr, cut = tail(r.Stderr)
	out.Truncated = out.Truncated || cut
	return nil, out, nil
}

// tail keeps the last maxOutput bytes.
func tail(s string) (string, bool) {
	if len(s) <= maxOutput {
		return s, false
	}
	return s[len(s)-maxOutput:], true
}

func (h *handlers) env(_ context.Context, _ *mcp.CallToolRequest, in envIn) (*mcp.CallToolResult, envOut, error) {
	c, err := h.open(in.Cluster, nil)
	if err != nil {
		return nil, envOut{}, err
	}
	e, err := c.Env()
	if err != nil {
		return nil, envOut{}, err
	}
	if in.IncludeRootPassword {
		e.RootPassword = c.Password()
	}
	ca, err := os.ReadFile(c.CAPath())
	if err != nil {
		return nil, envOut{}, err
	}
	return nil, envOut{Env: e, CACertPEM: string(ca)}, nil
}

func (h *handlers) snapshotOp(f func(*cluster.Cluster, context.Context, string) error, done string) mcp.ToolHandlerFor[snapshotIn, messageOut] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in snapshotIn) (*mcp.CallToolResult, messageOut, error) {
		c, err := h.open(in.Cluster, h.sink(ctx, req))
		if err != nil {
			return nil, messageOut{}, err
		}
		t0 := time.Now()
		if err := f(c, ctx, in.Name); err != nil {
			return nil, messageOut{}, err
		}
		return nil, messageOut{Message: fmt.Sprintf("snapshot %s %s in %s", in.Name, done, time.Since(t0).Round(time.Millisecond))}, nil
	}
}

func (h *handlers) snapshots(_ context.Context, _ *mcp.CallToolRequest, in clusterIn) (*mcp.CallToolResult, snapshotsOut, error) {
	c, err := h.open(in.Cluster, nil)
	if err != nil {
		return nil, snapshotsOut{}, err
	}
	return nil, snapshotsOut{Snapshots: append([]state.Snapshot{}, c.St.Snapshots...)}, nil
}

func (h *handlers) fault(ctx context.Context, req *mcp.CallToolRequest, in faultIn) (*mcp.CallToolResult, faultsOut, error) {
	c, err := h.open(in.Cluster, h.sink(ctx, req))
	if err != nil {
		return nil, faultsOut{}, err
	}
	needNode := slices.Contains([]string{"kill", "freeze", "thaw", "link-down", "link-up"}, in.Kind)
	if needNode && in.Node == "" {
		return nil, faultsOut{}, fmt.Errorf("fault %s needs node", in.Kind)
	}
	switch in.Kind {
	case "kill":
		err = c.KillNode(in.Node)
	case "freeze":
		err = c.FreezeNode(ctx, in.Node)
	case "thaw":
		err = c.ThawNode(ctx, in.Node)
	case "link-down", "link-up":
		err = c.SetLink(ctx, in.Node, in.Network, in.Kind == "link-up")
	case "partition":
		if len(in.Groups) == 0 {
			return nil, faultsOut{}, errors.New("partition needs groups")
		}
		err = c.Partition(in.Network, in.Groups)
	case "degrade":
		err = c.Degrade(in.Network, time.Duration(in.DelayMs)*time.Millisecond, in.LossPercent/100)
	default:
		return nil, faultsOut{}, fmt.Errorf("unknown fault kind %q", in.Kind)
	}
	if err != nil {
		return nil, faultsOut{}, err
	}
	return nil, faultsOut{Faults: nonNil(c.Faults())}, nil
}

func (h *handlers) clearFaults(ctx context.Context, req *mcp.CallToolRequest, in clusterIn) (*mcp.CallToolResult, faultsOut, error) {
	c, err := h.open(in.Cluster, h.sink(ctx, req))
	if err != nil {
		return nil, faultsOut{}, err
	}
	cleared, err := c.ClearFaults(ctx)
	if err != nil {
		return nil, faultsOut{}, err
	}
	return nil, faultsOut{Faults: nonNil(c.Faults()), Cleared: cleared}, nil
}

func (h *handlers) validate(_ context.Context, _ *mcp.CallToolRequest, in validateIn) (*mcp.CallToolResult, validateOut, error) {
	cfg, err := config.Decode(strings.NewReader(in.Config))
	if err == nil {
		err = config.Overrides{}.Apply(cfg)
	}
	if err != nil {
		var errs []string
		for _, line := range strings.Split(err.Error(), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "invalid cluster configuration") {
				errs = append(errs, line)
			}
		}
		return nil, validateOut{Errors: errs}, nil
	}
	b, err := cfg.YAML()
	if err != nil {
		return nil, validateOut{}, err
	}
	return nil, validateOut{Valid: true, Warnings: cfg.Warnings(), Resolved: string(b)}, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
