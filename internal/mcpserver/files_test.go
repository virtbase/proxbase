package mcpserver

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestFileToolsCheckInput(t *testing.T) {
	cs, _ := connect(t)
	big := base64.StdEncoding.EncodeToString(make([]byte, maxFileBytes+1))
	for _, tt := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"file_put", map[string]any{"path": "/root/a", "content": "x", "contentBase64": "eA=="}, "not both"},
		{"file_put", map[string]any{"path": "/root/a", "contentBase64": "%%%"}, "contentBase64"},
		{"file_put", map[string]any{"path": "/root/a", "contentBase64": big}, "copy large files with proxbase cp"},
		{"file_put", map[string]any{"path": "/root/a", "content": "x", "mode": "4755"}, "octal permissions"},
		{"file_put", map[string]any{"cluster": "lab", "path": "/root/a", "content": "x"}, "does not exist"},
		{"file_get", map[string]any{"cluster": "lab", "path": "/root/a"}, "does not exist"},
	} {
		res, _ := call(t, cs, tt.tool, tt.args)
		if !res.IsError || !strings.Contains(text(res), tt.want) {
			t.Errorf("%s: %.200s, want an error with %q", tt.tool, text(res), tt.want)
		}
	}
}
