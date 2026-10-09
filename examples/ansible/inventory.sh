#!/bin/sh
# Ansible dynamic inventory for a proxbase cluster, from `proxbase env --format json`.
#   ansible -i examples/ansible/inventory.sh all -m ping
# Set PROXBASE_CLUSTER to pick a cluster (default: the only one).
set -eu
proxbase env ${PROXBASE_CLUSTER:+"$PROXBASE_CLUSTER"} --format json | python3 -c '
import json, sys
env = json.load(sys.stdin)
hosts = {n["name"]: {"ansible_host": n["sshHost"], "ansible_port": n["sshPort"]} for n in env["nodes"]}
print(json.dumps({
    "proxmox": {"hosts": list(hosts)},
    "_meta": {"hostvars": hosts},
    "all": {"vars": {
        "ansible_user": env["sshUser"],
        "ansible_ssh_private_key_file": env["sshKey"],
        "ansible_ssh_common_args": "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null",
        "proxmox_api_url": env["endpoint"],
        "proxmox_api_token": env["apiToken"],
    }},
}))'
