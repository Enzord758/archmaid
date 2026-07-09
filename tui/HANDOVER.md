# HANDOVER — test suite built (return handoff for TESTING.md)

Status: **done**. Every behaviour in TESTING.md's "Key testable behaviours" list
has a passing test; `go test ./...`, `go vet ./...`, and `gofmt -l .` are clean.
**No production code was changed** — the sketch passed every behaviour test as
written. Branch: `tui-test-suite` (based on `bubbletea-sketch`).

## What exists now

| Layer | File(s) | What it covers |
|---|---|---|
| Unit | `main_test.go` (seed + ~20 new tests) | all 7 TESTING.md behaviours, driven through `model.Update` |
| Integration | `teatest_test.go` + `testdata/*.golden` | headless `tea.Program`, golden frames for menu + services multi-select |
| E2E | `e2e/run.tape` + `e2e/run.sh` | VHS drives the real binary in a pty under `--dryrun`, asserts on the log |

## Running

```sh
go test ./...                        # unit + integration (~1.7s)
go test ./... -run Golden -update    # regenerate goldens after a deliberate UI change
bash e2e/run.sh                      # VHS e2e (needs vhs/ttyd/ffmpeg; builds ./archmaid itself)
```

## Determinism decisions (don't undo these casually)

- `TestMain` in `teatest_test.go` forces `lipgloss.SetColorProfile(termenv.Ascii)`
  so golden frames are plain text — no ANSI drift between terminals/CI.
- Goldens compare `FinalModel().View()` (a single re-rendered frame), not the
  raw output stream, and the live status line is pinned via `fixedModel()`
  (`testStatus` const). If you golden a new screen, pin anything live the same way.
- `e2e/run.sh` runs the app under a throwaway `HOME` (mktemp) so the e2e never
  touches the real `~/.archmaid-dryrun.log` and asserts on a clean log.
- Space key: `tea.KeyMsg{Type: tea.KeySpace}` stringifies to `" "` and hits the
  toggle case; the `key()` helper in `main_test.go` handles this.

## Deps

`go get github.com/charmbracelet/x/exp/teatest` pulled teatest + termenv as
direct deps and bumped bubbletea to v1.3.5, lipgloss to v1.1.0 (transitive
upgrades; everything still passes, including `-race`). `go mod tidy` has been run.

## Findings — real behaviours the tests pinned but you may want to change

1. **Skipped-for-sudo counts as an "issue"**: after `maxSudoAttempts` wrong
   passwords, each skipped sudo step increments `m.errs`, so the summary reads
   "⚠ Completed with N issue(s)" when the user merely declined to authenticate.
   Arguably skips should be tallied separately from failures.
   Pinned by `TestSudoThreeStrikesSkipsAndLogs` (asserts `errs == 1` — update
   the assertion if you change the policy).
2. **Abort leaves later steps rendered as pending**: a non-optional confirm
   answered "no" jumps to `screenSummary`, but the declined step and everything
   after it still show `○` (pending) instead of `–` (skipped/aborted) in the
   summary checklist. Pinned by `TestConfirmNonOptionalNoAborts` (asserts
   `statusPending` — same deal).

Both are cosmetic; I deliberately tested current behaviour rather than changing
code the tests were meant to characterize.

## Untested / thin spots (honest gaps)

- `editConfig`'s non-dry-run path (`tea.ExecProcess` hands over the terminal —
  not reasonably testable headless; the dry-run branch *is* covered).
