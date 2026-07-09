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

## Install

Build the TUI and register it as a launchable app (rofi etc.) — all under
`$HOME`, no root:

```sh
git clone https://github.com/Enzord758/archmaid.git ~/archmaid
cd ~/archmaid
./install.sh          # binary → ~/.local/bin, icon + .desktop for launchers
./install.sh --uninstall
```

Then launch it from **rofi** as *archmaid*, or run `archmaid`. Needs Go to build
and a terminal (defaults to `kitty`; override with `ARCHMAID_TERMINAL`). Full
details in [`INSTALL.md`](INSTALL.md).

---

The rest of this README documents the original **`archmaid.sh`** gum script.

## Requirements

- [`gum`](https://github.com/charmbracelet/gum) — required, the script
  exits immediately if it's missing
- `pacman-contrib` — optional, provides `paccache` for package cache
  cleanup (step is skipped with a warning if not installed)
- [`yay`](https://github.com/Jguer/yay) — optional, used to clean the AUR
  build cache (step is skipped if not installed)
- `topgrade` — optional, used for system updates; falls back to
  `pacman -Syu` if not installed

## Usage

```sh
git clone https://github.com/Enzord758/archmaid.git ~/archmaid
cd ~/archmaid
./archmaid.sh
```

Some steps run `sudo` commands — you'll be prompted to authenticate once
up front for any menu option that needs it.

## Menu options

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

## Logging

Every run overwrites `~/.archmaid.log` with a timestamped record of
each step (`START`/`END`/`FAIL`), viewable in-app via **View log**.
