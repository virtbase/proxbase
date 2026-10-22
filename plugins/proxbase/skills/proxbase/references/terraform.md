# Terraform and OpenTofu (bpg/proxmox)

1. Create a cluster with a baseline: `cluster_create {"name": "tf", "nodes": 1, "snapshot": "baseline"}`
   (3 nodes if the code places resources on several nodes or tests migration).
2. Write the provider block, which includes the SSH settings bpg/proxmox needs for
   file uploads and some resources:

   ```bash
   proxbase env tf --format terraform > provider.tf
   ```

   It sets `endpoint`, `api_token`, `insecure = true` (certificates are signed by the
   cluster CA, not a public one) and an `ssh` block with a `node` entry per node
   (127.0.0.1 and the forwarded SSH port). Do not commit it: it holds the token.
   Without a provider file, the same comes from the environment:
   `eval "$(proxbase env tf)"` sets `PROXMOX_VE_ENDPOINT`, `PROXMOX_VE_API_TOKEN`,
   `PROXMOX_VE_INSECURE` and `PROXMOX_VE_SSH_USERNAME`.
3. `tofu init && tofu apply -auto-approve` (or `terraform`). Check the result with
   `pve_get` (`/cluster/resources`, `/nodes/pve1/qemu/<vmid>/config`) instead of
   trusting the plan output.
4. Test idempotency: a second `plan` must show no changes. Test destroy:
   `tofu destroy -auto-approve`, then `pve_get /cluster/resources` must not list the
   resources.
5. Between runs: `cluster_reset`, and delete the local state
   (`rm -f terraform.tfstate*`): after a reset the cluster no longer has the resources
   the state lists.

Notes:

- Node names are `pve1`, `pve2`, …. Storages: `local`, `local-zfs` (ZFS root) and the
  data storage, `tank` (ZFS, the default) or `ceph-vm` and `cephfs` with Ceph;
  `pve_get /storage` lists them.
- Small test VMs (1 core, 512 MiB, no disk or OS) are enough to test create, update
  and destroy. For VMs that must boot, import a cloud image on the node first.
- For provider development (Go acceptance tests), export the environment from
  `proxbase env` and run `TF_ACC=1 go test ./... -run TestAcc…`.
