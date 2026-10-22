---
name: proxbase
description: Test code against real Proxmox VE clusters that proxbase runs as QEMU/KVM VMs on this machine. Create a cluster in about a minute, point Terraform (bpg/proxmox), Ansible, API clients or scripts at it, reset it to a snapshot between runs, inject faults for HA and Ceph tests, and diagnose failures. Use when developing or testing anything that talks to Proxmox VE, or when a task needs a PVE cluster.
---

# Proxbase

Proxbase runs real Proxmox VE clusters on this host: every node is a QEMU/KVM VM
installed from the stock ISO, joined into a cluster, with ZFS or Ceph storage and an
API token. Use them as disposable test targets.

The proxbase MCP tools (`cluster_*`, `node_exec`, `pve_get`, `pve_call`, `task_*`,
`snapshot_*`, `fault_*`) do everything below. Without them, use the CLI (see the end).
`host_check` (or `proxbase doctor`) checks the host: KVM, QEMU, memory, ports. Each node needs about
4 GiB of RAM, 6 GiB with Ceph.

## The loop

1. **Reuse first.** `cluster_list`. Use a ready cluster of the right shape if it is
   yours to use (`owner` says who created it). Name new clusters after the task or
   worktree (lowercase letters, digits and dashes, at most 15 characters), not
   `default`.
2. **Create with a baseline.**
   `cluster_create {"name": "tf-vm", "nodes": 1, "snapshot": "baseline", "ttlMinutes": 240}`.
   The lifetime lets `cluster_gc` clean up the cluster if nobody destroys it;
   extend it with `cluster_ttl` while you still need it.
   If the result has `running: true`, call `cluster_wait` until `running: false`.
   The first create on a host downloads the ISO and builds a base image (10-20
   minutes); later creates take under a minute (Ceph about 1.5 minutes).
   - 1 node: API clients, Terraform providers, Ansible roles.
   - 3 nodes: clustering, migration, HA.
   - `"storage": "ceph"` (3+ nodes): Ceph. `"storage": "none"`: no data pool.
   - Networks, VLANs, disks: pass a cluster file as `config` (YAML); check it
     with `config_validate`, the schema is the server's resource. Fields sit at
     the top level (no `metadata` or `spec`), for example:

     ```yaml
     nodes:
       count: 3
       defaults: {memory: 6G, dataDisks: [{size: 32G}, {size: 32G}]}
     networks:
       - {name: cluster, cidr: 10.10.10.0/24, roles: [corosync]}
       - {name: storage, cidr: 10.10.20.0/24, roles: [ceph-public, ceph-cluster]}
     storage:
       ceph: {enabled: true}
     ```

   - Each new cluster gets its own free port range; `cluster_plan` shows the ports,
     the memory and disk a create needs and what the host has, without creating.
3. **Connect the code under test** with `cluster_env`: endpoint, API token,
   CA certificate. In a shell, `eval "$(proxbase env <cluster>)"` exports
   `PROXMOX_VE_ENDPOINT`, `PROXMOX_VE_API_TOKEN`, `PROXBASE_CA_CERT` and more.
4. **Run the tests, then look.** `pve_get` reads the API (`/cluster/resources`,
   `/nodes/pve1/qemu`, `/cluster/ha/status/current`); `pve_call` changes things and
   waits for the task it starts. `node_exec` runs shell commands as root
   (`qm config 100`, `journalctl -u pvedaemon -n 50`). Prefer `pve_get`/`pve_call`
   to `node_exec pvesh`: no quoting, JSON parameters. `file_put` and `file_get` copy
   configs, scripts and fixtures to and from nodes; `node_add` and `node_remove`
   change the cluster's size (follow them with `cluster_wait`).
5. **When something fails, diagnose before guessing.** `cluster_diagnose` returns,
   per node, quorum, failed units, failed tasks, errors of corosync, pmxcfs,
   pveproxy, pvedaemon, HA and Ceph, the clock offset and `ceph health detail`.
   Recent failed tasks: `task_list {"errorsOnly": true}`; a task's log: `task_log`;
   a task `pve_call` left running: `task_wait`.
