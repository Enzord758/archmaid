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

Current status: **~50 tests, all green under `-race`; `vet` and `gofmt` clean.**

## Newly added — needs tests (Fable)

Two behaviours landed without tests; they're the priority to cover next.

### Proceed-gate rolled into the sudo prompt (`main.go`)

A `steps`-action can carry a `gate` string (only **Full Cleanup** does:
`"Proceed with full cleanup?"`). The gate replaces a separate "are you sure"
step — the sudo password prompt already forces a deliberate action, so:

- **Sudo uncached** → the password prompt is the gate; **no** extra confirm.
  (`sudoCachedMsg{cached:false}` → `screenPassword`, as before.)
- **Sudo cached** (`sudoCachedMsg{cached:true}`) → if `m.gate != ""`, show a
  confirm popup with `gateActive = true` over the menu; **yes** →
  `startQueue`, **no** → back to `screenMenu`. If `m.gate == ""` (every other
  action), run straight to the queue as before.
- `resolveConfirm` branches on `m.gateActive` *before* the per-step logic, so a
  gate confirm is not mistaken for a step confirm (no `m.queue` exists yet).
- `activate` resets `m.gate = ""` and only re-arms it for the chosen item;
  `beginPending` resets `m.gateActive = false`.

Worth asserting: cached Full Cleanup → gate confirm shown; "no" → menu, nothing
runs; "yes" → queue starts. Cached non-gate action (e.g. Clear user cache is
sudo-less, or a `repair` with cached sudo) → no gate. Uncached Full Cleanup →
password screen, not the gate confirm.

### Orphan removal in Full Cleanup (`tasks.go`)

`fullCleanupSteps()` now runs `topgrade` **first**, then queries
`orphanPackages()` (`pacman -Qdtq`) at build time: when orphans exist it inserts
an optional sudo step `"Remove N orphaned package(s)?"` with the real package
list spliced into `pacman -Rns --noconfirm …`; when there are none the step is
omitted entirely. Fixture idea: since `orphanPackages` shells out, either test it
behind a PATH shim for `pacman`, or unit-test the step-assembly by factoring the
list in (current form calls `pacman` directly — a shim is simplest).

## Test inventory

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
