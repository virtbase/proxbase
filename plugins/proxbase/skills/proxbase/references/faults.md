# HA, fencing, partitions and Ceph recovery

Faults act on the node VMs and on the cluster's internal switch:

| `fault_apply` kind | Simulates | Undo |
| --- | --- | --- |
| `kill` (node) | Power loss | `cluster_start` boots it |
| `freeze` (node) | A hung node: up but silent | `thaw` or `fault_clear` |
| `link-down` (node, network) | Pulled cable | `link-up` or `fault_clear` |
| `partition` (groups, network) | Network split; unlisted nodes form one more group | `fault_clear` |
| `degrade` (delayMs, lossPercent, network) | Slow or lossy network | `fault_clear` |

`network` defaults to the corosync network. Measured on 3 nodes: a partition of
`[["pve3"]]` leaves pve3 without quorum while pve1 and pve2 stay quorate; a frozen node
drops out after about 20 s.

## HA failover test

1. `cluster_create {"name": "ha", "nodes": 3, "snapshot": "baseline"}`.
2. Create a small guest and an HA resource with `pve_call`, e.g.
   `POST /nodes/pve1/qemu {"vmid": 100, "memory": 512, "cores": 1}`, then
   `POST /cluster/ha/resources {"sid": "vm:100", "state": "started"}`.
   Save `snapshot_save {"name": "ha-ready", "live": true}` to repeat from here.
3. Break the node that runs it: `fault_apply {"kind": "kill", "node": "pve1"}`, or
   `partition` to test fencing (the isolated node must fence itself).
4. Watch `pve_get /cluster/ha/status/current` until the resource runs elsewhere.
   Fencing and recovery take about 2-3 minutes; poll, do not sleep in one long call.
5. `fault_clear`, `cluster_start` for killed nodes, then `cluster_reset {"snapshot": "ha-ready"}`
   for the next variant.

## Ceph recovery

- Needs `"storage": "ceph"` and 3+ nodes (6 GiB each).
- Fail an OSD: `node_exec {"node": "pve2", "command": "systemctl stop ceph-osd@1"}`;
  a node: `fault_apply kill`. Disk hot-unplug is not supported.
- Follow with `cluster_diagnose` (`ceph health detail`) or
  `node_exec "ceph -s"` until `HEALTH_OK` again.

## Network quality

`degrade {"delayMs": 50}` adds 50 ms per direction (about 100 ms RTT); use it to test
corosync timeouts, migration speed or application retries. Combine with `lossPercent`.
