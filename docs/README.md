# Proxbase documentation

| Guide | What it covers |
| --- | --- |
| [Getting started](getting-started.md) | Requirements, first cluster, web UI, credentials, clean up |
| [Installation](install.md) | Packages, Homebrew, container image, verifying releases |
| [Configuration](configuration.md) | Every field of the cluster file, defaults and limits |
| [Networking](networking.md) | NAT uplink, internal networks, roles, VLANs, MTU, guest internet |
| [Storage](storage.md) | ZFS pools and hyperconverged Ceph |
| [Lifecycle](lifecycle.md) | Start/stop, adding and removing nodes, snapshots, SSH and console |
| [Golden images](golden-images.md) | Faster creates from a cached base image |
| [Fault injection](fault-injection.md) | Power loss, hangs, pulled cables, partitions, latency and loss |
| [Docker](docker.md) | Running clusters in a container with Docker Compose |
| [CI and automation](ci.md) | GitHub Action, other CI systems, JSON progress |
| [MCP server](mcp.md) | Letting AI agents create, use and break clusters |
| [CLI reference](cli.md) | All commands and flags |
| [Troubleshooting](troubleshooting.md) | Logs, common errors and how to resume |
| [Architecture](architecture.md) | How Proxbase is built, with diagrams |
| [Examples](../examples) | Cluster files and integrations for common use cases |
| [Bridge mode](bridge-mode.md) | Design for host-reachable networks (not implemented) |
| [Releasing](releasing.md) · [GitHub setup](github-setup.md) | For maintainers |
| [Feasibility research](research/feasibility.md) | Measurements behind the design decisions |
