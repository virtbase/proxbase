package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/cluster"
	"github.com/virtbase/proxbase/internal/state"
)

const planDescription = `Work out what cluster_create would set up, without creating anything: nodes with IPs and ports (a new
cluster takes the first free port range), the memory and disk of all nodes, whether the ISO is cached, the host's free
memory and disk, warnings, and the resolved cluster file. Takes the parameters of cluster_create.`

type planOut struct {
	Plan     *cluster.Plan `json:"plan"`
	Resolved string        `json:"resolved" jsonschema:"the cluster file with all defaults and the port base, as cluster_create would use it"`
}

func (h *handlers) plan(_ context.Context, _ *mcp.CallToolRequest, in createIn) (*mcp.CallToolResult, planOut, error) {
	cfg, err := in.resolve()
	if err != nil {
		return nil, planOut{}, err
	}
	if state.ForCluster(cfg.Name).Exists() {
		return nil, planOut{}, fmt.Errorf("cluster %q exists; cluster_status shows it", cfg.Name)
	}
	p, err := cluster.NewPlan(cfg)
	if err != nil {
		return nil, planOut{}, err
	}
	b, err := cfg.YAML()
	if err != nil {
		return nil, planOut{}, err
	}
	return nil, planOut{Plan: p, Resolved: string(b)}, nil
}
