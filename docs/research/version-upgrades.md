# Versions and upgrade paths research

Which Proxmox VE versions Proxbase can install and how to test major upgrades such as
8 → 9. Researched October 2026; only 9.2 is tested today.

## Releases and ISOs

| Major | Debian | Releases | End of life |
| --- | --- | --- | --- |
| 7 | 11 bullseye | 7.0 (2021-07) … 7.4 (2023-03) | 2024-07 |
| 8 | 12 bookworm | 8.0 (2023-06) … 8.4 (2025-04) | 2026-08 (bookworm repo still updated) |
| 9 | 13 trixie | 9.0 (2025-08), 9.1 (2025-11), 9.2 (2026-05) | current |

No PVE 10 yet: there is no `forky` suite in the repository, and Debian 14 is not expected
before mid-2027.

The mirror keeps one ISO per major plus the latest minors. `SHA256SUMS` (checked
2026-10-22) lists `proxmox-ve_7.4-1`, `8.4-1`, `9.1-1`, `9.2-1` and `9.2-1-arm64`;
8.0–8.3 and 9.0 return 404.

## Installer compatibility

| Installer | Change |
| --- | --- |
| 8.2 | Automated installer. Debug shell without `auto-installer-mode.toml`. `proxmox-fetch-answer partition` takes no label. Keys snake_case only |
| 8.3 | `[first-boot]` hook, post-install webhook. `partition <label>` required. Keys still snake_case, unknown fields rejected |
| 8.4 | kebab-case accepted for all keys (pve-installer e126ee8), `reboot-mode` |
| 9.0 | snake_case deprecated (warning) |
| 9.1 | `[network.interface-name-pinning]` |
| 9.2 | `subscription-key`, webhook `auth-token` |

Boot files (`boot/linux26`, `boot/initrd.img`, `boot/grub/grub.cfg`) have the same
names in 7.4, 8.4 and 9.x; 7.4 has no automated grub entry.

| ISO | Proxbase install path |
| --- | --- |
| 9.1-1, 9.2-1 | Works |
| 8.4-1 | Answer file, fetch command and grub parsing match the installer source (not booted yet). Post-install steps need a bookworm profile |
| 8.2, 8.3 | Would need a snake_case answer file and no `reboot-mode`; not downloadable anyway |
| ≤ 8.1, 7.x | No automated installer |

8.4 is the natural floor: it is the oldest 8.x ISO that can be downloaded.

## What the code assumes today

- `nodesetup.Repositories` disables only the deb822 files `pve-enterprise.sources` and
  `ceph.sources` and writes `proxmox.sources` (`internal/nodesetup/nodesetup.go`).
  PVE 8 has one-line `.list` files, so the enterprise repos stay enabled and `apt`
  fails with 401. The suite itself comes from `/etc/os-release`.
- The Ceph bake script writes deb822 `ceph.sources` (`internal/firstboot/firstboot.go`).
- Ceph releases are limited to `squid` and `tentacle` (`internal/config/ceph.go`). PVE 8
  offers `reef` and `squid`; tentacle has no bookworm repository.
- `ceph.Install` always upgrades `libpve-storage-perl`, a workaround for the 9.2 ISO
  (`internal/storage/ceph/install.go`).
- The answer file is kebab-case with `[first-boot]` and `reboot-mode`
  (`internal/answer/answer.go`), so 8.4 or newer.
- Version validation is a regex without minimum or maximum
  (`internal/config/validate.go`).
- The state records one `ISO` per cluster and no version per node
  (`internal/state/state.go`).
- `node add` re-runs `create()`, which resolves the `9.2` alias again: after a 9.2-2
  release, new nodes install it and the cluster's `ISO` is overwritten
  (`internal/cluster/create.go`, `internal/cluster/nodes.go`). Pin the resolved release
  on the first create.

## Packages

The Proxmox repositories keep every version in their index (bookworm `pve-manager`
8.0.0 to 8.4.21, trixie 9.0.0 to 9.2.21), and old packages still download. Proxmox
packages can be pinned (`apt install proxmox-ve=… pve-manager=…`); Debian base packages
only exist at the latest point release without snapshot.debian.org. So "upgrade from
8.3" means "8.4 ISO with 8.3 packages". `proxmox-offline-mirror` can snapshot
repositories for reproducible runs. The no-subscription repositories need no key.

