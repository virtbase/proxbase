# CLI reference

Commands take the cluster name as first argument. Without it they use `default`, or
the only existing cluster. `proxbase <command> --help` shows the same information.

Every command accepts `--progress text|json`: with `json`, progress and messages on
stderr are JSON lines, ending with a `done` or `error` event (see
[CI and automation](ci.md#machine-readable-output)). Results (`-o json`) stay on stdout.

## Clusters

| Command | Description |
| --- | --- |
| `create [name]` | Create a cluster from a file and/or flags; re-run to resume a failed create |
| `up [name]` | Create, resume or start a cluster and run in the foreground; stop it on SIGTERM/SIGINT |
| `list` | List clusters with owner and expiry (`-o json`) |
| `status [name]` | Nodes, quorum, Ceph health and ports (`-o json`, `--check`) |
| `start [name]` | Start a stopped cluster and wait for quorum and storage |
| `stop [name]` | Shut down all nodes (`--timeout`, default 3m; `0` = hard) |
| `destroy [name]` | Kill the VMs and remove all files (`--yes`) |
| `ttl [name] <duration\|none>` | Expire the cluster that long from now (e.g. `2h`), or never |
| `gc` | List expired clusters; `--yes` destroys them (not those with a running job) |

Flags of `create` and `up` (they override fields of the file, see
[configuration](configuration.md#flags)):

| Flag | Description |
| --- | --- |
| `-f, --file` | Cluster file (YAML) |
| `--nodes` | Number of nodes |
| `--cpus`, `--memory`, `--disk` | vCPUs, memory (`4G`) and root disk size (`32G`) per node |
| `--data-disks` | Data disks per node: `2x32G`, `32G,64G` or `none` |
| `--storage` | `zfs`, `ceph` or `none` |
| `--pve-version` | ISO version: `9.2` or `9.2-1` |
| `--golden` | Clone nodes from a cached base image ([golden images](golden-images.md)) |
| `--bind-address` | Address of the UI/SSH forwards (default `$PROXBASE_BIND_ADDRESS` or `127.0.0.1`) |
| `--dry-run` | `create` only: print the resolved file with the port base it would take (`-o yaml\|json`) and log the plan: ports, memory, disk, capacity warnings |
| `--snapshot` | `create` only: save a live snapshot with this name when the cluster is ready (kept if it exists) |
| `--ttl` | Lifetime from the create, e.g. `2h`: `proxbase gc` deletes the cluster afterwards (default: none) |
| `--owner` | Who owns the cluster, e.g. an agent and its task (default `$USER`); kept from the first create |
| `-o, --output` | `create` only: `table` or `json` |
| `--stop-timeout` | `up` only: clean shutdown time on SIGTERM (default 2m) |

`status --check` exits non-zero unless the cluster is ready, quorate, every node is
online and Ceph (if configured) is `HEALTH_OK`; use it for health checks.

## Nodes and snapshots

| Command | Description |
| --- | --- |
| `node add [cluster] --count N` | Install, join and configure N new nodes |
| `node remove [cluster] <node>` | Drain and remove a node (`--force` with guests or degraded Ceph pools) |
| `snapshot save [cluster] <name>` | Snapshot every disk of the stopped cluster; `--live`: the running cluster with its memory |
| `snapshot restore [cluster] <name>` | Reset every disk to a snapshot; a live snapshot also starts the cluster in its saved state |
| `snapshot delete [cluster] <name>` | Delete a snapshot |
| `snapshot list [cluster]` | List snapshots |

## Fault injection

| Command | Description |
| --- | --- |
| `fault kill [cluster] <node>` | Power a node off hard (`start` brings it back) |
| `fault freeze\|thaw [cluster] <node>` | Stop or resume a node's vCPUs |
| `fault link [cluster] <node> --down\|--up` | Pull or plug in a node's cable (`--network`) |
| `fault partition [cluster] <group>...` | Split a network into groups (`--network`) |
| `fault degrade [cluster] --delay D --loss P` | Latency and frame loss per direction (`--network`) |
| `fault clear [cluster]` | Undo all faults except kills |

See [fault-injection.md](fault-injection.md).

## Access

| Command | Description |
| --- | --- |
| `ssh [cluster] [node] [-- command…]` | SSH as root (default: first node) |
| `cp <source> <destination>` | Copy a file to or from a node: `[cluster:]node:/path`, a local path or `-` (`--mode`) |
| `console [cluster] <node>` | Serial console, Ctrl-] to detach |
| `env [cluster]` | API endpoint and credentials: `--format shell\|json\|terraform`, `--export <dir>`, `--token <name>` for a scoped token |
| `token create\|list\|delete [cluster] <name>` | Scoped API tokens: one role on one path (`--role PVEAuditor`, `--path /vms/100`, `--no-propagate`); `create` prints `tokenId=secret` |
| `mcp` | MCP server on stdio for AI agents: `--call-timeout` ([call timeouts](mcp.md#call-timeouts)), `--read-only`, `--toolsets`, `--audit-log` ([security](mcp.md#security)); `--http ADDR` with `--token-file`, `--tls-cert`, `--tls-key` serves [remote agents](mcp.md#remote-agents-http) |

## Host and files

| Command | Description |
| --- | --- |
| `image list` | Cached base images and the clusters using them |
| `image prune` | Delete base images no cluster uses |
| `doctor` | Check KVM, nested virtualization, QEMU, memory, KSM, disk, ports (`--port-base`) |
| `config init [file]` | Write an annotated example (`--force` to overwrite) |
| `config validate <file>` | Strictly parse and check a cluster file |
| `config schema` | Print the JSON schema |
| `upgrade` | Upgrade proxbase itself to the latest release (`--version`, `--check`, `--yes`, `--allow-downgrade`, `--signature auto\|require\|skip`); see [upgrading](install.md#upgrading) |
| `version` | Print the version |
| `completion <shell>` | Shell completion script |

`upgrade --check` exits with 0 when proxbase is up to date and 100 when a newer
release exists, 1 on errors; `upgrade` without a terminal needs `--yes`. Installs that
a package manager owns are not replaced: `upgrade` prints the command to use.

## Environment

| Variable | Effect |
| --- | --- |
| `XDG_DATA_HOME` | Cluster state in `$XDG_DATA_HOME/proxbase/clusters`, MCP create jobs in `…/proxbase/jobs` (default `~/.local/share`) |
| `XDG_CACHE_HOME` | ISO cache in `$XDG_CACHE_HOME/proxbase/iso` (default `~/.cache`) |
| `PROXBASE_BIND_ADDRESS` | Default for `access.bindAddress` of new clusters |
| `GITHUB_TOKEN`, `GH_TOKEN` | `upgrade`: token for the GitHub API (higher rate limit) |
| `PROXBASE_INSTALL_METHOD` | `upgrade`: how proxbase was installed, overriding the detection: `binary` (replace in place), `homebrew`, `deb`, `rpm`, `apk`, `go`, `container`, `action` |
| `PROXBASE_UPGRADE_URL` | `upgrade`: GitHub API base URL instead of `https://api.github.com`, for mirrors and tests (gets no token) |
