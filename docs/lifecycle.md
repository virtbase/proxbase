# Lifecycle

## Create and resume

`proxbase create` runs a fixed sequence of steps; each step first checks what is
already done. If a step fails, the cluster is marked `failed`; run the same
`create` again (flags are ignored, the stored configuration is used) and it continues
where it stopped. `proxbase destroy` starts over.

## Start and stop

```bash
proxbase stop lab                # ACPI shutdown, hard power-off after --timeout (3m)
proxbase stop lab --timeout 0    # power off at once
proxbase start lab               # boot, wait for quorum, API and storage health
```

Nodes shut down their own guests first, so a guest that ignores ACPI can delay the
stop. With CephFS, Proxbase unmounts it on every node before the shutdown; otherwise
the kernel client would wait for monitors that are already gone.

`proxbase up` combines create/start with a foreground wait and a clean stop on
SIGTERM or SIGINT; it is meant for containers ([docker](docker.md)).

## Adding nodes

```bash
proxbase node add lab --count 2
```

New nodes are installed, configured and joined; ZFS pools, Ceph OSDs, managers and
MDS daemons (and a monitor while there are fewer than three) are created as
configured. `/etc/hosts` is updated on all nodes and the switch picks up the new
ports without a restart. Numbers of removed nodes are reused first.

## Removing nodes

```bash
proxbase node remove lab pve2
```

1. Refuses if guests are still on the node, or if a Ceph pool keeps more copies than
   nodes would remain (`--force` overrides both).
2. Ceph: marks the node's OSDs out, waits until `ceph osd safe-to-destroy` agrees,
   destroys the OSDs, MDS, manager and monitor, and removes the CRUSH host. It never
   shuts down a node that still has OSDs.
3. ZFS: removes the node from the storage entries.
4. Shuts the node down, runs the equivalent of `pvecm delnode`, deletes its disks.
5. Rewrites `/etc/hosts`, waits for quorum and re-runs the storage setup (for example
   to bring Ceph back to three monitors).

The removed node's number is recorded in `nodes.removed`; the other nodes keep their
addresses and ports.

## Snapshots

Snapshots are internal qcow2 snapshots of every disk of every node, taken while the
cluster is stopped, so they are consistent across nodes (corosync, Ceph, ZFS).

```bash
proxbase stop lab
proxbase snapshot save lab base
proxbase start lab
# … experiment …
proxbase stop lab
proxbase snapshot restore lab base
proxbase start lab
proxbase snapshot list lab
proxbase snapshot delete lab base
```

A snapshot can only be restored with the same set of nodes it was taken with.
Saving takes well under a second; restoring about a second.

## Access

```bash
proxbase ssh lab                    # first node
proxbase ssh lab pve2 -- pveversion # run a command
proxbase console lab pve1           # serial console; Enter for a login prompt, Ctrl-] to detach
proxbase env lab                    # shell exports
proxbase env lab --format json      # endpoint, token, CA, SSH key, root password, nodes
proxbase env lab --format terraform # provider block for bpg/proxmox
proxbase env lab --export ./creds   # id_ed25519, pve-root-ca.pem, api-token, env.sh
```

`proxbase ssh` checks host keys against those recorded at create time.

## Destroy

```bash
proxbase destroy lab --yes
```

Kills the VMs and the switch (also processes left over without pidfiles) and deletes
the cluster directory. Without `--yes` it asks for the cluster name on a terminal.
