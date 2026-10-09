# Storage

The root disk of every node is installed with ZFS (RAID0 on one disk) by default
(`nodes.defaults.rootDisk.filesystem`). Data disks (`vdb`, `vdc`, …) are used by one
of the storage backends.

## ZFS

Default: one pool `tank` over all data disks of each node, registered as storage
`tank` (type `zfspool`, content `images,rootdir`, thin provisioned) for all nodes.

```yaml
nodes:
  defaults:
    dataDisks: [{size: 32G}, {size: 32G}]
storage:
  zfs:
    - {name: tank, raid: mirror, disks: [vdb, vdc]}
```

Every node has its own pool with the same name; live migration of VMs on it copies
the disks (`qm migrate --with-local-disks`). Pools are created through the Proxmox
API (`POST /nodes/{node}/disks/zfs`, `ashift=12`).

## Ceph

```bash
proxbase create ceph --nodes 3 --storage ceph
```

Proxbase installs Ceph from the no-subscription repository and builds a
hyperconverged cluster:

- monitors on three nodes (fewer on smaller clusters), a manager and an MDS on every node
- one OSD per OSD disk (default: the data disks not used by ZFS)
- RBD pool `ceph-vm`: size 3, min size 2, 32 placement groups, autoscaler off,
  registered as storage `ceph-vm` (content `images,rootdir`)
- CephFS `cephfs`, mounted on every node at `/mnt/pve/cephfs` and registered as storage
  with content `iso,vztmpl,backup,snippets,import`, so uploaded ISOs and templates
  are visible on all nodes
- public and cluster networks from the `ceph-public` and `ceph-cluster` roles
  ([networking](networking.md))

`create` and `start` wait until Ceph reports `HEALTH_OK`, every OSD is up and in, all
placement groups are `active+clean` and the Ceph storages are active on every node.

Notes:

- Ceph needs RAM: the default node memory becomes 6G; less only prints a warning.
- The Proxmox VE 9.2 ISO ships a storage library that rejects keys of current Ceph
  releases ("Not a proper rbd authentication file"). Proxbase upgrades
  `libpve-storage-perl` (and the packages it depends on) when it installs Ceph.
- With fewer than three nodes, set `storage.ceph.pools[].size` to the node count.
  `size: 2` with `minSize: 2` stops I/O while one node is down; Proxbase warns.
- Ceph and ZFS can be combined on different data disks:

```yaml
nodes:
  defaults:
    dataDisks: [{size: 32G}, {size: 32G}]
storage:
  zfs: [{name: tank, disks: [vdb]}]
  ceph: {enabled: true}          # uses vdc
```

## Removing nodes

`node remove` drains the node's OSDs (`ceph osd out`, then waits for
`safe-to-destroy`), destroys its OSDs, MDS, manager and monitor, and creates a
replacement monitor on another node. It refuses if a pool keeps more copies than
nodes would remain (`--force` accepts a degraded pool). ZFS storage entries drop the
node from their node list. See [lifecycle](lifecycle.md).
