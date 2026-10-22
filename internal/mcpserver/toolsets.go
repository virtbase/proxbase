package mcpserver

import (
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolsets groups the tools for proxbase mcp --toolsets.
var toolsets = map[string][]string{
	"cluster":  {"cluster_list", "cluster_status", "cluster_plan", "cluster_create", "cluster_wait", "cluster_start", "cluster_stop", "cluster_reset", "cluster_diagnose", "cluster_env", "cluster_ttl", "cluster_gc", "cluster_destroy"},
	"node":     {"node_exec", "file_put", "file_get", "node_add", "node_remove"},
	"api":      {"pve_get", "pve_call", "task_list", "task_log", "task_wait", "token_create", "token_list", "token_delete"},
	"snapshot": {"snapshot_save", "snapshot_restore", "snapshot_delete", "snapshot_list"},
	"fault":    {"fault_apply", "fault_clear"},
	"host":     {"image_list", "image_prune", "host_check", "config_validate"},
}

// ToolsetNames lists the toolsets.
func ToolsetNames() []string {
	names := make([]string, 0, len(toolsets))
	for name := range toolsets {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// checkToolsets validates toolset names.
func checkToolsets(names []string) error {
	for _, n := range names {
		if _, ok := toolsets[n]; !ok {
			return fmt.Errorf("unknown toolset %q; toolsets: %s", n, strings.Join(ToolsetNames(), ", "))
		}
	}
	return nil
}

// allows reports whether the server offers tool t of the options.
func (h *handlers) allows(t *mcp.Tool) bool {
	if h.opts.ReadOnly && (t.Annotations == nil || !t.Annotations.ReadOnlyHint) {
		return false
	}
	if len(h.opts.Toolsets) == 0 {
		return true
	}
	for _, set := range h.opts.Toolsets {
		if slices.Contains(toolsets[set], t.Name) {
			return true
		}
	}
	return false
}

// addTool registers a tool the options allow and notes whether it is read-only.
func addTool[In, Out any](s *mcp.Server, h *handlers, t *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	h.readOnly[t.Name] = t.Annotations != nil && t.Annotations.ReadOnlyHint
	if h.allows(t) {
		mcp.AddTool(s, t, handler)
	}
}
