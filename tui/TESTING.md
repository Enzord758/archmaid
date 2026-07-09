# TESTING — test suite reference

The Bubble Tea rewrite (this `tui/` directory, `package main`) has a full test
suite: unit tests driving `Update`, `teatest` golden frames, and a VHS e2e
harness. This doc is the inventory and how-to-run.

## Ground rules

- **Dry run stubs everything destructive.** `--dryrun` (global `dryRun`) makes
  mutating steps report "would run" instead of executing, skips sudo, and logs to
  `~/.local/state/archmaid/dryrun.log`. Tests that would otherwise run real commands set
  `dryRun = true`. Never exercise the real `paccache -rk0` / `rm -rf` /
  `systemctl disable` paths in a test.
- **`HOME` is redirected** with `t.Setenv("HOME", t.TempDir())` in any test that
  writes a log or touches `~/.config` / `~/Downloads`, so tests never touch real
  files.
- **Logs live in `~/.local/state/archmaid/`** (see `log.go`): real runs →
  `<YYYYMMDD-HHMM>.log` (one per run, failures only), dry runs → a single
  `dryrun.log`. `resolveLogDir()` uses `$HOME/.local/state/archmaid` unless the
  `logDir` var is overridden. Real-log tests set `logDir = t.TempDir()`
  (restored via defer) so they never touch the user's real state dir
  (`TestSudoThreeStrikesSkipsAndLogs`, `TestStreamFailurePropagates`); dry-run
  tests isolate via `HOME`. `pruneLogs()` deletes run logs older than
  `logRetention` (1 week) at startup — covered by
  `TestPruneLogsDeletesOnlyStaleRunLogs` (temp-`logDir`, `os.Chtimes`-aged files).
- **Determinism**: `TestMain` forces `lipgloss` to the ASCII (no-color) profile;
  goldens compare a single re-rendered `View()` frame with the live status line
  pinned via `fixedModel()`; the spinner is pinned to one frame where a running
  row must render deterministically.
- Don't weaken a test to make it pass — a failure is a bug in the code.

## Running

```sh
go test ./...            # everything (~1.7s)
go test -race ./...      # with the race detector (the CI-grade run)
go vet ./... && gofmt -l .   # must be clean
go test -run TestAbort   # a subset by name
go test ./... -run Golden -update   # regenerate goldens after a deliberate UI change
bash e2e/run.sh          # VHS e2e; builds ./archmaid, drives it under --dryrun, asserts on the log
go build -o archmaid . && ./archmaid --dryrun   # manual e2e; then inspect ~/.local/state/archmaid/dryrun.log
```

Current status: **75 tests, all green under `-race`; `vet` and `gofmt` clean.**

## Still needs tests: sudo askpass

Sudo password prompts now route to a TUI popup via `SUDO_ASKPASS`, so no sudo
call ever prompts on the tty (which hung/collided with bubbletea) — the fix for
"Cleaning AUR cache pops a raw sudo prompt" and the `sudo -n` "password required"
failures during a long Full Cleanup. The password is passed straight through,
never stored. `askpass.go` + `main.go`/`tasks.go`; not yet unit-tested.

