package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// confirmInput is the ID of the input request that asks the user to confirm.
const confirmInput = "confirm"

// multiRoundTrip is the protocol revision from which a server asks for input by
// returning input requests instead of sending them while it handles a call.
const multiRoundTrip = "2026-07-28"

// askUser asks the user through the client (MCP elicitation) to confirm
// question. It returns the answer of a call that the client retries with it.
// Otherwise, for a client of protocol 2026-07-28 or later, it returns a result
// that asks for input and makes the client call again with the answer; older
// clients are asked directly. Clients that cannot ask yield false.
func askUser(ctx context.Context, req *mcp.CallToolRequest, question string) (bool, *mcp.CallToolResult) {
	if r, ok := req.Params.InputResponses[confirmInput].(*mcp.ElicitResult); ok {
		return accepted(r), nil
	}
	caps := req.ClientCapabilities()
	if req.Session == nil || caps == nil || caps.Elicitation == nil {
		return false, nil
	}
	params := &mcp.ElicitParams{
		Mode:    "form",
		Message: question,
		RequestedSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"confirm": map[string]any{"type": "boolean", "title": "Confirm", "description": question},
			},
			"required": []string{"confirm"},
		},
	}
	if req.ProtocolVersion() >= multiRoundTrip {
		return false, &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{confirmInput: params}}
	}
	res, err := req.Session.Elicit(ctx, params)
	return err == nil && accepted(res), nil
}

// accepted reports whether the user accepted and ticked confirm.
func accepted(r *mcp.ElicitResult) bool {
	return r != nil && r.Action == "accept" && r.Content["confirm"] == true
}