## The 8 → 9 procedure

Prerequisites: every node on the latest 8.4 (`pveversion` ≥ 8.4.1), a quorate healthy
cluster, 5–10 GB free on `/`, Ceph already on Squid 19.2 (Reef → Squid first).

Per node, one at a time:

1. `apt update && apt dist-upgrade` on bookworm.
2. Gate: `pve8to9 --full` without FAIL lines.
3. Migrate guests away (old → new works, new → old does not).
4. Repositories: `bookworm` → `trixie` in `/etc/apt/sources.list`; replace the `.list`
   files with deb822 `proxmox.sources` (`pve-no-subscription`) and `ceph.sources`
   (`ceph-squid`, `no-subscription`), both
   `Signed-By: /usr/share/keyrings/proxmox-archive-keyring.gpg`.
5. `DEBIAN_FRONTEND=noninteractive apt -o Dpkg::Options::=--force-confdef
   -o Dpkg::Options::=--force-confold dist-upgrade -y`.
6. What `pve8to9` points out: remove the `systemd-boot` meta package (8.1–8.4 ISOs),
   `pve-lvm-disable-autoactivation`, settings out of `/etc/sysctl.conf`.
7. `pve8to9` again, reboot, check `pveversion`, node online, quorum, Ceph health.

After the last node, HA groups become HA rules. Mixed 8/9 clusters are supported only
during the upgrade. Pitfalls: NIC renames with the new kernel, cgroup v1 removed, the
Ceph "insecure key types" health error with Squid 19.2.6 (expected).

Ceph Reef → Squid on 8.x: `reef` → `squid` in `ceph.list`, `ceph osd set noout`,
`apt full-upgrade` everywhere, restart mon, mgr, osd targets node by node,
`ceph osd require-osd-release squid`, restart MDS, `ceph osd unset noout`.

## Proposed design

1. **Version profile per major**, derived from the installed system: suite, `.list` vs
   `.sources`, allowed Ceph releases (8: reef, squid; 9: squid, tentacle), the 9.2
   workaround. Refuse versions below 8.4 with a clear error.
2. **Pin the exact ISO** at the first create; record `pveVersion`, suite, Ceph release
   and kernel per node, shown by `status`.
3. **`proxbase upgrade <cluster> --to 9`**: preflight (quorum, `pve8to9` on every node,
   latest 8.4, Ceph on Squid or `--ceph-first`), then node by node: migrate guests,
   repositories, non-interactive dist-upgrade, the fixes above, reboot, wait healthy.
   Per-node phase in the state, so a failed upgrade resumes. Progress like `create`.
4. **Snapshot before upgrading** and `--rollback`: the advantage over hardware.
5. **Golden images keyed by ISO and package pin**; cache "8.4 with latest packages".
6. Lighter options: `--pin-packages 9.1.x`, opt-in kernels (`proxmox-kernel-6.14`,
   `6.17`).

Test matrix: 8.4 → 9.2 with ZFS; 8.4 Ceph Reef → Squid → 9.2; 9.2 Ceph Squid →
Tentacle; 9.1 → 9.2.

Prior art: `lae/ansible-role-proxmox` (in-major upgrades only), `proxmox-pve7to9`
(Ansible, partly manual). The upgrade wiki itself recommends trying the upgrade in a VM.

## Sources

[Roadmap](https://pve.proxmox.com/wiki/Roadmap), [FAQ](https://pve.proxmox.com/wiki/FAQ),
[Upgrade from 8 to 9](https://pve.proxmox.com/wiki/Upgrade_from_8_to_9),
[Upgrade from 7 to 8](https://pve.proxmox.com/wiki/Upgrade_from_7_to_8),
[Ceph Reef to Squid](https://pve.proxmox.com/wiki/Ceph_Reef_to_Squid),
[Automated Installation](https://pve.proxmox.com/wiki/Automated_Installation),
[pve-installer](https://git.proxmox.com/git/pve-installer.git),
[ISO mirror](https://enterprise.proxmox.com/iso/),
[offline mirror](https://pom.proxmox.com/offline-mirror.html),
[lae/ansible-role-proxmox](https://github.com/lae/ansible-role-proxmox),
[proxmox-pve7to9](https://github.com/darrenlavery91/proxmox-pve7to9)
