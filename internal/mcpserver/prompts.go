package mcpserver

import (
	"cmp"
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// prompts are workflows that clients offer as slash commands.
func (h *handlers) prompts(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name: "test-loop", Title: "Test against a Proxmox VE cluster",
		Description: "Set up a cluster with a baseline, test the code, diagnose failures and reset between runs.",
		Arguments: []*mcp.PromptArgument{
			{Name: "task", Description: "what to test, e.g. the Terraform module in ./infra", Required: true},
			{Name: "cluster", Description: "cluster to use or create (default: one named after the task)"},
			{Name: "nodes", Description: "number of nodes (default 1; 3 for clustering and HA)"},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		a := req.Params.Arguments
		return userPrompt("Test against a Proxmox VE cluster", fmt.Sprintf(`Test this against a real Proxmox VE cluster from proxbase: %s

1. cluster_list: reuse cluster %q if it exists and is ready, else cluster_plan and cluster_create it with
   nodes=%s, snapshot="baseline" and ttlMinutes=240 (follow running=true with cluster_wait).
2. cluster_env gives the endpoint, token and CA for the code under test; a token_create token tests with
   less privilege.
3. Run the tests. Check results with pve_get, task_list and node_exec, not only the test output.
4. On a failure call cluster_diagnose and task_log before changing code.
5. Between runs call cluster_reset to return to the baseline.
6. Report what passed and failed; leave the cluster for me unless I say otherwise.`,
			a["task"], cmp.Or(a["cluster"], "test"), cmp.Or(a["nodes"], "1"))), nil
	})
	s.AddPrompt(&mcp.Prompt{
		Name: "diagnose", Title: "Diagnose a cluster",
		Description: "Find out why a cluster is not healthy, without changing it.",
		Arguments:   []*mcp.PromptArgument{{Name: "cluster", Description: "cluster name", Required: true}},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return userPrompt("Diagnose a cluster", fmt.Sprintf(`Find out why proxbase cluster %q is not healthy. Do not change anything yet.

1. cluster_status, then cluster_diagnose: quorum, failed units and tasks, service errors, clock, Ceph.
2. task_log for failed tasks; the host logs linked from cluster_diagnose for nodes that are not running.
3. Explain the cause in a few sentences and propose a fix (fault_clear, cluster_start, a config change,
   cluster_reset); wait for my go before applying it.`, req.Params.Arguments["cluster"])), nil
	})
	s.AddPrompt(&mcp.Prompt{
		Name: "ha-failover", Title: "Test HA failover",
		Description: "Check that HA restarts a guest on another node when its node loses power.",
		Arguments:   []*mcp.PromptArgument{{Name: "cluster", Description: "a 3-node cluster to use or create (default ha)"}},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return userPrompt("Test HA failover", fmt.Sprintf(`Test HA failover on proxbase cluster %q (create it with nodes=3, snapshot="baseline" and
ttlMinutes=120 if it does not exist).

1. pve_call: create a small VM (POST /nodes/pve1/qemu with vmid 100, memory 512, cores 1) and put it under HA
   (POST /cluster/ha/resources with sid vm:100, state started); wait until it runs.
2. snapshot_save live=true name=ha-ready, then fault_apply kind=kill on the node that runs it.
3. Poll pve_get /cluster/ha/status/current (every 15 seconds, not in one long call) until the VM runs on
   another node; note the times of fence, recovery and start.
4. cluster_reset to ha-ready (or cluster_start) and report the timeline.`, cmp.Or(req.Params.Arguments["cluster"], "ha"))), nil
	})
}

func userPrompt(description, text string) *mcp.GetPromptResult {
	return &mcp.GetPromptResult{
		Description: description,
		Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
	}
}
