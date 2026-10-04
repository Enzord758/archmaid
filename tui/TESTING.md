# TESTING

How archmaid is tested, the rules that keep the suite safe and deterministic,
and how to add a test. For *what the app does* see `README.md`; for design
decisions and open work see `HANDOVER.md`.

## Strategy

A bottom-heavy pyramid. Almost everything is a fast unit test against
`model.Update`; above that, whole actions are recorded as transcripts, a set of
rendered-frame goldens pins every screen and popup, and there is one
real-binary end-to-end run.

| Layer | Tool | What it is for |
|---|---|---|
| Unit | plain `go test`, driving `Update` with `tea.Msg`s | State transitions, command construction, parsers, log routing. The bulk of the suite. |
| Transcript | a driver pumping a whole menu action through `Update` against `sh` shims | What an action *does* end to end — programs run, prompts, plan, step results, log entries — as a frontend-agnostic golden. |
| Layout/render | direct `View()` assertions + `teatest` goldens | Frames at a pinned size and color profile; the output pane never overflowing the terminal. |
| Real processes | a real `sleep` child, real signals | Only where behaviour *is* the OS (abort → SIGINT → SIGKILL escalation). |
| E2E | VHS drives the real binary in a pty under `--dryrun` | One smoke path proving the wiring (build, alt-screen, keys, log file). |

Keep new tests at the lowest layer that can prove the behaviour.

## Running the gate

```sh
../scripts/check.sh                 # from tui/; or scripts/check.sh from the repo root
```

Every commit must pass it. In order, it runs `go test -race -count=3 ./...`
(three runs to shake out flakes), `go vet ./...`, the `gofmt -l .` check, then a
coverage run into a temporary profile (removed on exit) and enforces the floors
set at the top of the script: `MIN_TOTAL` for the whole package and
`MIN_ASKPASS` for `askpass.go`, which carries the sudo password. Per-file
coverage is computed from the profile itself. When coverage rises, raise the
floor in the same commit; never lower one to make a change pass.

## Running

```sh
go test -race ./...                 # the quick loop — must be green
go vet ./... && gofmt -l .          # must print nothing
go test -run TestAbort ./...        # a subset by name
go test ./... -run Golden -update   # regenerate goldens after a deliberate UI change
go test -run TestTranscript ./...   # the transcripts; -update-transcripts rewrites them
bash e2e/run.sh                     # VHS e2e (needs vhs, ttyd, ffmpeg)
go build -o archmaid . && ./archmaid --dryrun   # manual poke; logs go to ~/.local/state/archmaid/dryrun/
```

`go build ./...` (no `-o`) leaves a `tui` binary here; it's gitignored.
Don't track a test count in docs — it rots on the next commit; `go test -list .`
is the source of truth.

## Ground rules

These are what make the suite safe to run on a real machine. Break one and a
test can delete your files or need root.

1. **Nothing destructive ever runs.** Tests that would execute a mutating step
   set `dryRun = true` (steps then only *report* `[dry-run] would run: …`) or
   shim the binary. Never exercise the real `paccache -rk0`, `rm -rf`,
   `systemctl disable`, `pacman -Rns` or a real `sudo`.
2. **No real state.** Tests that write a log, touch `~/.config` / `~/Downloads`
   or read `$HOME` set `t.Setenv("HOME", t.TempDir())`. Tests that read or write
   run logs or the AUR snapshot also set `logDir = t.TempDir()` (restored on
   cleanup), so nothing lands in the real `~/.local/state/archmaid`.
3. **No real external programs.** `pacman`, `sudo`, `su`, `systemctl` etc. are
   replaced by shell shims placed first on `PATH` (see Recipes).
4. **No network.** The AUR RPC is an `httptest` server reached through the
   `aurRPCURL` var. `TestMain` sets `aurRPCURL` to a closed local port, so any
   test that reaches the audit without `fakeAUR` (Full Cleanup and Run topgrade
   start with it) fails fast instead of calling the real AUR.
5. **Deterministic frames.** `TestMain` (in `teatest_test.go`) forces the
   lipgloss ASCII profile so goldens are plain text. Goldens compare one
   re-rendered `View()` frame with the live status line pinned by
   `fixedModel()`; pin anything else time- or machine-dependent the same way.
   The spinner is pinned to one frame where a running row must render.
6. **Don't weaken a test to make it pass.** A failure is a bug in the code (or
   a deliberate behaviour change — then change the test and say why).

### Test seams

Package-level vars exist purely so tests can intercept side effects; use them
rather than adding more indirection:

| Var | Seam for |
|---|---|
| `dryRun` | stub every mutating step |
| `logDir` | redirect run logs + the audit snapshot |
| `execProcess` | capture the editor command instead of handing over the terminal (`tea.ExecProcess`) |
| `sigkillGrace` | shrink the SIGINT→SIGKILL grace period |
| `aurRPCURL` | point the AUR audit at `httptest` |
| `tickFn` | `tea.Tick` for the status refresh and sudo keep-alive; the transcript driver drops the ticks |

`TestMain` also dispatches `--askpass <sock> [prompt]` to `runAskpass` exactly
as `main()` does: under `go test` the `SUDO_ASKPASS` wrapper execs the test
binary, so the askpass round trip runs for real.

## Recipes

**Drive the model.** `update(m, msg)` runs one `Update` and returns the model;
`key("enter")` builds a `tea.KeyMsg` the way `handleKey` matches it (space is
`tea.KeySpace`, which stringifies to `" "`). To test a returned `tea.Cmd`, call
it and assert on the `tea.Msg`.

**Shim a program.** Write a tiny `sh` script into `t.TempDir()`, `chmod 0755`,
and prepend that dir to `PATH` with `t.Setenv`. Existing helpers:
`shimPacman` (`-Qdtq` orphans), `shimPacmanCascade` (adds `-Rs --print`),
`shimPacmanForeign` (`-Qqm`), `shimEchoArgs` (prints its argv),
`shimAuthPair` (a `su`/`sudo` pair), `shimExclusivePath` (a PATH containing only
named tools, for "not installed" branches). Two gotchas: emit newlines through
printf *format* escapes, not `%s` (sh `printf` doesn't expand `\n` in
arguments); and print argv with `printf '%s\n' "$*"` — `echo "$@"` swallows a
leading `-n`, which is exactly what the sudo tests are trying to prove.

**Test an in-process step.** Build a queue, call `startQueue()`, execute the
returned `tea.Cmd` synchronously and feed its `stepResultMsg` back through
`update` (see `startAudit`). If the step after it could run a real command when
the test gets gating wrong, substitute a harmless stand-in (`command: "true"`,
as `harmlessTopgrade` does).

**Fake the AUR.** `fakeAUR(t, db)` serves the RPC from a map and records
request count and batches; it also fails the test if a lookup isn't a POST.
`auditEnv(t)` gives a private `HOME` + `logDir` for anything that persists.

**Add or change a golden.** Write the test, run
`go test ./... -run Golden -update`, then **read the diff** — it should be
exactly the change you intended — before committing the `.golden` file. Prefer
a direct frame (`viewGolden(t, m)` on a `sizedModel()`, see `golden_test.go`):
a live teatest program ticks the spinner and the input cursor, so its frames
drift. Use a teatest program (`TestMenuGolden`) only when the point is the
real program's key handling.

**Test a real signal path.** Spawn a real child (`sleep`, or a script that traps
SIGINT), never a mock; shrink `sigkillGrace`; assert on process liveness with a
signal-0 probe (see `abort_test.go`).

## Transcripts

`transcript_test.go` is the harness; `transcript_scenarios_test.go` holds the
scenarios, each a menu action recorded to
`testdata/transcripts/<action>_<case>.<mode>.golden` — `.dryrun` under
`dryRun`, `.shim` run for real against shims. They characterize today's
behaviour, bugs included: a fix that changes behaviour changes a golden, and
the diff is the review.

```sh
go test -run TestTranscript ./...                       # compare
go test -run 'TestTranscript/fullcleanup_' ./...        # a subset
go test -run TestTranscript ./... -update-transcripts   # rewrite, then read the diff
go test -race -count=5 -run Transcript ./...            # must stay deterministic
```

**Format.** One event per line, `<kind padded to 5> <fields>`:

| Kind | Fields |
|---|---|
| `action` | the menu label, without its icon |
| `exec` | a shimmed program's argv, from the shim log (`su $USER …` normalised) |
| `ask` | `"<confirm text>" -> yes\|no` |
| `auth` | `password -> <correct>\|<wrong>` |
| `pick` | `[<services offered>] -> [<ticked>]` |
| `plan` | `N steps`, then `<i> "<title>" inproc\|argv=… [sudo] [confirm] [optional] [alwayslog] cat=<c>` per step; re-emitted when a step's flags change mid-run (the audit gating topgrade) |
| `step` | `<i> running\|ok\|failed\|skipped` |
| `out` | `<i> <line>` of a step's output, as the model accepted it |
| `log` | `<cat> <KIND>: <label>` — the entry header, no timestamp or run dir |
| `snap` | the AUR maintainer snapshot, whenever its content changes |
| `abort` | `<i> "<title>"` — ctrl+c, then y |
| `note` | a notice popup's text |
| `audit` | a line of the AUR-audit report screen |
| `end` | `outcome=completed\|aborted\|cancelled\|notice\|report errs=N skipped=N` |