- `discoverDownloads` against a real `~/Downloads` (only the routing of its
  `discoveryMsg` is covered; the find/wc pipeline itself isn't fixture-tested).
- Password **masking** is asserted only by configuration (`EchoPassword` in
  `initialModel`), not by a golden of the password screen with typed input.
- The running-screen checklist mid-transition (spinner frame makes it
  golden-hostile; the status transitions themselves are unit-tested).
- `refreshStatus`/`systemStatus` (wall-clock tick + machine-specific output).

## If you pick this up next

Likely-best next steps, in order: fix finding 1 and/or 2 above (then update the
two named assertions), fixture-test `discoverDownloads` with a temp dir of aged
files (`touch -d`), and add a golden for the summary screen once finding 2 is
settled so the checklist glyphs are pinned there too.

---

## Update — findings fixed and thin spots closed (follow-up pass)

Findings 1 & 2 are fixed in code (skips no longer counted as failures; aborted
steps render as skipped, with an "⚠ Aborted" summary). The two pinned assertions
were updated to match.

The thin spots above are now covered (`coverage_test.go`):

- **editConfig non-dry path** — an `execProcess` seam (`var execProcess =
  tea.ExecProcess`) lets a test capture the editor command and assert the config
  file is created with defaults, without launching an editor. Existing-file
  no-clobber is tested too.
- **discoverDownloads** — real-filesystem fixtures via a temp `$HOME/Downloads`
  with `os.Chtimes`-aged files; asserts the count in the confirm string and the
  empty→notice path.
- **Password masking** — types into `screenPassword` and asserts the rendered
  frame shows mask glyphs, never cleartext.
- **Running checklist** — spinner pinned to a single frame (`Frames: ["*"]`) so
  every status glyph (running/ok/failed/skipped/pending) renders deterministically.
- **systemStatus/refreshStatus** — asserts the status line's label contract and
  that a `statusMsg` updates `m.status` and re-arms the tick.
- **Summary** — table test over all five outcomes (success/issues/skipped/
  issues+skipped/aborted).

Genuinely still not automated (inherent, low value): the OS-level terminal
handover inside `tea.ExecProcess` (we verify the command, not that the editor
visually takes over), and `tea.Tick`'s timer firing (a library primitive).

Suite is now 37 passing (`-race`), `go vet` and `gofmt -l` clean.

---

## Update 2 — abort flow + floating overlays (second follow-up pass)

Two feature commits landed on top, each with its own test file:

- **Abortable running steps** (`abort_test.go`): ctrl+c during a running step no
  longer quits the program — it pops an abort-confirm; "no" resumes (child kept
  alive), "yes" SIGINTs the child, escalating to SIGKILL after a grace period if
  it traps SIGINT, then returns to the menu. Late `done` messages arriving after
  an abort are dropped (no screen change, no `errs`). Tests exercise a **real
  `sleep` child** and a SIGINT-trapping script — they're the slowest in the
  suite but verify actual signal delivery. `runStep` now returns the
  `*os.Process` for this.
- **Floating popup overlays** (`overlay.go` + `overlay_test.go`): password,
  confirm, abort, and notice prompts render as bordered boxes composited *over*
  the current screen by an ANSI-width-aware compositor. Tests assert centering,
  background preservation (including styled/colored rows), and that the abort
  dialog floats over the running screen. If you change any popup, the overlay
  tests are where width regressions will surface.

`TESTING.md` was rewritten as the suite inventory/how-to-run doc (this file
stays the narrative handover). Current gate, re-verified after the merge:
**46 tests passing under `-race`; `go vet` and `gofmt -l .` clean.**

### If you pick this up next

- The VHS e2e still only drives the Clear-user-cache flow; a second tape through
  the services multi-select (inject fake `systemctl` output via a PATH shim)
  would give the overlay + abort paths a pty-level check.
- `TestSummaryMessages` covers the five outcomes as strings; a summary-screen
  golden would also pin the checklist glyphs.
- Goldens for the popup screens (password/confirm over a background) are now
  feasible since the overlay is deterministic — `fixedModel()` + Ascii profile
  apply as before.

---

## Update 3 — gate + orphan tests, hermetic real-log tests (Fable, third pass)

Covered the two behaviours TESTING.md flagged as untested, in
`gate_orphan_test.go` (9 tests — see the inventory table in TESTING.md):

- **Proceed-gate**: shown only when sudo is cached and the action carries a
  gate (Full Cleanup); "no" → menu with nothing run, "yes" → queue; ungated
  actions and the uncached path (password prompt *is* the gate) never see it;
  `activate` clears a stale gate before dispatching another action.
- **Orphan removal**: `fullCleanupSteps()` puts topgrade first; with orphans a
  sudo-optional `pacman -Rns --noconfirm <list>` step is spliced in with the
  real count in the confirm; with none the step is omitted. `orphanPackages`'
  `pacman -Qdtq` is stubbed with a PATH shim (`shimPacman`) — the gate tests
  reuse it so nothing reads real package state. Shim gotcha: emit newlines via
  printf *format* escapes, not `%s` (sh printf doesn't expand `\n` in args).

Also made the two real-log tests hermetic per TESTING.md's note: they now set
`logDir = t.TempDir()` (restored via defer), so they can't touch
`/var/log/archmaid` even once the install script creates it, and don't depend
on the HOME fallback. TESTING.md's ground rule and inventory updated to match.

Gate re-verified: **55 tests passing under `-race`; `go vet` and `gofmt -l .`
clean.** No production code changed.

### If you pick this up next

The suggestions from Update 2 still stand (services-flow VHS tape, summary
golden, popup goldens). New: a golden of the gate confirm floating over the
menu would pin the cached-sudo Full Cleanup entry point end to end.

---

## Update 4 — sudo-hardening tests + pruneLogs fixture (Fable, fourth pass)

Covered the sudo-hardening changes TESTING.md flagged, in
`sudo_hardening_test.go` (11 tests; inventory table in TESTING.md):

- `sudo -n` argv rewrite proven with a PATH-shimmed `sudo` that prints its
  argv (gotcha: the shim must use `printf '%s\n' "$*"` — `echo "$@"` eats the
  very `-n` under test). Non-sudo argv asserted unchanged; topgrade steps
  asserted `needsSudo` in both the standalone action and Full Cleanup.
- Keep-alive lifecycle: armed by a real-run sudo queue (a step confirm keeps
  the test from exec'ing anything), never in dry-run; reschedules while armed,
  no-ops once disarmed; disarmed on summary, confirm-abort, and running-abort.
- `pruneLogs`: temp-`logDir` fixture with `os.Chtimes`-aged files — only the
  stale `.log` goes; fresh logs, non-`.log` files, and directories survive.

Gate: **66 tests green under `-race`; vet and gofmt clean.** No production
code changed.

### Live-test findings (real binary, no sudo password — wrong-password flow)

Driven via VHS against the real (non-dryrun) binary under a scratch HOME:
menu → topgrade action → real `sudo -n -v` probe → password overlay → two
deliberately wrong passwords → esc → menu → q. Verified live: the overlay
floats over the menu, input is masked, "Incorrect password — 2 attempts left"
renders, esc cancels back to the menu, q exits cleanly, and no run log is
written for auth failures. Two real defects surfaced:

1. **No in-flight auth state.** Between enter and `sudoResultMsg` (several
   seconds — sudo's PAM delay + its stdin-retry behaviour), the password field
   keeps accepting keystrokes (a second password typed during the wait is
   silently *appended* to the first — the popup visibly widens) and a second
   enter fires a **concurrent** `sudo -S -v`. There is zero feedback that
   authentication is running. Fix shape: on submit, disable the input + show a
   spinner/"authenticating…" state and ignore enter until the result arrives.
2. **faillock burn.** Two in-app submissions produced **three** PAM failures
   (`faillock` records 2 s apart) — with Arch's default `deny=3` the account
   auth-locked (10 min) before the app's own three-strikes ever fired. In-app
   `maxSudoAttempts = 3` collides with faillock's 3; consider `-p ''` aside,
   the concurrency fix in (1) removes the extra failure, and the prompt could
   surface "account temporarily locked" (sudo exit/stderr distinguishes it).

Artifacts (screenshots + full text capture) in the job tmp dir; faillock state
was checked before and after (pre-existing failures had aged out; the run's
budget of 2 was chosen to stay under deny=3 — the concurrency bug spent 3).
