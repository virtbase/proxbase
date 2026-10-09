# CI and automation

GitHub-hosted Linux runners expose `/dev/kvm`, so they can run a real cluster. A
single node is ready in 2-3 minutes with a cached base image.

## GitHub Action

```yaml
jobs:
  integration:
    runs-on: ubuntu-latest
    timeout-minutes: 45
    steps:
      - uses: actions/checkout@v7
      - id: pve
        uses: virtbase/proxbase@v0      # or pin a commit SHA
        with:
          nodes: "1"
      - env:
          PVE_ENV: ${{ steps.pve.outputs.env-file }}
        run: |
          . "$PVE_ENV"
          go test ./... -tags integration
      - if: always()
        run: proxbase destroy ci --yes
```

The action enables KVM for the runner user, installs QEMU, downloads the release
binary and checks it against `checksums.txt` (and the cosign signature with
`verify-signature: "true"`), restores the ISO and base image from `actions/cache`,
and creates the cluster with `--golden`. On failure it prints the node logs.

| Input | Default | |
| --- | --- | --- |
| `version` | `latest` | Release tag, e.g. `v0.2.0` |
| `binary` | | Use this binary instead of downloading one |
| `name` | `ci` | Cluster name |
| `config` | | Cluster file; the inputs below override it |
| `nodes`, `memory`, `data-disks`, `storage`, `pve-version` | `1`, file defaults | As the `create` flags |
| `golden` | `true` | Clone from a cached base image |
| `cache` | `true` | Cache `~/.cache/proxbase` |
| `verify-signature` | `false` | Verify with cosign (install it first) |

| Output | |
| --- | --- |
| `endpoint` | `https://127.0.0.1:18001/` |
| `api-token` | `proxbase@pve!api=…` (masked in logs) |
| `ca-cert`, `ssh-key` | Paths of the cluster CA and the root key |
| `env-file` | Shell file with `PROXMOX_VE_*` and `PROXBASE_*`; source it |

Composite actions have no post step, so destroy the cluster with an `if: always()`
step. A complete workflow is in [examples/ci/github-actions.yml](../examples/ci/github-actions.yml);
the repository's own [E2E workflow](../.github/workflows/e2e.yml) uses the action
from the checkout with a freshly built binary.

## Other CI systems

Any Linux runner with KVM works: install QEMU and the binary, then

```bash
proxbase create ci --nodes 1 --golden --progress json   # JSON lines on stderr
proxbase status ci --check                              # exit code for health checks
eval "$(proxbase env ci)"
proxbase destroy ci --yes
```

Cache `~/.cache/proxbase` between runs to skip the ISO download and the base image
build.

## Machine-readable output

- `-o json` on `create`, `status` and `list` prints JSON on stdout.
- `--progress json` (any command) writes progress and messages to stderr as one
  JSON object per line:

  ```json
  {"time":"…","type":"step","cluster":"ci","node":"pve1","message":"pve1 installed in 1m51s","elapsed":111.5}
  {"time":"…","type":"error","error":"pve1: SSH not reachable: …","hint":"Re-run `proxbase create ci` to resume, or `proxbase destroy ci` to start over","log":"…/clusters/ci/logs"}
  ```

  `type` is `step`, `log`, `done` (last line on success) or `error` (last line on
  failure, with `hint` and `log` where known).
- The exit code is 0 on success and 1 on any error.
- For AI agents, [`proxbase mcp`](mcp.md) offers the same operations as MCP tools.
