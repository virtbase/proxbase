# Configuration

A cluster is described by a YAML file (`apiVersion: proxbase.dev/v1alpha1`,
`kind: Cluster`). Every field has a default, so the smallest file is just a name.
Flags of `create` and `up` override fields of the file.

```bash
proxbase config init cluster.yaml      # annotated example
proxbase config validate cluster.yaml  # strict parsing (unknown fields are errors) and checks
proxbase config schema > cluster.schema.json
proxbase create -f cluster.yaml --nodes 5 --dry-run   # resolved file with all defaults
```

The schema enables completion and validation in editors; add
`# yaml-language-server: $schema=./cluster.schema.json` at the top of the file.
When a cluster exists, its resolved file is stored as `cluster.yaml` in the cluster
directory and is the source of truth for `start`, `node add` and friends.

## Example

```yaml
apiVersion: proxbase.dev/v1alpha1
kind: Cluster
name: lab
proxmox:
  version: "9.2"
nodes:
  count: 3
  defaults:
    cpus: 4
    memory: 4G
    dataDisks: [{size: 32G}]
networks:
  - name: cluster
    cidr: 10.10.10.0/24
    roles: [corosync]
storage:
  zfs:
    - {name: tank, raid: single, disks: [vdb]}
```

More in [examples/](../examples).

## Reference

### Top level

| Field | Default | Notes |
| --- | --- | --- |
| `name` | `default` | Cluster and corosync cluster name: lowercase, up to 15 characters |
| `proxmox` | | Installation, see below |
| `nodes` | | Node count and sizes |
| `networks` | one network `cluster` | Internal networks |
| `storage` | ZFS pool `tank` | Data storage on the data disks |
| `access` | | Host ports and API token |

### `proxmox`

| Field | Default | Notes |
| --- | --- | --- |
| `version` | `9.2` | ISO release: `9.2` picks the newest `9.2-N`, `9.2-1` is exact |
| `mirror` | `https://enterprise.proxmox.com/iso` | Base URL with the ISOs and `SHA256SUMS` |
| `timezone` | `UTC` | |
| `keyboard` | `en-us` | Installer keyboard layout |
| `country` | `us` | |
| `domain` | `proxbase.internal` | Nodes are `pveN.<domain>` |
| `sshKeys` | none | Extra public keys for root, next to the generated one |
| `upgrade` | `false` | `apt dist-upgrade` on every node before clustering; reboots into a new kernel |
| `goldenImage` | `false` | Clone nodes from a cached base image instead of installing each ([golden images](golden-images.md)) |

### `nodes`

| Field | Default | Notes |
| --- | --- | --- |
| `count` | `1` | 1 to 16 |
| `namePattern` | `pve{n}` | `{n}` is the node number |
| `defaults` | | Node settings, see below |
| `overrides` | none | Per node, keyed by name: `{pve1: {memory: 8G}}` |
| `removed` | none | Written by `node remove`; the remaining nodes keep their numbers |

Node settings (`defaults`, `overrides.<name>`):

| Field | Default | Notes |
| --- | --- | --- |
| `cpus` | `4` | 1 to 64 |
| `memory` | `4G` (`6G` with Ceph) | `512M`, `8G`; at least 2G |
| `nested` | `true` | Expose VMX/SVM so guests inside the node can use KVM |
| `rootDisk.size` | `32G` | |
| `rootDisk.filesystem` | `zfs` | `zfs` (RAID0 on one disk), `ext4`, `xfs`, `btrfs` |
| `dataDisks` | one `32G` disk | Up to 8; they appear as `vdb`, `vdc`, … in the node. `[]` for none |

### `networks`

Each network adds a NIC, a bridge and (with `cidr`) the address `.1N` on node N
(`pve1` = `.11`). See [networking](networking.md).

| Field | Default | Notes |
| --- | --- | --- |
| `name` | required | Short lowercase identifier |
| `cidr` | none | IPv4 prefix; omit for an L2-only network (bridge without addresses) |
| `bridge` | `vmbr<position>` | `vmbr1`, `vmbr2`, …; `vmbr0` is the NAT uplink |
| `vlanAware` | `false` | VLAN-aware bridge |
| `vlans` | `2-4094` | Allowed VLANs on a VLAN-aware bridge: `["100", "200-299"]` |
| `mtu` | unset (1500) | 576 to 9000 |
| `roles` | `[corosync]` on the first network with a cidr | `corosync` (up to two: link0, link1), `ceph-public`, `ceph-cluster`, `migration` |

At most 8 networks; prefixes must not overlap each other or `10.0.2.0/24` (NAT).

### `storage`

`zfs` is a list of pools, created on every node:

| Field | Default | Notes |
| --- | --- | --- |
| `name` | required | Pool and storage ID |
| `disks` | all data disks | Device names: `[vdb, vdc]` |
| `raid` | `single` (1 disk), `mirror` | `single`, `mirror`, `raid10`, `raidz`, `raidz2`, `raidz3` with their minimum disk counts |

Without data disks or with Ceph enabled there is no default pool; `zfs: []` disables it.

`ceph` (see [storage](storage.md)):

| Field | Default | Notes |
| --- | --- | --- |
| `enabled` | `false` | `--storage ceph` sets it |
| `version` | `squid` | `squid` or `tentacle` |
| `osdDisks` | data disks not used by ZFS | Device names, one OSD each |
| `pools` | `ceph-vm` | RBD pools, see below |
| `cephfs` | `true` | CephFS storage `cephfs` for ISOs, templates, backups, snippets, imports |

Pool fields: `name`, `size` (`3`), `minSize` (`2`, or `size` if smaller), `pgNum`
(`32`, power of two, autoscaler off), `application` (`rbd`). Ceph needs at least
`size` nodes; for one or two nodes set the pool `size` explicitly.

### `access`

| Field | Default | Notes |
| --- | --- | --- |
| `portBase` | `18000` | Node N: web UI/API on `portBase+N`, SSH on `portBase+100+N` |
| `bindAddress` | `127.0.0.1` | Host address of the forwards; `0.0.0.0` in the container image |
| `apiToken` | `true` | Create `proxbase@pve!api` with Administrator on `/` |

## Flags

| Flag | Field |
| --- | --- |
| `--nodes` | `nodes.count` |
| `--cpus`, `--memory`, `--disk` | `nodes.defaults.cpus`, `.memory`, `.rootDisk.size` |
| `--data-disks 2x32G`, `32G,64G`, `none` | `nodes.defaults.dataDisks` (resets the default ZFS pool) |
| `--storage zfs\|ceph\|none` | `storage` |
| `--pve-version` | `proxmox.version` |
| `--golden` | `proxmox.goldenImage` |
| `--bind-address` (or `$PROXBASE_BIND_ADDRESS`) | `access.bindAddress` |
