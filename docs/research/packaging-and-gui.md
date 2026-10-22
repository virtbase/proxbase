# Packaging metadata and a GUI

What software centres show for Proxbase packages, what the packages should carry, and
how a GUI could ship with the same builds. Researched October 2026 against Debian 13
(GNOME Software 48, AppStream 1.0), GoReleaser v2 and nfpm.

## Where Proxbase stands

GoReleaser builds `CGO_ENABLED=0` binaries for linux and darwin (amd64, arm64), a
`tar.gz` per target, `.deb`, `.rpm` and `.apk` with nfpm, a Homebrew cask and a
container image. Releases are downloads: there is no apt, dnf or apk repository.

Before this research the packages had a one-line description and no man pages, shell
completions, icon or AppStream metadata.

## What software centres show

GNOME Software and KDE Discover (the default on Debian 13 GNOME and KDE desktops):

| Situation | Shown | Source |
| --- | --- | --- |
| Opening a downloaded `.deb` | Name, summary, extended description, homepage, a generic icon | Control fields only; AppStream data in the package is not read before it is installed ([cline#14688](https://github.com/cline/cline/pull/14688)) |
| Installed | Icon, description, screenshots, releases, licence, links, for desktop applications | `/usr/share/metainfo/<id>.metainfo.xml` plus a `.desktop` file |
| Search and browse | As installed | An AppStream catalog (DEP-11) in an apt repository |

Verified on Debian 13 in a container under Xvfb: GNOME Software 48.3 opens the local
`.deb` with the package's summary and long description, a generic icon and an Install
button.

- **GNOME Software never shows console applications**: `gs_plugin_loader_app_is_valid`
  rejects `AS_COMPONENT_KIND_CONSOLE_APP` ("never show CLI apps", 48.3 and `main`). Its
  search and category queries do not filter by type; the loader drops them afterwards.
  With the package installed on Debian 13, `--details=io.github.virtbase.proxbase`
  reports no details and a search for "proxmox" finds nothing.
- **KDE Discover** (PackageKit backend) only lists AppStream components that a
  repository catalog maps to a package name; for the installed package it reports that
  the component is not in the available repositories. It does not filter console
  applications as such, so a DEP-11 catalog with `pkgname` would make Proxbase
  searchable there.
- `appstreamcli get` and `appstreamcli search proxmox` find the installed component.
- A console application's metainfo needs `id`, `metadata_license`, `name`, `summary`
  and `<provides><binary>` ([spec](https://www.freedesktop.org/software/appstream/docs/sect-Metadata-ConsoleApplication.html)).
- `gnome-software --show-metainfo=FILE` previews a metainfo file; `appstreamcli
  validate` checks it.
- Fedora's GNOME Software behaves the same for `.rpm`. Alpine has a software centre
  only on postmarketOS; Homebrew and archives have none.

## What the packages carry

| | `.deb` | `.rpm` | `.apk` | `tar.gz` | Homebrew |
| --- | --- | --- | --- | --- | --- |
| Extended description, section | yes | yes | description | - | description |
| Man pages (one per command) | `/usr/share/man/man1` | same | same | `man/` | `manpages` |
| Completions (bash, zsh, fish) | yes (zsh: `vendor-completions`) | yes (zsh: `site-functions`) | yes | `completions/` | `completions` |
| AppStream metainfo and icon | yes | yes | yes | - | - |
| Copyright file | DEP-5 | licence file | - | `LICENSE` | - |

For a command-line tool the metainfo and icon are groundwork: they make no software
centre page today, but a GUI with a desktop entry and a DEP-11 catalog both build on
them.

The man pages, completions and metainfo are generated at release time by
`cmd/gendist` (cobra's man page and completion generators), so they always match the
binary. It is a separate program so that the released binary does not link the man
page renderer.

Not done yet:

- **An apt repository.** It is the only way to get `apt upgrade` and search in
  GNOME Software. It could be signed with a GPG key and served from GitHub Pages,
  built with `reprepro` or `apt-ftparchive` from the release `.deb` files, with a DEP-11
  catalog composed from the same metainfo (`appstreamcli compose`). The work is key
  management and a release job; `proxbase upgrade` would then point to `apt upgrade`.
- **Screenshots** in the metainfo, hosted on GitHub Pages.
- **Packaging in Debian itself** (Go team policy, every dependency packaged) is not
  worth it at this stage.

## A GUI

The constraint: one `CGO_ENABLED=0` configuration cross-builds every target, and the
package mostly runs on headless KVM hosts and CI runners. Most native Go toolkits need
cgo and would pull GUI libraries into the package.

| Option | Same binary | Platforms | Maturity |
| --- | --- | --- | --- |
| **Embedded web UI**: `proxbase ui` serves an embedded frontend and a JSON API on 127.0.0.1 with a token and opens the browser | yes | all builds, the container, remote hosts over an SSH tunnel | standard library |
| The web UI in a native window through a cgo-free webview binding ([glaze](https://pkg.go.dev/github.com/daforester/glaze), [go-webview](https://pkg.go.dev/github.com/moxcomic/go-webview)) | yes, loads WebKitGTK or WKWebView at runtime and falls back to the browser | Linux, macOS | experimental |
| Wails | no (cgo, WebKitGTK) | Linux, macOS | v2 stable, v3 at beta.32 ([tracker](https://github.com/wailsapp/wails/issues/5844)) |
| Fyne, Gio | no (cgo, OpenGL; cross-compilers per target, [Fyne](https://docs.fyne.io/started/cross-compiling/), [GoReleaser](https://goreleaser.com/limitations/cgo/)) | Linux, macOS | stable |
| GTK4 and libadwaita via [puregotk](https://pkg.go.dev/github.com/bnema/puregotk) | yes (purego) | Linux | experimental |
| Cockpit page in `/usr/share/cockpit/proxbase` ([guide](https://cockpit-project.org/guide/latest/packages.html)) | separate small package | Linux servers with Cockpit | stable |

The embedded web UI covers every build with one frontend and keeps working where
Proxbase usually runs. The MCP handlers already take typed input, return typed output
and report progress, so the UI's API can adapt them, and the HTTP transport's bearer
token and cross-origin protection carry over (plus a Host check against DNS
rebinding). A `proxbase.desktop` entry that runs `proxbase ui` would turn the
metainfo into a `desktop-application`: the only way for Proxbase to get a GNOME
Software page, with an Open button and a launcher entry.

## Roadmap

| Priority | Item | Status |
| --- | --- | --- |
| P1 | Extended description, man pages, completions, metainfo, icon, copyright file | Done (#38) |
| P1 | `proxbase ui`: embedded web UI with list and status, create with progress, start, stop, destroy, snapshots; `.desktop` entry | Open |
| P2 | Signed apt repository with a DEP-11 catalog | Open |
| P2 | `proxbase ui --window` through a cgo-free webview; WebKitGTK as a recommended package | Open |
| P3 | Cockpit page embedding the web UI | Open |
| - | Wails, Fyne, Gio, native GTK | Not planned |
