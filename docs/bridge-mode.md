# Bridge mode (design, not implemented)

Today every internal network is a rootless segment: QEMU `-netdev dgram` sockets
connected by the per-cluster switch. The host cannot reach node addresses on those
networks; it only reaches the NAT forwards on `127.0.0.1`. Bridge mode would attach a
network to a real Linux bridge on the host instead, so nodes (and their guests) get
addresses the host and the LAN can reach.

## Config

```yaml
networks:
  - name: lan
    cidr: 192.168.50.0/24
    hostBridge: pbr0        # attach to this host bridge instead of the internal switch
    roles: [corosync]
```

Only the NIC backend changes; bridges, VLANs, MTU and roles inside the node stay as
they are. `doctor` would check that the bridge exists and that QEMU may use it.

## QEMU side

`-netdev bridge,id=netN,br=pbr0` with the same fixed PCI address and MAC as today.
QEMU runs `qemu-bridge-helper`, which creates a tap device and adds it to the bridge.
Nothing else in Proxbase needs privileges; the switch simply skips such networks.

## What the host admin has to grant (once, as root)

1. A bridge, for example with systemd-networkd or `/etc/network/interfaces`:
   `pbr0` with an address on the network (and NAT or a LAN uplink if wanted).
2. Allow the helper to use it:
   ```bash
   echo "allow pbr0" > /etc/qemu/bridge.conf
   chmod u+s /usr/lib/qemu/qemu-bridge-helper
   ```
   On this host the helper exists but is not setuid (`-rwxr-xr-x`) and
   `/etc/qemu/bridge.conf` is missing, so this is not possible without root today.

Alternative without a setuid helper: root pre-creates tap devices owned by the user
(`ip tuntap add pbtap0 mode tap user "$USER"` plus `ip link set pbtap0 master pbr0`) and
Proxbase uses `-netdev tap,ifname=pbtap0,script=no,downscript=no`. This needs one tap
per node and network, so it is less convenient.

## Not covered

Inside Docker, bridge mode needs `NET_ADMIN` and `/dev/net/tun`. The rootless
default stays the recommended mode.
