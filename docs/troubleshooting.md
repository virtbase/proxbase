# Troubleshooting

## Where to look

Everything of a cluster lives in `~/.local/share/proxbase/clusters/<name>/`:

| Path | Content |
| --- | --- |
| `logs/<node>-install.log` | Serial console of the installer |
| `logs/<node>-qemu.log` | QEMU errors during the install |
| `logs/<node>-console.log` | Serial console of the running node |
| `logs/switch.log` | Internal network switch |
| `state.json`, `cluster.yaml` | Progress and the resolved configuration |
| `secrets/` | Root password, SSH key, API token, CA (mode 0600) |

`proxbase status <name>` shows the last error of a failed create; re-running
`proxbase create <name>` resumes it.

## Common problems

| Symptom | Cause and fix |
| --- | --- |
| `KVM is required: … permission denied` | Add your user to the `kvm` group (or the group owning `/dev/kvm`) and log in again. In Docker: `group_add` with the host's kvm GID |
| `port 18001 for pve1 is in use` | Another cluster or program uses the ports; set `access.portBase` |
| `socket path … is too long` | Unix socket paths are limited to 107 bytes; use a shorter `XDG_DATA_HOME` or cluster/network names |
| Install times out or `installer reported failure` | Read `logs/<node>-install.log`; the last lines are also in the error |
| `fetch checksums` fails | The ISO mirror is unreachable; set `proxmox.mirror` |
| `Hash Sum mismatch` during apt (nodes or image build) | A broken mirror or path (seen over IPv6). Retry; for image builds pass `--build-arg APT_OPTS="-o Acquire::ForceIPv4=true"` |
| `ceph not ready after 10m0s` | The error includes `ceph -s` and `ceph health detail`; usually too little memory per node |
| Nested guests are slow or fail to start | Nested virtualization is off on the host (`proxbase doctor`) |
| `cluster "x" is busy` | Another proxbase command holds the cluster lock |

## Starting over

```bash
proxbase destroy <name> --yes
```

This also kills processes that reference the cluster directory but lost their
pidfiles. The ISO cache (`~/.cache/proxbase`) is kept; delete it to free 1.7 GiB.
