# Ansible

Nodes are reachable by SSH as root on 127.0.0.1 with a forwarded port each; the API
on 127.0.0.1 with a forwarded port per node.

## Inventory

`proxbase env <cluster> --format json` has everything an inventory needs: per node
`name`, `sshHost`, `sshPort`; for all `sshUser`, `sshKey`, `endpoint`, `apiToken`,
`tokenId`, `tokenSecret` and `caCert`. The proxbase repository has a dynamic inventory
script (`examples/ansible/inventory.sh`) that turns it into groups and host variables:

```bash
PROXBASE_CLUSTER=lab ansible -i inventory.sh all -m ping
```

Host keys change whenever a cluster is created again, so disable checking for these
hosts (`-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null`).

## Roles and modules

- Roles that configure nodes (repositories, users, storage, networks) run against the
  `proxmox` hosts over SSH.
- Modules that use the API (`community.proxmox.*`, formerly
  `community.general.proxmox*`) take `api_host: 127.0.0.1`, `api_port` (the node's
  forwarded port), `api_user: proxbase@pve`, `api_token_id: api`, `api_token_secret`
  and `validate_certs: false` (or the CA from `caCert`).

## Loop

1. `cluster_create {"name": "ans", "nodes": 3, "snapshot": "baseline"}`.
2. Run the playbook; check the result with `pve_get` or `node_exec`.
3. Run it again: a second run must report no changes (idempotency).
4. `cluster_reset` before the next variant.
