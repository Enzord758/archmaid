# archmaid (TUI)

The archmaid app: a [Bubble Tea](https://github.com/charmbracelet/bubbletea) TUI
for Arch maintenance — `btop`/`wiremix`-style, an event loop that redraws as
state changes, rather than a scripted sequence of prompts.

All eight menu actions are implemented, with a `--dryrun` mode to exercise it
safely end to end. It grew out of a gum bash script (now retired); the sections
below explain the design that replaced it.

## Why a TUI (not a gum script)

`gum` is built for scripted flows: ask, run, print, repeat. A real TUI needs an
event loop that redraws panels as state changes — live command output, a status
header that updates on its own, keyboard navigation between views. That's an
architectural difference bash/gum can't cross, so it moves to Go + Bubble Tea
(the same toolkit `gum` itself is built on, so the Tokyo Night styling carries
straight over via `lipgloss`).

## What the sketch demonstrates

- **The Elm-style model/update/view loop** (`main.go`) with five screens: menu,
  confirm, running, summary, log.
- **Live streaming command output** (`tasks.go`) — the whole point. A command's
  combined stdout/stderr streams into a scrollable viewport line by line while a
  spinner runs, instead of gum swallowing it and dumping a pager afterward. One
  ordered channel (line messages, then a single done message) keeps output and
  exit status race-free; `waitForStream` pumps it through `Update`.
- **A live checklist** of steps with per-step status (pending/running/ok/failed/
  skipped), mirroring `start_msg`/`end_msg`/`fail_msg`.
- **In-TUI sudo authentication** — a masked (`•`) `textinput` collects the
  password inside the alt-screen and pipes it to `sudo -S -v`, which caches the
  credential so the queue's later `sudo` calls run without prompting (a 60s
  keep-alive maintains it during long runs). It probes `sudo -n -v` first and
  skips the prompt when credentials are already cached. Wrong passwords are
  reported inline with a remaining-attempts count; after three failures it gives
  up, skips the sudo-requiring steps, and logs each as `SKIP: …`. If anything
  needs to re-authenticate mid-run, `SUDO_ASKPASS` routes the prompt back into
  the TUI as a popup (`askpass.go`) — nothing can ever prompt on the raw
  terminal. The password is passed straight through, never stored or logged.
- **A self-updating status header** via `tea.Tick` (disk/RAM/cache), the thing
  gum couldn't do without redrawing everything by hand.
- **Dynamic actions** with an async, read-only discovery pass on a spinner
  screen: repair broken packages (`pacman -Qk`), delete old downloads
  (`find … -mtime +30`), and a **multi-select** (`screenServices`) of
  enabled-but-inactive services to disable. "Nothing to do" routes to a notice.
- **Edit topgrade config** hands the terminal to `$EDITOR` via `tea.ExecProcess`,
  creating `~/.config/topgrade.toml` with defaults first.
- **Abortable steps** — ctrl+c while a step runs opens a confirmation; confirming
  sends the child SIGINT (sudo relays it to the real command) and returns to the
  menu, rather than quitting. A child that ignores SIGINT is force-killed
  (SIGKILL) after a grace period.
- **Floating popups** — the prompts (password input, confirm/abort two-button
  dialogs, notices) render as bordered boxes composited over the current screen
  via an ANSI-aware overlay (`overlay.go`), not full-screen takeovers, so the
  running checklist stays visible behind an abort dialog.
- **Category-split, mostly-failures logging** (`log.go`): each run gets its own
  timestamped directory, `~/.local/state/archmaid/<YYYYMMDD-HHMM>/` (a
  user-writable dir — no root/system setup), holding one file per category —
  `topgrade.log`, `orphans.log`, `cleanup.log` (the default bucket for
  everything else). Ordinary steps only log on failure; steps that remove or
  overwrite something (orphan removal, cache/temp/log wipes, service
  disabling, downloads/repair cleanup) log their outcome even on success, so
  what a destructive step actually did stays auditable without digging through
  `pacman`'s own log. Only the newest 5 run directories are kept — older ones
  are pruned at startup. "View log" picks a category before opening the pager.
  Dry runs log everything to a single reused `~/.local/state/archmaid/dryrun/`
  slot instead.

## Dry run

`go run . --dryrun` (or `./archmaid --dryrun`) makes every mutating step *report what
it would do* instead of running it, logs **every** step (not just failures) to
`~/.local/state/archmaid/dryrun/<category>.log`, and skips sudo entirely. Read-only
discovery still runs, so the menus show real data. A `[dry-run]` tag appears in
the header. This is the safe way to exercise the whole UI.

## Design lineage (from the original gum script)

| the old gum script | here |
| --- | --- |
| `run_task` (gum spin + tempfile) | `runStep` + `waitForStream` streaming |
| `start_msg`/`end_msg`/`fail_msg` | `stepState` + `checklist()` |
| `sudo -v` priming | in-TUI masked prompt (`screenPassword`, `authSudo`) |
| `gum confirm` | `screenConfirm` |
| `gum choose` menu / service multi-select | `screenMenu` / `screenServices` |
| `view_log` + sed colouring | `screenLog` + `lipgloss` styles |
| `system_status` | `systemStatus` + `refreshStatus` tick |

## Still to do

The app is feature-complete. Remaining items are test
coverage (owned separately — see `TESTING.md`): broader VHS e2e through the
services/abort paths, and a summary-screen golden.

## Install

To install as a launchable app (binary + icon + `.desktop` for rofi), use the
repo-root installer — see [`../INSTALL.md`](../INSTALL.md):

```sh
cd ..            # repo root
./install.sh
```

## Build & test

Needs Go (`pacman -S go`).

```sh
cd tui
go mod tidy   # resolve the dependency graph + go.sum
go build -o archmaid .
go test ./...  # unit tests: dry-run safety, sudo retries, screen routing
go run . --dryrun
```

See `TESTING.md` for the full test plan and how to drive the TUI headlessly.

## Logs

Logs live in `~/.local/state/archmaid/` — a user-writable dir, so there's no
install-time or root setup. One timestamped directory per run, holding
`topgrade.log` / `orphans.log` / `cleanup.log` (failures only, plus any
destructive step's outcome even on success), and a single reused `dryrun/`
directory for `--dryrun` runs. Only the newest 5 run directories are kept;
older ones are pruned on startup.
