# Contributing

Thanks for helping. Issues and pull requests are welcome; for larger changes please
open an issue first so we can agree on the approach.

## Development

- Go (version from `go.mod`), QEMU 7.2+ and a Linux host with `/dev/kvm` for end-to-end tests
- `make build test lint` builds `bin/proxbase`, runs the unit tests (no KVM needed) and
  golangci-lint
- `bin/proxbase doctor` checks the host; `bin/proxbase create test --nodes 1` is the
  quickest end-to-end check (about 3 minutes once the ISO is cached)

The package layout and extension points (node runtime, storage backends, networks) are
described in [docs/architecture.md](docs/architecture.md).

Keep changes focused, add or update unit tests (golden files under `testdata/` are
updated with `go test ./internal/qemu -update`), and describe what you ran in the PR.

## Commits and pull requests

Pull requests are squash-merged and the PR title becomes the commit message, so the
title must follow [Conventional Commits](https://www.conventionalcommits.org/):
`feat: add node add`, `fix(ceph): wait for OSDs`, `docs: …`. `feat` and `fix` end up
in the changelog and decide the next version (see [docs/releasing.md](docs/releasing.md)).

## Code of conduct

This project follows the [Code of Conduct](CODE_OF_CONDUCT.md).
