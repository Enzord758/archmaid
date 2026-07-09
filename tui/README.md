# cleanup TUI (bubbletea sketch)

A [Bubble Tea](https://github.com/charmbracelet/bubbletea) rewrite sketch of the
gum-based `cleanup.sh`, exploring what it takes to make this a real TUI —
`btop`/`wiremix`-style — rather than a scripted sequence of prompts.

**This is a sketch, not a finished program.** It shows the architecture and
the parts that are genuinely hard in bash; several actions are stubbed.

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
- **Error-only logging** to `~/.maintenance_tui.log` (`log.go`), same policy and
  path as the bash version.

## Mapping to cleanup.sh

| cleanup.sh | here |
| --- | --- |
| `run_task` (gum spin + tempfile) | `runStep` + `waitForStream` streaming |
| `start_msg`/`end_msg`/`fail_msg` | `stepState` + `checklist()` |
| `sudo -v` priming | `primeSudo` via `tea.ExecProcess` |
| `gum confirm` | `screenConfirm` |
| `gum choose` menu | `screenMenu` |
| `view_log` + sed colouring | `screenLog` + `lipgloss` styles |
| `system_status` | `systemStatus` + `refreshStatus` tick |

## Still stubbed

- **Repair broken packages** / **orphan removal** — need a discovery pass
  (`pacman -Qk`, `pacman -Qdtq`) whose output splices into a step's args before
  the queue runs.
- **Disable unused services** — a `bubbles/list` multi-select over the
  enabled-but-inactive units.
- **Delete old downloads**, **Edit topgrade config** (`tea.ExecProcess $EDITOR`).
- Aborting a running step should SIGINT the child, not quit the program.

## Build

Needs Go (`pacman -S go`); not yet installed on this machine.

```sh
cd tui
go mod tidy   # resolve the dependency graph + go.sum
go run .
```
