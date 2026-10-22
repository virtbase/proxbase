package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/cluster"
)

const (
	ttlDescription = `Set how long a cluster may live: it expires minutes from now (0: never). cluster_gc deletes expired
clusters; set a lifetime on clusters you create so that forgotten ones are cleaned up.`
	gcDescription = `Delete every expired cluster on this host (see ttlMinutes of cluster_create and cluster_ttl), with its
disks and snapshots; clusters with a running job are left. dryRun only lists them.`
)

type ttlIn struct {
	Cluster string `json:"cluster,omitempty" jsonschema:"cluster name; default: the only cluster"`
	Minutes int    `json:"minutes" jsonschema:"the cluster expires this many minutes from now; 0: never"`
}

type ttlOut struct {
	Cluster   string `json:"cluster"`
	ExpiresAt string `json:"expiresAt,omitempty" jsonschema:"RFC 3339; empty: never"`
}

type gcIn struct {
	DryRun bool `json:"dryRun,omitempty" jsonschema:"only list the expired clusters"`
}

func (h *handlers) ttl(_ context.Context, _ *mcp.CallToolRequest, in ttlIn) (*mcp.CallToolResult, ttlOut, error) {
	c, err := h.open(in.Cluster, nil)
	if err != nil {
		return nil, ttlOut{}, err
	}
	if err := c.SetExpiry(time.Duration(max(in.Minutes, 0)) * time.Minute); err != nil {
		return nil, ttlOut{}, err
	}
	out := ttlOut{Cluster: c.Cfg.Name}
	if c.St.ExpiresAt != nil {
		out.ExpiresAt = c.St.ExpiresAt.Format(time.RFC3339)
	}
	return nil, out, nil
}

func (h *handlers) gc(ctx context.Context, req *mcp.CallToolRequest, in gcIn) (*mcp.CallToolResult, cluster.GCResult, error) {
	res, err := cluster.GC(time.Now(), in.DryRun, h.sink(ctx, req))
	return nil, res, err
}
