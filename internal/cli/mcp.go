package cli

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/mcpserver"
	"github.com/virtbase/proxbase/internal/state"
)

func mcpCmd() *cobra.Command {
	var opts mcpserver.Options
	var httpOpts mcpserver.HTTPOptions
	var tokenFile string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run a Model Context Protocol server for AI agents (stdio, or HTTP with --http)",
		Long: `mcp serves the proxbase tools (create, status, exec, snapshot, fault, destroy, ...)
to an MCP client such as Claude Code, Cursor or VS Code over stdin/stdout.
The client starts it; register it e.g. with:

  claude mcp add proxbase -- proxbase mcp

Tool calls return within 50s, because many clients cancel calls after a minute;
a create keeps running in the background and cluster_wait follows it. Claude Code
waits for long calls, so it gets no limit. With a longer client timeout (e.g.
tool_timeout_sec in Codex), set --call-timeout a little below it.

--read-only offers only the tools that change nothing, --toolsets only some groups
of tools (cluster, node, api, snapshot, fault, host), and --audit-log records every
call of a tool that changes something as a JSON line.

--http serves Streamable HTTP at /mcp instead, for agents on other machines or in
sandboxes without KVM. Every request needs "Authorization: Bearer <token>"; the
token is read from --token-file, or generated once and stored with the state. Off
loopback, --tls-cert and --tls-key are required.

Progress and logs go to stderr. See docs/mcp.md.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if httpOpts.Addr == "" {
				return mcpserver.Run(cmd.Context(), version, sink, opts)
			}
			var err error
			if httpOpts.Token, err = mcpToken(tokenFile); err != nil {
				return err
			}
			return mcpserver.ServeHTTP(cmd.Context(), version, sink, opts, httpOpts)
		},
	}
	f := cmd.Flags()
	f.DurationVar(&opts.CallTimeout, "call-timeout", 0, "longest a tool call blocks (default: none for Claude Code, 50s for other clients)")
	f.BoolVar(&opts.ReadOnly, "read-only", false, "offer only the tools that change nothing (status, plans, logs, API reads)")
	f.StringSliceVar(&opts.Toolsets, "toolsets", nil, "offer only these groups of tools: "+strings.Join(mcpserver.ToolsetNames(), ", ")+" (default: all)")
	f.StringVar(&opts.AuditLog, "audit-log", "", "append every call of a tool that changes something to this file (JSON lines)")
	f.StringVar(&httpOpts.Addr, "http", "", "serve Streamable HTTP on this address, e.g. 127.0.0.1:8765, instead of stdio")
	f.StringVar(&tokenFile, "token-file", "", "file with the bearer token for --http (default: generated once, in the state directory)")
	f.StringVar(&httpOpts.CertFile, "tls-cert", "", "TLS certificate for --http")
	f.StringVar(&httpOpts.KeyFile, "tls-key", "", "TLS key for --http")
	return cmd
}

// mcpToken reads the bearer token from file or, without one, the token in the
// state directory, which it creates on first use.
func mcpToken(file string) (string, error) {
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	path := filepath.Join(state.DataHome(), "mcp-token")
	if b, err := os.ReadFile(path); err == nil {
		logf("bearer token in %s", path)
		return strings.TrimSpace(string(b)), nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	if err := os.MkdirAll(state.DataHome(), 0o755); err != nil {
		return "", err
	}
	if err := state.WriteFileAtomic(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	logf("generated a bearer token in %s", path)
	return token, nil
}
