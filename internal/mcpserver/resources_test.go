package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/state"
)

// fakeCluster writes the state of a cluster with a console log.
func fakeCluster(t *testing.T, name string) {
	t.Helper()
	d := state.ForCluster(name)
	if err := d.Init(); err != nil {
		t.Fatal(err)
	}
	if err := d.Save(&state.State{Name: name, Phase: state.PhaseReady, Nodes: []*state.NodeState{{Name: "pve1"}, {Name: "pve2"}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(d.Path("logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.Log("pve1-console.log"), []byte("pve1 login:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLogResources(t *testing.T) {
	cs, _ := connect(t)
	fakeCluster(t, "lab")
	ctx := context.Background()
	res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "proxbase://clusters/lab/logs/pve1-console.log"})
	if err != nil || res.Contents[0].Text != "pve1 login:\n" {
		t.Fatalf("console log: %v %+v", err, res)
	}
	for _, uri := range []string{
		"proxbase://clusters/lab/logs/../state.json",
		"proxbase://clusters/lab/logs/missing.log",
		"proxbase://clusters/../../etc/logs/passwd",
		"proxbase://jobs/lab/log",
	} {
		if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri}); err == nil {
			t.Errorf("read %s", uri)
		}
	}
	if err := os.MkdirAll(filepath.Join(state.DataHome(), "jobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state.DataHome(), "jobs", "lab.jsonl"), []byte(`{"type":"done"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "proxbase://jobs/lab/log"}); err != nil || !strings.Contains(res.Contents[0].Text, "done") {
		t.Errorf("job log: %v", err)
	}
}

func TestComplete(t *testing.T) {
	cs, _ := connect(t)
	fakeCluster(t, "lab")
	fakeCluster(t, "lb")
	ctx := context.Background()
	complete := func(ref *mcp.CompleteReference, arg, value string, context map[string]string) []string {
		t.Helper()
		p := &mcp.CompleteParams{Ref: ref, Argument: mcp.CompleteParamsArgument{Name: arg, Value: value}}
		if context != nil {
			p.Context = &mcp.CompleteContext{Arguments: context}
		}
		res, err := cs.Complete(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return res.Completion.Values
	}
	prompt := &mcp.CompleteReference{Type: "ref/prompt", Name: "diagnose"}
	if got := complete(prompt, "cluster", "la", nil); !slices.Equal(got, []string{"lab"}) {
		t.Errorf("cluster completion %v", got)
	}
	logs := &mcp.CompleteReference{Type: "ref/resource", URI: logURI}
	if got := complete(logs, "file", "pve", map[string]string{"cluster": "lab"}); !slices.Equal(got, []string{"pve1-console.log"}) {
		t.Errorf("file completion %v", got)
	}
	if got := complete(logs, "node", "", map[string]string{"cluster": "lab"}); !slices.Equal(got, []string{"pve1", "pve2"}) {
		t.Errorf("node completion %v", got)
	}
}

// Clients of protocol 2026-07-28 get input requests and call again; older ones
// are asked directly.
func TestDestroyAsksTheUser(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) { testDestroyAsks(t, protocol) })
	}
}

func testDestroyAsks(t *testing.T, protocol string) {
	for _, tt := range []struct {
		name   string
		answer *mcp.ElicitResult
		want   string
	}{
		{"accepted", &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, "does not exist"},
		{"declined", &mcp.ElicitResult{Action: "decline"}, "set confirm"},
		{"accepted without confirming", &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": false}}, "set confirm"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var asked string
			cs, _ := connectWith(t, "test", Options{}, &mcp.ClientOptions{
				ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
					asked = req.Params.Message
					return tt.answer, nil
				},
			}, &mcp.ClientSessionOptions{ProtocolVersion: protocol})
			res, _ := call(t, cs, "cluster_destroy", map[string]any{"cluster": "lab"})
			if !res.IsError || !strings.Contains(text(res), tt.want) || !strings.Contains(asked, "Destroy cluster lab?") {
				t.Errorf("destroy: %s (asked %q), want %q", text(res), asked, tt.want)
			}
		})
	}
}
