# Security policy

## Supported versions

Only the latest release receives fixes.

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub:
**Security → Report a vulnerability** on <https://github.com/virtbase/proxbase>.
If that is not possible, email contact@janic.dev. Do not open a public issue.
You will get an answer within a week.

## Scope

Proxbase builds disposable lab clusters. By design it generates root passwords, SSH
keys and API tokens and stores them unencrypted (mode 0600) in the cluster's state
directory, and it forwards the nodes' web UI and SSH to `127.0.0.1` (to `0.0.0.0`
inside the container image). Do not expose these ports to untrusted networks.
Reports about these defaults are still welcome if you see a safer option.

## Verifying releases

Release checksums and container images are signed with Sigstore cosign (keyless);
see [docs/install.md](docs/install.md#verify).
