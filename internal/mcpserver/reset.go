package mcpserver

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/cluster"
	"github.com/virtbase/proxbase/internal/job"
)

const resetDescription = `Return a cluster to a snapshot in one call (default "baseline"): power the nodes off, restore the snapshot
and start the cluster. A live snapshot resumes with its memory in seconds; a disk snapshot boots the nodes.
Changes since the snapshot are lost. cluster_create with snapshot="baseline" saves one when the cluster is ready;
snapshot_save saves one at any time.`

type resetIn struct {
	Cluster  string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	Snapshot string `json:"snapshot,omitempty" jsonschema:"snapshot to return to (default baseline)"`
}

type resetOut struct {
	Healthy  bool            `json:"healthy"`
	Problem  string          `json:"problem,omitempty"`
	Status   *cluster.Status `json:"status"`
	Snapshot string          `json:"snapshot"`
	Live     bool            `json:"live" jsonschema:"the snapshot holds the memory, so the cluster resumed instead of booting"`
	Duration string          `json:"duration"`
}

// resumeSettle is how long a reset to a live snapshot waits for the resumed
// cluster to report healthy.
const resumeSettle = 30 * time.Second

func (h *handlers) reset(ctx context.Context, req *mcp.CallToolRequest, in resetIn) (*mcp.CallToolResult, resetOut, error) {
	name := cmp.Or(in.Snapshot, "baseline")
	j, err := job.New(clusterName(in.Cluster))
	if err != nil {
		return nil, resetOut{}, err
	}
	if j.PID() != 0 {
		return nil, resetOut{}, fmt.Errorf("a create is running for cluster %s; wait for it with cluster_wait", j.Cluster())
	}
	c, err := h.open(in.Cluster, h.sink(ctx, req))
	if err != nil {
		return nil, resetOut{}, err
	}
	snap := c.St.Snapshot(name)
	if snap == nil {
		return nil, resetOut{}, fmt.Errorf("cluster %s has no snapshot %q; snapshot_list shows its snapshots, snapshot_save saves one (live=true resumes fastest)", c.Cfg.Name, name)
	}
	live := snap.Live
	t0 := time.Now()
	// Powered off hard: the state is thrown away anyway.
	if err := c.Stop(ctx, 0); err != nil {
		return nil, resetOut{}, fmt.Errorf("power off: %w", err)
	}
	// Not cut short by the call limit: a live restore resumes all nodes together
	// and must not stop halfway; it takes seconds.
	if err := c.SnapshotRestore(ctx, name); err != nil {
		return nil, resetOut{}, err
	}
	limit := h.limit(req)
	if limit > 0 {
		limit = max(limit-time.Since(t0), time.Second)
	}
	var st statusOut
	if live {
		wctx := ctx
		if limit > 0 {
			var cancel context.CancelFunc
			wctx, cancel = context.WithTimeout(ctx, limit)
			defer cancel()
		}
		st = waitHealthy(wctx, c, within(beforeStatus(limit), resumeSettle))
	} else if st, err = startCluster(ctx, c, limit); err != nil {
		return nil, resetOut{}, err
	}
	return nil, resetOut{
		Healthy: st.Healthy, Problem: st.Problem, Status: st.Status,
		Snapshot: name, Live: live, Duration: time.Since(t0).Round(100 * time.Millisecond).String(),
	}, nil
}

// waitHealthy polls the status of c until it is healthy or d has passed.
func waitHealthy(ctx context.Context, c *cluster.Cluster, d time.Duration) statusOut {
	deadline := time.Now().Add(d)
	for {
		s := c.Status(ctx)
		out := statusOut{Status: s, Problem: s.Problem()}
		out.Healthy = out.Problem == ""
		if out.Healthy || ctx.Err() != nil || time.Now().After(deadline) {
			return out
		}
		select {
		case <-ctx.Done():
			return out
		case <-time.After(time.Second):
		}
	}
}
