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

From source: `go install github.com/virtbase/proxbase/cmd/proxbase@latest`

## Container image

`ghcr.io/virtbase/proxbase:<version>` (linux/amd64; an arm64 image may follow); see the Docker section
of the [README](../README.md#docker) and [examples/compose](../examples/compose).

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
