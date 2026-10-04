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
| **Full Cleanup** (recommended) | Runs every step in sequence: audits your AUR packages first (if one changed maintainer or gained a co-maintainer since the last audit, it names it and asks before updating — declining skips just the update), updates the system via topgrade, trims the package cache to 3 versions (with an optional full wipe), looks for orphaned packages *after* the update (so it also catches the ones the update left behind) and, when there are any, asks before removing them — the confirm gives both the orphan count and the total `pacman -Rns` would take with their now-unneeded dependencies — then cleans the AUR cache, ages out temp files with `systemd-tmpfiles --clean` (by the system's tmpfiles.d rules — on Arch, 10 days in `/tmp` and 30 days in `/var/tmp`), vacuums logs older than 2 weeks, checks package integrity, and finally updates Hyprland plugins via hyprpm (if installed; last on purpose — hyprpm drops the sudo session on exit) |
| Clear user cache | Deletes everything under `~/.cache` |
| Repair broken packages | Finds packages with missing files (`pacman -Qkq`) and reinstalls them |
| Delete old downloads | Deletes files and folders not changed in 30 days (by ctime, so a back-dated download isn't mistaken for an old one; a folder goes only if nothing inside it is new) from your downloads folder, listing them first — `--downloads <dir>`, `$ARCHMAID_DOWNLOADS`, your XDG download dir, `~/Downloads` or `~/Download`, whichever comes first; never `$HOME` itself (see [`tui/README.md`](tui/README.md#downloads-folder)) |
| Run topgrade | Audits your AUR packages first, exactly as Full Cleanup does (a maintainer change or added co-maintainer since the last audit is named and asked about — declining skips the update), then runs a system update via topgrade, streaming its output live |
| Disable unused services | Lists systemd services that are enabled but not currently running, and lets you multi-select which to disable |
| Edit topgrade config | Opens `~/.config/topgrade.toml` in `$EDITOR`, creating it with sane defaults first if it doesn't exist |
| Audit AUR packages | Read-only check of your foreign packages against the AUR: flags adopted or maintainer-changed packages and added co-maintainers (compared with your previous audit), removed co-maintainers, orphaned or out-of-date ones, very recent pushes, and packages no longer on the AUR |
| View log | Pick a category (topgrade, orphans, cleanup, AUR audit) and page through its log |

Every destructive action asks for confirmation before running. Sudo is handled
in-TUI: one password popup up front covers the whole run (never a raw terminal
prompt), and if anything genuinely needs to re-authenticate mid-run, it pops up
again — the password is passed straight to sudo, never stored.

## Logs

Each run gets a directory, `~/.local/state/archmaid/<YYYYMMDD-HHMM>/`, with one
file per category (`topgrade.log`, `orphans.log`, `cleanup.log`, `audit.log`).
Ordinary steps log only failures; topgrade, the AUR audit, and steps that remove
or overwrite something log their outcome even on success. `--dryrun` logs everything to a reused `dryrun/`
directory. The newest 5 runs are kept; older ones are pruned automatically. No
setup — it's a user-writable dir.

## Disclosure: vibecoded

This TUI was written almost entirely by an AI agent (Claude Code), with a human
directing design, reviewing behavior, and pulling the trigger on every decision
that matters (what runs as root, what gets deleted, what ships). It is tested
accordingly: a 100+ test suite run under the race detector, golden-frame UI
tests, a VHS end-to-end harness, and live verification against a real Arch
system — including the sudo/faillock edge cases (see `tui/TESTING.md`; the
reasoning behind the risky parts is in the commit history and
`tui/HANDOVER.md`). Read the code with the same
skepticism you'd apply to any code, and run `--dryrun` first if you want to see
exactly what it would do to your system.
