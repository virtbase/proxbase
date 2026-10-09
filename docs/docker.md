# Docker

Proxbase runs well in a container on a Linux host with KVM: the nodes are VMs inside
the container, the internal networks are unix sockets, and no `privileged` mode or
extra capabilities are needed, only `/dev/kvm` and its group.

```bash
cd examples/compose
echo "KVM_GID=$(getent group kvm | cut -d: -f3)" > .env && mkdir -p export
docker compose up -d
docker compose logs -f
docker compose exec proxbase proxbase status
```

[examples/compose/docker-compose.yml](../examples/compose/docker-compose.yml):

- runs `proxbase up -f /config/cluster.yaml`: creates the cluster (or resumes a failed
  create, or starts the existing one from the volume), waits, and shuts all nodes down
  cleanly on SIGTERM; `stop_grace_period: 3m` gives it the time
- `devices: [/dev/kvm]` and `group_add: [$KVM_GID]`: the image runs as uid 1000
- volume `/data`: cluster state and the ISO cache (`XDG_DATA_HOME`, `XDG_CACHE_HOME`)
- ports `127.0.0.1:18001-18003` (web UI/API) and `127.0.0.1:18101-18103` (SSH); inside
  the container the forwards listen on `0.0.0.0` (`PROXBASE_BIND_ADDRESS`)
- health check `proxbase status --check`: exit 0 only when the cluster is ready,
  quorate, all nodes are online and Ceph (if any) is `HEALTH_OK`

Lifecycle:

| Command | Effect |
| --- | --- |
| `docker compose up -d` | create (first time, about 4 minutes) or start (about 30 s) |
| `docker compose restart` | clean shutdown, start again from the volume |
| `docker compose down` | clean shutdown; the cluster stays in the volume |
| `docker compose down -v` | also deletes the volume: nothing is left |

Credentials for clients on the host:

```bash
docker compose exec proxbase proxbase env --export /export
. ./export/env.sh
curl --cacert "$PROXBASE_CA_CERT" -H "Authorization: PVEAPIToken=$PROXMOX_VE_API_TOKEN" \
  "${PROXMOX_VE_ENDPOINT}api2/json/version"
```

Limits:

- Linux hosts with KVM only, and nested virtualization for guests inside the nodes.
  Docker Desktop on macOS and Windows does not provide `/dev/kvm`.
- A 3-node ZFS cluster needs about 12 GiB RAM and 15 GiB in the volume; Ceph about 18 GiB RAM.
- The image is built for linux/amd64.

The image also runs the MCP server for AI agents (`… ghcr.io/virtbase/proxbase mcp`);
see [mcp.md](mcp.md#setup) for the client configuration.

Building the image yourself: `docker build -t proxbase .` (source build) or the
release image `ghcr.io/virtbase/proxbase`. On networks where the Debian mirror is
unreliable over IPv6, pass `--build-arg APT_OPTS="-o Acquire::ForceIPv4=true"`.