- **`askpass.go` round-trip** — `runAskpass(sock, prompt)` dials the socket,
  sends the prompt, prints back the line it receives, exit 0; exit 1 if it can't
  connect or the connection closes unanswered. Testable with a fake unix-socket
  server (see the manual check that's already been done: send a password, assert
  it's printed to stdout). `startAskpassServer` writes a wrapper script + socket
  and sets `SUDO_ASKPASS`; `close()` removes both.
- **Ticket sharing = plain children, on purpose** (history: two reverted
  attempts live in git — per-child `Setsid` isolated every internal sudo into
  its own ppid-keyed ticket (repeated popups during topgrade), and a shared-pty
  `ptysession.go` failed because sudo's tty tickets are keyed to tty **and
  session** — per-child Setsid meant no match, so topgrade's sudo prompted on
  the hidden pty → freeze). Steps now run as plain children: they inherit
  archmaid's session + controlling terminal, so the prime's ticket is shared by
  every step **and** everything topgrade/yay spawn. Don't reintroduce Setsid on
  step children. Verified live (real password, tmux pty): prime → nested
  `sh -c 'sudo -n true'` passes; a full live Full Cleanup completed with no
  freeze, no raw prompts, no log failures.
- **hyprpm runs last** (root-caused by step isolation with a real ticket, then
  confirmed via binary symbols `NSys::root::cacheSudo`/`dropSudo`): `hyprpm`
  drops the sudo ticket on exit — even a no-op `hyprpm update` kills the primed
  credentials, silently breaking the keep-alive and forcing an askpass popup on
  the next sudo step. Full Cleanup therefore runs topgrade with
  `--disable hyprpm` and appends a dedicated "Updating Hyprland plugins" step
  at the END of the queue (only when hyprpm is installed — `exec.LookPath`),
  where its ticket-drop can't affect anything. Live-verified: a full run now
  shows exactly ONE password popup (the prime), zero mid-run. Covered by
  `hyprpm_test.go`: `TestFullCleanupDisablesHyprpmInTopgrade`,
  `TestHyprpmStepLastWhenInstalled`, `TestHyprpmStepOmittedWhenNotInstalled`
  (both PATH branches via `shimExclusivePath`), and
  `TestStandaloneTopgradeKeepsHyprpm` (the standalone action deliberately keeps
  hyprpm — nothing runs after it there).
- **Model** (`askpassMsg`): shows the password popup over the current screen
  (`m.bg`), holds `m.askpassConn`; on submit writes `password+"\n"` to the conn &
  closes (pass-through), on cancel closes it. `handleStream`'s drop-guard now
  only drops on `screenMenu`/`screenSummary` so streaming keeps pumping while the
  askpass popup is up over a running step. Assert: `askpassMsg` → `screenPassword`
  with `askpassConn` set; the prime-mode prompt (`askpassConn == nil`) still calls
  `authSudo`.

The prime/gate/keep-alive flow and the `sudo -A` argv rewrite are covered by
`sudo_hardening_test.go` (see inventory).

## Test inventory

### Sudo hardening + log pruning — `sudo_hardening_test.go`

The `sudo -A` rewrite, the keep-alive loop, and `pruneLogs`, all hermetic (PATH
shims; no real sudo/pacman touched).

| Test | Asserts |
|---|---|
| `TestSudoStepGetsDashA` | a sudo step execs `sudo -n <args>` (shimmed sudo prints argv) |
| `TestNonSudoStepUnchanged` | non-sudo commands keep their argv (no `-n` injected) |
| `TestTopgradeStepsNeedSudo` | standalone + Full Cleanup topgrade steps are `needsSudo` |
| `TestKeepAliveStartsWithSudoQueue` | real-run sudo queue arms `sudoAlive` (confirm pauses before exec) |
| `TestKeepAliveNotStartedInDryRun` | dry run never arms the keep-alive |
| `TestKeepAliveReschedulesWhileAlive` | armed + `sudoKeepAliveMsg` → refresh+tick cmd, stays armed |
| `TestKeepAliveNoopWhenCleared` | disarmed + stale msg → nil cmd (loop ends) |
| `TestKeepAliveClearedOnSummary` | queue exhausted → summary disarms |
| `TestKeepAliveClearedOnConfirmAbort` | non-optional "no" abort disarms |
| `TestKeepAliveClearedOnAbortRunning` | ctrl+c abort disarms (nil proc safe) |
| `TestPruneLogsDeletesOnlyStaleRunLogs` | Chtimes-aged fixture: stale `.log` pruned; fresh logs, non-`.log`, dirs survive |

Shim gotcha: the argv shim must print with `printf '%s\n' "$*"` — `echo "$@"`
swallows the leading `-n` it is supposed to prove.


### Proceed-gate + orphan removal — `gate_orphan_test.go`

The gate (Full Cleanup's "Proceed with full cleanup?" shown only when sudo is
already cached — the password prompt is the gate otherwise) and the build-time
orphan splice in `fullCleanupSteps()`.

| Test | Asserts |
|---|---|
| `TestGateShownWhenSudoCached` | cached probe + gated action → gate confirm over the menu, defaults No, no queue yet |
| `TestGateNoReturnsToMenu` | gate "no" → menu, `gateActive` cleared, nothing runs |
| `TestGateYesStartsQueue` | gate "yes" → queue starts with all pending steps |
| `TestNoGateForUngatedAction` | cached probe without a gate → straight to the queue |
| `TestStaleGateClearedByActivate` | `activate` clears a leftover gate before dispatching another action |
| `TestGateUncachedShowsPasswordNotGate` | uncached probe → password screen, gate confirm never activates |
| `TestFullCleanupTopgradeFirst` | topgrade leads the Full Cleanup queue |
| `TestOrphanStepSplicedIn` | orphans present → optional sudo `pacman -Rns --noconfirm <list>` step, confirm shows the real count |
| `TestOrphanStepOmittedWhenNone` | no orphans → step omitted, queue exactly one shorter |

`orphanPackages` is shimmed via a fake `pacman` on `PATH` (`shimPacman`) so no
real package state is read; the gate tests reuse the shim to stay hermetic.

### Unit — `main_test.go` (drives `model.Update` / helpers directly)

| Area | Tests |
|---|---|
| Dry-run safety | `TestDryRunDoesNotExecute` (marker file proves no exec), `TestDryRunFullCleanupExecutesNothing`, `TestDryRunLogsEveryStep`, `TestCheckSudoCachedDryRunNeverProbes` |
| Sudo flow | `TestSudoCachedSkipsPrompt`, `TestSudoUncachedShowsPassword`, `TestWrongPasswordShowsAttemptsLeft`, `TestSudoThreeStrikes`, `TestSudoThreeStrikesSkipsAndLogs` (skip tallied separately from errs, logged `SKIP:`) |
| Confirm flow | `TestConfirmStepShowsModal`, `TestConfirmOptionalNoSkips`, `TestConfirmNonOptionalNoAborts` (declined + rest → skipped, `aborted` set), `TestConfirmYesLaunches` |
| Streaming | `TestStreamOrderAndSingleDone` (ordered lines then one done), `TestStreamFailurePropagates` (→ `statusFailed`, `errs++`, `FAIL:` log) |
| Discovery routing | `TestDiscoveryNotice`, `TestDiscoveryServices`, `TestDiscoveryPendingRunsQueue`, `TestDiscoveryPendingSudoProbes`, `TestDiscoverServicesRuns` |
| Services multi-select | `TestServiceToggle`, `TestServicesEnterBuildsDisableStep`, `TestServicesEnterNoneReturnsMenu` |
| Parsers | `TestBrokenPackagesParsing`, `TestBrokenPackagesNoMatchNoPanic`, `TestNonEmptyLinesTrimsAndDropsBlanks` |

### Thin-spot coverage — `coverage_test.go`

These close the gaps HANDOVER.md called out as untested:

| Thin spot | Test | Technique |
|---|---|---|
| `editConfig` non-dry path | `TestEditConfigCreatesFileAndOpensEditor`, `TestEditConfigKeepsExistingFile` | `execProcess` seam captures the editor command; asserts file created with defaults / not clobbered — no real editor launched |
| `discoverDownloads` real pipeline | `TestDiscoverDownloadsCountsAgedFiles`, `TestDiscoverDownloadsEmptyShowsNotice` | temp `$HOME/Downloads` + `os.Chtimes`-aged files; asserts the count in the confirm and the empty→notice path |
| Password masking (rendered) | `TestPasswordInputIsMasked` | types into `screenPassword`, asserts mask glyphs, never cleartext |
| Running checklist glyphs | `TestChecklistRendersAllStatuses` | spinner pinned to one frame → every status renders deterministically |
| status line / tick | `TestSystemStatusFormat`, `TestStatusMsgUpdatesHeader` | label contract + `statusMsg` → `m.status` + re-armed tick |
| Summary outcomes | `TestSummaryMessages` (table) | success / issues / skipped / issues+skipped / aborted |

### Abort flow — `abort_test.go`

ctrl+c while a step runs pops a confirm; yes kills the child and returns to menu.

| Test | Asserts |
|---|---|
| `TestAbortConfirmShownNotQuit` | ctrl+c on run screen → `screenAbortConfirm`, no `tea.Quit`, defaults to No |
| `TestAbortDeclineResumes` | "no" → back to `screenRunning`, process handle kept, real `sleep` child still alive (signal-0 probe) |
| `TestAbortAcceptSignalsAndMenus` | "yes" → child actually terminated (spawns real `sleep 30`, waits for exit), screen → menu, `proc` cleared |
| `TestAbortEscalatesToSigkill` | a child that traps/ignores SIGINT is force-killed after the (test-shrunk) grace period |
| `TestLateOutputDroppedAfterAbort` | a late `done` after leaving the run is dropped: no screen change, no `errs`, pumping stops |
| `TestAbortNilProcSafe` | aborting with no live process (dry run) still returns to menu |

### Overlay popups — `overlay_test.go`

Prompts render as bordered boxes composited over the current screen (`overlay.go`).

| Test | Asserts |
|---|---|
| `TestOverlayCentersAndPreservesBackground` | popup centered; background rows/edges preserved; every row keeps bg width |
| `TestOverlayIsANSIWidthSafe` | styled (colored) background keeps its visible width under the popup |
| `TestAbortPopupFloatsOverRunningScreen` | the abort dialog renders as a border box *over* the running screen — header + running step stay visible behind it |

### Integration goldens — `teatest_test.go` + `testdata/*.golden`

- `TestMenuGolden` — headless program, cursor moved, golden of the menu frame.
- `TestServicesGolden` — routes into the services multi-select, toggles entries,
  golden of the checklist.

### End-to-end — `e2e/run.tape` + `e2e/run.sh`

VHS drives the real binary in a pty under `--dryrun` and asserts on the resulting
`~/.local/state/archmaid/dryrun.log` (under a throwaway `HOME`). Needs `vhs`/`ttyd`/`ffmpeg`.

## Architecture (where the logic lives)

- `main.go` — the model/update/view loop. Screens: menu, password, confirm,
  running, summary, log, notice, discovering, services, **abortConfirm**. `Update`
  is the single source of state transitions; most unit tests drive it directly.
- `tasks.go` — `runStep` (starts the child, returns `*os.Process`, streams
  output), sudo commands, discovery cmds, `editConfig` (+ `execProcess` seam).
- `overlay.go` — ANSI-aware compositor that floats a popup box over a background
  screen; `View` uses it for the password/confirm/abort/notice screens.
- `log.go` — `appendLog(kind, label, body)`, `logPath()` (dry-run aware).

## Not automated (inherent)

- The OS-level terminal handover inside `tea.ExecProcess` (editor) — we assert the
  command built, not that the editor visually takes over.
- `tea.Tick`'s wall-clock firing (a library primitive).
- Full-tree kill semantics of aborting a `sudo` step rely on sudo relaying the
  signal; the unit tests cover a direct child, and the signal path is verified
  against a real `sleep` process.
