# MCP server for AI agents

`proxbase mcp` is a [Model Context Protocol](https://modelcontextprotocol.io) server
built into the binary. An agent client (Claude Code, Cursor, VS Code, …) starts it
as a subprocess and talks to it over stdin/stdout. It needs no hosting: it runs on
the machine with KVM where the clusters run. Clusters it creates are ordinary
proxbase clusters, so the CLI sees them too and vice versa.

## Setup

Claude Code, with the [skill](#skill-and-plugin) (needs `proxbase` on `PATH`):

```text
/plugin install proxbase --marketplace virtbase/proxbase
```

Claude Code, only the server:

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

Codex (`~/.codex/config.toml`; see [call timeouts](#call-timeouts)):

```toml
[mcp_servers.proxbase]
command = "proxbase"
args = ["mcp"]
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
| `cluster_diagnose` | Everything that explains a problem in one call: status plus, per node, quorum, failed units and tasks, errors of corosync, pmxcfs, pveproxy, HA and Ceph (`sinceMinutes`, default 30), clock offset, `ceph health detail`; the console of stopped nodes; host log files |
| `cluster_plan` | What `cluster_create` would set up, without creating anything: nodes with IPs and ports, memory and disk, ISO cached, host resources, warnings, the resolved file |
| `cluster_create` | Create (or resume) a cluster from parameters or a cluster file; reports progress; `snapshot: baseline` saves a live snapshot when it is ready |
| `cluster_wait` | Keep waiting for a create that is still running |
| `cluster_start`, `cluster_stop` | Boot or shut down all nodes |
| `cluster_destroy` | Delete a cluster; `confirm` must repeat the cluster name |
| `node_exec` | Run a command as root on a node; returns `stdout`, `stderr`, `exitCode` |
| `file_put`, `file_get` | Write a file on a node (text or base64, up to 4 MiB, replaced atomically) or read one (`maxBytes`, default 256 KiB) |
| `node_add`, `node_remove` | Grow or shrink a cluster as a [background job](#background-jobs); `node_remove` needs `confirm` set to the node name |
| `pve_get` | Read from the Proxmox VE API with the cluster's token: `path` (e.g. `/cluster/resources`), `params`; returns the response data |
| `pve_call` | POST, PUT or DELETE through the API; waits for the task it starts and returns its exit status and log, a failed task is an error |
| `task_list` | Proxmox VE tasks of every node or one, newest first; filters `errorsOnly`, `running`, `type`, `vmid`, `sinceMinutes` |
| `task_log` | Status and the last `lines` of a task's log, by UPID |
| `task_wait` | Wait for a task to end; returns its exit status and log, a failed task is an error |
| `cluster_env` | API endpoint, token, CA certificate, SSH key and ports; root password only with `includeRootPassword` |
| `snapshot_save`, `snapshot_restore`, `snapshot_delete`, `snapshot_list` | Snapshots of the stopped cluster, or with `live` of the running cluster including memory (restoring resumes it) |
| `cluster_reset` | Back to a snapshot (default `baseline`) in one call: power off, restore, start or resume; returns the status |
| `fault_apply`, `fault_clear` | kill, freeze, thaw, link-down/up, partition, degrade |
| `config_validate` | Check a cluster file; returns errors, warnings and the file with defaults |
| `image_list`, `image_prune` | Cached base images and the clusters using them; delete unused ones (`dryRun`) |
| `host_check` | The checks of `proxbase doctor`: KVM, nested virtualization, QEMU, memory, disk, ports |

The resource `https://proxbase.virtbase.com/schema/v1alpha1/cluster.json` is the JSON
schema of cluster files. Tool annotations mark read-only and destructive tools, so
clients can ask before `cluster_destroy`, `snapshot_restore`, `fault_apply` or `pve_call`.

## Skill and plugin

The tools say what they do; the proxbase skill teaches agents how to use them
together: reuse or create a cluster with a `baseline` snapshot, connect the code
under test, `cluster_diagnose` before guessing, `cluster_reset` between runs, and
destroy only their own clusters. Its references cover Terraform (bpg/proxmox),
Ansible, HA and Ceph fault tests and CI. It is an [Agent Skill](https://agentskills.io)
in [`plugins/proxbase/skills/proxbase`](../plugins/proxbase/skills/proxbase/SKILL.md).

| Agent | Install |
| --- | --- |
| Claude Code | `/plugin install proxbase --marketplace virtbase/proxbase`: plugin with the skill and the MCP server, or `/plugin marketplace add virtbase/proxbase` and `/plugin install proxbase@virtbase` |
| Codex, Cursor, others | `npx skills add virtbase/proxbase`: copies the skill to `.agents/skills/proxbase` in the project (`-g` for your user); add the MCP server as above |

## Background jobs

A create takes under a minute with a cached [golden image](golden-images.md) (the
default for `cluster_create`; Ceph about 1.5 minutes) and 10-20 minutes the first time, when it downloads the
ISO and builds the base image. Many clients time out tool calls earlier, so:

- `cluster_create` starts the create as a background job on the host
  (`proxbase create --progress json`), follows it, and sends each step as an MCP
  progress notification if the client passed a progress token. `node_add` and
  `node_remove` run `proxbase node add` and `node remove` the same way; a cluster
  has at most one job at a time.
- It returns when the cluster is ready or failed, or after `timeoutSeconds`
  (default 600, capped by the [call timeout](#call-timeouts)) with `running: true`;
  then `cluster_wait` continues.
- The job survives the MCP server: a restarted client can call `cluster_wait` or
  `cluster_status` (its `job` field shows the command and latest steps while it runs).
- Cancelling the call stops the job (SIGTERM); `cluster_create` with the cluster's
  name resumes a create or an interrupted node add. Cancelling `cluster_wait` only
  stops waiting.

The job log is `~/.local/share/proxbase/jobs/<cluster>.jsonl` (same events as
`--progress json`) and its command line `<cluster>.cmd`; node install logs stay in
the cluster's `logs/` directory.

## Call timeouts

Clients cancel tool calls that take too long: Codex after 60 seconds
(`tool_timeout_sec`), others after a similar time. Claude Code waits and moves long
calls to the background. So no call blocks longer than the call timeout:

| Client | Call timeout |
| --- | --- |
| Claude Code (`clientInfo` name `claude-code`) | none |
| All others | 50 s |
| With `proxbase mcp --call-timeout 10m` | 10 minutes, for every client |

Within it:

- `cluster_create` and `cluster_wait` return with `running: true` and the create
  continues in the background.
- `cluster_start` returns `healthy: false` with what is still pending while the
  nodes keep booting; `cluster_status` shows when it is healthy.
- `cluster_stop` gives the nodes the call timeout minus 20 s for a clean shutdown,
  then powers them off.
- `node_exec` kills the command at the call timeout. Run longer commands in the
  background (`nohup … >/root/job.log 2>&1 &`) and poll.

With a longer client timeout, set `--call-timeout` a little below it, for Codex in
`~/.codex/config.toml`:

```toml
[mcp_servers.proxbase]
command = "proxbase"
args = ["mcp", "--call-timeout", "14m"]
tool_timeout_sec = 900
```

## Example session (abridged)

```text
agent → cluster_create {"name": "lab", "nodes": 3, "storage": "ceph"}
      ← progress: [  12s] installing Proxmox VE 9.2-1 on pve1, pve2, pve3 …
      ← {"running": false, "status": {"phase": "ready", "quorate": true, …}}
agent → node_exec {"cluster": "lab", "node": "pve2", "command": "ceph -s"}
      ← {"stdout": "  cluster:\n    id: …\n    health: HEALTH_OK …", "exitCode": 0}
agent → pve_call {"cluster": "lab", "method": "POST", "path": "/pools", "params": {"poolid": "ci"}}
      ← {"data": null}
agent → pve_get {"cluster": "lab", "path": "/cluster/resources", "params": {"type": "node"}}
      ← {"data": [{"node": "pve1", "status": "online", …}, …]}
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
