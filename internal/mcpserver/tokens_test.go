package mcpserver

import (
	"strings"
	"testing"
)

func TestTokenToolsCheckInput(t *testing.T) {
	cs, _ := connect(t)
	for _, tt := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"token_create", map[string]any{"cluster": "lab", "name": "ro", "role": "PVEAuditor"}, "does not exist"},
		{"token_delete", map[string]any{"cluster": "lab", "name": "ro"}, "does not exist"},
		{"token_list", map[string]any{"cluster": "lab"}, "does not exist"},
		{"cluster_env", map[string]any{"cluster": "lab", "token": "ro"}, "does not exist"},
	} {
		res, _ := call(t, cs, tt.tool, tt.args)
		if !res.IsError || !strings.Contains(text(res), tt.want) {
			t.Errorf("%s %v: %s, want an error with %q", tt.tool, tt.args, text(res), tt.want)
		}
	}
}
