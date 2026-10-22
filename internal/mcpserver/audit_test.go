package mcpserver

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	cs, _ := connectAs(t, "codex-mcp-client", Options{AuditLog: path})
	call(t, cs, "cluster_list", nil) // read-only: not logged
	call(t, cs, "cluster_destroy", map[string]any{"cluster": "lab", "confirm": "lab"})
	call(t, cs, "file_put", map[string]any{"cluster": "lab", "path": "/root/a", "content": strings.Repeat("x", 1000)})

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if fi, _ := f.Stat(); fi.Mode().Perm() != 0o600 {
		t.Errorf("audit log mode %o, want 600", fi.Mode().Perm())
	}
	var entries []auditEntry
	for sc := bufio.NewScanner(f); sc.Scan(); {
		var e auditEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		entries = append(entries, e)
	}
	if len(entries) != 2 {
		t.Fatalf("entries %+v, want destroy and file_put", entries)
	}
	d := entries[0]
	if d.Tool != "cluster_destroy" || d.Client != "codex-mcp-client" || d.Args["confirm"] != "lab" || !strings.Contains(d.Error, "does not exist") {
		t.Errorf("destroy entry %+v", d)
	}
	if c, _ := entries[1].Args["content"].(string); len(c) > maxAuditString+30 || !strings.HasSuffix(c, "(1000 bytes)") {
		t.Errorf("file content not cut: %q", c)
	}
}
