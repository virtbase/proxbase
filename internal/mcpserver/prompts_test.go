package mcpserver

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPrompts(t *testing.T) {
	cs, _ := connect(t)
	var names []string
	for p, err := range cs.Prompts(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, p.Name)
	}
	if !slices.Equal(names, []string{"diagnose", "ha-failover", "test-loop"}) {
		t.Errorf("prompts %v", names)
	}
	res, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "test-loop", Arguments: map[string]string{"task": "the module in ./infra"}})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Messages[0].Content.(*mcp.TextContent).Text
	for _, want := range []string{"the module in ./infra", `"test"`, "nodes=1", "cluster_reset", "cluster_diagnose"} {
		if !strings.Contains(text, want) {
			t.Errorf("test-loop prompt lacks %q:\n%s", want, text)
		}
	}
	res, err = cs.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "diagnose", Arguments: map[string]string{"cluster": "lab"}})
	if err != nil || !strings.Contains(res.Messages[0].Content.(*mcp.TextContent).Text, `"lab"`) {
		t.Errorf("diagnose prompt: %v", err)
	}
}

func TestServerIcon(t *testing.T) {
	cs, _ := connect(t)
	icons := cs.InitializeResult().ServerInfo.Icons
	if len(icons) != 1 || !strings.HasPrefix(icons[0].Source, "data:image/svg+xml;base64,") {
		t.Errorf("icons %+v", icons)
	}
}
