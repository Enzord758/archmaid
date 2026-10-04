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
| E2E | VHS drives the real binary in a pty under `--dryrun`, with every external command shimmed | A few short paths proving the wiring (build, alt-screen, keys, log files): Clear user cache, services multi-select, Full Cleanup's orphan confirm, abort. |

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

## Make targets

A root `Makefile` wraps everything CI runs; paths live in variables at its top.

| Target | Runs |
|---|---|
| `make gate` | `scripts/check.sh` (TUI module) |
| `make helper-test` | `go test -race`, `go vet`, `gofmt -l` in `cmd/archmaid-helper` (its own module) |
| `make e2e` | `bash tui/e2e/run.sh` (needs vhs, ttyd, ffmpeg) |
| `make perf` | `ARCHMAID_PERF_GUARD=1 scripts/check.sh` (opt-in perf guard; a no-op extra until check.sh reads it) |
| `make all` | `gate` + `helper-test` — run before pushing |

## CI

`.github/workflows/ci.yml` runs on every push and pull request; a newer push to
the same ref cancels the older run. Paths are in the workflow's `env:` block.

| Job | Where | Runs |
|---|---|---|
| `core` | `archlinux:latest` container (some discovery tests expect Arch tooling) | `make all`: the gate plus the helper module. Go build and module caches keyed on `go.sum` and the Go version. |
| `e2e` | `ubuntu-latest` + setup-go + vhs-action (vhs, ttyd, ffmpeg) | `make e2e`; uploads `tui/e2e/*.txt` as an artifact on failure. |
| `gui` | not yet | Placeholder in the workflow: headless weston, once the GUI module exists. |

Stays manual (not in CI): a real Full Cleanup, sudo/faillock behaviour, the
polkit live test on a scratch user, sudo without a tty, and askpass against real
sudo. See "Manual verification".

## Running

