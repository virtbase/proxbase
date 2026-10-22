package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/job"
	"github.com/virtbase/proxbase/internal/state"
)

const (
	nodeAddDescription = `Add nodes to a ready cluster: install (or clone) them, join them and set up ZFS or Ceph as configured.
Runs in the background like cluster_create; returns when done or with running=true (then call cluster_wait).
Cancelling the call stops it; cluster_create with the cluster's name finishes an interrupted add.`
	nodeRemoveDescription = `Remove a node from a cluster: drain and destroy its Ceph daemons, take it out of the cluster and delete
its VM and disks. Refuses with guests on the node or Ceph pools that would be left degraded unless force is set.
Runs in the background like cluster_create. Set confirm to the node name.`
)

type nodeAddIn struct {
	Cluster        string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	Count          int    `json:"count,omitempty" jsonschema:"nodes to add (default 1); numbers of removed nodes are reused first"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"return after this many seconds while the job keeps running (default 600, max 3600; at most 50 for clients that cancel tool calls after a minute)"`
}

type nodeRemoveIn struct {
	Cluster        string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	Node           string `json:"node" jsonschema:"node to remove, e.g. pve3"`
	Confirm        string `json:"confirm,omitempty" jsonschema:"must equal the node name (removing deletes the node's disks); without it, clients that support elicitation ask the user"`
	Force          bool   `json:"force,omitempty" jsonschema:"remove even with guests on the node or Ceph pools left degraded"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty" jsonschema:"return after this many seconds while the job keeps running (default 600, max 3600; at most 50 for clients that cancel tool calls after a minute)"`
}

func (h *handlers) nodeAdd(ctx context.Context, req *mcp.CallToolRequest, in nodeAddIn) (*mcp.CallToolResult, jobOut, error) {
	j, err := h.readyClusterJob(in.Cluster)
	if err != nil {
		return nil, jobOut{}, err
	}
	out, err := h.runJob(ctx, req, j, []string{"node", "add", j.Cluster(), "--count", strconv.Itoa(max(in.Count, 1))}, in.TimeoutSeconds)
	return nil, out, err
}

func (h *handlers) nodeRemove(ctx context.Context, req *mcp.CallToolRequest, in nodeRemoveIn) (*mcp.CallToolResult, jobOut, error) {
	if in.Node == "" {
		return nil, jobOut{}, errors.New("set node, and confirm to the node name, to remove it")
	}
	if in.Confirm != in.Node {
		ok, ask := askUser(ctx, req, fmt.Sprintf("Remove node %s from cluster %s? Its VM and disks are deleted.", in.Node, clusterName(in.Cluster)))
		if ask != nil {
			return ask, jobOut{}, nil
		}
		if !ok {
			return nil, jobOut{}, errors.New("set confirm to the node name to remove it")
		}
	}
	j, err := h.readyClusterJob(in.Cluster)
	if err != nil {
		return nil, jobOut{}, err
	}
	st, err := state.ForCluster(j.Cluster()).Load()
	if err != nil {
		return nil, jobOut{}, err
	}
	if st.Node(in.Node) == nil {
		return nil, jobOut{}, fmt.Errorf("cluster %s has no node %q", j.Cluster(), in.Node)
	}
	args := []string{"node", "remove", j.Cluster(), in.Node}
	if in.Force {
		args = append(args, "--force")
	}
	out, err := h.runJob(ctx, req, j, args, in.TimeoutSeconds)
	return nil, out, err
}

// readyClusterJob returns the job slot of a ready cluster without a running job.
func (h *handlers) readyClusterJob(name string) (job.Job, error) {
	c, err := h.open(name, nil)
	if err != nil {
		return job.Job{}, err
	}
	j, err := job.New(c.Cfg.Name)
	if err != nil {
		return job.Job{}, err
	}
	if j.PID() != 0 {
		return job.Job{}, fmt.Errorf("%q is running for cluster %s; follow it with cluster_wait", j.Command(), c.Cfg.Name)
	}
	if c.St.Phase != state.PhaseReady {
		return job.Job{}, fmt.Errorf("cluster %s is %s; finish it with cluster_create first", c.Cfg.Name, c.St.Phase)
	}
	return j, nil
}
