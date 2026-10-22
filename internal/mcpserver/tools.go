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
	Name      string `json:"name"`
	Phase     string `json:"phase"`
	Nodes     int    `json:"nodes"`
	Running   int    `json:"running"`
	Owner     string `json:"owner,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty" jsonschema:"RFC 3339; cluster_gc deletes the cluster after it"`
	Expired   bool   `json:"expired,omitempty"`
	Job       bool   `json:"jobRunning,omitempty" jsonschema:"a create or node change runs in the background"`
}

type statusOut struct {
	Healthy bool            `json:"healthy"`
	Problem string          `json:"problem,omitempty"`
	Status  *cluster.Status `json:"status"`
	Job     *jobOut         `json:"job,omitempty" jsonschema:"the background job (create, node add or remove) and its latest progress"`
}

type messageOut struct {
	Message string `json:"message"`
}

type stopIn struct {
	Cluster        string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"time for a clean shutdown before powering off hard (default 180; nodes shut their guests down first; at most 30 for clients that cancel tool calls after a minute)"`
}

type destroyIn struct {
	Cluster string `json:"cluster" jsonschema:"cluster name"`
	Confirm string `json:"confirm" jsonschema:"must equal the cluster name; destroying deletes all disks and cannot be undone"`
}

type execIn struct {
	Cluster        string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	Node           string `json:"node,omitempty" jsonschema:"node name, e.g. pve1 (default: the first node)"`
	Command        string `json:"command" jsonschema:"shell command line, run as root through the login shell"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"kill the command after this many seconds (default 120, max 3600; at most 50 for clients that cancel tool calls after a minute)"`
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

type snapshotSaveIn struct {
	Cluster string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	Name    string `json:"name" jsonschema:"snapshot name"`
	Live    bool   `json:"live,omitempty" jsonschema:"snapshot the running cluster with its memory; restoring it resumes the cluster in seconds"`
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
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_plan", Description: planDescription, Annotations: readOnly}, h.plan)
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_create", Description: createDescription, Annotations: local(false, true)}, h.create)
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_wait", Description: "Wait for the background job of a cluster (after cluster_create, node_add or node_remove returned running=true), reporting its progress. Returns running=false with the status when it finished, or running=true again after timeoutSeconds.", Annotations: readOnly}, h.wait)
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_start", Description: "Boot a stopped cluster and wait for quorum, the API and storage. Also boots nodes killed by fault_apply kill. If the call limit (50 s for clients that cancel tool calls after a minute) passes first, it returns healthy=false with what is pending while the nodes keep booting.", Annotations: local(false, true)}, h.start)
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_stop", Description: "Shut all nodes of a cluster down (cleanly, then hard after the timeout). Disks are kept; cluster_start boots it again. Clears all faults.", Annotations: local(false, true)}, h.stop)
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_ttl", Description: ttlDescription, Annotations: local(false, true)}, h.ttl)
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_gc", Description: gcDescription, Annotations: local(true, true)}, h.gc)
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_destroy", Description: "Delete a cluster: kill its VMs and remove all its disks, snapshots and credentials. Also stops a create running in the background. Set confirm to the cluster name.", Annotations: local(true, true)}, h.destroy)
	mcp.AddTool(s, &mcp.Tool{Name: "node_exec", Description: "Run a shell command as root on a running node over SSH and return stdout, stderr and the exit code. Use it for pvesh, qm, pct, pvecm, ceph and the like. Non-interactive; output is capped at 64 KiB per stream.", Annotations: local(true, false)}, h.exec)
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_diagnose", Description: diagnoseDescription, Annotations: readOnly}, h.diagnose)
	mcp.AddTool(s, &mcp.Tool{Name: "pve_get", Description: pveGetDescription, Annotations: readOnly}, h.pveGet)
	mcp.AddTool(s, &mcp.Tool{Name: "pve_call", Description: pveCallDescription, Annotations: local(true, false)}, h.pveCall)
	mcp.AddTool(s, &mcp.Tool{Name: "task_list", Description: taskListDescription, Annotations: readOnly}, h.taskList)
	mcp.AddTool(s, &mcp.Tool{Name: "task_log", Description: taskLogDescription, Annotations: readOnly}, h.taskLog)
	mcp.AddTool(s, &mcp.Tool{Name: "task_wait", Description: taskWaitDescription, Annotations: readOnly}, h.taskWait)
	mcp.AddTool(s, &mcp.Tool{Name: "file_put", Description: filePutDescription, Annotations: local(true, true)}, h.filePut)
	mcp.AddTool(s, &mcp.Tool{Name: "file_get", Description: fileGetDescription, Annotations: readOnly}, h.fileGet)
	mcp.AddTool(s, &mcp.Tool{Name: "node_add", Description: nodeAddDescription, Annotations: local(false, false)}, h.nodeAdd)
	mcp.AddTool(s, &mcp.Tool{Name: "node_remove", Description: nodeRemoveDescription, Annotations: local(true, false)}, h.nodeRemove)
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_env", Description: "Return what API and SSH clients need: API endpoint, API token (proxbase@pve!api, full privileges), the cluster CA certificate (PEM and path), SSH user, key path and ports per node. Root password only if includeRootPassword is set.", Annotations: readOnly}, h.env)
	mcp.AddTool(s, &mcp.Tool{Name: "snapshot_save", Description: "Snapshot all disks of a stopped cluster (call cluster_stop first), or with live=true the running cluster including its memory. Restoring is much faster than creating again.", Annotations: local(false, false)}, h.snapshotSave)
	mcp.AddTool(s, &mcp.Tool{Name: "snapshot_restore", Description: "Reset all disks of a stopped cluster to a snapshot (call cluster_stop first). Changes since the snapshot are lost. After a disk snapshot call cluster_start; a live snapshot starts the cluster in its saved state by itself.", Annotations: local(true, true)}, h.snapshotOp((*cluster.Cluster).SnapshotRestore, "restored"))
	mcp.AddTool(s, &mcp.Tool{Name: "snapshot_delete", Description: "Delete a snapshot of a cluster.", Annotations: local(true, true)}, h.snapshotOp((*cluster.Cluster).SnapshotDelete, "deleted"))
	mcp.AddTool(s, &mcp.Tool{Name: "cluster_reset", Description: resetDescription, Annotations: local(true, true)}, h.reset)
	mcp.AddTool(s, &mcp.Tool{Name: "snapshot_list", Description: "List the snapshots of a cluster.", Annotations: readOnly}, h.snapshots)
	mcp.AddTool(s, &mcp.Tool{Name: "fault_apply", Description: "Inject a failure into a running cluster to test HA, fencing, Ceph or applications: kill, freeze, thaw, link-down, link-up, partition or degrade. fault_clear undoes them.", Annotations: local(true, false)}, h.fault)
	mcp.AddTool(s, &mcp.Tool{Name: "fault_clear", Description: "Undo all injected faults (thaw, plug cables in, heal partitions and degradations). Killed nodes need cluster_start.", Annotations: local(false, true)}, h.clearFaults)
	mcp.AddTool(s, &mcp.Tool{Name: "image_list", Description: "List the cached base images (golden images) with the clusters that use them.", Annotations: readOnly}, h.imageList)
	mcp.AddTool(s, &mcp.Tool{Name: "image_prune", Description: "Delete the base images no cluster uses (dryRun only lists them). The next create with a deleted image builds it again.", Annotations: local(true, true)}, h.imagePrune)
	mcp.AddTool(s, &mcp.Tool{Name: "host_check", Description: "Check whether this host can run clusters: KVM, nested virtualization, QEMU, free memory, KSM, disk space, free ports and path lengths (as proxbase doctor).", Annotations: readOnly}, h.hostCheck)
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
		r := clusterSummary{Name: name, Phase: s.Phase, Nodes: len(s.Nodes), Owner: s.Owner, ExpiresAt: s.Expires, Expired: s.Expired}
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
	out, err := startCluster(ctx, c, h.limit(req))
	return nil, out, err
}

// statusMargin is the part of the call limit kept for reading the status, which
// waits up to 10 seconds for the API.
const statusMargin = 10 * time.Second

// beforeStatus is the part of a call limit left for work that the status
// follows (0 means no limit).
func beforeStatus(limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}
	return max(limit-statusMargin, limit/2)
}

// startCluster starts c within limit (0 means no limit). When the limit passes
// first, the nodes keep booting and the result says what is still pending.
func startCluster(ctx context.Context, c *cluster.Cluster, limit time.Duration) (statusOut, error) {
	sctx := ctx
	if limit > 0 {
		// The whole call ends at the limit, the status included; the start
		// leaves room for the status.
		var cancelCall, cancelStart context.CancelFunc
		ctx, cancelCall = context.WithTimeout(ctx, limit)
		defer cancelCall()
		sctx, cancelStart = context.WithTimeout(ctx, beforeStatus(limit))
		defer cancelStart()
	}
	err := c.Start(sctx)
	pending := err != nil && sctx.Err() != nil && ctx.Err() == nil
	if err != nil && !pending {
		return statusOut{}, err
	}
	s := c.Status(ctx)
	out := statusOut{Status: s, Problem: s.Problem()}
	out.Healthy = out.Problem == ""
	if pending && !out.Healthy {
		out.Problem = fmt.Sprintf("still starting after %s (%s); the nodes keep booting, call cluster_status until healthy",
			limit.Round(time.Second), out.Problem)
	}
	return out, nil
}

// stopMargin is the part of the call limit kept for powering nodes off hard
// after the clean shutdown time.
const stopMargin = 20 * time.Second

func (h *handlers) stop(ctx context.Context, req *mcp.CallToolRequest, in stopIn) (*mcp.CallToolResult, messageOut, error) {
	c, err := h.open(in.Cluster, h.sink(ctx, req))
	if err != nil {
		return nil, messageOut{}, err
	}
	clean := seconds(in.TimeoutSeconds, 180, 1800)
	if limit := h.limit(req); limit > 0 {
		clean = min(clean, max(limit-stopMargin, 0))
	}
	if err := c.Stop(ctx, clean); err != nil {
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

func (h *handlers) exec(ctx context.Context, req *mcp.CallToolRequest, in execIn) (*mcp.CallToolResult, execOut, error) {
	c, err := h.open(in.Cluster, nil)
	if err != nil {
		return nil, execOut{}, err
	}
	node := in.Node
	if node == "" {
		node = c.St.Nodes[0].Name
	}
	requested := seconds(in.TimeoutSeconds, 120, 3600)
	timeout := within(h.limit(req), requested)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	r, err := c.Exec(ctx, node, in.Command)
	if errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("%s: command killed after %s", node, timeout)
		if timeout < requested {
			err = fmt.Errorf("%w, the longest this client waits for a tool call; run longer commands in the background "+
				"(nohup ... >/root/job.log 2>&1 &) and poll, or raise the client's tool timeout and start the server with --call-timeout", err)
		}
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

func (h *handlers) snapshotSave(ctx context.Context, req *mcp.CallToolRequest, in snapshotSaveIn) (*mcp.CallToolResult, messageOut, error) {
	save := (*cluster.Cluster).SnapshotSave
	if in.Live {
		save = (*cluster.Cluster).SnapshotSaveLive
	}
	return h.snapshotOp(save, "saved")(ctx, req, snapshotIn{Cluster: in.Cluster, Name: in.Name})
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
