# Golden images

With `--golden` (or `proxmox.goldenImage: true`), Proxbase installs Proxmox VE once into
a cached base image and clones nodes from it instead of running the installer for
every node.

```bash
proxbase create lab --nodes 3 --golden      # first time: builds the base image
proxbase create lab2 --nodes 3 --golden     # later: clones, no installer
proxbase image list                         # base images and the clusters using them
proxbase image prune                        # delete base images no cluster uses
```

Measured on the same host (3 nodes, ISO cached):

| | Installer | Golden image (cached) |
| --- | --- | --- |
| ZFS create | 3m04s | 1m36s |
| Ceph create | 4m43s | 3m03s |
| `node add` | 3m21s | 1m14s |

The first golden create builds the base image (about 1m40s) and takes about as long
as a normal create.

## How it works

- A base image is the root disk of an installed but **never booted** node, cached in
  `~/.cache/proxbase/images/<key>/`. The key covers the ISO, the root filesystem and
  size, and keyboard, country and time zone; nodes with different root disks use
  different base images.
- Each node's root disk is a qcow2 overlay of the base image; data disks are created
  fresh. The base image must stay while clusters use it, so `image prune` keeps those.
- Everything Proxmox VE creates on first boot is created per node: the cluster CA, the
  auth key, the node certificate and root's SSH key.
- What the installer wrote is replaced before clustering, over SSH with the base image's
  own key: host name (`/etc/hostname`, `/etc/hosts`, postfix), SSH host keys,
  machine-id, root password and authorized keys. The node reboots, the base image's
  node directory in `/etc/pve` is removed, and the new node certificate is checked.
- A create interrupted during personalization resumes correctly: nodes that already
  have their identity are only finished.

Verified per node and across clusters: host keys, machine-ids, node certificates and
cluster CAs all differ.

## When not to use it

- The base image is from the ISO; `proxmox.upgrade` still runs on every node.
- To test the Proxmox installer itself, use the normal create.
