# Getting started

## Requirements

- Linux on amd64 with KVM: `/dev/kvm` readable and writable by your user
- Nested virtualization (`kvm_intel`/`kvm_amd` with `nested=1`) if you want to run
  guests inside the Proxmox nodes
- QEMU 7.2 or newer: `qemu-system-x86_64` and `qemu-img`
- About 4 GiB RAM and 4 GiB disk per node (6 GiB RAM with Ceph), plus 1.7 GiB for the
  cached Proxmox ISO

Check the host:

```console
$ proxbase doctor
OK    kvm          /dev/kvm is read/writable
OK    nested       kvm_amd nested=1
OK    qemu         QEMU emulator version 10.0.13 (Debian 1:10.0.13+ds-0+deb13u1)
OK    qemu-img     found
OK    memory       24.5 GiB available (default node: 4 GiB)
OK    disk         168 GiB free in /home/you/.local/share (about 4 GiB per node + 1.7 GiB ISO cache)
OK    ports        127.0.0.1:18001-18003 and 18101-18103 free
OK    paths        state in /home/you/.local/share/proxbase, ISO cache in /home/you/.cache/proxbase
```

Install Proxbase as described in [install.md](install.md).

## Your first cluster

```console
$ proxbase create lab --nodes 3
[    0s] cluster lab: 3 node(s), state in /home/you/.local/share/proxbase/clusters/lab
[    0s] installing Proxmox VE 9.2-1 on pve1, pve2, pve3 (logs: …/logs/<node>-install.log)
[  117s] pve1 installed in 1m57s
…
[  171s] cluster quorate: 3/3 votes, /etc/pve writable on every node
[  175s] storage tank (zfspool) added for all nodes
[  176s] cluster lab is ready
created in 2m56s
Cluster lab: ready, quorate: yes, switch: running
NODE  VM       CLUSTER  IP           WEB UI                   SSH
pve1  running  online   10.10.10.11  https://127.0.0.1:18001  127.0.0.1:18101
pve2  running  online   10.10.10.12  https://127.0.0.1:18002  127.0.0.1:18102
pve3  running  online   10.10.10.13  https://127.0.0.1:18003  127.0.0.1:18103
```

The first create downloads and verifies the ISO (about 30 s on a fast line); later
creates use the cache. If a create fails, fix the cause and run the same command
again: it resumes where it stopped.

## Using the cluster

- **Web UI:** open `https://127.0.0.1:18001` and log in as `root` with the password
  from `proxbase env lab` (`PROXBASE_ROOT_PASSWORD`). The certificate is signed by the
  cluster's own CA.
- **SSH:** `proxbase ssh lab pve2` or `proxbase ssh lab pve2 -- pvecm status`.
- **API clients:** `eval "$(proxbase env lab)"` sets `PROXMOX_VE_ENDPOINT`,
  `PROXMOX_VE_API_TOKEN` and friends; `proxbase env lab --format terraform` prints a
  provider block for [bpg/proxmox](https://registry.terraform.io/providers/bpg/proxmox).

```bash
eval "$(proxbase env lab)"
curl --cacert "$PROXBASE_CA_CERT" -H "Authorization: PVEAPIToken=$PROXMOX_VE_API_TOKEN" \
  "${PROXMOX_VE_ENDPOINT}api2/json/cluster/status"
```

## Stop, start, clean up

```bash
proxbase stop lab      # clean shutdown of all nodes
proxbase start lab     # boots and waits for quorum (and Ceph health)
proxbase destroy lab --yes
```

`destroy` kills the VMs and deletes the cluster directory; nothing else is left on the
host except the ISO cache in `~/.cache/proxbase`.

## Next steps

- Write a cluster file: `proxbase config init cluster.yaml`, then
  `proxbase create -f cluster.yaml` ([configuration](configuration.md))
- Ceph instead of ZFS: `proxbase create ceph --nodes 3 --storage ceph` ([storage](storage.md))
- Separate networks and VLANs ([networking](networking.md))
- More [examples](../examples)
