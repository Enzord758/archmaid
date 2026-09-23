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

**Three-strikes verified live** (second run, attempts paced ~10 s apart so no
typing happened while auth was in flight): "2 attempts left" → "1 attempt
left" → give-up → summary rendered "– Updating system with topgrade…
(skipped)" / "Completed — 1 step(s) skipped", and the real run log (scratch
state dir) contained exactly `SKIP: Topgrade completed (sudo authentication
failed)`. Matches the unit-test contract end to end.

Root cause for the faillock double-burn, pinned by the second run's records:
a single `sudo -S -v` submission can log **two** PAM failures 2 s apart —
sudo retries reading the password from stdin and burns an extra attempt on
EOF. So in-app strike 1 can consume 2 of faillock's 3, and in-app strike 3
may be rejected by the lockout rather than the password check. The in-flight
input fix removes the *concurrent* burn; the stdin-retry burn is sudo/PAM
behaviour the prompt should at least surface ("account temporarily locked" —
sudo's stderr distinguishes it).

---

## Update 5 — both live-test defects fixed (in-thread, measured + live-verified)

Both Update-4 findings are fixed and regression-tested (`auth_test.go`):

1. **In-flight auth state** (`m.authInFlight`): submit locks the popup — all
   input ignored, the field is cleared immediately (password not retained),
   and the popup shows a spinner + "authenticating…" until `sudoResultMsg`
   lands. No concurrent sudo, no appended keystrokes.
2. **faillock burn eliminated, not just halved.** Re-measured: ONE wrong
   `sudo -S -v` burns TWO pam_faillock failures (wrong password + sudo's
   retry hitting stdin EOF) — and `sudo -A` with a refusing askpass *also*
   burns two, so no invocation shaping fixes it. The fix is two-stage auth
   (`authSudo`): validate via `su <user> -c true` first — single-shot PAM,
   and on Arch su's stack doesn't include pam_faillock, so a wrong entry
   burns NOTHING — then only a known-correct password reaches `sudo -S -v`
   (which succeeds: no burn, clears stale failures). A pam_faillock lockout
   notice in stderr is surfaced verbatim via `sudoResultMsg.detail` instead
   of the generic wrong-password message.

Live-verified (tmux, real credentials): wrong submit → "authenticating…"
spinner (input dead) → "Incorrect password — 2 attempts left" → **zero
faillock records**; correct submit → queue starts; ctrl+c abort → menu.

---

## Update 6 — file-per-concern split, orphan-cascade preview + category-split logging (Sonnet, sixth pass)

Two separate passes, both against the running app rather than starting from a
handoff note:

**Pure refactor, no behaviour change.** `main.go` had grown to 930 lines
mixing the model, `Update`, `View`, and system-status concerns. Split to match
the package's existing per-file convention (`tasks.go`, `theme.go`,
`overlay.go`, `log.go`, `askpass.go`): `model.go` (screens/`model`/`Init`),
`update.go` (`Update`/`handleKey` + orchestration), `view.go` (`View` +
renderers), `status.go` (the status line), leaving `main.go` as just the
package doc + `main()`. Verified byte-for-byte behaviour preservation via the
full suite, not just a green run.

**A real incident, root-caused live.** A Full Cleanup's orphan-removal step
confirmed "23 orphaned package(s)?" but `pacman -Rns` actually removed 46 —
`-s` recursively drops each target's own now-unneeded dependencies too, and
the confirm only ever showed the direct `pacman -Qdtq` count. Root-caused via
`/var/log/pacman.log`'s `[PACMAN] Running 'pacman -Rns ...'` line (the exact
23 targets) diffed against its `[ALPM] removed` entries (46 total) — archmaid's
own log had nothing, since normal runs only ever logged failures. Two fixes:

1. **Cascade-aware confirm** (`orphanRemovalTotal` in `tasks.go`): a
   `pacman -Rs --print` dry-run pass (drops `-n`/`--nosave`, which conflicts
   with `--print`; needs no root) previews the true removal count before the
   confirm is built, so it now reads "Remove 23 orphaned package(s) — 46 total
   once dependencies cascade?" when the two diverge, and stays the plain count
   otherwise. Falls back to `len(orphans)` if the probe itself fails.
2. **Category-split logging + `alwaysLog`** (`log.go`, `tasks.go`): each run's
   log directory (`<run>/`, keeping the newest 5 — was age-based before) now
   holds `topgrade.log` / `orphans.log` / `cleanup.log` instead of one blob,
   and steps that remove or overwrite something (orphan removal, cache/temp/
   log wipes, service disabling, downloads/repair cleanup) are flagged
   `alwaysLog: true` — they log their outcome even on success. This is what
   would have made the incident traceable from archmaid's own log instead of
   pacman's. "View log" gained a category picker (`screenLogPicker`) in front
   of the pager.

