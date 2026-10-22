# Architecture

Proxbase is a single Go binary. `create` turns a cluster file into a desired state,
starts one QEMU process per node and then runs idempotent steps against each node
(SSH) and the Proxmox API until the cluster matches the file. There is no daemon
apart from one small switch process per running cluster.

## Packages

```mermaid
flowchart TD
    cli["cli<br/>cobra commands"] --> cluster
    cli --> config
    cli --> network
    cli --> mcpserver["mcpserver<br/>MCP tools"]
    mcpserver --> cluster
    mcpserver --> job["job<br/>detached creates"]
    cluster --> progress["progress<br/>events, text/JSON sinks"]
    job --> progress
    cluster["cluster<br/>orchestration"] --> vm
    cluster --> network
    cluster --> storage
    cluster --> nodesetup
    cluster --> image
    cluster --> answer
    cluster --> firstboot["firstboot<br/>first-boot hook"]
    cluster --> aptcache["aptcache<br/>package cache"]
    cluster --> golden["golden<br/>base images"]
    golden --> firstboot
    cluster --> state
    cluster --> pve
    cluster --> remote
    qemu["qemu<br/>vm.Runtime"] -. implements .-> vm["vm<br/>Spec, Runtime"]
    cluster --> qemu
    qemu --> serial["serial<br/>drives the installer"]
    network["network<br/>switch process"] --> netswitch["netswitch<br/>L2 switch"]
    zfs["storage/zfs"] -. implements .-> storage["storage<br/>Backend, Host"]
    ceph["storage/ceph"] -. implements .-> storage
    cluster --> zfs
    cluster --> ceph
    storage --> pve["pve<br/>REST client"]
    storage --> remote["remote<br/>SSH"]
    state --> config["config<br/>file format"]
```

| Package | Responsibility |
| --- | --- |
| `config` | Cluster file: types, defaults, validation, JSON schema |
| `state` | On-disk layout of a cluster, `state.json`, secrets, locking |
| `image` | Download, verify and cache ISOs; extract the installer kernel and command line |
| `answer` | Render the installer's `answer.toml` |
| `firstboot` | The installer's first-boot hook: node identity before networking, base image baking; serves the hook to the installer |
| `aptcache` | Caching HTTP proxy for the packages Proxbase installs on the nodes |
| `vm` | Hypervisor-neutral node description and the runtime interfaces |
| `qemu` | QEMU runtime: command lines with fixed PCI slots, install, start/stop via pidfile and QMP, qcow2 snapshots (live ones through QMP jobs) |
| `serial` | Answer the installer's debug shell over the serial console and log it |
| `netswitch` | Learning L2 switch over unix datagram sockets |
| `network` | The per-cluster switch process, the nodes' NIC sockets and network faults (`run/faults.json`) |
| `nodesetup` | Shell scripts that prepare nodes (hosts, repositories, bridges, upgrades) |
| `storage`, `storage/zfs`, `storage/ceph` | Storage backend contract and implementations |
| `pve` | Proxmox VE REST client with certificate pinning and task helpers |
| `remote` | SSH to nodes through the forwarded ports |
| `retry` | Retry with timeout, interval and permanent errors |
| `golden` | Cached base images: build, bake, and the personalization of clones from older images |
| `cluster` | Orchestration: create, lifecycle, nodes, snapshots, faults, status, exec |
| `progress` | Progress events and sinks: text, JSON lines, MCP notifications |
| `job` | Runs a create detached from its caller, with a JSON-lines progress log |
| `mcpserver` | MCP server (`proxbase mcp`): tools, the schema resource, progress forwarding |
| `cli` | Commands, flags and output |

Extension points are small interfaces: a new hypervisor implements `vm.Runtime` (and
optionally `vm.FaultInjector` for `proxbase fault`), a new
storage backend implements `storage.Backend` (plus optional `Waiter`,
`StopPreparer`, `NodeRemover`, `HealthReporter`), and a different network backend
(for example host bridges) would replace `network.Switch`.

## Create

```mermaid
sequenceDiagram
    participant CLI as proxbase create
    participant Img as image
    participant SW as switch process
    participant VM as QEMU (per node)
    participant Node as node (SSH)
    participant API as Proxmox API
    CLI->>Img: ISO + SHA256SUMS (cached and verified, no network for a cached exact version)
    CLI->>SW: start proxbase _switch
    par every node
        CLI->>VM: install: -kernel/-initrd, answer disk PROXMOX-AIS
        VM-->>CLI: serial: debug shell
        CLI->>VM: proxmox-fetch-answer partition PROXMOX-AIS && exit
        VM->>CLI: fetch the first-boot hook (HTTP on 10.0.2.2)
        VM-->>CLI: power off = installed
    end
    CLI->>VM: boot nodes (daemonized), clones get their identity as fw_cfg file
    Note over VM: first boot: hook runs before networking
    CLI->>Node: provisioning mode, hosts, repositories, bridges (nodesetup)
    CLI->>API: create cluster on node 1, join the others
    CLI->>Node: wait for quorum and a writable /etc/pve, repair lost node certificates
    CLI->>API: storage backends: ZFS pools / Ceph
    CLI->>API: API token, export CA
    CLI->>Node: end provisioning mode
```

