package cli

import (
	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/mcpserver"
)

func mcpCmd() *cobra.Command {
	var opts mcpserver.Options
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run a Model Context Protocol server on stdio for AI agents",
		Long: `mcp serves the proxbase tools (create, status, exec, snapshot, fault, destroy, ...)
to an MCP client such as Claude Code, Cursor or VS Code over stdin/stdout.
The client starts it; register it e.g. with:

  claude mcp add proxbase -- proxbase mcp

Tool calls return within 50s, because many clients cancel calls after a minute;
a create keeps running in the background and cluster_wait follows it. Claude Code
waits for long calls, so it gets no limit. With a longer client timeout (e.g.
tool_timeout_sec in Codex), set --call-timeout a little below it.

Progress and logs go to stderr. See docs/mcp.md.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return mcpserver.Run(cmd.Context(), version, sink, opts)
		},
	}
	cmd.Flags().DurationVar(&opts.CallTimeout, "call-timeout", 0, "longest a tool call blocks (default: none for Claude Code, 50s for other clients)")
	return cmd
}
