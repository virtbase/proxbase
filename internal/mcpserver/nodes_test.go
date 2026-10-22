package mcpserver

import (
	"strings"
	"testing"
)

func TestNodeToolsCheckInput(t *testing.T) {
	cs, _ := connect(t)
	for _, tt := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"node_remove", map[string]any{"cluster": "lab", "node": "pve3", "confirm": "pve2"}, "set confirm to the node name"},
		{"node_remove", map[string]any{"cluster": "lab", "node": "pve3", "confirm": "pve3"}, "does not exist"},
		{"node_add", map[string]any{"cluster": "lab"}, "does not exist"},
	} {
		res, _ := call(t, cs, tt.tool, tt.args)
		if !res.IsError || !strings.Contains(text(res), tt.want) {
			t.Errorf("%s %v: %s, want an error with %q", tt.tool, tt.args, text(res), tt.want)
		}
	}
}
