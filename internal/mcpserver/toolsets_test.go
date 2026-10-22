package mcpserver

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/progress"
)

// tools returns the tools a client sees.
func tools(t *testing.T, cs *mcp.ClientSession) []*mcp.Tool {
	t.Helper()
	var out []*mcp.Tool
	for tool, err := range cs.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, tool)
	}
	return out
}

func TestToolsetsCoverEveryTool(t *testing.T) {
	h := &handlers{log: progress.Discard, readOnly: map[string]bool{}}
	h.register(mcp.NewServer(&mcp.Implementation{Name: "test"}, nil))
	seen := map[string]string{}
	for set, names := range toolsets {
		for _, n := range names {
			if other, ok := seen[n]; ok {
				t.Errorf("%s is in toolsets %s and %s", n, other, set)
			}
			seen[n] = set
			if _, ok := h.readOnly[n]; !ok {
				t.Errorf("toolset %s lists %s, which is not a tool", set, n)
			}
		}
	}
	for n := range h.readOnly {
		if _, ok := seen[n]; !ok {
			t.Errorf("tool %s is in no toolset", n)
		}
	}
}

func TestReadOnlyServer(t *testing.T) {
	cs, _ := connectAs(t, "test", Options{ReadOnly: true})
	var names []string
	for _, tool := range tools(t, cs) {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("read-only server offers %s", tool.Name)
		}
		names = append(names, tool.Name)
	}
	if !slices.Contains(names, "cluster_status") || slices.Contains(names, "cluster_create") {
		t.Errorf("tools %v", names)
	}
	if !strings.Contains(cs.InitializeResult().Instructions, "read-only") {
		t.Error("instructions do not say the server is read-only")
	}
}

func TestToolsetOption(t *testing.T) {
	cs, _ := connectAs(t, "test", Options{Toolsets: []string{"api", "fault"}})
	var names []string
	for _, tool := range tools(t, cs) {
		names = append(names, tool.Name)
	}
	want := append(slices.Clone(toolsets["api"]), toolsets["fault"]...)
	slices.Sort(names)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Errorf("tools %v, want %v", names, want)
	}
	if _, err := New("test", nil, Options{Toolsets: []string{"nodes"}}); err == nil || !strings.Contains(err.Error(), "unknown toolset") {
		t.Errorf("unknown toolset: %v", err)
	}
}
