# Proxbase

[![CI](https://github.com/virtbase/proxbase/actions/workflows/ci.yml/badge.svg)](https://github.com/virtbase/proxbase/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/github/go-mod/go-version/virtbase/proxbase)](go.mod)
[![License](https://img.shields.io/github/license/virtbase/proxbase)](LICENSE)

**Real Proxmox VE clusters on your Linux machine in about three minutes, for labs,
demos and CI.**

Proxbase starts every node as a QEMU/KVM virtual machine, installs it from the
official Proxmox VE ISO with the automated installer, and joins the nodes into a
cluster through the Proxmox API, with ZFS or Ceph storage, internal networks and an
API token ready for Terraform or Ansible. No root, no Docker, no manual clicks.

```console
$ proxbase create lab --nodes 3
…
created in 2m56s
Cluster lab: ready, quorate: yes, switch: running
NODE  VM       CLUSTER  IP           WEB UI                   SSH
pve1  running  online   10.10.10.11  https://127.0.0.1:18001  127.0.0.1:18101
pve2  running  online   10.10.10.12  https://127.0.0.1:18002  127.0.0.1:18102
pve3  running  online   10.10.10.13  https://127.0.0.1:18003  127.0.0.1:18103
```

## Features

- **Real nodes:** each node is a VM with its own kernel installed from the stock ISO;
  nested virtualization lets you run guests inside the cluster.
- **Storage:** ZFS pools per node, or hyperconverged Ceph (RBD and CephFS) that waits
  for `HEALTH_OK`.
- **Networking without root:** several internal networks with roles (corosync
  link0/link1, Ceph public/cluster, migration), VLAN-aware and L2-only bridges, jumbo
  frames.
- **Lifecycle:** start/stop, add and remove nodes (with Ceph draining), consistent
  snapshots of the whole cluster, resumable creates.
- **Fault injection:** power loss, hung nodes, pulled cables, network partitions,
  latency and loss, to test HA, fencing and Ceph recovery.
- **Ready for automation:** API token and CA export, `proxbase env` for shells, JSON
  and the bpg/proxmox Terraform provider, `-o json` and `--progress json` for scripts,
  and a [GitHub Action](docs/ci.md) for CI.
- **For AI agents:** `proxbase mcp` is an [MCP server](docs/mcp.md): agents create
  clusters, run commands on nodes, snapshot and inject faults through tools.
- **Fast:** `--golden` clones nodes from a cached base image: a 3-node cluster in
  about 1.5 minutes, with a unique identity per node.
- **Declarative:** one YAML file with a JSON schema; every flag overrides a field.
- **Docker Compose:** `proxbase up` runs a cluster in the foreground with clean
  shutdown and a health check.

## Quick start

Requirements: Linux on amd64 with `/dev/kvm`, QEMU 7.2+ (`qemu-system-x86_64`,
`qemu-img`) and about 4 GiB RAM per node. Nested virtualization is needed for guests
inside the nodes.

```bash
go install github.com/virtbase/proxbase/cmd/proxbase@latest   # or see docs/install.md
proxbase doctor                                               # checks KVM, QEMU, memory, ports
proxbase create lab --nodes 3
```

Then open `https://127.0.0.1:18001` (user `root`, password from `proxbase env lab`), or:

```bash
proxbase ssh lab pve2 -- pvecm status
eval "$(proxbase env lab)"            # PROXMOX_VE_ENDPOINT, PROXMOX_VE_API_TOKEN, …
proxbase destroy lab --yes            # removes everything
```

## Usage

```bash
proxbase create ceph --nodes 3 --storage ceph       # hyperconverged Ceph
proxbase create fast --nodes 3 --golden             # clone from a cached base image
proxbase create -f examples/networks.yaml           # separate networks and VLANs
proxbase node add lab --count 1                     # grow the cluster
proxbase node remove lab pve2                       # shrink it again
proxbase stop lab && proxbase snapshot save lab base
proxbase snapshot restore lab base && proxbase start lab
proxbase env lab --format terraform                 # provider block for bpg/proxmox
proxbase fault partition lab pve3                   # isolate a node, then: fault clear
proxbase status lab --check                         # exit code for CI and health checks
claude mcp add proxbase -- proxbase mcp             # let an AI agent drive proxbase
```

In GitHub Actions:

```yaml
- id: pve
  uses: virtbase/proxbase@v0          # KVM, QEMU, cache, create; outputs endpoint, api-token, env-file
- env: {PVE_ENV: "${{ steps.pve.outputs.env-file }}"}
  run: . "$PVE_ENV" && go test ./... -tags integration
- if: always()
  run: proxbase destroy ci --yes
```

A cluster file looks like this (everything has a default):

```yaml
apiVersion: proxbase.virtbase.com/v1alpha1
kind: Cluster
name: lab
nodes:
  count: 3
  defaults: {cpus: 4, memory: 4G, dataDisks: [{size: 32G}]}
networks:
  - {name: cluster, cidr: 10.10.10.0/24, roles: [corosync]}
storage:
  zfs: [{name: tank, raid: single}]
```

## Documentation

| Topic | |
| --- | --- |
| [Getting started](docs/getting-started.md) | First cluster, web UI, credentials |
| [Installation](docs/install.md) | Packages, Homebrew, container image, verifying releases |
| [Configuration](docs/configuration.md) | Every field, default and limit |
| [Networking](docs/networking.md) · [Storage](docs/storage.md) · [Lifecycle](docs/lifecycle.md) | How it works and what to expect |
| [Fault injection](docs/fault-injection.md) · [Golden images](docs/golden-images.md) | Breaking the cluster on purpose, faster creates |
| [CI and automation](docs/ci.md) · [MCP server](docs/mcp.md) | GitHub Action, JSON progress, AI agents |
| [Docker](docs/docker.md) · [CLI reference](docs/cli.md) · [Troubleshooting](docs/troubleshooting.md) | |
| [Architecture](docs/architecture.md) | Packages, create flow, state on disk |
| [Examples](examples/) | Ready-to-use cluster files for common setups |

## Status and limits

Proxbase is young; the file format may change before v1.0. It is tested with
Proxmox VE 9.2 on Debian 13 (QEMU 10.0, AMD). Clusters need a Linux host with KVM:
macOS builds are experimental and Docker Desktop has no `/dev/kvm`. Nodes are
reachable from the host through forwarded ports on `127.0.0.1`; host-reachable bridge
networking is [designed](docs/bridge-mode.md) but not implemented. Proxbase builds
labs: it stores generated passwords and keys unencrypted in its state directory.

## Contributing

Issues and pull requests are welcome; see [CONTRIBUTING.md](CONTRIBUTING.md) and the
[code of conduct](CODE_OF_CONDUCT.md). Report security issues privately as described in
[SECURITY.md](SECURITY.md).

## License

[Apache-2.0](LICENSE)
