# Fault injection

`proxbase fault` breaks a running cluster on purpose, to test HA, fencing, Ceph
recovery or your own applications. Node faults act on the node's VM (QEMU); network
faults act on the cluster's internal switch, per network.

| Command | Simulates | How |
| --- | --- | --- |
| `fault kill [cluster] <node>` | Power loss | SIGKILL of the QEMU process |
| `fault freeze [cluster] <node>` | A hung node (still "up", but silent) | QMP `stop`; `fault thaw` resumes it |
| `fault link [cluster] <node> --down\|--up [--network N]` | Pulled cable | QMP `set_link`: the node sees no carrier |
| `fault partition [cluster] <group>... [--network N]` | Network split | Switch drops frames between groups |
| `fault degrade [cluster] --delay 20ms --loss 1 [--network N]` | Slow or lossy network | Switch delays or drops frames (per direction) |
| `fault clear [cluster]` | Repair | Thaws, reconnects, removes network faults |

`--network` defaults to the corosync link0 network. Groups for `partition` are
comma-separated node lists; nodes not listed form one more group, so
`fault partition lab pve3` isolates pve3. Killed nodes stay off until
`proxbase start`. `proxbase status` lists active faults, and `stop` clears them.

## Examples and what to expect

Measured on a 3-node cluster:

| Fault | Effect |
| --- | --- |
| `fault partition lab pve3` | pve3: `Quorate: No`, 1 vote; pve1/pve2: quorate with 2 votes |
| `fault degrade lab --delay 50ms` | ping between nodes: RTT about 102 ms |
| `fault degrade lab --loss 30` | ping loss about 51% (30% in each direction) |
| `fault link lab pve2 --down` | pve2's NIC and bridge show `NO-CARRIER`, ping 100% loss |
| `fault freeze lab pve2` | after about 20 s the others continue with 2 votes |
| `fault kill lab pve3` | pve3 `stopped`/`offline`; `proxbase start lab` brings it back quorate |

A typical HA test:

```bash
proxbase fault partition lab pve3   # pve3 loses quorum, HA fences it
sleep 120
proxbase ssh lab pve1 -- ha-manager status
proxbase fault clear lab
```

## Not covered

Disk failures (hot-unplugging a data disk) need PCIe hotplug slots; the nodes'
disks sit on the root bus, which does not support hotplug. To fail an OSD, stop its
service inside the node instead: `proxbase ssh lab pve2 -- systemctl stop ceph-osd@1`.
