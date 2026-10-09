# Proxbase

Proxbase creates Proxmox VE clusters out of QEMU/KVM virtual machines on one Linux
host, for labs, demos and CI. Each node is a real VM installed from the official ISO
with the Proxmox automated installer, then joined into a cluster through the
Proxmox API.

```bash
proxbase create lab --nodes 3
proxbase status lab
eval "$(proxbase env lab)"
proxbase stop lab && proxbase start lab
proxbase destroy lab --yes
```

## Status

Early development (milestone M3). Tested on Debian 13 with QEMU 10.0 on an AMD host.
What works today:

- Unattended install of Proxmox VE 9.2 from the stock ISO (no root, no Docker)
- N-node cluster with corosync on an internal network, joined via the API
- ZFS root and a ZFS data pool per node, registered as cluster storage
- Ceph (`--storage ceph`): mon on the first three nodes, mgr and MDS on every node,
  one OSD per data disk, an RBD pool `ceph-vm` and CephFS `cephfs` for ISOs,
  templates, backups and snippets; `create` waits for `HEALTH_OK`
- API token `proxbase@pve!api` and CA export; `env` output for shells, JSON and the
  [bpg/proxmox](https://registry.terraform.io/providers/bpg/proxmox) Terraform provider
- Several internal networks with roles (corosync link0/link1, Ceph public/cluster,
  migration), VLAN-aware and L2-only bridges, jumbo frames
- `node add` / `node remove`, snapshots of stopped clusters, `ssh`, `console`
- `create` is resumable: re-run it after a failure

Not yet: bridge networking ([design](docs/bridge-mode.md)), Docker image, macOS. Interfaces and
the file format may change without notice until v0.1.0.

## Requirements

- Linux on amd64 with `/dev/kvm` access; nested virtualization for guests inside nodes
- QEMU 7.2 or newer (`qemu-system-x86_64`, `qemu-img`)
- About 4 GiB RAM and 4 GiB disk per node, plus 1.7 GiB for the ISO cache

`proxbase doctor` checks all of this.

## How it works

- The ISO and `SHA256SUMS` come from `proxmox.mirror` (default
  `https://enterprise.proxmox.com/iso`) and are cached in `~/.cache/proxbase`.
- Each node boots the installer kernel directly with an answer file on a virtual FAT
  disk labelled `PROXMOX-AIS`; Proxbase answers the installer over the serial console.
- Every node has a NAT NIC (`vmbr0`) with the web UI and SSH forwarded to
  `127.0.0.1`, and one NIC per configured network. Networks are rootless: a small
  switch process per cluster connects the nodes over unix datagram sockets.
- All state lives in `~/.local/share/proxbase/clusters/<name>/` (disks, sockets,
  logs, generated root password and SSH key). `destroy` deletes that directory.

Node `pveN` gets web UI `https://127.0.0.1:18000+N`, SSH `127.0.0.1:18100+N` and
address `.1N` (e.g. `10.10.10.11`) on each internal network.

## Ceph

```bash
proxbase create ceph --nodes 3 --storage ceph
```

Defaults: Ceph Squid from the no-subscription repository, 6G memory per node, one
32G data disk per node as OSD, pool `ceph-vm` (size 3, min 2, 32 PGs, autoscaler off).
The 9.2 ISO's storage library rejects keys of current Ceph releases, so Proxbase
upgrades `libpve-storage-perl` (and its dependencies) on the nodes when it installs Ceph.
Fewer than three nodes work if you lower `storage.ceph.pools[].size`.

## Networks

`vmbr0` on every node is the NAT uplink (QEMU user networking): node internet, the
forwarded web UI and SSH, and internet for guests bridged to `vmbr0` (they get DHCP
leases from `10.0.2.100` on). Each entry in `networks` adds a NIC, a bridge and an
address `.1N` on node N. Roles decide what runs where:

| Role | Effect |
| --- | --- |
| `corosync` | corosync link0 (first network with the role) and link1 (second); node names resolve to link0 |
| `ceph-public` / `ceph-cluster` | Ceph public and replication networks (default: corosync link0) |
| `migration` | network for live migration (`datacenter.cfg`) |

Networks without `cidr` are L2 only (bridge without node addresses), typically
`vlanAware` with a `vlans` list for guests. See [examples/networks.yaml](examples/networks.yaml).

## Lifecycle

```bash
proxbase node add lab --count 1      # install, join, ZFS pool / Ceph OSD as configured
proxbase node remove lab pve4        # drain Ceph OSDs, remove daemons, delnode, delete VM
proxbase stop lab && proxbase snapshot save lab base
proxbase snapshot restore lab base && proxbase start lab
proxbase ssh lab pve2 -- pveversion
proxbase console lab pve1            # serial console, Ctrl-] to detach
```

Removed nodes keep their number (`nodes.removed`), so the other nodes keep their
addresses and ports; `node add` reuses the lowest free number. Snapshots are internal
qcow2 snapshots of every disk, taken while all nodes are stopped.
`proxmox.upgrade: true` runs `apt dist-upgrade` on every node before clustering.

## Configuration

Everything has a default; flags override fields of the file.

```bash
proxbase config init cluster.yaml   # annotated example
proxbase config schema > cluster.schema.json
proxbase create -f cluster.yaml --nodes 5
proxbase create lab --dry-run       # print the resolved file
```

## Development

```bash
make build test lint
```

## License

Apache-2.0
