# archmaid

Housekeeping for Arch Linux — clean package caches, remove orphans, vacuum
logs, update via [topgrade](https://github.com/topgrade-rs/topgrade), and more,
all from one menu. Tokyo Night themed.

Two implementations live here:
- **`tui/`** — the current app: a [Bubble Tea](https://github.com/charmbracelet/bubbletea)
  TUI with live output, in-TUI sudo, floating popups, orphan removal, and a
  `--dryrun` mode. Installable as a launchable app (below). See
  [`tui/README.md`](tui/README.md).
- **`archmaid.sh`** — the original [gum](https://github.com/charmbracelet/gum)-based
  script the TUI grew out of (documented further down).

## Install (the TUI)

Build the TUI and register it as a launchable app (rofi etc.) — all under
`$HOME`, no root:

```sh
git clone https://github.com/Enzord758/archmaid.git ~/archmaid
cd ~/archmaid
./install.sh          # binary → ~/.local/bin, icon + .desktop for launchers
./install.sh --uninstall
```

Then launch it from **rofi** as *archmaid*, or run `archmaid`.

**Requirements:** **Go** to build, and a **terminal** to run in (`kitty` by
default; override with `ARCHMAID_TERMINAL`). **No `gum`** — the TUI reimplements
all of that UI in Go (Bubble Tea). At runtime it just drives the usual Arch
tools (`pacman`, `sudo`, and optionally `paccache`, `yay`, `topgrade`). Full
details in [`INSTALL.md`](INSTALL.md).

## Menu options

Both implementations share the same menu:

| Option | What it does |
| --- | --- |
| **Full Cleanup** (recommended) | Runs every step below in sequence: trims the package cache to 3 versions (with an optional full wipe), removes orphaned packages, cleans the AUR cache, deletes temp files older than 2 days from `/tmp` and `/var/tmp`, vacuums logs older than 2 weeks, checks package integrity, and updates the system via topgrade |
| Clear user cache | Deletes everything under `~/.cache` |
| Repair broken packages | Finds packages with missing files (`pacman -Qk`) and reinstalls them |
| Delete old downloads | Deletes files in `~/Downloads` older than 30 days |
| Run topgrade | Runs a system update via topgrade (or `pacman -Syu` as a fallback), showing output only on failure |
| Disable unused services | Lists systemd services that are enabled but not currently running, and lets you multi-select which to disable |
| Edit topgrade config | Opens `~/.config/topgrade.toml` in `$EDITOR`, creating it with sane defaults first if it doesn't exist |
| View log | Opens the run log in a pager |

Every destructive action asks for confirmation before running.

## The `archmaid.sh` gum script

The original bash version — same menu, driven by
[gum](https://github.com/charmbracelet/gum). Run it directly, no build step:

```sh
./archmaid.sh
```

**Requirements (this script only):**

- [`gum`](https://github.com/charmbracelet/gum) — **required by the script**;
  it exits immediately if missing (the TUI does *not* need this)
- `pacman-contrib` — optional, provides `paccache` for cache cleanup
- [`yay`](https://github.com/Jguer/yay) — optional, cleans the AUR build cache
- `topgrade` — optional; falls back to `pacman -Syu` if not installed

It logs each run to `~/.archmaid.log` (`START`/`END`/`FAIL` per step), viewable
in-app via **View log**.
