# Installing Proxbase

Releases are built by GoReleaser for Linux and macOS (amd64, arm64). Clusters need a
Linux host with KVM; macOS builds are experimental.

## Packages and binaries

Download from <https://github.com/virtbase/proxbase/releases>:

```bash
# Debian/Ubuntu (pulls qemu-utils, recommends qemu-system-x86)
sudo apt install ./proxbase_<version>_linux_amd64.deb
# Fedora/RHEL
sudo dnf install ./proxbase_<version>_linux_amd64.rpm
# any Linux
tar -xzf proxbase_<version>_linux_amd64.tar.gz && sudo install proxbase /usr/local/bin/
```

Homebrew (macOS and Linux): `brew install --cask virtbase/tap/proxbase`

## Man pages and completions

Packages and the Homebrew cask install a man page per command (`man proxbase`,
`man proxbase-create`, `man proxbase-node-add`) and completions for bash, zsh and fish.
The archives contain them in `man/` and `completions/`:

```bash
sudo install -Dm644 -t /usr/local/share/man/man1 man/*.1.gz
install -Dm644 completions/proxbase.bash ~/.local/share/bash-completion/completions/proxbase
install -Dm644 completions/proxbase.fish ~/.config/fish/completions/proxbase.fish
```

For zsh, copy `completions/_proxbase` to a directory in `$fpath`. `proxbase completion
--help` prints the scripts for other setups.

Opening the `.deb` in GNOME Software or KDE Discover shows its description before
installing. The packages also ship AppStream metadata and an icon
(`io.github.virtbase.proxbase`), but neither lists command-line tools that come from a
downloaded package: GNOME Software hides console applications, and Discover shows only
components from a repository's catalog.

From source: `go install github.com/virtbase/proxbase/cmd/proxbase@latest`

## Container image

`ghcr.io/virtbase/proxbase:<version>` (linux/amd64; an arm64 image may follow); see
[Docker](docker.md) and [examples/compose](../examples/compose).

## Upgrading

`proxbase upgrade` installs the latest release (`--version v0.3.0` for a given one,
`--check` to only look). It replaces the binary itself only where no package
manager owns it; otherwise it prints the command for that package manager:

| Installed with | Upgrade |
| --- | --- |
| Release archive (`tar -xzf`, any directory) | `proxbase upgrade` (`sudo` if the directory is not yours, e.g. `/usr/local/bin`) |
| Homebrew | `brew upgrade --cask virtbase/tap/proxbase` |
| `.deb`, `.rpm`, `.apk` | Download the new package from the release and install it as above; `proxbase upgrade` prints the commands with its SHA-256 |
| `go install` | `go install github.com/virtbase/proxbase/cmd/proxbase@latest` |
| Container image | Pull the new tag and recreate the container |
| GitHub Action | Nothing: `version: latest` (the default) installs the newest release in every run |

How `proxbase upgrade` decides and what it checks:

- **Install method**: a binary under a Homebrew `Caskroom`/`Cellar`, one that
  `dpkg`, `rpm` or `apk` owns, one in `$GOBIN` or `$GOPATH/bin`, the action's
  `$RUNNER_TEMP/proxbase-bin`, and the container image (it sets
  `PROXBASE_INSTALL_METHOD=container`) are left to their package manager.
  `PROXBASE_INSTALL_METHOD=binary` upgrades such a binary in place anyway.
- **Verification**: the archive must match its SHA-256 in the release's
  `checksums.txt`. If `cosign` is installed, the signature of `checksums.txt` is
  verified as well, with the identity of the release workflow (`--signature require`
  fails without cosign, `--signature skip` does not try). Downloads use HTTPS only,
  with timeouts and size limits.
- **Replacing**: the new binary is written next to the old one (symlinks resolved,
  same mode and, as root, owner), must run and report the expected version, and is
  then renamed over the old binary. The rename is atomic: the path is always either
  the complete old or the complete new binary.
- **Versions**: it refuses to install an older release without `--allow-downgrade`.
  A build from a checkout (`v0.2.2-7-gabc1234`, see `make build`) counts as newer
  than its tag; a build without a version (`dev`) can always be replaced.
- **Running clusters**: processes that already run keep the old binary (the kernel
  keeps the replaced file until they exit): the per-cluster switch processes, a
  `proxbase mcp` server and creates it runs in the background. Clusters stay up.
  Everything started afterwards runs the new binary, including the switch of a
  cluster started later and background creates that a running `proxbase mcp` starts
  (it starts them from the binary's path). Restart `proxbase mcp` (reconnect the
  client) to serve the new version.
- `--check` exits with 0 when up to date, 100 when a newer release exists and 1 on
  errors. Without a terminal, `--yes` is required. `GITHUB_TOKEN` (or `GH_TOKEN`)
  raises the GitHub API rate limit for frequent checks.

## Verify

`checksums.txt` is signed keyless by the release workflow; its Sigstore bundle is
`checksums.txt.sigstore.json`. Each archive has an SPDX SBOM (`*.sbom.json`) and a
build provenance attestation.

```bash
cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-identity https://github.com/virtbase/proxbase/.github/workflows/release.yml@refs/heads/main \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c checksums.txt
gh attestation verify proxbase_<version>_linux_amd64.tar.gz --repo virtbase/proxbase

cosign verify ghcr.io/virtbase/proxbase:<version> \
  --certificate-identity https://github.com/virtbase/proxbase/.github/workflows/release.yml@refs/heads/main \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```
