//go:build e2e

package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/state"
)

// TestE2E drives a real `proxbase mcp` over stdio against a real 1-node cluster:
//
//	go build -o proxbase ./cmd/proxbase
//	PROXBASE_BIN=$PWD/proxbase go test -tags e2e -run TestE2E -timeout 60m -v ./internal/mcpserver
//
// It needs KVM, QEMU and about 5 GB of free memory, and destroys the cluster at
// the end. If PROXBASE_E2E_CLUSTER names an existing cluster, it uses that one
// and keeps it.
func TestE2E(t *testing.T) {
	bin := os.Getenv("PROXBASE_BIN")
	if bin == "" {
		t.Skip("PROXBASE_BIN not set")
	}
	name := os.Getenv("PROXBASE_E2E_CLUSTER")
	if name == "" {
		name = "mcp-e2e"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Minute)
	defer cancel()
	p := &progressLog{}
	// This client waits for calls of any length.
	cmd := exec.Command(bin, "mcp", "--call-timeout", "1h") //nolint:gosec // the binary under test, set by whoever runs the test
	cmd.Stderr = os.Stderr
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "e2e"}, &mcp.ClientOptions{ProgressNotificationHandler: p.add}).
		Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() }) // runs after the destroy registered below
	t.Logf("server %s %s", cs.InitializeResult().ServerInfo.Name, cs.InitializeResult().ServerInfo.Version)

	created := !state.ForCluster(name).Exists()
	if !created {
		t.Logf("using existing cluster %s", name)
	} else {
		t.Cleanup(func() {
			res, _ := call(t, cs, "cluster_destroy", map[string]any{"cluster": name, "confirm": name})
			t.Logf("cluster_destroy: %s", text(res))
		})
		res, out := call(t, cs, "cluster_create", map[string]any{"name": name, "snapshot": "baseline", "timeoutSeconds": 3000})
		if res.IsError {
			t.Fatalf("cluster_create: %s", text(res))
		}
		for out["running"] == true {
			res, out = call(t, cs, "cluster_wait", map[string]any{"cluster": name, "timeoutSeconds": 600})
			if res.IsError {
				t.Fatalf("cluster_wait: %s", text(res))
			}
		}
		p.mu.Lock()
		t.Logf("%d progress notifications, last: %q", len(p.msgs), p.msgs[max(0, len(p.msgs)-3):])
		p.mu.Unlock()
	}

	res, out := call(t, cs, "cluster_status", map[string]any{"cluster": name})
	if out["healthy"] != true {
		t.Fatalf("cluster_status: %s", text(res))
	}
	res, out = call(t, cs, "node_exec", map[string]any{"cluster": name, "command": "pveversion"})
	if res.IsError || out["exitCode"] != float64(0) || !strings.Contains(out["stdout"].(string), "pve-manager/") {
		t.Fatalf("node_exec pveversion: %s", text(res))
	}
	t.Logf("pveversion: %s", strings.TrimSpace(out["stdout"].(string)))
	_, out = call(t, cs, "node_exec", map[string]any{"cluster": name, "command": "echo err >&2; exit 3"})
	if out["exitCode"] != float64(3) || out["stderr"] != "err\n" {
		t.Errorf("node_exec exit code: %v", out)
	}
	res, out = call(t, cs, "cluster_env", map[string]any{"cluster": name})
	env, _ := out["env"].(map[string]any)
	if res.IsError || env["rootPassword"] != nil || !strings.HasPrefix(out["caCertPem"].(string), "-----BEGIN CERTIFICATE-----") {
		t.Errorf("cluster_env: %s", text(res))
	}

	res, out = call(t, cs, "pve_get", map[string]any{"cluster": name, "path": "/cluster/resources", "params": map[string]any{"type": "node"}})
	resources, _ := out["data"].([]any)
	if res.IsError || len(resources) == 0 {
		t.Fatalf("pve_get /cluster/resources: %s", text(res))
	}
	node, _ := resources[0].(map[string]any)["node"].(string)
	res, out = call(t, cs, "pve_call", map[string]any{"cluster": name, "method": "POST", "path": "/nodes/" + node + "/startall"})
	task, _ := out["task"].(map[string]any)
	if res.IsError || task["status"] != "stopped" || task["exitStatus"] != "OK" {
		t.Errorf("pve_call startall (a task): %s", text(res))
	}
	res, out = call(t, cs, "cluster_diagnose", map[string]any{"cluster": name})
	reports, _ := out["nodes"].([]any)
	if res.IsError || out["healthy"] != true || len(reports) == 0 || reports[0].(map[string]any)["quorum"] == nil {
		t.Errorf("cluster_diagnose: %s", text(res))
	}
	t.Logf("cluster_diagnose: %.1500s", text(res))

	// A change after a live snapshot is gone after cluster_reset.
	pools := func() string {
		_, out := call(t, cs, "pve_get", map[string]any{"cluster": name, "path": "/pools"})
		b, _ := json.Marshal(out["data"])
		return string(b)
	}
	if res, _ = call(t, cs, "snapshot_save", map[string]any{"cluster": name, "name": "e2e-live", "live": true}); res.IsError {
		t.Fatalf("snapshot_save live: %s", text(res))
	}
	if res, _ = call(t, cs, "pve_call", map[string]any{"cluster": name, "method": "POST", "path": "/pools", "params": map[string]any{"poolid": "e2e"}}); res.IsError {
		t.Errorf("pve_call POST /pools: %s", text(res))
	}
	if !strings.Contains(pools(), `"e2e"`) {
		t.Errorf("pool e2e not created: %s", pools())
	}
	t0 := time.Now()
	res, out = call(t, cs, "cluster_reset", map[string]any{"cluster": name, "snapshot": "e2e-live"})
	if res.IsError || out["healthy"] != true || out["live"] != true {
		t.Fatalf("cluster_reset: %s", text(res))
	}
	t.Logf("cluster_reset to a live snapshot: %s", time.Since(t0).Round(time.Second))
	if p := pools(); strings.Contains(p, `"e2e"`) {
		t.Errorf("pool e2e still there after the reset: %s", p)
	}
	if res, _ = call(t, cs, "snapshot_delete", map[string]any{"cluster": name, "name": "e2e-live"}); res.IsError {
		t.Errorf("snapshot_delete: %s", text(res))
	}

	for _, step := range []struct {
		tool string
		args map[string]any
	}{
		{"cluster_stop", map[string]any{"cluster": name}},
		{"snapshot_save", map[string]any{"cluster": name, "name": "clean"}},
		{"snapshot_restore", map[string]any{"cluster": name, "name": "clean"}},
		{"cluster_start", map[string]any{"cluster": name}},
	} {
		t0 := time.Now()
		res, _ := call(t, cs, step.tool, step.args)
		if res.IsError {
			t.Fatalf("%s: %s", step.tool, text(res))
		}
		t.Logf("%s: %s (%s)", step.tool, firstLine(text(res)), time.Since(t0).Round(time.Second))
	}
	res, out = call(t, cs, "snapshot_list", map[string]any{"cluster": name})
	snaps, _ := json.Marshal(out["snapshots"])
	if !strings.Contains(string(snaps), `"name":"clean"`) || (created && !strings.Contains(string(snaps), `"name":"baseline"`)) {
		t.Errorf("snapshot_list (want clean, and baseline from the create): %s", text(res))
	}
	res, out = call(t, cs, "cluster_status", map[string]any{"cluster": name})
	if out["healthy"] != true {
		t.Errorf("cluster_status after restore: %s", text(res))
	}
}

func firstLine(s string) string { return strings.SplitN(s, "\n", 2)[0] }
