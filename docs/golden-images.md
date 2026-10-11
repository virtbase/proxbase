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

Measured on the same host (3 nodes, 4 GiB each, ISO cached):

| | Installer | Golden image (cached) |
| --- | --- | --- |
| ZFS create, 1 node | – | 21s |
| ZFS create | 2m59s | 49s |
| Ceph create | – | 1m30s |

The first golden create builds the base image (1m56s; 3m43s with Ceph, which
downloads the Ceph packages) and takes about as long as a normal create.

## How it works

- A base image is the root disk of an installed node, cached in
  `~/.cache/proxbase/images/<key>/`. The key covers the ISO, the root filesystem and
  size, keyboard, country and time zone, and the Ceph release if the cluster uses
  Ceph; nodes with different root disks use different base images.
- Each node's root disk is a qcow2 overlay of the base image; data disks are created
  fresh. The base image must stay while clusters use it, so `image prune` keeps those.
- The installer installs a first-boot hook (package `firstboot`) that runs on the
  system's first boot, before networking and before any Proxmox VE service starts.
- **Baking.** After the install, the base image boots once with a bake script: it
  removes the GRUB menu wait, marks the appliance list as fetched (no `pveupdate` on
  first boot) and, for Ceph clusters, installs the Ceph packages and the newer
  `libpve-storage-perl` they need. Then it powers off before any Proxmox VE service
  has started, and the hook stays pending for the clones.
- **Identity.** Each clone gets its identity as a QEMU fw_cfg file. The hook applies
  it on the clone's first boot, before networking: host name (`/etc/hostname`,
  `/etc/hosts`, search domain, postfix), new SSH host keys, a new machine-id, the root
  password and the authorized keys. Everything Proxmox VE creates on its first start
  (node certificate, root's SSH key; the cluster CA and auth key on the first node)
  is therefore created per node, under the right name. No reboot is needed.
- Base images from Proxbase versions before the first-boot hook have a different
  key, so the first golden create after an upgrade builds a new one; the old ones
  stay for the clusters that use them (`image prune` removes them afterwards). A
  create interrupted under an older version resumes by personalizing the remaining
  clones over SSH, with a reboot.

Verified per node and across clusters: host keys, machine-ids, node certificates and
cluster CAs all differ.

## When not to use it

- The base image is from the ISO; `proxmox.upgrade` still runs on every node.
- To test the Proxmox installer itself, use the normal create.
