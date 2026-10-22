package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/virtbase/proxbase/internal/progress"
)

// auditEntry is one line of the audit log: a call of a tool that changes something.
type auditEntry struct {
	Time    time.Time      `json:"time"`
	Client  string         `json:"client,omitempty"`
	Tool    string         `json:"tool"`
	Args    map[string]any `json:"args,omitempty"`
	Error   string         `json:"error,omitempty"`
	Seconds float64        `json:"seconds"`
}

// maxAuditString cuts long arguments, such as file contents, in the audit log.
const maxAuditString = 256

// auditMiddleware appends a line to path for every call of a tool that is not
// read-only, after it returned. Write errors go to log.
func auditMiddleware(path string, readOnly map[string]bool, log progress.Sink) mcp.Middleware {
	var mu sync.Mutex
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			call, ok := req.(*mcp.CallToolRequest)
			if method != "tools/call" || !ok || readOnly[call.Params.Name] {
				return next(ctx, method, req)
			}
			t0 := time.Now()
			res, err := next(ctx, method, req)
			e := auditEntry{Time: t0.UTC(), Client: clientName(call), Tool: call.Params.Name, Args: auditArgs(call.Params.Arguments), Seconds: time.Since(t0).Seconds()}
			switch r, _ := res.(*mcp.CallToolResult); {
			case err != nil:
				e.Error = err.Error()
			case r != nil && r.IsError:
				e.Error = firstText(r)
			}
			mu.Lock()
			defer mu.Unlock()
			if werr := appendJSONLine(path, e); werr != nil {
				log.Logf("audit log %s: %v", path, werr)
			}
			return res, err
		}
	}
}

// auditArgs decodes tool arguments for the log, cutting long strings.
func auditArgs(raw json.RawMessage) map[string]any {
	var args map[string]any
	if json.Unmarshal(raw, &args) != nil {
		return nil
	}
	for k, v := range args {
		if s, ok := v.(string); ok && len(s) > maxAuditString {
			args[k] = fmt.Sprintf("%s… (%d bytes)", s[:maxAuditString], len(s))
		}
	}
	return args
}

// firstText returns the first line of a result's text.
func firstText(r *mcp.CallToolResult) string {
	for _, c := range r.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			return strings.SplitN(t.Text, "\n", 2)[0]
		}
	}
	return ""
}

func appendJSONLine(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