6. **Reset between iterations.** `cluster_reset` returns to `baseline` in seconds
   (a live snapshot resumes with its memory). Do not clean up by hand or create a
   new cluster for each run. Save more snapshots with
   `snapshot_save {"live": true}` at interesting points.
7. **Clean up.** `cluster_destroy {"cluster": "tf-vm", "confirm": "tf-vm"}` for
   clusters you created for the task, unless the user wants to keep them.

## Rules

- Do not destroy, reset or break clusters you did not create without asking: they
  may hold someone's work.
- If a tool you need is missing, the user may run the server with `--read-only` or
  `--toolsets`; say which tool you need instead of working around it.
- Tool calls return before the client's timeout (50 s for clients other than Claude
  Code). `running: true` means call `cluster_wait`; `cluster_start` with
  `healthy: false` and "still starting" means poll `cluster_status`. In `node_exec`,
  run long commands in the background (`nohup cmd >/root/job.log 2>&1 &`) and poll.
- The API token has full privileges on the lab cluster; the root password is only
  returned with `includeRootPassword`. Keep credentials out of commits and answers.
  To test with least privilege (or that an operation is refused), create a scoped
  token with `token_create {"name": "ro", "role": "PVEAuditor"}` and pass its
  `apiToken` (or `cluster_env {"token": "ro"}`) to the code under test.
- Guests inside the nodes use nested virtualization: keep them small (512 MiB to
  1 GiB) so a 4 GiB node can run them.
- Faults (`fault_apply`) stay until `fault_clear`; killed nodes need `cluster_start`.
  `cluster_stop` clears all faults.

## Use cases

- Terraform or OpenTofu with bpg/proxmox: [references/terraform.md](references/terraform.md)
- Ansible against nodes and the API: [references/ansible.md](references/ansible.md)
- HA, fencing, partitions and Ceph recovery: [references/faults.md](references/faults.md)
- CI with the GitHub Action: [references/ci.md](references/ci.md)

## Without the MCP tools

| Tool | CLI |
| --- | --- |
| `cluster_list`, `cluster_status` | `proxbase list`, `proxbase status <c> [-o json] [--check]` |
| `cluster_plan` | `proxbase create <c> --nodes 3 --dry-run` |
| `cluster_create` + `cluster_wait` | `proxbase create <c> --nodes 3 --golden --snapshot baseline --ttl 4h` (blocks) |
| `cluster_env` | `proxbase env <c> [--format shell\|json\|terraform] [--token <name>]` |
| `token_create`, `token_list`, `token_delete` | `proxbase token create <c> <name> --role PVEAuditor --path /` |
| `node_exec` | `proxbase ssh <c> <node> -- <command>` |
| `file_put`, `file_get` | `proxbase cp <file> <c>:<node>:/path`, `proxbase cp <c>:<node>:/path <file>` |
| `node_add`, `node_remove` | `proxbase node add <c> --count 1`, `proxbase node remove <c> <node>` |
| `pve_get`, `pve_call` | `proxbase ssh <c> -- pvesh get <path> --output-format json` |
| `task_list`, `task_log` | `proxbase ssh <c> -- pvesh get /nodes/<node>/tasks` (`…/tasks/<upid>/log`) |
| `cluster_reset` | `proxbase stop <c> --timeout 0 && proxbase snapshot restore <c> baseline` (live snapshots resume; disk snapshots need `proxbase start <c>`) |
| `snapshot_*` | `proxbase snapshot save\|restore\|delete\|list <c> [<name>] [--live]` |
| `fault_apply`, `fault_clear` | `proxbase fault kill\|freeze\|thaw\|link\|partition\|degrade\|clear <c> …` |
| `cluster_destroy` | `proxbase destroy <c> --yes` |
| `cluster_ttl`, `cluster_gc` | `proxbase ttl <c> 2h`, `proxbase gc --yes` |