Each step checks first and skips what is done, so a failed create resumes when run
again. Progress (`installed` per node, phase, errors) is kept in `state.json`.

### Speed

- The installer's first-boot hook ([golden images](golden-images.md)) runs on each
  node's first boot before networking: it skips the `pveupdate` Proxmox VE starts on
  a first boot, removes the 5 s GRUB menu wait and, on clones, sets the identity,
  so clones need no extra reboot.
- While provisioning, nodes write without fsync (`zfs set sync=disabled` on `rpool`,
  dpkg `force-unsafe-io`); the last step restores both, so a ready cluster has real
  fsync semantics for fault injection.
- Per-node work runs in parallel: installs, boots, configuration, ZFS pools, API
  logins, Ceph managers and OSDs. Cluster joins, Ceph monitors and MDS daemons stay
  sequential (the cluster file system and `ceph.conf` are not safe for concurrent
  changes).
- SSH probes time out their handshake after 3 s: QEMU's port forward accepts
  connections before the guest is up and otherwise retries towards the guest only
  every 12 s.
- NIC 0 stays on QEMU user networking (slirp): host to node it measured 6-7 Gbit/s,
  so passt would not speed up provisioning, and slirp also serves the nested guests
  on `vmbr0` with DHCP.
- Packages Proxbase installs go through a cache on the host (`aptcache`, reached as
  `http://10.0.2.2:PORT` like the hook server), so each is downloaded once. It only
  keeps `.deb` files; if apt rejects one (Hash Sum mismatch), Proxbase's apt runs
  retry once with `Acquire::http::No-Cache`, which makes the cache fetch it again.

## Progress and the MCP server

Long operations report through a `progress.Sink` (a function taking an `Event`).
The CLI passes a text or JSON-lines sink for stderr (`--progress`); the MCP server
passes one that also forwards events as MCP progress notifications.

```mermaid
sequenceDiagram
    participant Agent as MCP client
    participant MCP as proxbase mcp
    participant Job as proxbase create --progress json (detached)
    participant Log as job log (jobs/NAME.jsonl)
    Agent->>MCP: tools/call cluster_create (progressToken)
    MCP->>Job: start in its own session
    loop every second until done or timeoutSeconds
        Job->>Log: one event per line
        MCP->>Log: read new lines
        MCP-->>Agent: notifications/progress
    end
    MCP-->>Agent: result (status, or running=true)
    Agent->>MCP: cluster_wait (if still running)
```

The create runs as a separate process so it survives client timeouts and restarts
of the MCP server; everything else (`node_exec`, `cluster_start`, snapshots, faults)
runs in the server process, guarded by the same cluster lock as the CLI.

## Nodes

- QEMU/KVM with q35, SeaBIOS and fixed PCI addresses for every device, identical
  for install and run, so interface names never change between them.
- NIC 0 is QEMU user networking (`vmbr0`, NAT, port forwards); NIC 1..n are unix
  datagram sockets to the switch. MACs are derived from node and NIC number.
- A virtio balloon with free page reporting returns memory the guest freed to the
  host; disks use `cache=unsafe`, `discard=unmap` and `detect-zeroes=unmap`, and ZFS
  pools in the nodes have `autotrim` on, so freed space shrinks the images.
- The installer runs in the foreground (killed if proxbase dies); installed nodes run
  daemonized with a pidfile, a QMP socket and a serial console socket with a log file.
- Stop: ACPI powerdown via QMP, `quit` after the timeout, then SIGKILL.

## Trust

- Each cluster has a generated root password and ed25519 key; SSH host keys are
  recorded on first use and checked afterwards.
- During setup the API client pins each node's certificate fingerprint (read over
  SSH); afterwards clients verify against the exported cluster CA, ignoring the host
  name because nodes are reached through `127.0.0.1`.

## State on disk

```text
~/.local/share/proxbase/clusters/<name>/
├── cluster.yaml        resolved configuration (source of truth)
├── state.json          phase, nodes, ports, host keys, snapshots
├── lock                flock for mutating commands
├── secrets/            root-password, id_ed25519(.pub), api-token, pve-root-ca.pem (0600)
├── disks/              <node>-root.qcow2, <node>-dataN.qcow2
├── run/                pidfiles, QMP/console sockets, switch and NIC sockets
└── logs/               install, console, QEMU and switch logs
~/.local/share/proxbase/jobs/  <cluster>.jsonl, .pid, .yaml of creates started over MCP
~/.cache/proxbase/iso/     ISOs, SHA256SUMS, extracted installer files
~/.cache/proxbase/images/  golden base images (create --golden)
~/.cache/proxbase/apt/     packages served to the nodes (unused for 30 days: removed)
```

Design decisions and measurements are in [research/feasibility.md](research/feasibility.md).
