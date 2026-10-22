# CI with the GitHub Action

GitHub-hosted Linux runners expose `/dev/kvm`. The action installs QEMU and proxbase,
enables KVM, caches the ISO and base image, and creates the cluster:

```yaml
jobs:
  integration:
    runs-on: ubuntu-latest
    timeout-minutes: 45
    steps:
      - uses: actions/checkout@v7
      - id: pve
        uses: virtbase/proxbase@v0      # pin a commit SHA in real workflows
        with:
          nodes: "1"
          # config: test/cluster.yaml   # cluster file
          # template: "true"            # cache the ready cluster, resume it in seconds next time
      - env:
          PVE_ENV: ${{ steps.pve.outputs.env-file }}
        run: |
          . "$PVE_ENV"   # PROXMOX_VE_ENDPOINT, PROXMOX_VE_API_TOKEN, PROXBASE_CA_CERT, ...
          go test ./... -tags integration
      - if: always()
        run: proxbase destroy ci --yes
```

Outputs: `endpoint`, `api-token`, `ca-cert`, `ssh-key`, `env-file`. Composite actions
have no post step, so destroy the cluster explicitly.

When adding CI for a project:

- Run the same tests locally against a proxbase cluster first; CI only repeats them.
- Keep clusters small (1 node unless the test needs more): runners have 16 GB.
- On failure, print diagnostics in a step with `if: failure()`:
  `proxbase status ci` and `proxbase ssh ci -- journalctl -p err -n 100`.
- Some days a runner lands on a host without nested virtualization; the action then
  fails early with "/dev/kvm does not exist". Re-run the job.
