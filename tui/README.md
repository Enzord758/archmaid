# cleanup TUI (bubbletea sketch)

A [Bubble Tea](https://github.com/charmbracelet/bubbletea) rewrite sketch of the
gum-based `cleanup.sh`, exploring what it takes to make this a real TUI —
`btop`/`wiremix`-style — rather than a scripted sequence of prompts.

**Working prototype.** All eight menu actions are implemented; a `--dryrun`
mode makes it safe to exercise end to end. Not yet at full parity with
`cleanup.sh` in every edge case — see "Still to do".

## Why a rewrite (not a refactor)

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
  credential so the queue's later `sudo` calls run without prompting. It probes
  `sudo -n -v` first and skips the prompt when credentials are already cached.
  Wrong passwords are reported inline with a remaining-attempts count; after
  three failures it gives up, skips the sudo-requiring steps, and logs each as
  `SKIP: … (sudo authentication failed)`. The password is never logged and the
  input is reset immediately after use.
- **A self-updating status header** via `tea.Tick` (disk/RAM/cache), the thing
  gum couldn't do without redrawing everything by hand.
- **Dynamic actions** with an async, read-only discovery pass on a spinner
  screen: repair broken packages (`pacman -Qk`), delete old downloads
  (`find … -mtime +30`), and a **multi-select** (`screenServices`) of
  enabled-but-inactive services to disable. "Nothing to do" routes to a notice.
- **Edit topgrade config** hands the terminal to `$EDITOR` via `tea.ExecProcess`,
  creating `~/.config/topgrade.toml` with defaults first.
- **Abortable steps** — ctrl+c while a step runs opens a confirmation
  (`screenAbortConfirm`); confirming sends the child SIGINT (sudo relays it to the
  real command) and returns to the menu, rather than quitting the program.
- **Error-only logging** to `~/.maintenance_tui.log` (`log.go`), same policy and
  path as the bash version.

## Dry run

`go run . --dryrun` (or `./tui --dryrun`) makes every mutating step *report what
it would do* instead of running it, logs **every** step (not just failures) to a
separate `~/.maintenance_dryrun.log`, and skips sudo entirely. Read-only
discovery still runs, so the menus show real data. A `[dry-run]` tag appears in
the header. This is the safe way to exercise the whole UI.

## Mapping to cleanup.sh

| cleanup.sh | here |
| --- | --- |
| `run_task` (gum spin + tempfile) | `runStep` + `waitForStream` streaming |
| `start_msg`/`end_msg`/`fail_msg` | `stepState` + `checklist()` |
| `sudo -v` priming | in-TUI masked prompt (`screenPassword`, `authSudo`) |
| `gum confirm` | `screenConfirm` |
| `gum choose` menu / service multi-select | `screenMenu` / `screenServices` |
| `view_log` + sed colouring | `screenLog` + `lipgloss` styles |
| `system_status` | `systemStatus` + `refreshStatus` tick |

## Still to do

- Orphaned-package removal as its own step inside Full Cleanup (currently only
  the standalone "repair broken packages" discovery exists).
- Parity pass on Full Cleanup's per-step optional confirms vs. the bash script.
- A SIGKILL fallback if a child ignores the SIGINT from an abort.

## Build & test

Needs Go (`pacman -S go`).

```sh
cd tui
go mod tidy   # resolve the dependency graph + go.sum
go build ./...
go test ./...  # unit tests: dry-run safety, sudo retries, screen routing
go run . --dryrun
```

See `TESTING.md` for the full test plan and how to drive the TUI headlessly.
