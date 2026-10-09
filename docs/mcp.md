# MCP server for AI agents

`proxbase mcp` is a [Model Context Protocol](https://modelcontextprotocol.io) server
built into the binary. An agent client (Claude Code, Cursor, VS Code, …) starts it
as a subprocess and talks to it over stdin/stdout. It needs no hosting: it runs on
the machine with KVM where the clusters run. Clusters it creates are ordinary
proxbase clusters, so the CLI sees them too and vice versa.

## Setup

Claude Code:

```bash
claude mcp add proxbase -- proxbase mcp
```

Cursor (`.cursor/mcp.json`) and other clients with the common format:

```json
{
  "mcpServers": {
    "proxbase": { "command": "proxbase", "args": ["mcp"] }
  }
}
```

VS Code (`.vscode/mcp.json`):

```json
{
  "servers": {
    "proxbase": { "type": "stdio", "command": "proxbase", "args": ["mcp"] }
  }
}
```

With the container image instead of a local binary (host network so the agent and
your tools reach the forwarded ports; `--group-add` is the GID of `/dev/kvm`):

```json
{
  "mcpServers": {
    "proxbase": {
      "command": "docker",
      "args": ["run", "-i", "--rm", "--device", "/dev/kvm", "--group-add", "993",
               "--network", "host", "-v", "proxbase:/data",
               "ghcr.io/virtbase/proxbase:latest", "mcp"]
    }
  }
}
```

Clusters created in the container live in the `proxbase` volume; use
`docker run … proxbase list` to see them from the CLI.

## Tools

| Tool | Does |
| --- | --- |
| `cluster_list` | All clusters with phase and running nodes |
| `cluster_status` | Phase, quorum, nodes, Ceph health, faults; `healthy` and `problem` |
| `cluster_create` | Create (or resume) a cluster from parameters or a cluster file; reports progress |
| `cluster_wait` | Keep waiting for a create that is still running |
| `cluster_start`, `cluster_stop` | Boot or shut down all nodes |
| `cluster_destroy` | Delete a cluster; `confirm` must repeat the cluster name |
| `node_exec` | Run a command as root on a node; returns `stdout`, `stderr`, `exitCode` |
| `cluster_env` | API endpoint, token, CA certificate, SSH key and ports; root password only with `includeRootPassword` |
| `snapshot_save`, `snapshot_restore`, `snapshot_delete`, `snapshot_list` | Snapshots of the stopped cluster |
| `fault_apply`, `fault_clear` | kill, freeze, thaw, link-down/up, partition, degrade |
| `config_validate` | Check a cluster file; returns errors, warnings and the file with defaults |

The resource `https://proxbase.virtbase.com/schema/v1alpha1/cluster.json` is the JSON
schema of cluster files. Tool annotations mark read-only and destructive tools, so
clients can ask before `cluster_destroy`, `snapshot_restore` or `fault_apply`.

## Long-running creates

A create takes 2-3 minutes with a cached [golden image](golden-images.md) (the
default for `cluster_create`) and 10-20 minutes the first time, when it downloads the
ISO and builds the base image. Many clients time out tool calls earlier, so:

- `cluster_create` starts the create as a background job on the host
  (`proxbase create --progress json`), follows it, and sends each step as an MCP
  progress notification if the client passed a progress token.
- It returns when the cluster is ready or failed, or after `timeoutSeconds`
  (default 600) with `running: true`; then `cluster_wait` continues.
- The job survives the MCP server: a restarted client can call `cluster_wait` or
  `cluster_status` (which shows the latest steps while it runs).
- Cancelling `cluster_create` stops the create (SIGTERM); calling it again resumes.
  Cancelling `cluster_wait` only stops waiting.

The job log is `~/.local/share/proxbase/jobs/<cluster>.jsonl` (same events as
`--progress json`); node install logs stay in the cluster's `logs/` directory.

## Example session (abridged)

```text
agent → cluster_create {"name": "lab", "nodes": 3, "storage": "ceph"}
      ← progress: [  12s] installing Proxmox VE 9.2-1 on pve1, pve2, pve3 …
      ← {"running": false, "status": {"phase": "ready", "quorate": true, …}}
agent → node_exec {"cluster": "lab", "node": "pve2", "command": "ceph -s"}
      ← {"stdout": "  cluster:\n    id: …\n    health: HEALTH_OK …", "exitCode": 0}
agent → fault_apply {"cluster": "lab", "kind": "partition", "groups": [["pve3"]]}
agent → cluster_status {"cluster": "lab"}
      ← {"healthy": false, "problem": "pve3 not running or offline", …}
agent → fault_clear {"cluster": "lab"}
agent → cluster_destroy {"cluster": "lab", "confirm": "lab"}
```

## Security

The server runs with the rights of the user who starts it and can create VMs,
run commands as root inside them and delete clusters. It only talks to its client
over stdio and opens no network port. `cluster_env` returns the API token (full
privileges on the lab cluster) and, on request, the root password; treat the
conversation accordingly. Progress and errors are logged to stderr.

## MCP registry

[`server.json`](../server.json) describes the server for the
[MCP registry](https://registry.modelcontextprotocol.io) as the container image
(`io.github.virtbase/proxbase`); the image carries the matching
`io.modelcontextprotocol.server.name` label and Pages publishes the file at
`https://proxbase.virtbase.com/server.json`. The `Release` workflow publishes every
release with `mcp-publisher` and the workflow's OIDC token
([releasing.md](releasing.md)). By hand, after setting `version` and the image tag in `server.json` to the release:

```bash
mcp-publisher login github      # as a member of the virtbase organization
mcp-publisher publish
```

`mcp-publisher` is in the
[registry releases](https://github.com/modelcontextprotocol/registry/releases).