Within one `Update` the order is fixed: exec, out, finished steps, changed plan
flags, log entries (grouped by category file), snapshot, then steps that
started, then a notice or report. `$HOME` and the log dir are normalised.

**The driver.** Runs each `tea.Cmd` synchronously, FIFO, feeding its msg back
through `Update`; `tea.BatchMsg` is expanded in order; ticks come back through
`tickFn` as an immediate msg that's dropped; `Init` is never called, so the
spinner never ticks, and the password cursor is static. A msg type it doesn't
know (e.g. a `tea.Sequence`) fails the test — teach it rather than ignore it.
When the queue is empty an open prompt is answered from the script
(`Answer{Kind, Match, Reply}`: `confirm`, `password`, `select`, or `abort`,
which fires when the matching step turns running); an unscripted prompt fails
and dumps the transcript so far, and so does an unused answer. A step child
logs itself asynchronously, so its `exec` line is taken with its first output
or exit, never from the `Update` that launched it.

**Shims.** `installShims(t, Shims{"pacman": {{Prefix: "-Qdtq", Out: "a\nb"}}},
passthrough...)` writes one `#!/bin/sh` per command: it logs `<cmd> $*` to
`$ARCHMAID_SHIM_LOG`, then the first `Rule` whose `Prefix` starts `$*` runs
`Script`, prints `Out` and exits `Exit` (or, with `Block`, `exec sleep 30` to be
aborted; `TrapINT` ignores SIGINT so only the SIGKILL escalation ends it).
Unmatched argv exits 0. PATH is *only* that dir, so anything unshimmed is "not
installed"; `passthrough` symlinks real tools (`bash`, `find`, …) in for
actions that need them — always under the scratch `HOME` from
`transcriptEnv`. The AUR is `fakeAUR` plus `saveBaseline` for the snapshot.

**Add a scenario.** Append to `transcriptScenarios`: `transcriptEnv(t, dry)`,
shims, fakes, then `NewDriver(t, shimLog, answers...)` and `d.Select("<menu
label>")`. Run with `-update-transcripts` and read the new golden line by line
— it's the spec you're signing off.

## Coverage map

What each file pins. Behaviour contracts, not test names — grep the file for
the names.