```sh
go test -race ./...                 # the quick loop — must be green
go vet ./... && gofmt -l .          # must print nothing
go test -run TestAbort ./...        # a subset by name
go test ./... -run Golden -update   # regenerate goldens after a deliberate UI change
go test -run TestTranscript ./...   # the transcripts; -update-transcripts rewrites them
go test -run '^$' -bench StreamView -benchmem ./...   # UI cost of streaming 5k / 20k lines
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
2. **No real state.** Tests that write a log, touch `~/.config` or read `$HOME`
   set `t.Setenv("HOME", t.TempDir())`; tests that resolve the downloads folder
   use `isolateDownloads`, which also points `XDG_CONFIG_HOME` at the scratch
   home and clears `--downloads` / `ARCHMAID_DOWNLOADS`. Tests that read or write
   run logs or the AUR snapshot also set `logDir = t.TempDir()` (restored on
   cleanup), so nothing lands in the real `~/.local/state/archmaid`.
3. **No real external programs.** `pacman`, `sudo`, `su`, `systemctl` etc. are
   replaced by shell shims placed first on `PATH` (see Recipes).
4. **No network.** The AUR RPC is an `httptest` server reached through the
   `aurRPCURL` var. `TestMain` sets `aurRPCURL` to a closed local port, so any
   test that reaches the audit without `fakeAUR` (choosing Full Cleanup or Run
   topgrade starts it at once, see `auditFetch`) fails fast instead of calling
   the real AUR.
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
| `askpassWrapper` | wire askpass up (or not) for `runStep` without starting a server |
| `askpassReadTimeout` | shrink the deadline for an askpass client's prompt line (read when the server starts) |
| `readProc` | stand in a fake process table for the askpass peer check (`fakeProcs`; the server fixes it when it starts) |
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
`shimPacman` (`-Qdtq` orphans),
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
| `plan` | `N steps`, then `<i> "<title>" inproc\|argv=… [sudo] [confirm] [optional] [alwayslog] cat=<c>` per step; re-emitted when a step's flags change mid-run (the audit gating topgrade); a step inserted mid-run (the orphan probe's removal) is the new `N steps` plus `+<i> "<title>" …`, and the steps behind it are renumbered |
| `step` | `<i> running\|ok\|failed\|skipped` |
| `out` | `<i> <line>` of a step's output, as the model accepted it — one per line, however the pump batched them |
| `log` | `<cat> <KIND>: <label>` — the entry header, no timestamp or run dir |
| `snap` | the AUR maintainer snapshot, whenever its content changes: `name="maintainer"`, plus `+co[a,b]` for co-maintainers or `+co?` for an entry with no co-maintainer record |
| `abort` | `<i> "<title>"` — ctrl+c, then y |
| `note` | a notice popup's text |
| `audit` | a line of the AUR-audit report screen |
| `end` | `outcome=completed\|aborted\|cancelled\|notice\|report errs=N skipped=N` |

Within one `Update` the order is fixed: exec, out, finished steps (an
inserted step in its place among them), changed plan flags, log entries
(grouped by category file), snapshot, then steps that started, then a notice
or report. `$HOME` and the log dir are normalised.

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
or exit, never from the `Update` that launched it. The AUR audit's lookup,
started on its own goroutine when Full Cleanup or Run topgrade is chosen
(`auditFetch`), is awaited right after that `Update`, so its `pacman -Qqm`
lands with the action's own queries, before the sudo probe.

**Shims.** `installShims(t, Shims{"pacman": {{Prefix: "-Qdtq", Out: "a\nb"}}},
passthrough...)` writes one `#!/bin/sh` per command: it logs `<cmd> $*` to
`$ARCHMAID_SHIM_LOG`, then the first `Rule` whose `Prefix` starts `$*` runs
`Script`, prints `Out` and exits `Exit` (or, with `Block`, `exec sleep 30` to be
aborted; `TrapINT` ignores SIGINT so only the SIGKILL escalation ends it).
Unmatched argv exits 0. PATH is *only* that dir, so anything unshimmed is "not
installed"; `passthrough` symlinks real tools (`bash`, `find`, …) in for
actions that need them — always under the scratch `HOME` from
`transcriptEnv`. Consecutive `exec` lines of one command with the same first
argument are sorted (a pipeline's processes, or Repair's parallel
`pacman -Qkq -- <chunk>` scans, start concurrently); different subcommands
keep the order they ran in, so `pacman -Qq` stays ahead of the scans it feeds.
Repair's scenarios pin `repairScanWorkers` (`pinScanWorkers`) so the chunks
don't depend on the machine's core count. The AUR is `fakeAUR` plus
`saveBaseline` for the snapshot.

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
| `gate_orphan_test.go` | The proceed-gate (only when sudo is already cached; the password prompt is the gate otherwise). Orphans: building the Full Cleanup queue and choosing it run no pacman (only the audit prefetch's `-Qqm`, off the UI thread); the in-process probe sits after topgrade and the cache steps, before the AUR cache step; it inserts the removal right behind itself only when orphans exist, with exactly the names it found and a confirm that previews the real `pacman -Rns` cascade total (both counts when they differ, plain count when equal), defaulting to No, a decline logged as SKIP; none → a clear line and no confirm; a failing `-Qdtq` or `-Rs --print` fails the probe and inserts nothing; an abort kills the probe's pacman and drops its late result. Temp files are cleaned with `sudo systemd-tmpfiles --clean`, not a find/rm. |
| `sudo_hardening_test.go` | Sudo steps run as `sudo -A` (password via `SUDO_ASKPASS`, never the tty), or `sudo -n` when askpass isn't wired, and other commands are untouched; the keep-alive loop arms only for real sudo queues and disarms on summary/abort; `pruneLogs` keeps the newest 5 run directories and exempts `dryrun/` and non-run entries. |
| `auth_test.go` | Two-stage prime: a wrong password never reaches `sudo` (no faillock burn), and su runs as `su -s /usr/bin/true <user>`; a correct one mints the ticket; a lockout notice is surfaced verbatim; submit locks the input and ignores all keys until the result arrives. |
| `abort_test.go` | ctrl+c opens a confirm instead of quitting; "no" resumes with the child alive; "yes" really terminates a real child; a SIGINT-ignoring child is SIGKILLed after the grace period; late output after an abort is dropped, by its step sequence, so even once a new queue has started an aborted step's late line never reaches the new step's pane or log and its late done never finishes it. `abort_leak_test.go`: aborts leave no goroutine behind, both for a child blocked on a full stream and for one that left a background process holding its output pipe. |
| `stream_test.go` | The pump batches what's buffered (at most `streamBatchMax` lines, in order, ending at the done, nothing left behind, seq on every batch); an aborted pump returns nil. The pane follows output only at the bottom: scrolled up, new batches don't yank it, and when the tail drops old lines the line being read stays on top. A 20k-line `alwaysLog` step logs all 20k lines in order while the pane holds the newest 2000. A step whose child leaves a background process holding its output pipe is done within `stepWaitDelay` of the child's exit, with every line, one done carrying the child's own status, and no goroutine left behind. `BenchmarkStreamView5k`/`20k`: Update + View cost of streaming a flood (guarded by `TestPerfGuard`, opt-in). |
| `overlay_test.go` | Popups float, centered, over their background; ANSI-styled backgrounds keep their width; the abort dialog stays over the running screen. |
| `resize_test.go` | The output pane never overflows the terminal (running queue, log viewer, resize while a popup is open); askpass prompts over another popup keep the real background; nested popups resolve to the right base screen. |
| `services_test.go` | Services discovery: `parseInactiveServices` on a captured `list-units` fixture (the `●` not-found row, masked, template instances, excluded names), the batched `is-enabled` with its per-unit fallback, and an equivalence test running the old bash pipeline against the same fixture system (`testdata/systemctl_list_*.txt` served by a shim) to prove both select the same units. |
| `repair_test.go` | Repair, against a `pacman`/`sudo` shim pair that flags different packages as the user and as root: the user scan (`pacman -Qq`, then `pacman -Qkq -- <chunk>` per worker) reports the same packages in the same order for 1, 2, 4, 16 or 100 workers, checks each installed package once and never runs sudo; nothing flagged ends in "No broken packages" with no sudo at all; candidates get one probe (password prompt, or straight to the recheck when cached) and one `sudo -n pacman -Qkq -- <candidates>`, and only what root confirms is offered for reinstalling — a package flagged only as the user is dropped; esc drops the held recheck, three wrong passwords end in a notice. Both stages treat pacman's exit 1 as a result and a failure with empty stdout as an error (a missing ticket, a failing chunk), never as "nothing broken"; a package with several missing files is listed once (fixture `testdata/pacman_Qkq.txt`). |
| `hyprpm_test.go` | `hyprpm` is disabled inside topgrade and runs as the last step, only when installed (it drops the sudo ticket on exit). |
| `coverage_test.go` | Thin spots: `editConfig` (creates defaults, never clobbers, via the `execProcess` seam); password masking in the rendered frame; every checklist glyph; the status line (formatting, N/A fallbacks and the no-child-process guarantee are in `status_test.go`, which also has `BenchmarkSystemStatus`); all summary outcomes. |
| `audit_test.go` | AUR audit, fully offline: severity classification (adoption/maintainer change/added co-maintainer = high; removed co-maintainer, orphaned, recent push, out of date, 0 votes, not-on-AUR = glance; the gate reason naming each); local `-debug` companions skipped; chunked POST lookups and RPC error handling, an 8 MiB response cap, results for names that weren't requested dropped; AUR names and maintainers stripped of control/format characters and length-capped before display (the raw value still drives the comparison and the snapshot); the maintainer snapshot (round-trip with co-maintainers sorted, kept for missing packages, an old-format file (maintainer strings only) loads with no co-maintainer record so it never gates on them and the next commit records them, written atomically at 0600, a held lock skips the save, a corrupt or unreadable file is never "no baseline": it fails closed as a high finding that gates topgrade, is left alone until accepted, then kept aside as `.corrupt-<time>`); an end-to-end first-audit-then-takeover run; dry run never advances the snapshot; the menu audit saves all but the high findings and `a` on the report accepts those (q drops the accept, so they stay flagged), added co-maintainers included; the screen/notice flow. |
| `fullcleanup_audit_test.go` | Full Cleanup's audit step: it's first, in-process, `alwaysLog`, category `audit`; both topgrade steps are `alwaysLog`. A maintainer change gates topgrade with a confirm naming the package(s), defaulting to No, before topgrade starts; declining skips only the update and logs `SKIP: … (declined)`; the report is in the output pane and `audit.log`. No foreign packages is a clean pass; an unreachable AUR fails the step without gating. A stale or post-abort result is dropped. In a dry run it still audits, logs exactly once, and leaves the snapshot alone, even when the gate is accepted. The step itself never saves the snapshot: with nothing high the model commits it at once; behind a gate only "yes" saves it (logged `ACCEPT`), while "no" or a gate with no step after it leaves it, so the next run gates again. |
| `topgrade_audit_test.go` | Run topgrade's queue is [audit, topgrade], built from the same `auditStep`, and gated identically: a maintainer change puts a named confirm defaulting to No in front of topgrade; "no" skips the update (logged) and leaves the snapshot, so the next run gates again; "yes" launches it and saves the snapshot (logged `ACCEPT`); an unreachable AUR fails the audit without gating; a dry run gates but never saves. |
| `askpass_test.go` | The `SUDO_ASKPASS` round trip over a real unix socket: the helper prints the answer and exits 0, exits 1 on an unanswered close or no socket; the server keeps its socket and 0700 wrapper in a private 0700 directory under `XDG_RUNTIME_DIR`, never sets `SUDO_ASKPASS` in our own environment, delivers prompts and `close()` removes the directory; no `XDG_RUNTIME_DIR` (or an unusable one) leaves askpass unwired, with no `/tmp` fallback; the wrapper's shell quoting survives quotes and `$(…)` in paths; a client that never sends its prompt doesn't block the next one and is hung up on at the read deadline, an oversized prompt is dropped; only `needsSudo` children see `SUDO_ASKPASS`; the real wrapper end to end; the popup checks a password with su first: a right one is written to the connection, a wrong one never is (popup stays up, attempts counted, the third cancels the request and skips later sudo steps), a line break is refused before su, input locks while su runs; cancel closes the connection unanswered. |
| `downloads_test.go` | Which folder "Delete old downloads" cleans: the order flag → `ARCHMAID_DOWNLOADS` → `XDG_DOWNLOAD_DIR` → `~/Downloads` → `~/Download`; `$HOME` refused however it's spelled (`"$HOME/"`, a symlink to it, `$HOME` behind a symlink), as are `/` and ancestors of `$HOME`; only the flag may leave `$HOME`, and a bad flag is an error, not a fall-through; the notice names the folder; `--downloads` parsing. What gets deleted (real ctimes, with the clock moved instead): old files, folders and symlinks go — a symlink as the link, its target untouched; new files, a back-dated mtime, and a folder with anything new deep inside stay; the downloads folder itself is never a candidate; an entry changed or replaced between the confirm and the delete is kept; a folder swapped since the scan deletes nothing; a dry run only reports; the confirm lists ten names then "…and N more", unsafe names quoted; end to end through `Update` with the deletion in `cleanup.log`. |
| `audit_cancel_test.go` | The audit's one cancellable deadline, against a fake AUR that never answers: a cancel ends the RPC within 100 ms; the deadline spans every chunk, not each request; an abort mid-audit returns at once, its late result is dropped, and no goroutine (connection, handler, pacman) is left behind. |
| `audit_prefetch_test.go` | The audit fetched behind the password prompt: choosing Full Cleanup starts the lookup before the prompt is answered, saving nothing; a whole Full Cleanup asks the AUR exactly once; esc on the prompt, a declined gate and an abort while Run topgrade's step awaits it each cancel it promptly, with no second request and no goroutine left. |
| `transcript_*_test.go` + `testdata/transcripts/` | Whole actions end to end against shims — see Transcripts above and the scenario names. |
| `askpass_peer_test.go` | Who may have the password, against a fake process table: the step's own sudo (via yay, or a direct `sudo -A` step) is answered and named in the popup; refused are a wrong uid, a gone or reused helper pid, a helper that isn't archmaid, a parent that isn't sudo (a user process named sudo, a root process with another name, a readable exe elsewhere), a chain that misses the step or breaks, a reused step or ancestor pid, and a cycle. The model hangs up with no popup when no sudo step runs (menu, non-sudo step, finished, aborted) and on a second request while one is open; a refusal mid-step leaves a sanitized note in the output pane; the popup shows sudo's prompt only quoted and stripped of escapes. `readProcFS` against the real `/proc` (a root process's exe is unreadable to a user); the server reports the real `SO_PEERCRED` peer. |
| `teatest_test.go` + `testdata/` | Headless-program goldens for the menu and the services multi-select. |
| `golden_test.go` + `testdata/` | Direct `View()` goldens at 80×24: the summary for each outcome (ok, issues, skipped, aborted); the password popup over the menu (masked input, and with the attempts-left error); a confirm over the running checklist; a mid-run askpass prompt naming its requester; the log picker; the audit report; a notice; the discovering spinner. |
| `e2e/*.tape` + `run.sh` + `shim.sh` | The real binary under `--dryrun`, one tape each, in a throwaway `HOME` with a shim dir first on `PATH` (every external command is a symlink to `shim.sh`, which only logs argv to a file and prints canned output; `run.sh` checks the shadowing first and that no mutating shim ran). `clear-cache`: asserts the `DRYRUN:` entry. `services`: toggles one of two shimmed units; the log names exactly that unit and `systemctl` only ever listed. `orphans`: Full Cleanup's confirm reads "2 … 3 total" (from the tape's `.txt`) and `orphans.log` has the dry-run removal. `abort`: ctrl+c on the blocked audit step, confirm; back on the menu, nothing logged. `bash e2e/run.sh services abort` runs a subset; generated `e2e/*.txt` is gitignored. |

## Manual verification

Run by hand, as root, before the polkit path ships. Unit tests can't see real
PAM. `archmaid-helper`'s own suite (`cd cmd/archmaid-helper && go test -race ./...`)
covers validation, argv, hashes, the denylist, locks and env scrubbing with a
fake runner. It can't answer these questions, which decide the GUI's design:

1. How many `pam_faillock` failures does one wrong password in a polkit dialog
   cost against Arch's `deny=3`? sudo costs 2 (see HANDOVER → two-stage auth).
2. Does cancelling the dialog count as a failure?
3. Is `auth_admin_keep` scoped to the requesting process (so one GUI process
   can reuse it, but a fresh shell can't)?
4. Does pkexec 127 pick an action by `exec.argv1`?

To hand this to an agent with sudo instead, give it
[`packaging/polkit/LIVE-TEST-AGENT.md`](../packaging/polkit/LIVE-TEST-AGENT.md).
It runs the same cases without a tty.

**Never use your own account.** Everything runs against a scratch user, so a
lockout costs nothing.

### Setup (as root)

```sh
useradd -m amtest && passwd amtest           # NOT in wheel
cat >/usr/share/polkit-1/actions/org.archmaid.test.policy <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE policyconfig PUBLIC "-//freedesktop//DTD PolicyKit Policy Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/PolicyKit/1/policyconfig.dtd">
<policyconfig>
  <action id="org.archmaid.test.true">
    <description>archmaid test (auth_admin)</description><message>archmaid test: true</message>
    <defaults><allow_any>no</allow_any><allow_inactive>no</allow_inactive><allow_active>auth_admin</allow_active></defaults>
    <annotate key="org.freedesktop.policykit.exec.path">/usr/bin/true</annotate>
  </action>
  <action id="org.archmaid.test.keep">
    <description>archmaid test (keep)</description><message>archmaid test: keep</message>
    <defaults><allow_any>no</allow_any><allow_inactive>no</allow_inactive><allow_active>auth_admin_keep</allow_active></defaults>
    <annotate key="org.freedesktop.policykit.exec.path">/usr/bin/true</annotate>
    <annotate key="org.freedesktop.policykit.exec.argv1">keep</annotate>
  </action>
  <action id="org.archmaid.test.argv1">
    <description>archmaid test (argv1, no auth)</description><message>archmaid test: argv1</message>
    <defaults><allow_any>no</allow_any><allow_inactive>no</allow_inactive><allow_active>yes</allow_active></defaults>
    <annotate key="org.freedesktop.policykit.exec.path">/usr/bin/true</annotate>
    <annotate key="org.freedesktop.policykit.exec.argv1">argv1-match</annotate>
  </action>
</policyconfig>
EOF
cat >/etc/polkit-1/rules.d/49-amtest.rules <<'EOF'
// amtest authenticates as itself, and only for org.archmaid.test.*
polkit.addAdminRule(function(action, subject) {
    if (subject.user == "amtest" && action.id.indexOf("org.archmaid.test.") == 0)
        return ["unix-user:amtest"];
});
EOF
faillock --user amtest --reset
```

Log in as `amtest` on **tty3** (an active local session). Before **each**
case, check that `faillock --user amtest` shows 0 rows; reset if not. Leave
at least 10 s between password attempts. After each case, record the faillock
row count, `ls -l /run/faillock/amtest`, pkexec's exit code, and
`journalctl -u polkit --since "-2min"`.

| Case | On tty3 as amtest | Expect / learn |
|---|---|---|
| A | `pkexec /usr/bin/true`: one wrong password, then cancel (Ctrl-D) | failures for 1 wrong: 1 or 2? Does the agent re-prompt after a wrong one? |
| B | `pkexec /usr/bin/true`: one wrong, then the right one | rc 0; does success reset the count to 0? |
| C | three separate `pkexec /usr/bin/true`, each wrong; then a fourth with the right password | locked at 3? The 4th must fail (rc 127) while locked |
| D | `printf 'wrong\n' \| sudo -S -k -v`, then `sudo -A -k -v` with an askpass that answers wrong once and then exits 1 | baseline: 2 each (amtest is not in sudoers; note whether sudo prompts at all) |
| E | `su amtest -c true` with one wrong | 0 (su's stack has no faillock on Arch) |
| F | in one shell, `pkexec /usr/bin/true keep` twice (right password once); then `bash -c 'pkexec /usr/bin/true keep'` | the second call doesn't prompt; does the fresh process prompt? pkexec's subject is its parent process |
| G | `pkexec /usr/bin/true argv1-match`, then `pkexec /usr/bin/true nomatch` (right password) | first: no prompt, rc 0 (argv1 action). Second: falls through to `org.archmaid.test.true` (check the dialog/journal action id) |
| H | `pkexec /usr/bin/true`, cancel straight away (Ctrl-D, then Ctrl-C in a second run) | does a cancel count as a failure? rc 126 vs 127 |

### Cleanup (as root, always)

```sh
pkill -u amtest; userdel -r amtest
rm -f /etc/polkit-1/rules.d/49-amtest.rules /usr/share/polkit-1/actions/org.archmaid.test.policy
rm -f /run/faillock/amtest          # faillock --reset needs the user to exist
pkaction | grep -c org.archmaid.test   # must print 0
```

Paste the results into HANDOVER's open items. They decide whether the GUI
needs its own pre-validation (like the TUI's `su` stage), and whether
`auth_admin_keep` verbs really save dialogs.

## Manual measurements

Real pacman can't run in CI, so the Repair scan's speed-up is a hand
measurement, not a benchmark. Measured on the dev machine (16 cores, 1422
packages), warm cache, wall time:

| Scan | Time |
|---|---|
| `pacman -Qkq` as the user, one process (the old scan, as root) | ~850 ms |
| the same split into parallel chunks, 4 workers | 501 ms |
| 8 workers | 369 ms |
| 16 workers (`repairScanWorkers`' cap) | 316 ms |
| root recheck of only the flagged packages (`sudo -n pacman -Qkq -- <candidates>`) | ~50 ms |

The user scan flags every truly broken package (a missing file fails `stat`
for anyone) plus false positives from root-only directories — here always
amd-ucode (`/boot` is 0700) and nfs-utils (`/var/lib/nfs/statd`) — which is
why the root recheck stays. To repeat it (read-only; the recheck needs your
own sudo):

```sh
time pacman -Qkq >/dev/null
n=8; time (pacman -Qq | xargs -P$n -n$(( ($(pacman -Qq | wc -l) + n - 1) / n )) pacman -Qkq -- >/dev/null)
time sudo pacman -Qkq -- amd-ucode nfs-utils
```

## Known gaps

- **The audit gate was live-tested by hand, not automatically:** a dry-run Full
  Cleanup of the real binary via VHS, real AUR, with a scratch `HOME` whose
  seeded snapshot gave one real package a fake previous maintainer. A VHS tape
  for it would need a stable fake AUR, so it isn't in `e2e/`.
- **E2E abort uses the audit step:** under `--dryrun` every other step is stubbed
  and finishes instantly, so the in-process audit (blocked on a shimmed
  `pacman -Qqm`) is the only step that can be held open. The audit screen itself
  still needs a fake AUR, so it stays a unit test.
- **Inherently not automated:** the OS-level terminal handover inside
  `tea.ExecProcess` (we assert the command built, not that the editor takes
  over); `tea.Tick`'s wall-clock firing (a library primitive); that `sudo`
  relays SIGINT through a whole process tree (we verify a direct child).
- **Outside this repo:** the yay cooldown hook (`~/dotfiles/yay`). Its logic was
  checked under `luajit` with a stubbed `yay` API; there's no automated test.
