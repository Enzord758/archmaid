# archmaid

Housekeeping for Arch Linux — clean package caches, remove orphans, vacuum
logs, update via [topgrade](https://github.com/topgrade-rs/topgrade), and more,
all from one menu. A [Bubble Tea](https://github.com/charmbracelet/bubbletea) TUI
with live output, in-TUI sudo, floating popups, orphan removal, and a `--dryrun`
mode. Tokyo Night themed.

The app lives in [`tui/`](tui/README.md).

## Install

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
default; override with `ARCHMAID_TERMINAL`). At runtime it just drives the usual
Arch tools (`pacman`, `sudo`, and optionally `paccache`, `yay`, `topgrade`).
Full details in [`INSTALL.md`](INSTALL.md).

## Menu options

| Option | What it does |
| --- | --- |
| **Full Cleanup** (recommended) | Runs every step in sequence: updates the system via topgrade, trims the package cache to 3 versions (with an optional full wipe), removes orphaned packages (only when any exist — confirms with the count), cleans the AUR cache, deletes temp files older than 2 days from `/tmp` and `/var/tmp`, vacuums logs older than 2 weeks, checks package integrity, and finally updates Hyprland plugins via hyprpm (if installed; last on purpose — hyprpm drops the sudo session on exit) |
| Clear user cache | Deletes everything under `~/.cache` |
| Repair broken packages | Finds packages with missing files (`pacman -Qk`) and reinstalls them |
| Delete old downloads | Deletes files in `~/Downloads` older than 30 days |
| Run topgrade | Runs a system update via topgrade, streaming its output live |
| Disable unused services | Lists systemd services that are enabled but not currently running, and lets you multi-select which to disable |
| Edit topgrade config | Opens `~/.config/topgrade.toml` in `$EDITOR`, creating it with sane defaults first if it doesn't exist |
| View log | Opens the run log in a pager |

Every destructive action asks for confirmation before running. Sudo is handled
in-TUI: one password popup up front covers the whole run (never a raw terminal
prompt), and if anything genuinely needs to re-authenticate mid-run, it pops up
again — the password is passed straight to sudo, never stored.

## Logs

Run logs (failures only) go to `~/.local/state/archmaid/<YYYYMMDD-HHMM>.log`;
`--dryrun` uses `~/.local/state/archmaid/dryrun.log`. Logs older than a week are
pruned automatically. No setup — it's a user-writable dir.