| File | Pins |
|---|---|
| `main_test.go` | The core `Update` behaviours. Dry-run safety (a marker file proves nothing ran). Sudo flow: cached probe skips the prompt; wrong passwords show attempts left; three strikes skips sudo steps, logs `SKIP:`, and counts them as skipped, not failed. Confirm flow: optional "no" skips one step; non-optional "no" aborts and marks the rest skipped. Streaming: ordered lines then exactly one done; failure → `statusFailed`, `errs++`, `FAIL:` log. Discovery routing, services multi-select, parsers. Log categories: a step logs to its own category file; `alwaysLog` steps log on success, ordinary steps stay silent; the View-log picker opens/returns correctly. |
| `gate_orphan_test.go` | The proceed-gate (only when sudo is already cached; the password prompt is the gate otherwise). The orphan step is spliced in only when orphans exist; its confirm previews the real `pacman -Rns` cascade total (both counts when they differ, plain count when equal, falls back to the direct count if the probe fails). Temp files are cleaned with `sudo systemd-tmpfiles --clean`, not a find/rm. |
| `sudo_hardening_test.go` | Sudo steps run as `sudo -A` (password via `SUDO_ASKPASS`, never the tty) and other commands are untouched; the keep-alive loop arms only for real sudo queues and disarms on summary/abort; `pruneLogs` keeps the newest 5 run directories and exempts `dryrun/` and non-run entries. |
| `auth_test.go` | Two-stage prime: a wrong password never reaches `sudo` (no faillock burn); a correct one mints the ticket; a lockout notice is surfaced verbatim; submit locks the input and ignores all keys until the result arrives. |
| `abort_test.go` | ctrl+c opens a confirm instead of quitting; "no" resumes with the child alive; "yes" really terminates a real child; a SIGINT-ignoring child is SIGKILLed after the grace period; late output after an abort is dropped. |
| `overlay_test.go` | Popups float, centered, over their background; ANSI-styled backgrounds keep their width; the abort dialog stays over the running screen. |
| `resize_test.go` | The output pane never overflows the terminal (running queue, log viewer, resize while a popup is open); askpass prompts over another popup keep the real background; nested popups resolve to the right base screen. |
| `repair_test.go` | Repair: authenticates first (password prompt, or straight to the scan when cached), esc drops the held scan, three wrong passwords end in a notice; the scan runs `sudo -n pacman -Qk`, treats pacman's exit 1 as a result, reports a missing ticket instead of "nothing broken", and counts "10 missing files" as broken. |
| `hyprpm_test.go` | `hyprpm` is disabled inside topgrade and runs as the last step, only when installed (it drops the sudo ticket on exit). |
| `coverage_test.go` | Thin spots: `editConfig` (creates defaults, never clobbers, via the `execProcess` seam); `discoverDownloads` against aged files; password masking in the rendered frame; every checklist glyph; the status line; all summary outcomes. |
| `audit_test.go` | AUR audit, fully offline: severity classification (adoption/maintainer change = high; orphaned, recent push, out of date, 0 votes, not-on-AUR = glance); local `-debug` companions skipped; chunked POST lookups and RPC error handling, an 8 MiB response cap, results for names that weren't requested dropped; AUR names and maintainers stripped of control/format characters and length-capped before display (the raw value still drives the comparison and the snapshot); the maintainer snapshot (round-trip, kept for missing packages, written atomically at 0600, a held lock skips the save, a corrupt or unreadable file is never "no baseline": it fails closed as a high finding that gates topgrade, is left alone until accepted, then kept aside as `.corrupt-<time>`); an end-to-end first-audit-then-takeover run; dry run never advances the snapshot; the menu audit saves all but the high findings and `a` on the report accepts those (q drops the accept, so they stay flagged); the screen/notice flow. |
| `fullcleanup_audit_test.go` | Full Cleanup's audit step: it's first, in-process, `alwaysLog`, category `audit`; both topgrade steps are `alwaysLog`. A maintainer change gates topgrade with a confirm naming the package(s), defaulting to No, before topgrade starts; declining skips only the update and logs `SKIP: … (declined)`; the report is in the output pane and `audit.log`. No foreign packages is a clean pass; an unreachable AUR fails the step without gating. A stale or post-abort result is dropped. In a dry run it still audits, logs exactly once, and leaves the snapshot alone, even when the gate is accepted. The step itself never saves the snapshot: with nothing high the model commits it at once; behind a gate only "yes" saves it (logged `ACCEPT`), while "no" or a gate with no step after it leaves it, so the next run gates again. |
| `topgrade_audit_test.go` | Run topgrade's queue is [audit, topgrade], built from the same `auditStep`, and gated identically: a maintainer change puts a named confirm defaulting to No in front of topgrade; "no" skips the update (logged) and leaves the snapshot, so the next run gates again; "yes" launches it and saves the snapshot (logged `ACCEPT`); an unreachable AUR fails the audit without gating; a dry run gates but never saves. |
| `askpass_test.go` | The `SUDO_ASKPASS` round trip over a real unix socket: the helper prints the answer and exits 0, exits 1 on an unanswered close or no socket; the server writes a 0700 wrapper on its socket, sets `SUDO_ASKPASS`, delivers prompts and `close()` removes both, and stays unwired (but safely closable) when the socket or wrapper can't be made; the real wrapper end to end; submit writes the password straight to the connection, cancel closes it unanswered. |
| `transcript_*_test.go` + `testdata/transcripts/` | Whole actions end to end against shims — see Transcripts above and the scenario names. |
| `teatest_test.go` + `testdata/` | Headless-program goldens for the menu and the services multi-select. |
| `golden_test.go` + `testdata/` | Direct `View()` goldens at 80×24: the summary for each outcome (ok, issues, skipped, aborted); the password popup over the menu (masked input, and with the attempts-left error); a confirm over the running checklist; the log picker; the audit report; a notice; the discovering spinner. |
| `e2e/run.tape` + `run.sh` | The real binary under `--dryrun` in a throwaway `HOME`: menu → Clear user cache → confirm → asserts `dryrun/cleanup.log` contains the `DRYRUN:` entry. |

## Known gaps

- **The audit gate was live-tested by hand, not automatically:** a dry-run Full
  Cleanup of the real binary via VHS, real AUR, with a scratch `HOME` whose
  seeded snapshot gave one real package a fake previous maintainer. A VHS tape
  for it would need a stable fake AUR, so it isn't in `e2e/`.
- **E2E covers only Clear user cache.** Candidates: the services multi-select
  (fake `systemctl` via a PATH shim), Full Cleanup's orphan confirm (shimmed
  `pacman`), and abort. The audit screen needs a fake AUR, so it stays a unit
  test.
- **Inherently not automated:** the OS-level terminal handover inside
  `tea.ExecProcess` (we assert the command built, not that the editor takes
  over); `tea.Tick`'s wall-clock firing (a library primitive); that `sudo`
  relays SIGINT through a whole process tree (we verify a direct child).
- **Outside this repo:** the yay cooldown hook (`~/dotfiles/yay`). Its logic was
  checked under `luajit` with a stubbed `yay` API; there's no automated test.
