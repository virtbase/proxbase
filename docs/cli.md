# CLI reference

Commands take the cluster name as first argument. Without it they use `default`, or
the only existing cluster. `proxbase <command> --help` shows the same information.

## Clusters

| Command | Description |
| --- | --- |
| `create [name]` | Create a cluster from a file and/or flags; re-run to resume a failed create |
| `up [name]` | Create, resume or start a cluster and run in the foreground; stop it on SIGTERM/SIGINT |
| `list` | List clusters (`-o json`) |
| `status [name]` | Nodes, quorum, Ceph health and ports (`-o json`, `--check`) |
| `start [name]` | Start a stopped cluster and wait for quorum and storage |
| `stop [name]` | Shut down all nodes (`--timeout`, default 3m; `0` = hard) |
| `destroy [name]` | Kill the VMs and remove all files (`--yes`) |

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
| `--dry-run` | `create` only: print the resolved file (`-o yaml\|json`) |
| `-o, --output` | `create` only: `table` or `json` |
| `--stop-timeout` | `up` only: clean shutdown time on SIGTERM (default 2m) |

`status --check` exits non-zero unless the cluster is ready, quorate, every node is
online and Ceph (if configured) is `HEALTH_OK`; use it for health checks.

## Nodes and snapshots

| Command | Description |
| --- | --- |
| `node add [cluster] --count N` | Install, join and configure N new nodes |
| `node remove [cluster] <node>` | Drain and remove a node (`--force` with guests or degraded Ceph pools) |
| `snapshot save [cluster] <name>` | Snapshot every disk of the stopped cluster |
| `snapshot restore [cluster] <name>` | Reset every disk to a snapshot |
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
| `console [cluster] <node>` | Serial console, Ctrl-] to detach |
| `env [cluster]` | API endpoint and credentials: `--format shell\|json\|terraform`, `--export <dir>` |

## Host and files

| Command | Description |
| --- | --- |
| `image list` | Cached base images and the clusters using them |
| `image prune` | Delete base images no cluster uses |
| `doctor` | Check KVM, nested virtualization, QEMU, memory, disk, ports (`--port-base`) |
| `config init [file]` | Write an annotated example (`--force` to overwrite) |
| `config validate <file>` | Strictly parse and check a cluster file |
| `config schema` | Print the JSON schema |
| `version` | Print the version |
| `completion <shell>` | Shell completion script |

## Environment

| Variable | Effect |
| --- | --- |
| `XDG_DATA_HOME` | Cluster state in `$XDG_DATA_HOME/proxbase/clusters` (default `~/.local/share`) |
| `XDG_CACHE_HOME` | ISO cache in `$XDG_CACHE_HOME/proxbase/iso` (default `~/.cache`) |
| `PROXBASE_BIND_ADDRESS` | Default for `access.bindAddress` of new clusters |
