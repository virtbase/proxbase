# Examples

| Example | Use case |
| --- | --- |
| [single-node.yaml](single-node.yaml) | One node, local storage: UI, API clients, Ansible roles |
| [zfs-cluster.yaml](zfs-cluster.yaml) | Three nodes with a mirrored ZFS pool each |
| [ceph-hyperconverged.yaml](ceph-hyperconverged.yaml) | Ceph with two OSDs per node, RBD and CephFS, separate Ceph network |
| [networks.yaml](networks.yaml) | Separate management, cluster, storage, replication and guest (VLAN) networks |
| [terraform/](terraform) | Test Terraform/OpenTofu code with the bpg/proxmox provider |
| [ansible/](ansible) | Dynamic inventory and a playbook against the nodes |
| [ci/github-actions.yml](ci/github-actions.yml) | Integration tests against a real cluster in GitHub Actions |
| [compose/](compose) | Run a cluster with Docker Compose |

All cluster files are checked by `go test ./internal/config`. Run one with
`proxbase create -f examples/<file>.yaml`.
