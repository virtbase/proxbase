# Feasibility research

Measurements and decisions behind Proxbase's design, taken before the Go
implementation existed. Host: Debian 13, AMD Ryzen 7 5800X (8C/16T), 31 GiB RAM,
QEMU 10.0.13, Proxmox VE 9.2-1 ISO. The numbers come from runs with prototype scripts.

## Unattended install from the stock ISO

The official automated installer needs an answer file. The documented way is
`proxmox-auto-install-assistant prepare-iso`, which only exists as a Debian package.
Three options were compared:

| Option | Result |
| --- | --- |
| Boot the installer kernel directly (`-kernel/-initrd/-append`) from the stock ISO, answer file on a FAT disk labelled `PROXMOX-AIS` | Works. Without `auto-installer-mode.toml` on the ISO the installer stops in a debug shell; on the serial console the driver runs `proxmox-fetch-answer partition PROXMOX-AIS >/run/automatic-installer-answers && exit` and the install continues. No root, no container, full log on serial. **Chosen.** |
| `prepare-iso --fetch-from partition` in a `debian:trixie` container | Works (3.7 s; it only adds a 59-byte `auto-installer-mode.toml`), but makes Docker a requirement. Kept as a fallback if the debug shell behaviour changes. |
| Rewrite the hybrid ISO in Go | Not pursued: preserving El Torito, EFI image and MBR/GPT hybrid layout is a lot of work for one small file. |

Details that matter:

- The `Automated` GRUB entry boots `linux26` with `ro ramdisk_size=16777216 rw quiet splash=silent proxmox-start-auto-installer`; adding `console=ttyS0,115200` moves the shell to serial.
- `proxmox-fetch-answer partition` needs the label argument.
- `[network] source = "from-answer"` needs a `filter` (`ERROR: Installation failed: no filter defined`).
- The ISO must stay attached as CD-ROM; the initrd looks for it.
- A QEMU `vvfat` drive (`file.driver=vvfat,file.label=PROXMOX-AIS`) serves the answer directory without creating a FAT image; on `usb-storage` it needs `read-only=on`.
- `reboot-mode = "power-off"` makes QEMU exit when the install is done, a reliable completion signal.

Install time: 105–115 s per node (4 vCPU, 8 GiB, qcow2 `cache=unsafe`), the same for
SeaBIOS and OVMF; two nodes installed in parallel without slowing down. SSH was up
8–14 s after booting the installed node.

## Booting installed nodes

- **PCI addresses must be fixed** and identical between install and run. With an extra
  NIC added without addresses, the installer-pinned `vmbr0` ended up on the wrong
  interface and the node was unreachable.
- Per-node OVMF variable stores must be kept if UEFI is used. SeaBIOS avoids that.
- Nested virtualization works: `svm` visible in the node, `/dev/kvm` present, an L2
  guest boots with `Hypervisor detected: KVM`.
- The API answers through a user-mode port forward; ticket login and the cluster join
  API work from the host.

## ISO download

At the time, `download.proxmox.com` (CDN) presented a certificate for
`enterprise.proxmox.com`; `https://enterprise.proxmox.com/iso/` served the same files
with a valid certificate. Hence: configurable mirror, that default, and SHA256
verification of every ISO.

## Rootless networking between nodes

Every node gets a QEMU user-mode NIC for internet and port forwards. For the internal
L2 segments four backends were tested between two nodes (ping, VLAN 100 ping, 500×
flood ping at 2 ms, two-node cluster, frames echoed back to the sender):

| Backend | Ping | Flood ping avg / max | Cluster | Own frames echoed |
| --- | --- | --- | --- | --- |
| `-netdev dgram` multicast on loopback | 0.23 ms | 0.126 ms | yes | yes |
| `-netdev socket,mcast=` on loopback | 0.21 ms | 0.123 ms | yes | 126 in 6 s |
| `-netdev stream` (TCP, point to point) | ok | 1.0 ms / 45.8 ms | yes | 0 |
| `-netdev dgram` unix sockets + userspace learning switch | 0.28 ms | 0.146 ms / 0.94 ms | yes | 0 |

- Multicast needs no host configuration when bound to `127.0.0.1`, but echoes each
  guest's frames back, which confuses Linux bridges in the node (FDB flapping, "received
  packet with own address" warnings), and anything on the host can join the group.
- `stream` is point to point only and showed TCP latency peaks of up to 46 ms.
- The unix-socket switch has no echo, natural isolation via socket files, passes VLAN
  tags and reached 123 MB/s even as a Python prototype. **Chosen**, implemented in Go
  as the per-cluster switch process.

A two-node cluster (create on node 1, join node 2 via `POST /cluster/config/join`
with the node 1 certificate fingerprint) was quorate 7 s after the join call and stayed
quorate across reboots and backend changes.
