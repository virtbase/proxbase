# AGENTS.md

Guidance for coding agents working on this repository. Proxbase is one Go binary
that creates real Proxmox VE clusters as QEMU/KVM VMs; `docs/architecture.md`
describes the packages, the create flow and the state on disk.

## Build and test

```bash
make build                       # bin/proxbase
go test -race ./...              # unit tests, no KVM needed
golangci-lint run ./...          # v2.15.0, as CI (.golangci.yml)
make schema                      # after changing internal/config (a test fails otherwise)
make packaging                   # man pages, completions, metainfo in build/ (as GoReleaser)
```

End-to-end, on a Linux host with `/dev/kvm` (about a minute once the ISO and base
image are cached, 10-20 minutes the first time):

```bash
bin/proxbase doctor
PROXBASE_BIN=$PWD/bin/proxbase go test -tags e2e -run TestE2E -timeout 40m -v ./internal/mcpserver
```

The e2e test creates and destroys its own cluster (`mcp-e2e`, or
`PROXBASE_E2E_CLUSTER`). Do not stop, reset or destroy clusters you did not create:
`proxbase list` shows their owners.

## Code

- Follow https://go.dev/doc/effective_go and the code around the change: short
  doc comments that say why, errors wrapped with context, no panics for expected
  errors, `context` for everything that waits.
- Go skills are in `.agents/skills/` (`golang-*`); use them for design, errors,
  concurrency, testing and security questions.
- Tests are table-driven with named subtests and test behaviour; shell scripts
  that run on nodes are tested against a real cluster.
- MCP tools (`internal/mcpserver`) need a description, input and output types with
  `jsonschema` descriptions, read-only or destructive annotations, and a place in
  a toolset (`toolsets.go`; a test enforces it). Long operations must return
  within the call limit (`h.limit(req)`).
- A new or renamed command needs its man page in the Homebrew cask list in
  `.goreleaser.yaml` (`TestCaskManpages` in `cmd/gendist` prints the list).
- User-facing changes update the docs in the same change: `docs/cli.md`,
  `docs/mcp.md`, `docs/configuration.md`, `docs/architecture.md`, the topic page,
  and the agent skill in `plugins/proxbase/skills/proxbase/`.

## Commits and pull requests

- Pull requests are squash-merged; the title is a Conventional Commit
  (`feat: …`, `fix(mcp): …`, `docs: …`) and becomes the changelog entry.
- Describe what you ran to test the change in the PR.
- Never commit credentials; CI runs gitleaks over the whole history.
- `CHANGELOG.md` and versions are managed by release-please; do not edit them.