Verified against the real binary, not just tests: rebuilt + reinstalled via
`install.sh`, ran the VHS e2e (confirms `dryrun/cleanup.log` lands correctly),
and checked two live Full Cleanup runs' resulting log directories by hand —
`orphans.log` correctly captured the real `pacman` transaction output on
success, `topgrade.log` correctly stayed absent (no failures to log), and the
second run correctly omitted `orphans.log` entirely once no orphans remained.

Gate re-verified: **87 tests green under `-race`; `go vet` and `gofmt -l .`
clean.** `README.md` and this doc's own architecture references (which still
described the pre-split `main.go`) were brought current alongside the code.

### If you pick this up next

- The log-category picker (`screenLogPicker`) has no golden yet — the menu/
  services goldens are the pattern to follow (`fixedModel()` + Ascii profile).
- The VHS e2e still only drives Clear-user-cache; a second tape through Full
  Cleanup's orphan-removal confirm would exercise the cascade-preview text
  live, not just the shimmed unit tests.
- `topgrade` itself is deliberately *not* `alwaysLog` (only failures log) —
  worth reconsidering now that the machinery exists, since it's the step that
  mutates the most (a full system upgrade) and the one that actually failed
  in the incident's own trigger run.

---

## Update 7 — AUR audit action + yay cooldown hook (Sonnet, seventh pass)

Prompted by an article on the AUR maintainer-takeover pattern (adopt an
orphaned package, push a malicious update immediately). Assessed against what
archmaid actually touches: it never sees PKGBUILDs — Full Cleanup runs
`topgrade -y` → yay unattended — so most of the article's advice belongs in
yay's config, and only one part is a sensible archmaid feature. Both done:

1. **yay cooldown hook** (not in this repo): `~/dotfiles/yay/.config/yay/
   init.lua`, stowed to `~/.config/yay/init.lua`. An `UpgradeSelect` hook holds
   back AUR upgrades pushed to in the last 72h, prints each hold, appends it to
   `~/.local/state/yay/cooldown.log` (a skipped update must never be silent),
   and honours `YAY_NO_COOLDOWN=1`. Checked against yay's own doc/lua.md (v13.0.1
   confirmed to embed the hook API), tested under `luajit` with a stubbed `yay`
   table, and proven loaded by briefly appending a syntax error (yay reported the
   exact line) then restoring. Left **uncommitted** in the dotfiles repo. Only
   the cooldown was written — the article's `AURPreInstall` regex blocklist was
   deliberately skipped (trivially evaded, false-positives on legitimate
   `base64 -d`).
2. **"Audit AUR packages"** menu action (`audit.go`, new `screenAudit`, new
   `audit` log category): `pacman -Qqm` → AUR RPC v5 `info` (POST, chunked) →
   per-package flags. High severity = adopted-after-orphaned or maintainer
   changed. The RPC has no adoption history, so this diffs against a snapshot
   saved by the previous audit (`aur-maintainers.json`, state-dir root — not a
   run directory, so `pruneLogs` leaves it alone); the first audit only records
   the baseline. Glances: orphaned, pushed <72h (same window as the yay hook),
   out of date, 0 votes, not on the AUR. It is archmaid's first network call and
   is otherwise read-only; dry run reads the snapshot but never advances it.
   `TestMenuGolden` regenerated (one added row, diff-checked).

Live run against the real AUR + this machine's 30 foreign packages (scratch
state dir) found a real bug the mocked tests couldn't: `yay-debug` was reported
"not on the AUR" because `yay` comes from a CachyOS repo, not the foreign list.
Fixed by skipping any AUR-unknown `-debug` package (a local split companion)
instead of keying on the base being foreign.

Gate: **108 tests green under `-race`; `go vet` and `gofmt -l .` clean.**

Also on `main` since Update 6, from a separate pass (PR #3, not this one): the
running-screen pane-overflow fix, the askpass popup-background fix and
`resize_test.go`.

### If you pick this up next

- No VHS e2e drives the audit screen, and there's no golden for the report.
- The audit could feed Full Cleanup (e.g. warn before topgrade if a high finding
  exists) — deliberately not done: it would make a network call on every
  cleanup and archmaid can't hold an upgrade back; only yay can.
- The snapshot is only as fresh as the last audit; nothing schedules one.
