# Arch package (archmaid-git)

`PKGBUILD` builds the TUI (`tui/`) and the root helper
(`cmd/archmaid-helper/`) with `-buildmode=pie -trimpath -mod=readonly
-modcacherw` and makepkg's CGO flags.

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
makepkg -si      # with the env vars above for a local build
```

## What it installs

| Path | Mode | Notes |
|---|---|---|
| `/usr/bin/archmaid` | 0755 | the TUI |
| `/usr/lib/archmaid/archmaid-helper` | 0755 root:root | **not setuid**; run via `pkexec` |
| `/usr/share/polkit-1/actions/org.archmaid.helper.policy` | 0644 | one action per helper verb |
| `/usr/share/applications/archmaid.desktop` | 0644 | `Terminal=true`, so the launcher picks your terminal |
| `/usr/share/icons/hicolor/scalable/apps/archmaid.svg` | 0644 | |
| `/usr/share/doc/archmaid/helper.md` | 0644 | helper docs |

License: `LicenseRef-unknown` until upstream picks one (TODO in the PKGBUILD).
