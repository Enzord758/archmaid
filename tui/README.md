# archmaid (TUI)

The archmaid app: a [Bubble Tea](https://github.com/charmbracelet/bubbletea) TUI
for Arch maintenance — `btop`/`wiremix`-style, an event loop that redraws as
state changes, rather than a scripted sequence of prompts.

All nine menu actions are implemented, with a `--dryrun` mode to exercise it
safely end to end. It grew out of a gum bash script (now retired); the sections
below explain the design that replaced it.

## Why a TUI (not a gum script)

`gum` is built for scripted flows: ask, run, print, repeat. A real TUI needs an
event loop that redraws panels as state changes — live command output, a status
header that updates on its own, keyboard navigation between views. That's an
architectural difference bash/gum can't cross, so it moves to Go + Bubble Tea
(the same toolkit `gum` itself is built on, so the Tokyo Night styling carries
straight over via `lipgloss`).

## What it does

- **The Elm-style model/update/view loop**, split across `model.go` (state),
  `update.go` (`Update`/`handleKey`), and `view.go` (`View` + renderers) —
  twelve screens: menu, password, confirm, running, summary, log-category
  picker, log pager, notice, discovery spinner, services multi-select,
  abort-confirm, and the AUR-audit report.
- **Live streaming command output** (`tasks.go`) — the whole point. A command's
  combined stdout/stderr streams into a scrollable viewport line by line while a
  spinner runs, instead of gum swallowing it and dumping a pager afterward. One
  ordered channel (line messages, then a single done message) keeps output and
  exit status race-free; `waitForStream` pumps it through `Update`.
- **A live checklist** of steps with per-step status (pending/running/ok/failed/
  skipped), mirroring `start_msg`/`end_msg`/`fail_msg`.
- **A cascade-aware orphan-removal confirm** — `pacman -Rns` recursively drops
  a removed package's own now-unneeded dependencies (`-s`), so the real removal
  count can run well past the direct orphan list. The confirm previews that
  with a read-only `pacman -Rs --print` pass (`orphanRemovalTotal` in
  `tasks.go`) and shows both counts when they differ, instead of only the
  direct count.
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
  screen: repair broken packages (`pacman -Qkq`), delete old downloads
  (entries whose ctime is 30+ days old, named in the confirm, re-checked and
  removed in-process through an `os.Root`), and a **multi-select**
  (`screenServices`) of enabled-but-inactive services to disable: `systemctl list-units --all --state=inactive` plus one batched `is-enabled` (no `list-unit-files`, which costs ~430 ms; templates, instances and `●` not-found rows are dropped). "Nothing to do" routes to a notice.
- **AUR audit** (`audit.go`) — a read-only check of the foreign packages
  (`pacman -Qqm`) against the AUR RPC, aimed at the adopt-an-orphan-then-push
  takeover pattern. Flags: **adopted / maintainer changed / co-maintainer
  added** since the last audit (high severity — a co-maintainer can push to the
  package just like its maintainer; the RPC only exposes the *current*
  maintainer and co-maintainers, so this diffs against a snapshot the previous
  audit saved to `~/.local/state/archmaid/aur-maintainers.json`; the first
  audit just records the baseline, and so does the first audit after upgrading
  from a version that didn't record co-maintainers, for those), a removed
  co-maintainer, orphaned, pushed to within 72h, flagged out of date, 0 votes,
  and not on the AUR at all (locally built `-debug` companions are ignored). It
  makes archmaid's only network call, never changes the system, and results
  also land in the `AUR audit` log category. A dry run reads the snapshot but
  doesn't advance it. **Full Cleanup and Run topgrade run it as their first
  step**, directly in front of topgrade: a high-severity finding puts a
  confirm naming the package(s) in front of the update (defaults to No;
  declining skips only the update, and in Full Cleanup the rest of the
  cleanup runs). A maintainer change or added co-maintainer is only
  recorded once accepted (that confirm, or `a` on the audit report), so until
  then it's flagged on every audit; an unreadable snapshot is flagged high
  too, never treated as a first audit. An unreachable AUR fails that step
  but doesn't gate anything. The 72h window matches the yay cooldown hook in
  `~/.config/yay/init.lua` (holds back AUR upgrades pushed to in the last 72h,
  logged to `~/.local/state/yay/cooldown.log`; `YAY_NO_COOLDOWN=1` bypasses it).
  That hook lives in the dotfiles, not in this repo — archmaid's Full Cleanup
  runs yay unattended via topgrade, so the hold has to be yay-side.
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
  everything else). Ordinary steps only log on failure; topgrade, the AUR
  audit, and steps that remove or overwrite something (orphan removal,
  cache/temp/log wipes, service disabling, downloads/repair cleanup) log their
  outcome even on success, and a `SKIP` entry when declined, so
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

## Downloads folder

"Delete old downloads" cleans the first of these that is an existing folder:
`--downloads <dir>`, `$ARCHMAID_DOWNLOADS`, `XDG_DOWNLOAD_DIR` from
`$XDG_CONFIG_HOME/user-dirs.dirs`, `~/Downloads`, `~/Download`. Symlinks are
resolved first, and every candidate but the flag must lie strictly inside
`$HOME` — `$HOME` itself (e.g. `XDG_DOWNLOAD_DIR="$HOME/"`), `/` and anything
above `$HOME` are skipped. The flag may point outside `$HOME` but is still
refused for `$HOME`, `/` or an ancestor, and a bad flag is reported rather than
skipped. The confirm names the folder it picked.

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

## Status

The app is feature-complete. Open items and the decisions not to undo are in
[`HANDOVER.md`](HANDOVER.md); test gaps are in [`TESTING.md`](TESTING.md) →
Known gaps.

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
destructive step's outcome even on success) and `audit.log` (every AUR audit), and a single reused `dryrun/`
directory for `--dryrun` runs. Only the newest 5 run directories are kept;
older ones are pruned on startup.
