package cli

import (
	"github.com/spf13/cobra"

	"github.com/virtbase/proxbase/internal/mcpserver"
)

func mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run a Model Context Protocol server on stdio for AI agents",
		Long: `mcp serves the proxbase tools (create, status, exec, snapshot, fault, destroy, ...)
to an MCP client such as Claude Code, Cursor or VS Code over stdin/stdout.
The client starts it; register it e.g. with:

  claude mcp add proxbase -- proxbase mcp

Progress and logs go to stderr. See docs/mcp.md.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return mcpserver.Run(cmd.Context(), version, sink)
		},
	}
}
