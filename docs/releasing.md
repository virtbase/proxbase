# Releasing

Releases are automatic; nobody tags by hand.

1. Pull requests are squash-merged with a Conventional Commit title (checked by the
   `PR title` workflow).
2. On every push to `main`, release-please updates a release PR with the next version
   and `CHANGELOG.md`: `fix` → patch, `feat` → minor (also before 1.0), `feat!`/
   `BREAKING CHANGE` → major (minor before 1.0).
3. Merging the release PR makes release-please tag `vX.Y.Z` and create the GitHub
   release. The same `Release` workflow then runs GoReleaser, which uploads archives,
   deb/rpm/apk packages, SBOMs, `checksums.txt` with its cosign bundle, pushes the
   amd64 image to GHCR (signed), updates the Homebrew tap and attests build
   provenance. Afterwards the workflow
   - moves the major tag (`v0`) to the release for `uses: virtbase/proxbase@v0`, and
   - publishes `server.json` with the release's version and image tag to the
     [MCP registry](mcp.md#mcp-registry) (OIDC login, after the labelled image is
     pullable). release-please keeps `version` in `server.json` current.
4. Manual, optional: list the release in the GitHub Marketplace. GitHub offers no
   API for this: open the release, **Edit**, tick *Publish this Action to the GitHub
   Marketplace*, **Update release** (two-factor authentication required). The first
   time, an organization owner accepts the Marketplace Developer Agreement there.
   Users of `@v0` get new releases through the tag either way; the tick only updates
   the version shown on the Marketplace page.

To re-run the publishing for an existing release, run the `Release` workflow manually
with its tag.

Local dry run (no signing, no publishing):

```bash
goreleaser check
goreleaser release --snapshot --clean --skip=sign
```

The image is built for linux/amd64 only; arm64 binaries and packages are released.

Dependency updates come from Renovate (weekly, grouped, actions pinned to commit SHAs).
Security checks: CodeQL, govulncheck, gitleaks, Trivy (repository and image) and
OpenSSF Scorecard run on pushes, pull requests and weekly. The `E2E` workflow creates
a real cluster through the action nightly and on demand.
