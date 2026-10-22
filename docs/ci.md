# CI and automation

GitHub-hosted Linux runners expose `/dev/kvm`, so they can run a real cluster. A
single node is ready in well under a minute with a cached base image (21 s on a
desktop host; runners are slower), and in seconds with a [template](#cluster-templates).

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
| `template` | `false` | Cache the ready cluster as a live snapshot and resume it in later runs (see below) |
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

## Cluster templates

With `template: "true"` the first run creates the cluster, takes a
[live snapshot](lifecycle.md#live-snapshots) of it, stops it, saves the cluster
directory with `actions/cache/save` and resumes it from the snapshot. Later runs
restore that cache and resume the cluster in seconds instead of creating it.

```yaml
      - id: pve
        uses: virtbase/proxbase@v0
        with:
          nodes: "1"
          template: "true"
```

- The cache key covers the inputs (and the cluster file), the proxbase and QEMU
  versions and the CPU model: a live snapshot only loads into the same QEMU on the
  same CPU. A runner on another CPU model creates and caches its own template.
- If resuming fails, the action destroys the cluster and creates it as usual.
- The cached cluster includes its secrets (root password, SSH key, API token). They
  only reach a cluster bound to the runner's loopback address, but anyone who can
  read the repository's caches can read them.
- Each node needs cache space for its disk changes and its memory (about 2 GB for
  one idle 4 GiB node), next to the ISO and base image cache.

## Copilot coding agent

The Copilot coding agent runs on a GitHub Actions runner, which exposes `/dev/kvm`
like other GitHub-hosted Linux runners. A `copilot-setup-steps.yml` workflow
prepares it before the agent starts; with the proxbase action it gets a ready
cluster ([example](../examples/ci/copilot-setup-steps.yml)), and an MCP server entry
in the repository settings (Copilot > Coding agent) gives it the tools:

```json
{"mcpServers": {"proxbase": {"type": "local", "command": "proxbase", "args": ["mcp"], "tools": ["*"]}}}
```

List only the tools the agent needs in `tools` to narrow it down, or run the server
with `--read-only` or `--toolsets`. This repository's own
[setup steps](../.github/workflows/copilot-setup-steps.yml) install Go, QEMU and
golangci-lint and enable KVM, so the agent can run the e2e test.

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
