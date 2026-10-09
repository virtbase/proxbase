//go:build e2e

package mcpserver

import (
	"context"
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
	cmd := exec.Command(bin, "mcp") //nolint:gosec // the binary under test, set by whoever runs the test
	cmd.Stderr = os.Stderr
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "e2e"}, &mcp.ClientOptions{ProgressNotificationHandler: p.add}).
		Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() }) // runs after the destroy registered below
	t.Logf("server %s %s", cs.InitializeResult().ServerInfo.Name, cs.InitializeResult().ServerInfo.Version)

	if state.ForCluster(name).Exists() {
		t.Logf("using existing cluster %s", name)
	} else {
		t.Cleanup(func() {
			res, _ := call(t, cs, "cluster_destroy", map[string]any{"cluster": name, "confirm": name})
			t.Logf("cluster_destroy: %s", text(res))
		})
		res, out := call(t, cs, "cluster_create", map[string]any{"name": name, "timeoutSeconds": 3000})
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
	if snaps, _ := out["snapshots"].([]any); len(snaps) != 1 {
		t.Errorf("snapshot_list: %s", text(res))
	}
	res, out = call(t, cs, "cluster_status", map[string]any{"cluster": name})
	if out["healthy"] != true {
		t.Errorf("cluster_status after restore: %s", text(res))
	}
}

func firstLine(s string) string { return strings.SplitN(s, "\n", 2)[0] }
