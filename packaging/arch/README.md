# Arch packages (archmaid-git, archmaid-gui-git)

`PKGBUILD` is a split package built from one checkout, with `-buildmode=pie
-trimpath -mod=readonly -modcacherw` and makepkg's CGO flags for every binary:

- **`archmaid-git`**: the TUI (`cmd/archmaid/`), the root helper
  (`cmd/archmaid-helper/`), the polkit policy, the DMS widget and the
  updates timer. No GUI toolkit in its `depends`.
- **`archmaid-gui-git`**: `archmaid-gui`, built from the `gui/` module
  (gotk4, cgo), with its `.desktop` entry, icon and AppStream metainfo.
  It depends on `gtk4`, `libadwaita`, `libgirepository` (gotk4 links
  `libgirepository-1.0` directly) and on the exact `archmaid-git` of the
  same build, because it runs the same root helper through `pkexec`.

Building needs `gtk4`, `libadwaita`, `libgirepository` (it owns
`gobject-introspection-1.0.pc` and `girepository.h` on Arch; the
`gobject-introspection` package, with g-ir-scanner and python, isn't needed)
and `pkgconf` as makedepends, whichever package you install. A cold build
of `archmaid-gui` takes several minutes (gotk4's cgo).

`check()` runs the helper's and the DMS widget's tests and the GUI's plain-Go
view-model tests (`gui/internal/runview`). The GUI's display tests need a
private headless weston, so they stay out of `check()`; run `make gui-test`
from a checkout.

### Why two packages

The DMS widget opens `archmaid-gui` when it is on PATH and falls back to the
TUI in a terminal, so `archmaid-git` alone is a complete install and the GUI
is a choice:

- **For the split:** a terminal-only or minimal machine doesn't pull in the
  GTK4/libadwaita stack (and its GStreamer, Vulkan and X11 libraries); you
  pick the front end the widget opens by installing `archmaid-gui-git` or
  not; and the GUI can be held back (it has not been run live yet, see
  below) without holding back the TUI and helper.
- **Against:** one more package name, and building either still needs the
  GUI's makedepends (makepkg runs one `build()` for both).

**Install `archmaid-gui-git` only for the first live run.** The GUI runs on
the real engine and Driver (root steps through polkit, topgrade's sudo
through its password dialog), but so far only against test shims. The DMS
widget opens it when it is installed, so keep it off machines where you rely
on the widget until the live run has passed.

## Build locally (no install)

Only committed state is built, so commit first. From this directory:

```sh
ARCHMAID_REPO="file://$(git rev-parse --show-toplevel)" \
ARCHMAID_FRAGMENT="commit=$(git rev-parse HEAD)" \
makepkg -f
```

`ARCHMAID_REPO` / `ARCHMAID_FRAGMENT` override the `source` URL and ref. By
default the PKGBUILD clones the upstream GitHub repo at `branch=main`. For a
release, pin `_fragment` to `tag=vX.Y.Z` or `commit=<sha>`.

To keep the tree clean, build in a scratch copy: `mkdir /tmp/pk && cp PKGBUILD
/tmp/pk && cd /tmp/pk`, then run the command above there.

`pkg/`, `src/` and `*.pkg.tar.*` are gitignored here.

## Install

You run this yourself (it needs root):

```sh
makepkg -si      # both packages; with the env vars above for a local build
```

For the TUI only, build without installing and install that one file:

```sh
makepkg -s
pacman -U archmaid-git-*.pkg.tar.zst       # as root
```

## What it installs

`archmaid-git`:

| Path | Mode | Notes |
|---|---|---|
| `/usr/bin/archmaid` | 0755 | the TUI |
| `/usr/lib/archmaid/archmaid-helper` | 0755 root:root | **not setuid**; run via `pkexec` |
| `/usr/share/polkit-1/actions/org.archmaid.helper.policy` | 0644 | one action per helper verb |
| `/usr/share/applications/archmaid.desktop` | 0644 | `Terminal=true`, so the launcher picks your terminal |
| `/usr/share/icons/hicolor/scalable/apps/archmaid.svg` | 0644 | |
| `/usr/share/doc/archmaid/helper.md` | 0644 | helper docs |
| `/etc/xdg/quickshell/dms-plugins/archmaid/{plugin.json,ArchmaidWidget.qml,state.js}` | 0644 | DMS bar widget (system plugin dir); enable it in DMS |
| `/usr/lib/archmaid/archmaid-updates-check` | 0755 | runs `checkupdates`, writes the pending count |
| `/usr/lib/systemd/user/archmaid-updates-check.{service,timer}` | 0644 | hourly user timer; `systemctl --user enable --now archmaid-updates-check.timer` |

`archmaid-gui-git`:

| Path | Mode | Notes |
|---|---|---|
| `/usr/bin/archmaid-gui` | 0755 | the GTK4/libadwaita front end |
| `/usr/share/applications/io.github.enzord758.archmaid.desktop` | 0644 | `Exec=archmaid-gui`, `Terminal=false`; named after the application id so the window's `app_id` matches it |
| `/usr/share/icons/hicolor/scalable/apps/io.github.enzord758.archmaid.svg` | 0644 | the project icon (`packaging/archmaid.svg`) under the application id |
| `/usr/share/metainfo/io.github.enzord758.archmaid.metainfo.xml` | 0644 | AppStream; no `project_license` until the repo has a license |

License: `LicenseRef-unknown` until upstream picks one (TODO in the PKGBUILD).
