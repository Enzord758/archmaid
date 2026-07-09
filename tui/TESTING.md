# TESTING — test suite reference

The Bubble Tea rewrite (this `tui/` directory, `package main`) has a full test
suite: unit tests driving `Update`, `teatest` golden frames, and a VHS e2e
harness. This doc is the inventory and how-to-run.

## Ground rules

- **Dry run stubs everything destructive.** `--dryrun` (global `dryRun`) makes
  mutating steps report "would run" instead of executing, skips sudo, and logs to
  `~/.archmaid-dryrun.log`. Tests that would otherwise run real commands set
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
  `logRetention` (1 week) at startup — untested; a good add is a temp-`logDir`
  fixture with `os.Chtimes`-aged `.log` files asserting only the stale ones go.
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
go build -o archmaid . && ./archmaid --dryrun        # manual e2e; then inspect ~/.archmaid-dryrun.log
```

Current status: **55 tests, all green under `-race`; `vet` and `gofmt` clean.**

## Newly added — needs tests (Fable): sudo askpass

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
- **`runStep`** now runs sudo steps as `sudo -A <args>` (was `sudo -n`), and sets
  `SysProcAttr{Setsid: true}` for `topgrade`/`yay` (they shell out to sudo
  themselves; no tty → they use `SUDO_ASKPASS`). Assert the argv/`SysProcAttr` for
  a sudo step, a topgrade step, and a plain step.
- **Model** (`askpassMsg`): shows the password popup over the current screen
  (`m.bg`), holds `m.askpassConn`; on submit writes `password+"\n"` to the conn &
  closes (pass-through), on cancel closes it. `handleStream`'s drop-guard now
  only drops on `screenMenu`/`screenSummary` so streaming keeps pumping while the
  askpass popup is up over a running step. Assert: `askpassMsg` → `screenPassword`
  with `askpassConn` set; the prime-mode prompt (`askpassConn == nil`) still calls
  `authSudo`.

The prior prime/gate/keep-alive flow is unchanged (none of it stores the
password), so their existing tests still hold — only the sudo argv changed
(`-n` → `-A`), which no current test asserts.

## Test inventory

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
`~/.archmaid-dryrun.log` (under a throwaway `HOME`). Needs `vhs`/`ttyd`/`ffmpeg`.

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
