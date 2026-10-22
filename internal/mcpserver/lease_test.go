package mcpserver

import (
	"strings"
	"testing"
)

func TestLeaseTools(t *testing.T) {
	cs, _ := connect(t)
	if res, _ := call(t, cs, "cluster_ttl", map[string]any{"cluster": "lab", "minutes": 60}); !res.IsError || !strings.Contains(text(res), "does not exist") {
		t.Errorf("cluster_ttl of a missing cluster: %s", text(res))
	}
	if res, out := call(t, cs, "cluster_gc", map[string]any{"dryRun": true}); res.IsError || len(out["deleted"].([]any)) != 0 {
		t.Errorf("cluster_gc: %s", text(res))
	}
}
