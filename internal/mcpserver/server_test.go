package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The test binary stands in for proxbase when a tool starts a job: it prints
// the progress events of a create that fails.
func TestMain(m *testing.M) {
	if os.Getenv("PROXBASE_FAKE_JOB") == "1" {
		fmt.Fprintln(os.Stderr, `{"type":"step","cluster":"lab","message":"cluster lab: 1 node(s)","elapsed":0.1}`)
		fmt.Fprintln(os.Stderr, `{"type":"step","cluster":"lab","node":"pve1","message":"pve1 installed in 1s","elapsed":1.2}`)
		fmt.Fprintln(os.Stderr, `{"type":"error","error":"boom","hint":"Re-run `+"`proxbase create lab`"+` to resume"}`)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

type progressLog struct {
	mu   sync.Mutex
	msgs []string
}

func (p *progressLog) add(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.msgs = append(p.msgs, req.Params.Message)
}

func connect(t *testing.T) (*mcp.ClientSession, *progressLog) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := New("test", nil).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	p := &progressLog{}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, &mcp.ClientOptions{ProgressNotificationHandler: p.add}).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, p
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, map[string]any) {
	t.Helper()
	params := &mcp.CallToolParams{Name: name, Arguments: args}
	params.SetProgressToken("t1")
	res, err := cs.CallTool(context.Background(), params)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var out map[string]any
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &out)
	}
	return res, out
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

func TestTools(t *testing.T) {
	cs, _ := connect(t)
	var names []string
	for tool, err := range cs.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
		names = append(names, tool.Name)
	}
	for _, want := range []string{"cluster_list", "cluster_status", "cluster_create", "cluster_wait", "cluster_start", "cluster_stop",
		"cluster_destroy", "node_exec", "cluster_env", "snapshot_save", "snapshot_restore", "snapshot_list", "fault_apply", "fault_clear", "config_validate"} {
		if !slices.Contains(names, want) {
			t.Errorf("tool %s missing (have %v)", want, names)
		}
	}
}

func TestSchemaResource(t *testing.T) {
	cs, _ := connect(t)
	res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: SchemaURI})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Contents[0].Text, `"$id": "`+SchemaURI+`"`) {
		t.Errorf("schema resource does not carry its $id:\n%.300s", res.Contents[0].Text)
	}
}

func TestValidate(t *testing.T) {
	cs, _ := connect(t)
	_, out := call(t, cs, "config_validate", map[string]any{"config": "name: lab\nnodes:\n  count: 3\n"})
	if out["valid"] != true || !strings.Contains(out["resolved"].(string), "count: 3") {
		t.Errorf("valid file: %v", out)
	}
	_, out = call(t, cs, "config_validate", map[string]any{"config": "name: Lab_1\n"})
	if out["valid"] != false || len(out["errors"].([]any)) == 0 {
		t.Errorf("invalid name accepted: %v", out)
	}
	_, out = call(t, cs, "config_validate", map[string]any{"config": "name: lab\nnodez: 3\n"})
	if out["valid"] != false {
		t.Errorf("unknown field accepted: %v", out)
	}
}

func TestEmptyHost(t *testing.T) {
	cs, _ := connect(t)
	_, out := call(t, cs, "cluster_list", nil)
	if c := out["clusters"].([]any); len(c) != 0 {
		t.Errorf("clusters: %v", c)
	}
	res, _ := call(t, cs, "cluster_status", map[string]any{"cluster": "lab"})
	if !res.IsError || !strings.Contains(text(res), "does not exist") {
		t.Errorf("status of a missing cluster: %s", text(res))
	}
	res, _ = call(t, cs, "cluster_destroy", map[string]any{"cluster": "lab", "confirm": "other"})
	if !res.IsError || !strings.Contains(text(res), "confirm") {
		t.Errorf("destroy without confirmation: %s", text(res))
	}
	res, _ = call(t, cs, "cluster_destroy", map[string]any{"cluster": "lab", "confirm": "lab"})
	if !res.IsError || !strings.Contains(text(res), "does not exist") {
		t.Errorf("destroy of a missing cluster: %s", text(res))
	}
	const path = "../../tmp"
	for tool, args := range map[string]map[string]any{
		"cluster_destroy": {"cluster": path, "confirm": path},
		"cluster_status":  {"cluster": path},
		"cluster_wait":    {"cluster": path},
		"node_exec":       {"cluster": path, "command": "true"},
	} {
		res, _ = call(t, cs, tool, args)
		if !res.IsError || !strings.Contains(text(res), "invalid cluster name") {
			t.Errorf("%s accepted a path as cluster name: %s", tool, text(res))
		}
	}
	res, _ = call(t, cs, "cluster_create", map[string]any{"name": "lab", "storage": "nfs"})
	if !res.IsError || !strings.Contains(text(res), "storage must be") {
		t.Errorf("create with bad storage: %s", text(res))
	}
	res, _ = call(t, cs, "fault_apply", map[string]any{"cluster": "lab", "kind": "kill"})
	if !res.IsError {
		t.Errorf("fault on a missing cluster succeeded")
	}
}

func TestCreateReportsJobProgress(t *testing.T) {
	cs, p := connect(t)
	t.Setenv("PROXBASE_FAKE_JOB", "1")
	t0 := time.Now()
	res, _ := call(t, cs, "cluster_create", map[string]any{"name": "lab", "timeoutSeconds": 30})
	if time.Since(t0) > 20*time.Second {
		t.Errorf("create did not return when the job ended")
	}
	if !res.IsError || !strings.Contains(text(res), "boom") || !strings.Contains(text(res), "cluster_create name=lab") {
		t.Errorf("create result: %s", text(res))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !slices.Contains(p.msgs, "[   1s] pve1 installed in 1s") || !slices.Contains(p.msgs, "error: boom") {
		t.Errorf("progress notifications: %q", p.msgs)
	}

	// The job has ended: waiting again reports its result at once.
	res, _ = call(t, cs, "cluster_wait", map[string]any{"cluster": "lab", "timeoutSeconds": 5})
	if !res.IsError || !strings.Contains(text(res), "boom") {
		t.Errorf("wait for an ended job: %s", text(res))
	}
	res, _ = call(t, cs, "cluster_wait", map[string]any{"cluster": "other"})
	if !res.IsError || !strings.Contains(text(res), "no create") {
		t.Errorf("wait without job: %s", text(res))
	}
}
