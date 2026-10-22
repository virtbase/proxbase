# Product variations research

Whether Proxbase should create more than PVE nodes: a corosync QDevice, Proxmox Backup
Server, Proxmox Mail Gateway, Proxmox Datacenter Manager. Researched October 2026.

## Verdict

| | Effort | Fit | Notes |
| --- | --- | --- | --- |
| Proxmox Backup Server | S–M | High | Same installer as PVE; backup and restore tests are a classic lab need |
| Corosync QDevice | M | High | Needs a small Debian guest; 2-node clusters are common |
| Datacenter Manager | M | Medium–high | Multi-cluster and remote migration are hard to test on hardware |
| Mail Gateway | S to install, L to be useful | Low | Mail flow needs SMTP sources, sinks and DNS, unrelated to PVE |

Suggested order: node `kind` refactor, PBS, QDevice with a generic Debian guest, cheap
PVE add-ons, then PDM. Mail Gateway and external Ceph stay out of scope.

## Installer support

The mirror's `SHA256SUMS` (checked 2026-10-22) covers `proxmox-backup-server_2.4-1`,
`3.4-1`, `4.1-1`, `4.2-1`; `proxmox-datacenter-manager_1.0-2`, `1.1-1`;
`proxmox-mailgateway_7.3-1` (no hyphen) and `proxmox-mail-gateway_8.2-1`, `9.0-1`,
`9.1-1`.

| Product | Automated installer | First-boot hook |
| --- | --- | --- |
| PVE | 8.2 | 8.3 |
| PBS | 3.2 | 3.3 |
| PMG | 8.2 | 8.2 |
| PDM | 0.1 | yes |

All products use the same installer code and answer file schema; btrfs is PVE only.
Not verified: that PBS, PMG and PDM ISOs have the same `boot/linux26`, `boot/initrd.img`
and grub entry with `proxmox-start-auto-installer`. Unpack one ISO before coding.

## What the code assumes today

Every VM is a PVE cluster member from one homogeneous list. To add other kinds:

- Config and state have no node kind: `config.Nodes` is count, name pattern, defaults
  and overrides (`internal/config/config.go`), `state.NodeState` has no kind or version.
- Addressing derives from the index: MAC, network IP (.10+n), UI port (base+n), SSH port
  (base+100+n) (`internal/config/nodes.go`).
- Guest ports 8006 and 22 and guest address 10.0.2.15 are fixed in the QEMU forwards
  (`internal/qemu/qemu.go`). PBS needs 8007.
- The ISO prefix `proxmox-ve_` is fixed in `internal/image/image.go` (regex, file name,
  version parsing, error text).
- The API client uses PVE names (`PVEAPIToken=`, `PVEAuthCookie`) in
  `internal/pve/api.go`; PBS and PMG use their own.
- `create()` runs every step on every node: repositories, cluster create and join,
  certificate repair, storage, token (`internal/cluster/create.go`).
- `waitQuorum` requires total votes to equal the node count
  (`internal/cluster/pvecluster.go`); a QDevice adds a vote.
- `Status.Problem` requires every node to be online in `/cluster/status`
  (`internal/cluster/status.go`).
- Golden images bake PVE specifics (`/etc/pve/...` in `internal/golden/personalize.go`).

Generic and reusable: the QEMU runtime and its interfaces (`internal/vm`), the switch,
SSH, the apt cache, download and verification (apart from the prefix), snapshots, faults,
progress and jobs. Disk overlays work from any qcow2 base, including a cloud image.

## Proxmox Backup Server

- Current release 4.2 (April 2026, Debian 13). S3 datastores are supported since 4.2,
  removable datastores since 4.0.
- Proxbase needs: an ISO family by prefix; a per-kind guest port (8007); a data disk and
  `proxmox-backup-manager disk fs create store1 --disk vdb --filesystem ext4
  --add-datastore true`; an API token with a `DatastoreAdmin` ACL; the fingerprint from
  `proxmox-backup-manager cert info`; on the cluster
  `pvesm add pbs pbs1 --server <ip> --datastore store1 --username 'root@pam!pve'
  --password <secret> --fingerprint <fp>`. Health: `GET /api2/json/ping`.
- Lighter option: install the `proxmox-backup-server` package on a PVE node (works, the
  docs call it not recommended). Fine for a lab flag.
- Token and ACL flags need checking against the 4.2 CLI.

## Corosync QDevice

- External host with `corosync-qnetd` (TCP 5403, no latency requirement);
  `corosync-qdevice` on every node; `pvecm qdevice setup <ip>` copies node 1's key with
  `ssh-copy-id`, exchanges certificates and enables the service. Algorithm `ffsplit`;
  odd node counts need `--force` (`lms`) and are discouraged.
- Host it in a small Debian VM: `debian-13-genericcloud-amd64.qcow2` (SHA512SUMS) with a
  cloud-init NoCloud seed on a vvfat disk labelled `cidata`, served like the answer disk.
  The seed installs `corosync-qnetd`, sets the root key and a static IP on the cluster
  network.
- Needs: a second guest kind (cloud image instead of ISO), no cluster join, repository
  setup or online check for it, a votes check that counts the QDevice, `pvecm status`
  showing the Qdevice flag.
- The Debian guest kind is reusable: SMTP sinks, MinIO or Garage for PBS S3, external
  clients.

## Datacenter Manager

- 1.0 (December 2025), 1.1 (May 2026, answer file server, Ceph and snapshot views).
- Needs a multi-cluster topology on a shared network and remote registration with a PVE
  token and fingerprint (CLI not verified). Without PDM, `qm remote-migrate` between two
  clusters is still marked experimental.

## Cheap PVE add-ons

- SDN EVPN: `frr` and `frr-pythontools` on all nodes. The switch passes VLAN tags
  ([feasibility.md](feasibility.md)).
- HA fencing with an emulated hardware watchdog: QEMU `-device i6300esb` and
  `WATCHDOG_MODULE=i6300esb` in `/etc/default/pve-ha-manager`.
- Backup and restore jobs against PBS once it exists.

## Sources

[Automated Installation](https://pve.proxmox.com/wiki/Automated_Installation),
[Cluster Manager: QDevice](https://pve.proxmox.com/wiki/Cluster_Manager),
[pvecm source](https://git.proxmox.com/?p=pve-cluster.git;a=blob_plain;f=src/PVE/CLI/pvecm.pm),
[PBS storage](https://pve.proxmox.com/wiki/Storage:_Proxmox_Backup_Server),
[PBS roadmap](https://pbs.proxmox.com/wiki/Roadmap),
[PBS installation](https://pbs.proxmox.com/docs/installation.html),
[PMG roadmap](https://pmg.proxmox.com/wiki/Roadmap),
[PDM roadmap](https://pve.proxmox.com/wiki/Proxmox_Datacenter_Manager_Roadmap),
[PDM 1.0](https://forum.proxmox.com/threads/proxmox-datacenter-manager-1-0-stable.177321/),
[PDM 1.1](https://forum.proxmox.com/threads/proxmox-datacenter-manager-1-1-released.183903/),
[qm remote-migrate](https://pve.proxmox.com/pve-docs/qm.1.html),
[SDN](https://pve.proxmox.com/pve-docs/chapter-pvesdn.html),
[High Availability](https://pve.proxmox.com/wiki/High_Availability),
[Debian cloud images](https://cloud.debian.org/images/cloud/trixie/latest/)
