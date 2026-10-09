# GitHub setup (one-time, repository admin)

These steps cannot be done from the repository files.

1. **Default branch `main`.** The workflows trigger on `main`; rename locally with
   `git branch -m master main` and push `main` as the default branch.
2. **Actions permissions** (Settings → Actions → General): allow GitHub Actions to
   create and approve pull requests (release-please opens the release PR). Workflow
   permissions can stay read-only; each workflow requests what it needs.
3. **Homebrew tap:** create the public repository `virtbase/homebrew-tap` (empty, with
   a README) and a fine-grained token with *Contents: read and write* on that repository
   only. Store it as the Actions secret `HOMEBREW_TAP_TOKEN` in `virtbase/proxbase`.
4. **GHCR:** after the first release, open the `proxbase` package (organization →
   Packages), link it to the repository and set its visibility to public. The workflow
   pushes with `GITHUB_TOKEN` (`packages: write`); if the organization restricts package
   creation, allow it for this repository.
5. **Security features** (Settings → Code security): enable private vulnerability
   reporting, Dependabot alerts, secret scanning with push protection and code scanning
   (the CodeQL, Trivy and Scorecard workflows upload SARIF).
6. **Renovate:** install the Mend Renovate GitHub App for the repository. It picks up
   `renovate.json` and opens an onboarding PR.
7. **Branch protection / ruleset for `main`:** require pull requests, squash merges
   only, required checks `lint`, `test`, `build`, `docker`, `conventional-commit`,
   `analyze`, `govulncheck`, `gitleaks`, `trivy`; block force pushes.
8. **OpenSSF Scorecard** publishes results automatically (`publish_results: true`);
   add the badge to the README once the first run succeeded.
