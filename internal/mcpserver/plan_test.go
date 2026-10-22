package mcpserver

import (
	"strings"
	"testing"

	"github.com/virtbase/proxbase/internal/state"
)

func TestPlan(t *testing.T) {
	cs, _ := connect(t)
	res, out := call(t, cs, "cluster_plan", map[string]any{"name": "lab", "nodes": 3, "memory": "2G"})
	plan, _ := out["plan"].(map[string]any)
	nodes, _ := plan["nodes"].([]any)
	if res.IsError || len(nodes) != 3 || plan["memoryMiB"] != float64(3*2048) || !strings.Contains(out["resolved"].(string), "portBase:") {
		t.Fatalf("cluster_plan: %s", text(res))
	}
	if state.ForCluster("lab").Exists() {
		t.Error("cluster_plan wrote the cluster")
	}

	d := state.ForCluster("lab")
	if err := d.Init(); err != nil {
		t.Fatal(err)
	}
	if err := d.Save(&state.State{Name: "lab", Phase: state.PhaseReady}); err != nil {
		t.Fatal(err)
	}
	if res, _ = call(t, cs, "cluster_plan", map[string]any{"name": "lab"}); !res.IsError || !strings.Contains(text(res), "exists") {
		t.Errorf("plan of an existing cluster: %s", text(res))
	}
	if res, _ = call(t, cs, "cluster_plan", map[string]any{"name": "x", "storage": "nfs"}); !res.IsError {
		t.Errorf("plan with a bad storage: %s", text(res))
	}
}
