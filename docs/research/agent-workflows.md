# Agent workflows research

How AI coding agents (Claude Code, Codex, Cursor, VS Code Copilot) use Proxbase, what
comparable tools ship, and what to build next. Researched October 2026 against MCP spec
2026-07-28 and go-sdk v1.8.0.

## Where Proxbase stands

`proxbase mcp` exposes 16 tools with typed input and output schemas, progress
notifications for creates, one resource (the cluster schema) and read-only/destructive
annotations ([mcp.md](../mcp.md)). Not used: prompts, completions, resource links,
elicitation, icons.

Gaps between the CLI and MCP:

- CLI only: `node add`, `node remove`, `image list`, `image prune`, `doctor`, `up`,
  `env --format terraform|shell`, `env --export`.
- Neither has: file transfer to or from nodes, a PVE API call without shell quoting,
  log or diagnostics access (only file paths are returned), task logs, a one-call reset.
- `node_exec` has no stdin and no PTY, and buffers all output before cutting it to the
  last 64 KiB per stream (`internal/mcpserver/tools.go`).

## Client behaviour that matters

| | Claude Code | Codex CLI | Cursor | VS Code Copilot |
| --- | --- | --- | --- | --- |
| Tool call timeout | About 28 h; calls move to the background after 2 min | **60 s** (`tool_timeout_sec`), startup 10 s | not documented | not documented |
| Prompts | Slash commands | unverified | yes | `/<server>.<prompt>` |
| Elicitation | Form and URL mode | reported by third parties | yes | not documented |
| MCP Apps | no | no | yes | experimental |
| Tasks extension | no | no | no | no |

Consequences:

- `cluster_create` and `cluster_wait` default to `timeoutSeconds: 600`, `node_exec` to
  120. On Codex the client gives up first. Defaults must stay below 60 s, with longer
  waits only on request.
- Claude Code honours two fields in a tool's `_meta`. With `anthropic/maxResultSizeChars`
  a result may hold up to 500k characters before it goes to a file, and
  `anthropic/requiresUserInteraction` asks the user before every call.
- Spec 2026-07-28 deprecates Roots, Sampling and Logging (SEP-2577); do not adopt them.
  Tasks (SEP-2663) became an extension, but go-sdk does not implement it yet (issues
  #626, #942) and no coding client supports it: keep `cluster_wait`.

## What comparable tools ship

| Tool | Agent integration |
| --- | --- |
| HashiCorp Terraform | `terraform-mcp-server` with `--toolsets` and an opt-in for operations; `hashicorp/agent-skills` as Claude and Codex marketplaces, including `terraform-provider-development` |
| Pulumi | `pulumi/agent-skills` plugins |
| LocalStack | MCP server plus six skills and an `agents.md`; tools for lifecycle, logs analysis, chaos, Cloud Pods snapshots, state reset, ephemeral instances |
| kubernetes-mcp-server | `read_only`, toolsets, denied resources, log redaction, a `vm_troubleshoot` diagnose tool |
| Dagger container-use | One container and git branch per agent |

Community Proxmox MCP servers (ProxmoxMCP-Plus, GethosTheWalrus/proxmox-mcp,
lidless-labs/proxmox-mcp, proximo) all offer a generic API read/call, task logs by UPID,
journals, templates and a diagnose or plan step. That is what agents want inside a
cluster.

Packaging: Agent Skills (`SKILL.md`, agentskills.io) work across Claude Code, Codex and
Cursor; plugins bundle skills with the MCP server entry. `.mcpb` bundles target Claude
Desktop on macOS/Windows and matter little for a Linux/KVM tool.

## Roadmap

Status as of the end of October 2026.

| Priority | Item | Status |
| --- | --- | --- |
| P0 | Timeouts every client survives (`--call-timeout`, 50 s for clients other than Claude Code) | Done (#21) |
| P0 | Skill and plugin (Claude Code marketplace, `npx skills add`) | Done (#25) |
| P0 | `pve_get`, `pve_call` | Done (#22) |
| P0 | `cluster_diagnose` | Done (#23) |
| P0 | `cluster_reset`, baseline snapshot after create | Done (#24) |
| P1 | `task_list`, `task_log`, `task_wait` | Done (#26) |
| P1 | `node_add`, `node_remove`, `image_list`, `image_prune`, `host_check`, `file_put`, `file_get`, `proxbase cp` | Done (#27) |
| P1 | `cluster_plan` (dry run), capacity warnings, automatic port ranges | Done (#29) |
| P1 | Owner and lifetime per cluster, `proxbase gc`, `cluster_gc` | Done (#30) |
| P1 | Scoped API tokens (`token_create`, `env --token`) | Done (#31) |
| P1 | `--read-only`, `--toolsets`, `--audit-log` | Done (#32) |
| P1 | VM and container templates in the cluster file | Done (#33); declarative guests (`guests:`) left out: `pve_call` creates them |
| P2 | Prompts, resource templates and links, completions, icon, elicitation | Done (#34) |
| P2 | Streamable HTTP with a bearer token | Done (#35), stateful sessions for older clients' progress and elicitation |
| P2 | `AGENTS.md`, `llms.txt`, Copilot coding agent setup steps | Done |
| P2 | MCP Tasks and MCP Apps | Waiting for go-sdk and coding clients |

Cluster names from the worktree stay a convention of the skill (name clusters after
the task or worktree) rather than a default, so that two agents in one worktree do not
share a cluster by accident.

## Sources

- MCP spec [2025-11-25 changelog](https://modelcontextprotocol.io/specification/2025-11-25/changelog),
  [2026-07-28 release](https://blog.modelcontextprotocol.io/posts/2026-07-28/),
  [extension client matrix](https://modelcontextprotocol.io/extensions/client-matrix),
  [go-sdk releases](https://github.com/modelcontextprotocol/go-sdk/releases)
- Clients: [Codex config reference](https://learn.chatgpt.com/docs/config-file/config-reference),
  [Claude Code MCP](https://code.claude.com/docs/en/mcp),
  [Claude Code plugins](https://code.claude.com/docs/en/plugins-reference),
  [Claude Code hooks](https://code.claude.com/docs/en/hooks),
  [Cursor MCP](https://cursor.com/docs/context/mcp),
  [VS Code MCP servers](https://code.visualstudio.com/docs/copilot/customization/mcp-servers),
  [Agent Skills](https://agentskills.io/specification),
  [AGENTS.md](https://developers.openai.com/codex/guides/agents-md)
- Comparable tools: [hashicorp/agent-skills](https://github.com/hashicorp/agent-skills),
  [terraform-mcp-server](https://github.com/hashicorp/terraform-mcp-server),
  [Pulumi skills](https://www.pulumi.com/docs/ai/skills/),
  [LocalStack agents.md](https://docs.localstack.cloud/agents.md),
  [kubernetes-mcp-server](https://github.com/containers/kubernetes-mcp-server),
  [container-use](https://github.com/dagger/container-use)
- Proxmox MCP servers: [ProxmoxMCP-Plus](https://github.com/RekklesNA/ProxmoxMCP-Plus),
  [GethosTheWalrus/proxmox-mcp](https://github.com/GethosTheWalrus/proxmox-mcp),
  [lidless-labs/proxmox-mcp](https://github.com/lidless-labs/proxmox-mcp),
  [proximo](https://github.com/john-broadway/proximo)
