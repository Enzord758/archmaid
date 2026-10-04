# TESTING

How archmaid is tested, the rules that keep the suite safe and deterministic,
and how to add a test. For *what the app does* see `cmd/archmaid/README.md`; for design
decisions and open work see `HANDOVER.md`.

## Strategy

A bottom-heavy pyramid. Almost everything is a fast unit test against
`model.Update`; above that, whole actions are recorded as transcripts, a set of
rendered-frame goldens pins every screen and popup, and four short VHS tapes
drive the real binary end to end.

| Layer | Tool | What it is for |
|---|---|---|
| Unit | plain `go test`, driving `Update` with `tea.Msg`s | State transitions, command construction, parsers, log routing. The bulk of the suite. |
| Transcript | a recorder fed by a target that runs a whole menu action — today the model pumped through `Update` — against `sh` shims | What an action *does* end to end — programs run, prompts, plan, step results, log entries — as a frontend-agnostic golden. |
| Layout/render | direct `View()` assertions + `teatest` goldens | Frames at a pinned size and color profile; the output pane never overflowing the terminal. |
| Real processes | a real `sleep` child, real signals | Only where behaviour *is* the OS (abort → SIGINT → SIGKILL escalation). |
| E2E | VHS drives the real binary in a pty under `--dryrun`, with every external command shimmed | A few short paths proving the wiring (build, alt-screen, keys, log files): Clear user cache, services multi-select, Full Cleanup's orphan confirm, abort, Repair (nothing found; found + declined), Delete old downloads (`--downloads`; age is ctime, so only the "nothing old" notice is reachable), a declined Full Cleanup gate (cancelled record), `--help`/bad flag. |

Keep new tests at the lowest layer that can prove the behaviour.

## Running the gate

```sh
scripts/check.sh                    # from the repo root (it cds there itself)
```

Every commit must pass it. In order, it runs `go test -race -count=3 ./...`
(three runs to shake out flakes), `go vet ./...`, the `gofmt -l .` check, the
layering check (nothing under `internal/` imports charmbracelet), then a
coverage run into a temporary profile (removed on exit) and enforces the floors
set at the top of the script: `MIN_TOTAL` for `cmd/archmaid` plus `internal/...` (the TUI is `internal/tui`)
together, and `MIN_ASKPASS` for `internal/privilege/askpass.go` (matched by
that path suffix), which carries the sudo password. Per-file
coverage is computed from the profile itself. When coverage rises, raise the
floor in the same commit; never lower one to make a change pass. Around all
of that, `scripts/state-guard.sh` snapshots the real state dir first and
compares it last (ground rule 8); a change fails the gate. Before trusting
the guard, the gate runs `scripts/state-guard-selftest.sh`, which checks on
scratch dirs that the guard says ok for an unchanged or still-absent dir and
fails for a grown, deleted, added or touched entry and for a dir that
appeared.

### Perf guard (opt-in)

```sh
ARCHMAID_PERF_GUARD=1 scripts/check.sh   # the gate, then the perf guard
```

With `ARCHMAID_PERF_GUARD=1` set, the gate ends with one more step:
`TestPerfGuard` (`perfguard_test.go`), run without `-race` or coverage (it
skips itself under either, and without the variable). It runs
`BenchmarkStreamView5k`, `BenchmarkStreamView20k`, `BenchmarkSystemStatus` and
`BenchmarkActivateFullCleanup` and `BenchmarkViewRunning` through `testing.Benchmark` and fails if one is
over 3× its baseline, or over its fixed limit where it has one (best of up to
three runs, so a single noisy run doesn't fail it). `StreamView20k` is
the one that catches the streaming pane going quadratic again (it would be
~250× over). The baselines are the dev machine's (Ryzen 7 7800X3D, 16
threads, go1.27.1), which is why the guard isn't in the default gate:

| Benchmark | Baseline (ns/op, median of 6) | Fails above |
|---|---|---|
| `StreamView5k` | 5.4 ms | 16.2 ms |
| `StreamView20k` | 20.6 ms | 61.8 ms |
| `SystemStatus` | 28.2 µs | 84.6 µs |
| `ActivateFullCleanup` | 4.9 µs | 1 ms (fixed: it guards against pacman on the UI thread, 130–200 ms) |
| `ViewRunning` | 154 µs (633 allocs) | 600 µs (fixed: one Running-screen frame mid-queue with a full 2000-line output tail) |

Run it before and after touching `output.go`, `applyEvents`' `OutputChunk`, `proc.Next`,
`View`, `status.go` or `activate`. On another machine, or after a deliberate change,
re-measure with `go test -run '^$' -bench 'StreamView|SystemStatus|ActivateFullCleanup|ViewRunning' -count=6
./...` and update the baselines in the test.

### Bench compare (CI, and `make bench`)

The guard's absolute limits are the dev machine's, so they can't gate shared CI
runners. `scripts/bench-compare.sh [BASE]` (`make bench BASE=<ref>`; default the
merge-base with `origin/main`) is the machine-independent check: it builds the
test binary of BASE (in a temporary worktree) and of the working tree, runs the
same benchmarks six times each, alternating base and head so a slow stretch
hits both, and compares them with benchstat (pinned in the script). It fails
when:

- a benchmark's sec/op is significantly slower (benchstat p < 0.05) by more than 25 %;
- `SystemStatus` allocs/op rose at all (significant; it is deterministic, 68);
- HEAD's best `ActivateFullCleanup` sample is over 1 ms or `ViewRunning` over
  600 µs (backstop caps, the guard's fixed limits, for slow drift that no single
  base-to-head comparison shows).

Noise shows as `~` and passes. It takes about 40 s plus the build. Output
(`old.txt`, `new.txt`, `benchstat.txt`/`.csv`) goes to `$BENCH_OUT`, a temp
dir by default; `BENCH_COUNT` changes the sample count. If a benchmark is new on
HEAD it has no base column and is checked only against its cap.

## GUI (`gui/`, its own module)

The GUI is a separate Go module, so nothing above builds it: the root gate,
`go test ./...` at the root and CI's `core` job never compile gotk4 (a cold
gotk4 build takes about 7 minutes). Two layers:

| Layer | Where | Needs |
|---|---|---|
| View-models and pump | `gui/internal/runview`, `overview`, `logview`, plain `go test` (`-race` is fine: they don't build gotk4) | nothing: no display, no GTK |
| The real window | `gui/internal/ui/harness_test.go`, through `gui/scripts/headless.sh` | gtk4, libadwaita, weston |

`runview`'s tests replay `stub.FullCleanup` one event at a time and as one
batch, check that the widget-side output diff always reproduces the model's
output, including random batches across the 10k cap, and run the `Pump`
against the stub Driver with a fake main loop (one goroutine running posted
funcs, which also asserts that `apply` only ever runs on it): every control
event arrives once and in order, a 50k flood arrives whole in slices of at
most `FlushLines`, a stalled loop gets the newest `MaxPending` lines with the
drop counted, and Abort then Close, or Close alone, leaves no goroutine
behind (`go.uber.org/goleak`). Every gui test package runs under
`testenv.Run`, like the root's.

The display tests run the real window in a child process (the test binary
re-executed with `ARCHMAID_GUI_CHILD=<scenario>`, so GTK owns that process's
main thread) and read its JSON report. They skip unless
`ARCHMAID_GUI_DISPLAY_TESTS=1`, which only `headless.sh` sets, so a plain
`go test ./...` in `gui/` never opens a window. `headless.sh` starts
`weston --backend=headless` (pixman) with its socket in a fresh temp runtime
dir, points `WAYLAND_DISPLAY` at it by absolute path, drops `DISPLAY` and
`HYPRLAND_INSTANCE_SIGNATURE`, and runs the command on a private D-Bus with
no service activation (no portal, at-spi or gvfs starts). weston and the
command run under `timeout` (`HEADLESS_TIMEOUT`, default 60 s; the suite takes
~95 s warm, so with no command it runs three parts of at most ~40 s, each with its own weston; CI sets 600), so nothing outlives the run. Scenarios:
Actions start from their Overview row against the stub's `Catalog`
(the page must switch to Run). `fullcleanup` (the stub run end to end, each
confirm answered through its dialog, its buttons labelled by `YesLabel` and
"Skip"/"Cancel"), `usercache-no` (a required step's confirm declined: the
step skipped, outcome aborted), `repair` (the menu as shown, a discovery
ending in a notice, the empty last run), `logs` (a scratch state dir with a
20-day-old failed last-run.json and two runs' logs: the Overview's stale
warning, View log's run rows, one log opened; the state dir must be
unchanged after), `settings` (a scratch `XDG_CONFIG_HOME`: the home folder typed as the downloads folder is refused inline, a folder and dry run are kept in `archmaid/gui.json` at 0600, the next Start gets a fresh Driver made with them, both Drivers closed; a second app start begins with them), `stop` (Stop during topgrade → `Aborted`, outcome aborted),
`close-midrun` (closing mid-run asks first, then closes), `engine-stops`
and `engine-stops-rerun` (TestGUIEngineStops: the Driver's events end at the
second step without a Finished, as after a Session panic: the window leaves
the running state — not busy, Stop off, the menu back, no dialog open,
the status line and a toast say "archmaid's engine stopped unexpectedly"
— closing then closes at once, and a new Start runs on a fresh Driver;
runview's `TestPumpEndedWithoutFinished` is the plain-Go half), `services`,
`uncached`, `wrong-password` and `askpass` (from the stub's Catalog: the
services pick, the up-front password standing in for Full Cleanup's gate,
the stub's wrong password three times — each retry's dialog shows the error
and the attempts left, then the steps that need root are refused — and
sudo's askpass mid-topgrade naming who asked; each answered through its
dialog: boxes ticked, a password typed; the Driver must get `Select`,
`SubmitPassword` and `Confirm` with the right IDs, and the entry must
be empty after the answer), `audit` (the AUR report's Accept is
`Confirm(yes)`, and `ReportAccepted` is a toast), `idle` (first frame
and RAM), `flood` (50k unpaced lines, dropping off: the view's real capacity),
`flood-paced` (100 lines every 10 ms = 10k lines/s, default pump: nothing may
drop), `flood-default` (unpaced, default pump), `flood-colour` (as `flood`,
every line in colour with seven SGR spans: bold, the 16 colours, 256-colour
and 24-bit; it also checks that every visible output row carries its Pango
attributes). Every scenario also checks
that `Close` was called on the Driver and that no goroutine is left after
`Run` returns (goleak, against the goroutines from before the app started).

The `real-*` scenarios (`TestGUIRealDriver`, its own part of the default
`headless.sh` run) drive the **real Driver** through `gui/internal/realdriver`
on the polkit path. Every privileged binary is a shim first on PATH, ahead
of testenv's traps: `pkexec` checks it got the helper's install path and
speaks the helper's JSON lines (start, a line, check-files' check event,
result) after 0.3 s (the dialog), or exits 127 for the verbs it refuses;
`sudo` runs `$SUDO_ASKPASS` — archmaid's wrapper, which execs the test binary
in `--askpass` mode (`TestMain` calls `realdriver.Relay` first, as main
does) — and records the password it got; `su` takes one password; `topgrade`
and `yay` are shims. The faillock count, the orphan plan and the
foreign-package list (so no `pacman` and no AUR request) are fakes, and the
child's privilege view of `/proc` (`privtest.FakeSystem`) shows the sudo
shim as `/usr/bin/sudo`, so `Request.Verify` walks the real process tree.
The helper isn't installed in CI, so the child points `helperclient.Run`'s
install check at an empty stand-in in the shim dir
(`helpertest.UseHelperFile`); pkexec still gets the real install path.
`real-dry` (Full Cleanup as the Settings page's dry run: no shim runs),
`real-refused` (paccache's dialog refused: "Not authorised", the run goes on;
the other helper steps show "Waiting for authorisation…" with the
last-attempt warning, and pkexec gets exactly the six helper verbs),
`real-locked` (a locked account: no pkexec at all, each helper row
"Not authorised · account locked until …"), `real-askpass` and
`real-askpass-cancel` (the right password reaches the sudo shim; a cancel
doesn't, topgrade fails), `real-close` (the window closed mid-topgrade: the
topgrade shim's pid is gone and goleak is clean), `real-runs` (one window,
`KeepRuns`+1 real Run topgrade runs then a dry one, from the Overview row
each time, in a scratch state dir with a clock stepping a minute per
Driver: a fresh Driver per Start, one run directory per real run, the
oldest rotated out, the dry run in `dryrun/`, `last-run.json` in the
root). The plain-Go `gui/internal/realdriver` tests cover the relay,
`Options.Validate`, a dry Full Cleanup on the real Driver without a
display, the per-run directories and rotation (`TestRunDirs`, a topgrade
shim first on PATH), and the audit prefetch with a blocking fake
`Foreign` (no pacman, no AUR): it starts while the gate is up and the step
consumes it (one lookup), and a declined gate or `Close` cancels and
reaps it (`TestAuditPrefetch*`).

```sh
cd gui
go test ./...                                     # runview; the display tests skip
scripts/headless.sh                               # + the display tests, headless
ARCHMAID_GUI_PERF=1 scripts/headless.sh           # + enforce the budgets below
GSK_RENDERER=vulkan scripts/headless.sh           # another renderer (default: cairo)
```

With `ARCHMAID_GUI_PERF=1` the tests fail over budget: warm first frame
< 400 ms (the best of the runs after the first, measured from exec to the
frame clock's first `after-paint`), idle RSS < 120 MB, ≥ 5k lines/s for the
unpaced flood, main-thread work per flush p95 ≤ 4 ms (the time in the pump's
`apply`: model, list splice, queueing the scroll), RSS after 50k lines < 200 MB. They are
always measured and logged with `-v`. CI runs them without the variable.

### GUI measurements

Dev machine (Ryzen 7 7800X3D, RTX 4090 on driver 615.71, gtk4 4.22.5,
libadwaita 1.9.4, go1.27.1), headless weston, warm:

| Measure | Budget | `GSK_RENDERER=vulkan` (GTK's own default) | cairo (archmaid-gui's default) |
|---|---|---|---|
| First frame, warm | < 400 ms | 376–396 ms (one run 446) | 85–95 ms |
| Idle RSS (PSS, anon) | < 120 MB | 280–290 MB (181–192, 92–103) | 110–122 MB (45–56, 24–36) |
| 50k unpaced, nothing dropped | ≥ 5k lines/s | 220 ms = ~227k lines/s | 225 ms = ~222k lines/s |
| Main-thread work per flush, p95 (max) | ≤ 4 ms | 1.5 ms (6.0) | 1.6 ms (6.6) |
| 10k lines/s paced: p95 (max), dropped | ≤ 4 ms | 0.96 ms (2.6), 0 | 0.90 ms (2.0), 0 |
| RSS after 50k lines | < 200 MB | 320–341 MB | 142–149 MB |
| Frame work (before → after paint), paced p95 | — | 6.0 ms | 5.0 ms |
| 50k unpaced, all colour (`flood-colour`): rate; flush p95 (max) | ≥ 5k lines/s; ≤ 4 ms | — | ~95k lines/s; 3.4 ms (9.8) |

The max flush is the first one (the checklist's rows and the first output
rows are created). The RAM over budget with Vulkan (and the GL renderer:
~270–340 MB, first frame ~340 ms) is NVIDIA's userspace driver in the
process; cairo meets every budget. Headless weston has no GPU path of its
own, so the Vulkan numbers are the closest to the desktop we can get without
opening a window there. Before the follow-the-bottom fix the list's head trim
cost 6 ms a flush (the visible rows were rebound each time): keep the newest
line in view while appending.

Colour costs in the rows, not in the model: a line is stored raw and
stripped (`runview.StripSGR`, ~0.15 µs) as it arrives, and its spans are
parsed (`ParseSGR`, ~0.7 µs for seven spans) and turned into a Pango
attribute list only when a row binds it, so only the ~30 visible lines pay.
`BenchmarkApplyFlushColour` (a 512-line colourful flush through the model) is
~80 µs.

**The follow scroll runs once per frame, not per flush.** `ListView.ScrollTo`
measures the rows it brings into view on the spot; with the colour flood's
multi-run Pango layouts that was ~2.8 ms of a ~3.4 ms flush (instrumented:
model ~0.07 ms, splice ~0.6 ms, the Overview and status bar ~3 µs), and an
unpaced flood flushes about three times per frame, so it measured rows that
were never drawn. `output` now queues one tick callback that scrolls to the
newest line just before the frame's layout. Measured on the same machine
(three full `TestGUIThroughput` runs each, cairo, load average 1.7–6.6 from
other agents' builds), flush p95 before → after:

| Scenario | Before | After |
|---|---|---|
| `flood` | — | 0.48–0.52 ms |
| `flood-paced` | — | 0.29–0.49 ms |
| `flood-colour` | 3.5–4.5 ms | 0.53–0.85 ms |

The unpaced floods now finish in ~100–150 ms (~320k–520k lines/s) in 6–9
frames; the measuring moved into the frames that draw (paced frame work p95
6–10 ms under that load, against 5.0 ms before), but happens once per frame
instead of once per flush.

**archmaid-gui renders with cairo by default.** `ui.DefaultRenderer`, called
first thing in `main` (and in the harness's child, so the tests measure what
ships), sets `GSK_RENDERER=cairo` only when the variable is unset; the owner
overrides it with `GSK_RENDERER=vulkan` (or `ngl`, `gl`) in the environment
or the `.desktop` file. The table above is why: only cairo meets the RAM and
first-frame budgets on this NVIDIA machine, at the same throughput.

Builds: a cold `go build ./cmd/archmaid-gui` (empty `GOCACHE`, modules
downloaded) takes ~430 s on 16 threads; an incremental build after a change
in `internal/ui` or `internal/runview` ~1.0–1.1 s; the binary is 21 MB.

## Make targets

A root `Makefile` wraps everything CI runs; paths live in variables at its top.

| Target | Runs |
|---|---|
| `make gate` | `scripts/check.sh` (TUI and helper, one module at the repo root) |
| `make e2e` | `bash cmd/archmaid/e2e/run.sh` (needs vhs, ttyd, ffmpeg) |
| `make bench` | `scripts/bench-compare.sh $(BASE)`: HEAD against a base commit with benchstat — see "Bench compare" |
| `make perf` | `ARCHMAID_PERF_GUARD=1 scripts/check.sh` (the gate, then the opt-in perf guard — see "Perf guard") |
| `make gui` | in `gui/`: build, vet, gofmt, `runview` tests under `-race`, `go test ./...` (display tests skip) |
| `make gui-test` | `gui`, then the display tests under `gui/scripts/headless.sh` (needs weston) |
| `make all` | `gate` — run before pushing |

## CI

`.github/workflows/ci.yml` runs on every push and pull request; a newer push to
the same ref cancels the older run. Paths are in the workflow's `env:` block.

| Job | Where | Runs |
|---|---|---|
| `core` | `archlinux:latest` container (some discovery tests expect Arch tooling) | `make all`: the gate (TUI and helper). Go build and module caches keyed on `go.sum` and the Go version. |
| `e2e` | `ubuntu-latest` + setup-go + vhs-action (vhs, ttyd, ffmpeg) | `make e2e`; uploads `cmd/archmaid/e2e/*.txt` as an artifact on failure. |
| `bench` | `ubuntu-latest` + setup-go, full history | `scripts/bench-compare.sh <base>` on the one runner: the PR's merge-base (or a push's previous SHA) against HEAD, benchstat-compared; fails on a significant slowdown (see "Bench compare"); uploads `benchstat` (old/new results, report) as an artifact, always. About 1–2 minutes. |
| `gui` | `archlinux:latest` container + gtk4, libadwaita, weston, dbus, a font | `make gui-test` as an unprivileged user with `GSK_RENDERER=cairo` (no GPU): build, vet, the plain-Go tests, the display tests in headless weston; budgets not enforced. Build and module caches keyed on `gui/go.sum` + `go.sum` and the Go version (a cold gotk4 build is ~7 min on 16 threads, more on a runner). |

Stays manual (not in CI): a real Full Cleanup, sudo/faillock behaviour, the
polkit live test on a scratch user, sudo without a tty, and askpass against real
sudo. See "Manual verification".

## Running

```sh
# all from the repo root, the Go module root
go test -race ./...                 # the quick loop — must be green
go vet ./... && gofmt -l .          # must print nothing
go test -run TestAbort ./internal/tui   # a subset by name
go test ./internal/tui -run Golden -update   # regenerate goldens after a deliberate UI change
go test -run TestTranscript ./internal/tui   # the transcripts; -update-transcripts rewrites them
go test -run '^$' -bench StreamView -benchmem ./internal/tui   # UI cost of streaming 5k / 20k lines
ARCHMAID_PERF_GUARD=1 go test -run TestPerfGuard -v ./internal/tui   # the opt-in perf guard alone
bash cmd/archmaid/e2e/run.sh        # VHS e2e (needs vhs, ttyd, ffmpeg)
go build -o archmaid ./cmd/archmaid && s="$(mktemp -d)" && HOME="$s" XDG_STATE_HOME="$s/.local/state" ./archmaid --dryrun   # manual poke under a scratch HOME (ground rule 8); logs go to $s/.local/state/archmaid/dryrun/
scripts/state-guard.sh snapshot > /tmp/before && scripts/state-guard.sh compare /tmp/before   # the real-state-dir guard by hand
```

A binary built into `cmd/archmaid/` is gitignored; the ones above land in the repo root, so delete them when done.
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
   use `isolateDownloads` (cmd) or `isolate` (`internal/downloads`, which returns Options instead of setting env), which also point `XDG_CONFIG_HOME` at the scratch
   home and clear `--downloads` / `ARCHMAID_DOWNLOADS`. Tests that read or write
   run logs or the AUR snapshot also set `logDir = t.TempDir()` (restored on
   cleanup), so nothing lands in the real `~/.local/state/archmaid`.
   Every test package calls `testenv.Run` from its `TestMain` (after any
   re-exec dispatch it needs, like `--askpass`); `internal/testenv`'s
   `TestEveryTestPackageRunsUnderTestenv` fails a package that doesn't.
   `testenv.Run` snapshots the real state dir with `scripts/state-guard.sh`
   before the package's tests and compares after them, so plain `go test`
   FAILs a package that deleted, changed or added anything anywhere under
   it (`TestStateGuardFailsPackage` proves it on a scratch
   `XDG_STATE_HOME`).
3. **No real external programs.** `pacman`, `sudo`, `su`, `systemctl` etc. are
   replaced by shell shims placed first on `PATH` (see Recipes). This is
   enforced: `testenv.Run` puts traps first on `PATH` for the whole package
   run, for the privileged binaries (`su`, `sudo`, `pkexec`, `runuser`,
   `faillock`, `pkttyagent`) and every system-changing tool archmaid can
   invoke (`topgrade`, `yay`, `paru`, `pacman`, `paccache`, `systemctl`,
   `journalctl`, `systemd-tmpfiles`, `hyprpm`, `flatpak`, `fwupdmgr`;
   `testenv.Trapped`), read-only calls like `pacman -Qqm` included. A trap
   logs its argv and exits 97, and if any trap was hit the package FAILs
   with "a test reached a real privileged or system-changing binary" and the
   calls, even when every test passed (a wrong password through the real
   `su`/`sudo` can cost a pam_faillock failure, and deny=3 is one typo from
   a lockout; an unshimmed GUI test once ran the real `topgrade -y`, whose
   yay reached the real sudo twelve minutes later). `TestE2EShimsAreTrapped`
   keeps every tool the e2e harness shims trapped. A test's own shim dir, prepended to `PATH`
   or made the whole `PATH`, still wins; a re-exec'd test binary or shell
   script inherits the traps. `PATH` can't catch an absolute path, so
   `TestNoAbsolutePrivilegedExec` rejects any Go string literal naming one of
   the privileged binaries under a system dir (`/usr/bin/sudo`…) outside a short
   allow-list of literals that are only compared, never run. archmaid-helper
   runs `/usr/bin/pacman` and friends by absolute path (as root, under
   pkexec); its verb tests use a fake `Runner` and never execute them, and
   only the `ExecRunner` tests run real, harmless binaries (`env`, `cat`,
   `sh`).
4. **No network.** The AUR RPC is an `httptest` server reached through the
   `aurRPCURL` var. `TestMain` sets `aurRPCURL` to a closed local port, so any
   test that reaches the audit without `fakeAUR` (choosing Full Cleanup or Run
   topgrade starts it at once, see `auditFetch`) fails fast instead of calling
   the real AUR. `internal/audit` has no default endpoint at all: `Run`
   refuses a `Config` without `RPCURL`, and its tests pass an `httptest` URL
   and a `Config.Foreign` func instead of a pacman shim.
5. **Deterministic frames.** `TestMain` (in `teatest_test.go`) forces the
   lipgloss ASCII profile so goldens are plain text. Goldens compare one
   re-rendered `View()` frame with the live status line pinned by
   `fixedModel()`; pin anything else time- or machine-dependent the same way.
   The spinner is pinned to one frame where a running row must render.
6. **Don't weaken a test to make it pass.** A failure is a bug in the code (or
   a deliberate behaviour change — then change the test and say why).
7. **Nothing a test starts outlives it.** `testenv.Run` enforces it for the
   package: it sets `ARCHMAID_TESTENV` to a fresh id that every process a
   test starts inherits, and makes the test binary a child subreaper, so an
   orphan stays below it even with a hand-built environment. After the tests,
   before the traps go, it SIGKILLs every process of the uid that carries the
   id or descends from the test binary (and the process groups they lead),
   and FAILs the package with "a test left processes running" and each one's
   pid, comm and command line (`TestRunReapsOrphans`). Within the package,
   `PATH` and `ARCHMAID_SHIM_LOG` are
   process-wide and read when a command execs, so a goroutine left running
   runs the *next* test's shims and writes to its exec log (a stray
   `pacman -Qqm` from an earlier test's audit prefetch failed
   `TestActivateFullCleanupRunsNoPacman` on CI this way). Run a menu action
   on a Driver the test closes: `tuiEngineTarget` (and `run`, `startQueue`)
   awaits each prefetch right after the batch that started it and closes its
   `syncDriver` when the test ends; a test on the real Driver (`newDriver`)
   closes it, which waits for everything it started. A test that calls
   `startAuditFetch` itself reaps it with `reapAuditFetch`, which cancels it
   and waits for its goroutine when the test ends.
8. **Never start the binary against the real HOME — not even `--help`.** A
   real run folder was lost this way (HANDOVER.md, "Nothing deletes run
   history at startup"). Any `archmaid` you start — a manual poke, a VHS
   tape, a test that execs it, an agent checking a flag — gets a scratch
   `HOME` and `XDG_STATE_HOME` (see "Running" and `e2e/run.sh`). The gate
   enforces the result: `scripts/check.sh` and `make e2e` snapshot
   `${XDG_STATE_HOME:-$HOME/.local/state}/archmaid` before they start
   (`scripts/state-guard.sh`: recursive names, types, sizes and mtimes,
   read-only) and FAIL with a diff if anything in it changed, even when
   every test passed. A missing dir must still be missing. So don't run
   archmaid for real while the gate runs, and never "fix" a guard failure by
   re-snapshotting: find what wrote there. Startup itself must stay
   side-effect free: `TestStartupDeletesNothing` runs `--dryrun`, `--help`,
   bad flags and the askpass relay against a state dir holding
   `logKeepRuns`+1 runs and fails if any is gone.

### Test seams

Package-level vars exist purely so tests can intercept side effects; use them
rather than adding more indirection:

| Var | Seam for |
|---|---|
| `dryRun` | stub every mutating step |
| `logDir` | redirect run logs + the audit snapshot |
| `execProcess` | capture the editor command instead of handing over the terminal (`tea.ExecProcess`) |
| `sigkillGrace` | shrink the SIGINT→SIGKILL grace period (read into `proc.Options.KillGrace` at launch) |
| `stepWaitDelay` | shrink how long a finished step's output pipe is still read (`proc.Options.WaitDelay`) |
| `askpassWrapper` | wire askpass up (or not) for `privileged()` without starting a server (`privileged()` builds `privilege.Sudo{Wrapper: askpassWrapper}`: the Driver's `Deps.Priv`, and the tests' `runStep`) |
| `aurRPCURL` | point the AUR audit at `httptest` |
| `tickFn` | `tea.Tick` for the status refresh (the sudo keep-alive is the Driver's) |
| `logNow` | the clock that names the process's run directory (fixed by the first store) |

`TestMain` also dispatches `--askpass <sock> [prompt]` to `privilege.RunClient`
exactly as `main()` does, so a wrapper that execs the test binary relays
instead of running the suite again. `internal/privilege` has the same
dispatch in its own `TestMain`, which is what lets its askpass round trip run
for real. Its read deadline is an option (`ServerOptions.ReadTimeout`); the
view of the system that `Request.Verify` and `Listen` check against (uid,
own binary, `/proc`) is the unexported `sys`, the real thing unless a test
swaps it. Its own tests use `setSystem`; other packages' tests use
`internal/privilege/privtest` (`FakeSystem`, and `NewRequest` for a Request
over a `net.Pipe`), which reaches privilege through the internal `seam`
package. The TUI's `fakeProcs` is `privtest.FakeSystem`. No production
caller can hand Verify a process table, and `privtest` panics outside a
test binary; `scripts/check.sh` fails if a binary links `testing`.
`internal/helperclient/helpertest` is the same pattern for `helperclient.Run`'s
install check (`UseHelperFile`, through `helperclient/internal/seam`): it
moves only the file the check stats, never the `pkexec` argv, which always
names `HelperPath` (polkit's `exec.path`). helperclient's own tests use
`export_test.go`.

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

**Test an in-process step.** Run it in a queue of its own through the TUI on
the engine: `startQueue(t, steps...)` (`fullcleanup_audit_test.go`) adds a
menu action for them, starts it on a `tuiEngineTarget` and runs it up to its
first prompt or its end; `.answer("y")` presses a key and settles again, and
`.m` is the model left behind (see `startAudit`). If the step after it could
run a real command when the test gets gating wrong, substitute a harmless
stand-in (`Argv: true`, as `harmlessTopgrade` does). For an abort mid-step,
use the real Driver (`newDriver`; see `TestAbortCancelsAuditStep`): the
`syncDriver` runs work on the test's goroutine, so nothing can interrupt it.

**Test the model on Events.** The model is a front end to an
`engine.Driver`: give it a `fakeDriver` (`engineModel()`, or
`fixedModel()`/`sizedModel()`, which carry one), `feed` it Events and press
keys with `update`; `lastSent` is the Command a key produced
(`adapter_test.go`). A model without a Driver (`eng == nil`) ignores keys
and Events.

**Fake the AUR.** `fakeAUR(t, db)` serves the RPC from a map and records
request count and batches; it also fails the test if a lookup isn't a POST.
`auditEnv(t)` gives a private `HOME` + `logDir` for anything that persists.
In cmd, `aurInfo`/`snapEntry` alias `audit.Info`/`audit.Entry`, and
`baselinePath`/`loadBaseline`/`saveBaseline` read and seed the snapshot the
current settings point at; `runAudit(now)`/`runAuditStep(ctx)` run the menu
audit and the audit step against them. In `internal/audit`, `fakeAUR` returns
the server's URL and `config(t, url, pkgs...)` a `Config` with the snapshot in
a temp dir and the log collected.

**Add or change a golden.** Write the test, run
`go test ./internal/tui -run Golden -update`, then **read the diff** — it should be
exactly the change you intended — before committing the `.golden` file. Prefer
a direct frame (`viewGolden(t, m)` on a `sizedModel()`, see `golden_test.go`):
a live teatest program ticks the spinner and the input cursor, so its frames
drift. Use a teatest program (`TestMenuGolden`) only when the point is the
real program's key handling.

**Test a real signal path.** Spawn a real child (`sleep`, or a script that traps
SIGINT), never a mock; shrink `sigkillGrace` (or pass a small
`proc.Options.KillGrace`); assert on process liveness with a signal-0 probe or
on the done's wait status (see `internal/proc/stop_test.go`,
`internal/driver`'s `TestStopEscalates`).

## Transcripts

`internal/testtranscript` is the recorder and its event vocabulary;
`transcript_test.go` is the rest of the harness (shims, the side-effect
watcher, golden compare; the TUI's target is `tuiEngineTarget` in
`transcript_engine_test.go`); `transcript_scenarios_test.go` holds the
scenarios, each a menu action recorded to
`internal/tui/testdata/transcripts/<action>_<case>.<mode>.golden` — `.dryrun` under
`dryRun`, `.shim` run for real against shims. They characterize today's
behaviour, bugs included: a fix that changes behaviour changes a golden, and
the diff is the review.

The vocabulary is `internal/engine`'s events projected down: the
`sessionTarget` drives an `engine.Session` and maps its events one to one
(the mapping is in the engine's package doc, Transcripts); `Exec` and
`SnapshotChanged` stay the harness's own observations.

```sh
go test -run TestTranscript ./internal/tui                       # compare, through the TUI and the Session
go test -run TestTranscriptSession ./internal/tui                # the Session only (skips say what it can't run yet)
go test -run 'TestTranscript/fullcleanup_' ./internal/tui        # a subset
go test -run TestTranscript ./internal/tui -update-transcripts   # rewrite, then read the diff
go test -race -count=5 -run Transcript ./internal/tui            # must stay deterministic
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
| `abort` | `<i> "<title>"` — ctrl+c, then y. It stands for the checklist the abort settles (the running step aborted, every pending one behind it skipped), so no `step` line follows for those; the target checks them instead |
| `note` | a notice popup's text |
| `audit` | a line of the AUR-audit report screen |
| `end` | `outcome=completed\|aborted\|cancelled\|notice\|report errs=N skipped=N` |

Within one `Update` the order is fixed: exec, out, finished steps (an
inserted step in its place among them), changed plan flags, log entries
(grouped by category file), snapshot, then steps that started, then a notice
or report. `$HOME` and the log dir are normalised.

**Recorder and Target.** The harness is split so that more than one frontend
can be held to the same goldens. `testtranscript.Recorder` owns the format and
the answer script; it knows nothing of Bubble Tea, the model or the engine.
It drives a `Target`:

```go
type Target interface {
	Start(action string) (label string, err error) // choose the menu action
	Next() (Event, error)                          // next event; nil when the action is over
	Reply(Answer) error                            // answer the open prompt, or abort
	Outcome() (Outcome, error)                     // completed|aborted|cancelled|notice|report, errs, skipped
}
```

The events are the engine API planned for P4, projected down to what a
golden records — `QueueStarted`, `StepInserted`, `PlanChanged`, `StepStarted`,
`StepDone`, `OutputLine`, `Exec`, `LogWritten`, `SnapshotChanged`,
`NeedsConfirm`, `NeedsPassword`, `NeedsSelection`, `Aborted`, `Notice`,
`OutputReport` — and each has exactly one line shape (the table above). Steps
are queue positions, 0-based. The set is closed (an unexported marker
method), so a target can only report what the recorder can write. A target
returns the `Needs*` prompt only once everything else is done, and the
recorder replies before asking for more: it matches the open prompt against
the script (`Answer{Kind, Match, Reply}`: `confirm`, `password`, `select`),
writes the `ask`/`auth`/`pick` line and calls `Reply`. An `abort` answer is
the recorder's too: on the `StepStarted` of the step whose title matches it
calls `Reply`, and the target emits the child's `exec` line, then `Aborted`.
An unscripted prompt, a mismatch, an unused answer or a target error fails
the test with the transcript so far. `internal/testtranscript/recorder_test.go`
feeds the recorder a hand-built sequence through a fake target and pins
every line kind.

**Side effects.** `effects` (in `transcript_test.go`) turns what the action
left outside the frontend into events: new shim-log lines (`Exec`), new log
entry headers (`LogWritten`), a changed AUR snapshot (`SnapshotChanged`). It
only reads files, so any target running against the shims and the real log
store uses it unchanged.

**tuiEngineTarget (E7).** The TUI's target (`transcript_engine_test.go`):
the model on a `syncDriver` in pull mode, as the binary runs it on
`internal/driver`. Keys go through `Update`, which Sends Commands; the
Driver applies each at once and runs what takes time (a probe, a discovery,
a step) as work, FIFO, when `Next` pulls; the Events it published go back
through `Update`, batch by batch, and the target records what the model
then shows (checklist statuses, plan flags, the screen). Cmds `Update`
returns are never run (no ticks, a static password cursor). When nothing is
left to pull an open screen becomes a `Needs*` event; `Reply` types the
keys. A step child logs itself asynchronously, so its `exec` line is taken
with its first output or exit, never from the batch that launched it. The
AUR audit's prefetch is awaited right after the batch that started it, so
its `pacman -Qqm` lands with the action's own queries, before the sudo
probe. `run(t, shimLog, item, answers...)` records through a
`tuiEngineTarget` and returns a `Driver`, the recorder plus the target, for
a test that checks the model afterwards (`d.m`) or presses more keys
(`d.keys`, then `d.d.Pull`/`d.deliver`).

**sessionTarget (P4).** Scenarios take the target as a constructor,
`newTarget func(t, shimLog) tt.Target`, and `replay(t, mk, skip)` runs them all
against their goldens; `TestTranscript` is `replay` with
`newTUIEngineTarget`, and `TestTranscriptSession`
(`transcript_session_test.go`) runs the same scenarios with
`newSessionTarget` against the same goldens, no copies; `TestTranscriptDriver`
does the same on the `syncDriver` alone (`driverTarget`).
`sessionTarget` drives an `engine.Session` and carries out its Effects the way
a Driver would, on the test goroutine, against the same shims, log store and
fake AUR: `WriteLog`, `WriteLastRun`, `RunHook` and the audit prefetch (awaited,
as in `tuiEngineTarget`) run as an Output is applied; the probe, a discovery and each
step (a child through `runStep`/`proc`, pumped with `proc.Next`; an in-process
`Run`, awaiting its prefetch) become work, run FIFO by `Next`, their answers fed
back through `Handle`. It maps:

| Engine | Event |
|---|---|
| `QueueStarted`, `StepInserted`, `PlanChanged` | the same, `Step` from the `PlanStep`'s flags; a `PlanChanged` whose flags equal the plan line already written is dropped (a new confirm text alone isn't recorded) |
| `StepStarted`, `StepDone` (`Skipped`/`AuthRefused` → `skipped`) | `StepStarted`, `StepDone` |
| `OutputChunk` | one `OutputLine` per line |
| `NeedsConfirm{Msg}`, `NeedsPassword` | the prompt; `Reply` calls `Confirm(id, yes)` or `SubmitPassword(id, pw)` (the su shim's password for "correct") with its ID |
| `Notice` | `Notice` |
| `Finished` | the `Outcome` (`Outcome.String`, `Errs`, `Skipped`) |
| `LogWritten` | not recorded itself: the log entries come from disk (`effects`), and each one the Session reports must be among them, in order within its category (entries written in one go come ordered by file) |
| `PromptClosed` | the open prompt, if it is that one, is gone (not to be answered) |
| `AuthWaiting`, `AuthResolved`, `FaillockState` | nothing; `Authenticate` runs `privileged().Authenticate` against the shims, `ReadFaillock` is answered with a fixed count, never the real tally |

`Exec`, `LogWritten` and `SnapshotChanged` come from `effects`, called where
`tuiEngineTarget` calls it: execs after each piece of work (a step child's with its
first output or exit, never from the Output that launched it), logs and the
snapshot after finished steps and plan changes, before started steps. Each
Output's events are buffered in the **Format** order above. So that this
bucketing can't hide an order bug, each Output is also held, as it comes, to
the engine's ordering contract (an `enginetest.Checker` fed Output by Output,
reporting the Output that broke it) and to that order itself (`tuiOrder`: no
event after one its transcript records behind it, save a further `StepDone`
after a `LogWritten`, steps settled together). Past the golden, the
last-run record the Session's `WriteLastRun` carried is checked (at most
once, a sane span, and what the file reads back). Until E7b each scenario
was also held to a reference run through the old TUI path: every confirm's
fields and the last-run record matched it in all 31 scenarios when that
path was deleted; the outcomes and confirm fields are `internal/engine`'s
to test now.

What the Session can't do yet is skipped by name with the reason
(`sessionSkips`), never silently, and a skip naming no scenario fails the
test. Since E5 all 31 scenarios pass byte for byte and `sessionSkips` is
empty. Goldens are written only through the TUI (`tuiEngineTarget`):
`-update-transcripts` skips the session and driver replays.

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

**Add a scenario.** Append to `transcriptScenarios` a
`func(t *testing.T, mk newTarget) *tt.Recorder`: `transcriptEnv(t, dry)`,
shims, fakes, then `return record(t, mk, shimLog, "<menu label>",
answers...)`. Run with `-update-transcripts` and read the new golden line by line
— it's the spec you're signing off.

## DMS widget and updates timer (`packaging/dms/`)

`go test ./packaging/dms/` (part of `./...`, so of the gate):

- `TestStateJS` makes the real writers (`logstore.WriteLastRun`, `audit.WriteState`, the real
  `archmaid-updates-check` script with a fake `checkupdates`) produce the state files, writes
  them into a generated `fixtures.js` beside a copy of `archmaid/state.js` and
  `tests/tst_state.qml`, and runs them with `qmltestrunner` (offscreen). It covers parsing,
  neutral "no data" on missing/torn/wrong-version/dry-run input, levels, the 14-day and
  3-day rules and the launcher choice. Skipped when `qmltestrunner` is missing. It prefers
  `/usr/lib/qt6/bin/qmltestrunner`, because `/usr/bin/qmltestrunner` on this machine is a
  stale build that exits 1 silently.
- `TestUpdatesScript` (count, none, failure leaves no file, 0600/0700) and `TestManifest`.
- Not in the gate: loading the QML in a real quickshell. Do it by hand in a scratch dir that
  holds `shell.qml` plus stub `Common/`, `Widgets/`, `Modules/Plugins/` modules (qmldir files
  with `Theme`, `Proc`, `DankIcon`, `StyledText`, `DankButton`, `PluginComponent`,
  `PopoutComponent`), a `Loader` of `ArchmaidWidget.qml`, and
  `WIDGET_DIR=<packaging/dms/archmaid> XDG_STATE_HOME=<scratch> TERMINAL=kitty timeout 20 qs -p <scratch>`.
  That is a separate quickshell instance (own config id, no window); never touch the running
  DMS. Last result: widget, both pills and the popout instantiate; the three files are read.

## Coverage map

What each file pins. Behaviour contracts, not test names — grep the file for
the names.

| File | Pins |
|---|---|
| `internal/sanitize/*_test.go` | `String` for every escape/control/format-character class in both modes, the invalid-UTF-8 and CR behaviour, and the no-copy fast path (with benchmarks); `OneLine`, `Clip`, `ClipTail`, `Field`, `QuoteName`. Pure unit tests, no UI. |
| `internal/logstore/logstore_test.go` | The store itself: paths and the dryrun slot, reading creates nothing and reports `ErrEmpty`, 0700/0600 modes fresh and fixed up, entry format, plain-text logs (written and read back), `Prune` keeps the newest runs and spares `dryrun`, strays and its own run; `Options.KeepRuns` rotates only when a real run's first `Append` creates its directory (the new run counted), a dry-run store never prunes. |
| `internal/steps/steps_test.go` | `LogCategory` defaults to cleanup; `Command`/`Args` split `Argv` (nothing for an in-process step); `Result.Check` refuses `ConfirmNext` together with `InsertNext`, failed or not, and accepts each alone. |
| `cmd/archmaid-helper/verbs_test.go` | Step verbs and the helper's verb table in lockstep (the one package that can import both): every `steps.Verb*` constant, read from the source, is a helper verb; every non-empty `Verb` on a step the actions build is one too, including the steps only built at run time (Repair's reinstall after its root recheck, the orphan removal the probe inserts, the services disable, the downloads delete), reached by running each discovery and in-process step against shimmed `pacman`/`sudo`/`systemctl`. |
| `internal/actions/actions_test.go` | The menu table (unique IDs, `ByID`, each entry a queue, a discovery or a front-end entry, discoveries have a `Busy` label); Full Cleanup's step order with and without hyprpm, Run topgrade's; every queued step has a unique `ID` and the right helper `Verb`; `TestNoPrivilegedStepAfterHyprpm`: in no action's queue does a root step, or any in-process step (its result may insert a root step, as the orphan probe's does), follow hyprpm; `DisableUnits`; the audit report is the front end's (`Env.AuditReport`). |
| `internal/actions/repair_test.go`, `repair_err_test.go`, `services_test.go` | A Repair notice says "timed out" for a timeout, never the audit's deadline wording. The parsers: `parseBrokenPackages` (a package with several missing files once, stray lines ignored; fixture `testdata/pacman_Qkq.txt`), `chunkStrings`, `qkqOutput` (pacman's exit 1 with a report is a result; stderr or the exit status otherwise), `parseInactiveServices` on the captured `list-units` fixture (the `●` not-found row, masked, template instances, excluded names). |
| `main_test.go` | Dry-run safety: every Full Cleanup step, started as the Driver starts it, only reports what it would run. The services multi-select (toggle) and `actions.DisableUnits`, the parsers, the default log category, and the View-log picker (opens, returns where it came from, opens the chosen category). The queue, sudo, confirm and logging flows are `internal/engine`'s, with the TUI's side in `adapter_test.go` and the transcripts. |
| `gate_orphan_test.go` | Orphans: building the Full Cleanup queue and choosing it run no pacman (only the audit prefetch's `-Qqm`, off the UI thread); the in-process probe sits after topgrade and the cache steps, before the AUR cache step; it inserts the removal right behind itself only when orphans exist, with exactly the names it found (also as the step's `Items`, the cascade as its `Cascade`) and a confirm that previews the real `pacman -Rns` cascade total (both counts when they differ, plain count when equal), defaulting to No, a decline logged as SKIP (run through the TUI on the engine); none → a clear line and no confirm; a failing `-Qdtq` or `-Rs --print` fails the probe and inserts nothing; an abort on the real Driver kills the probe's pacman and inserts nothing. Temp files are cleaned with `sudo systemd-tmpfiles --clean`, not a find/rm. (The proceed-gate is `internal/engine`'s and the transcripts'.) |
| `activate_bench_test.go` | `BenchmarkActivateFullCleanup`: choosing Full Cleanup is the model's `Start` and the Session's `Start`, which builds the queue and asks for the probe and the prefetch, nothing else (guarded by `TestPerfGuard`, opt-in). `actions_glue_test.go` holds the helpers that build the real queues and discoveries through `actionEnv`. |
| `sudo_hardening_test.go` | Sudo steps, started as the Driver starts them (`runStep`), run as `sudo -A` (password via `SUDO_ASKPASS`, never the tty), or `sudo -n` when askpass isn't wired, and other commands are untouched; both topgrade steps need sudo. (The keep-alive is the Driver's.) |
| `auth_test.go` | `privileged().Authenticate`, the Driver's password check: a wrong password never reaches `sudo` and su runs as `su -s /usr/bin/true <user>`, a correct one mints the ticket. On the up-front prompt: enter sends the password and locks the input, every key is ignored until the engine answers, the re-ask unlocks it and shows its error (a lockout notice verbatim), and the popup shows "authenticating" meanwhile. |
| `stream_test.go` | The pane on the engine's `OutputChunk`s: it follows output only at the bottom: scrolled up, new chunks don't yank it, and when the tail drops old lines the line being read stays on top; a 20k-line step leaves the newest 2000 in order (the log gets all of it: the engine's). `BenchmarkStreamView5k`/`20k`: Update + View cost of a flood of 256-line chunks (guarded by `TestPerfGuard`, opt-in). |
| `perfguard_test.go` | Opt-in (`ARCHMAID_PERF_GUARD=1`): the streaming and status-line benchmarks stay under 3× the dev machine's baselines, choosing Full Cleanup under 1 ms and a Running frame (`BenchmarkViewRunning`, `view_bench_test.go`) under 600 µs; see Perf guard above. |
| `overlay_test.go` | Popups float, centered, over their background; ANSI-styled backgrounds keep their width; the abort dialog stays over the running screen. |
| `resize_test.go` | The output pane never overflows the terminal (running queue, log viewer, resize while a popup is open); an askpass prompt (the engine's `NeedsPassword`) over another popup keeps the real background; nested popups resolve to the right base screen. |
| `services_test.go` | Services discovery through `actions.Menu`: against a `systemctl` shim serving the `internal/actions/testdata/systemctl_list_*.txt` fixture system, the batched `is-enabled` with its per-unit fallback picks the enabled-but-inactive units, none is a notice, and an equivalence test runs the old bash pipeline against the same system to prove both select the same units. |
| `startup_test.go` | Startup deletes nothing (ground rule 8): `--dryrun` (with its own log and last-run writes), a plain start, `--help`/`-h` (usage on stdout, exit 0), an unknown option, a stray argument, a bad `--downloads` (exit 2) and the askpass relay each leave a state dir of `logKeepRuns`+1 runs intact, writing only under `dryrun/`; a real run rotates to `logKeepRuns` (itself included) on its first entry, not before. |
| `log_hygiene_test.go` | The log through the TUI: opening every category in View log creates no run directory (it would take one of the five prune slots); what a step streamed (colours, a progress bar, an OSC 52 clipboard write), run through the TUI on the engine, shows with its colours only and lands in its log as plain text, and an old log still holding raw escapes is shown plain; a store rebuilt because `logDir` moved keeps the process's run-directory name, even a minute later (`logNow`), while entries keep the real time. The store itself (modes, pruning) is `internal/logstore`'s. |
| `ticks_test.go` | Ticks never multiply: the status refreshes after a finished queue (`Finished`) and after the editor are one-offs, not another 4 s loop. (The sudo keep-alive is the Driver's.) |
| `repair_test.go` | Repair's discovery against a `pacman`/`sudo` shim pair that flags different packages as the user and as root: the user scan (`pacman -Qq`, then `pacman -Qkq -- <chunk>` per worker) flags the same packages in the same order for 1, 2, 4, 16 or 100 workers, checks each installed package once and never runs sudo; the root recheck is one `sudo -n pacman -Qkq -- <candidates>` and only what root confirms is offered for reinstalling; without a ticket it is a couldn't-scan notice, never "nothing broken". The flow around it (probe, prompt, give-up notice) is `internal/engine`'s and the `repair_*` transcripts'. |
| `hyprpm_test.go` | `hyprpm` is disabled inside topgrade and runs as the last step, only when installed (it drops the sudo ticket on exit). |
| `coverage_test.go` | Thin spots: `editConfig` (creates defaults, never clobbers, via the `execProcess` seam); password masking in the rendered frame; every checklist glyph; the status line's tea glue only (the line carries its three labels, a `statusMsg` updates the header and schedules the next tick); all summary outcomes. `status_test.go` is just the `BenchmarkSystemStatus` wrapper for the perf guard. |
| `internal/sysstat/sysstat_test.go` | The status line itself, against fixtures: the df/du-style and free-style size formatters, the rendered `Line`, `Read` spawning no child process (empty `PATH`), and `N/A` for every unreadable source. |
| `audit_test.go` | The AUR audit through the TUI, fully offline (the audit itself is `internal/audit`'s): an AUR maintainer with terminal escapes reaches neither the step output, the confirm, the report nor `audit.log` intact, while the raw value goes into the snapshot; a corrupt snapshot gates the step and offers `a` on the menu report, and is replaced (kept aside) only by the accepted commit; an end-to-end first-audit-then-takeover menu run, logged; a dry run never advances the snapshot; the notices (no foreign packages, an unreachable AUR, short enough for a popup); the menu audit saves all but the high findings and `a` on the report accepts those (q drops the accept, so they stay flagged), added co-maintainers included; an added co-maintainer gates the step by name until the commit records it; an old-format snapshot is a co-maintainer baseline for the step and the menu; the screen/notice flow. |
| `internal/audit/audit_test.go` | The audit itself, offline (an `httptest` AUR, `Config.Foreign` instead of pacman): severity classification (adoption/maintainer change/added co-maintainer = high; removed co-maintainer, orphaned, recent push, out of date, 0 votes, not-on-AUR = glance); `GateReason` naming maintainer changes and co-maintainers (sanitized, popup-sized) and gating a corrupt snapshot; local `-debug` companions skipped, high first; chunked POST lookups and RPC error handling, an 8 MiB response cap, results for names that weren't requested dropped; `sanitize.Field` at `FieldMax`; `Run` never saves, a first audit is a baseline and a takeover after a commit is high, in `Text` and `LogSummary`; `Commit` with `holdHigh` keeps the high findings' old entries, accepting saves them and logs `ACCEPT`, a dry run has no commit, a failed save is logged `WARN`; no foreign packages is `ErrNoForeign`, a failing lister or a missing setting fails the audit without an RPC; `ShortErr`. |
| `internal/audit/snapshot_test.go` | The maintainer snapshot: round-trip with co-maintainers sorted and deduplicated, entries kept for packages the AUR didn't report; an old-format file (maintainer strings only, or objects without `comaintainers`) loads with no co-maintainer record; every other shape, and an unreadable file, is `BaselineCorrupt`, never missing; written atomically at 0600 with no temp file left; a held lock skips the save (`ErrSnapshotLocked`); a corrupt snapshot fails closed, survives the holding save, and is kept aside as `.corrupt-<time>` by the accepting one; an old-format snapshot is a co-maintainer baseline until the commit records them, then an added one gates. |
| `internal/audit/cancel_test.go` | The one cancellable deadline, against a fake AUR that never answers: a cancel ends the RPC within 100 ms with no goroutine left; the deadline spans every chunk, not each request, and the foreign query too (a list cut short is the deadline, not `ErrNoForeign`). The prefetch: its result is `Run`'s and its commit writes the configured snapshot; `Cancel` and an aborted `Wait` each end a hanging lookup within 100 ms with `context.Canceled` and no goroutine left. |
| `fullcleanup_audit_test.go` | Full Cleanup's audit step: it's first, in-process, `AlwaysLog`, category `audit`; both topgrade steps are `AlwaysLog`. Run through the TUI on the engine (`startQueue`, `startAudit`): a maintainer change gates topgrade with a confirm naming the package(s), defaulting to No, before topgrade starts; declining skips only the update and logs `SKIP: … (declined)`; the report is in the output pane and `audit.log`. No foreign packages is a clean pass; an unreachable AUR fails the step without gating. In a dry run it still audits, logs exactly once, and leaves the snapshot alone, even when the gate is accepted. The step itself never saves the snapshot: with nothing high the engine commits it at once; behind a gate only "yes" saves it (logged `ACCEPT`), while "no" or a gate with no step after it leaves it, so the next run gates again. |
| `topgrade_audit_test.go` | Run topgrade's queue is [audit, topgrade], built from the same `auditStep`, and gated identically, run through the TUI on the engine (`startQueue`): a maintainer change puts a named confirm defaulting to No in front of topgrade; "no" skips the update (logged) and leaves the snapshot, so the next run gates again; "yes" runs it and saves the snapshot (logged `ACCEPT`); an unreachable AUR fails the audit without gating; a dry run gates but never saves. |
| `askpass_test.go` | The TUI's askpass glue: `startAskpassServer` under `XDG_RUNTIME_DIR` wires `askpassWrapper` (sudo steps get `-A`) without touching archmaid's own `SUDO_ASKPASS`, `stopAskpassServer` removes it and unwires; no `XDG_RUNTIME_DIR` leaves askpass unwired with no `/tmp` fallback; a real step child sees `SUDO_ASKPASS` only for a `NeedsPriv` step. Answering, refusing and verifying a request are the Driver's and the Session's (`internal/driver`, `internal/engine`, `internal/privilege`); the popup is `adapter_test.go`'s. |
| `internal/privilege/*_test.go` | The package itself, against `su`/`sudo` shims (`shimAuth`), real unix sockets in `t.TempDir()` and a fake process table. `Sudo`: `Authenticate` runs `su -s /usr/bin/true <user>` with the password on stdin and never reaches sudo on a wrong one, then `sudo -S -v`; lockout notices from su or sudo come back verbatim; a cancelled ctx fails; `Probe`/`Refresh` are `sudo -n -v`; `Command` gives sudo `-A` only with a wrapper and needsPriv, else `-n`, leaves other argv alone and keeps the step's display; `Env` strips an inherited `SUDO_ASKPASS` and adds the wrapper only for privileged steps. Askpass: `RunClient` prints the answer and exits 0, exits 1 on an unanswered close or no socket; `Listen` keeps its socket and 0700 wrapper in a private 0700 directory under the runtime dir, never sets `SUDO_ASKPASS`, delivers prompts, and `Close` (twice, or on nil) removes the directory; no runtime dir is `ErrNoRuntimeDir` with no `/tmp` fallback, and a failed `Listen` leaves nothing behind; the wrapper's shell quoting survives quotes and `$(…)` in paths; a client that never sends its prompt doesn't block the next one and is hung up on at the read deadline, an oversized prompt or an unidentifiable peer is dropped, an undelivered request is hung up on at `Close`; the server reports the real `SO_PEERCRED` peer; the real wrapper end to end. `Request.Answer` writes the password only after su accepts it, never on a wrong one (connection left open), refuses a line break before su; `Cancel` hangs up unanswered. `Verify`, against a fake process table: the step's own sudo (via yay, or a direct `sudo -A` step) is accepted and its caller named; refused are a wrong uid, a gone or reused helper pid, a helper that isn't archmaid, a parent that isn't sudo (a user process named sudo, a root process with another name, a readable exe elsewhere), a chain that misses the step, breaks, loops or runs past `maxChain`, and a reused step or ancestor pid; outside tests `Verify` uses the real uid, binary and `/proc`, and `seam.SetSystem` swaps them only until restored. |
| `downloads_test.go` | The downloads glue: the notice names the folder, `--downloads` parsing, a whole run through the TUI (discovery → named confirm → "y" → summary, the deletion in `cleanup.log` once), and the delete step's own records on the ctx causes the Driver uses: an abort (plain `context.Canceled`) mid-deletion or after the run writes one `ABORT` entry naming what went, a taken result (`steps.ErrResultTaken`) writes none, and the record goes to the store captured when the step was set up even while `logDir` keeps moving (race clean under `-race`). The glue on its own is `internal/frontend`; the rest is `internal/downloads/downloads_test.go`. |
| `internal/frontend/*_test.go` | The glue both front ends share, on its own (the GUI's use of it): `AuditConfig`'s defaults and its files in the store's root, `ForeignPackages` parsing a shimmed `pacman -Qqm` (a failing one reads as none), `MenuAudit`'s notices (nothing to audit, an unreachable AUR, a stopped audit that writes no snapshot) and its report (drawn by `render` or plain, `Accept` only for a high finding, cleared once accepted), `AuditStep` (its fields, the gate and commit, the notices as output), `Prefetcher` (only the audit, config read once, one AUR request) and a cancelled `AuditFetch`; the delete step's records on the Driver's ctx causes (committed: none; abort mid-deletion or after the run: one `ABORT` entry naming what went; `ErrResultTaken`: none; dry run and an unendable ctx arm nothing), `StepAborted`, `DownloadsOptions`, and `Glue.Env` (keeps the front end's fields, the menu audit's config read once and the step's per queue, base `Downloads` wins). Under `testenv.Run`. |
| `internal/downloads/downloads_test.go` | Which folder is cleaned: the order flag → `ARCHMAID_DOWNLOADS` → `XDG_DOWNLOAD_DIR` → `~/Downloads` → `~/Download`; `$HOME` and the passwd home refused however spelled (`"$HOME/"`, a symlink to it, `$HOME` behind a symlink), as are `/` and their ancestors; only the flag may leave `$HOME`, and a bad flag is an error, not a fall-through. What gets deleted (real ctimes, the clock moved via `Options.Now`): old files, folders and symlinks go, a symlink as the link; new files, a back-dated mtime and a folder with anything new deep inside stay; the folder itself is never a candidate; an entry changed or replaced between scan and delete is kept; a swapped folder deletes nothing; dry run only reports; the confirm lists ten names then "…and N more", unsafe names quoted. Never another filesystem: other devices, bind mounts from a fake mountinfo, a mount appearing after the scan, `removeTree` refusing to enter one, per-node re-checks, partial-delete reporting, hard links, and `TestDownloadsMountPoints`, which mounts for real under `unshare -rm` (skipped without unprivileged user namespaces). |
| `audit_cancel_test.go` | Aborting the audit step on the real Driver (`newDriver`) while the AUR sits on its request: the action finishes aborted within 100 ms, and once the Driver is closed no goroutine (connection, handler, pacman) is left behind. The deadline itself is `internal/audit/cancel_test.go`'s. |
| `audit_race_test.go` | Every audit setting is read on the model's goroutine (`currentAuditConfig`): a menu audit, a prefetch and an audit step run while the test rewrites `logDir`, `aurRPCURL`, `auditDeadline` and `dryRun`, and still finish against what they captured (race clean under `-race`); a commit run after `logDir` moved writes the snapshot the audit read and logs beside it. |
| `audit_prefetch_test.go` | A whole Full Cleanup through the TUI asks the AUR exactly once: the audit step uses the prefetched lookup, and the finished run drops it. When the engine starts and drops the prefetch is `internal/engine`'s; carrying it out `internal/driver`'s. Home of `reapAuditFetch` (ground rule 7). |
| `transcript_*_test.go` + `testdata/transcripts/` | Whole actions end to end against shims, through the TUI on a `syncDriver` (`TestTranscript`, `tuiEngineTarget`; `-update-transcripts` records through it), through the `syncDriver` alone (`TestTranscriptDriver`, `driverTarget`), through the real Driver wired as main wires it (`TestTranscriptRealDriver`, `tuiDriverDeps` = `driverDeps`) and through the engine's Session (`TestTranscriptSession`, `sessionTarget`): the same goldens byte for byte, every Session run also held to `enginetest.CheckContract` and its last-run record checked; a real downloads delete through the Session is logged once, with no ABORT entry, because the target releases the step's ctx with `steps.ErrResultTaken` — see Transcripts above and the scenario names. `syncdriver_test.go` is the tests' pull-mode Driver. |
| `internal/testtranscript/recorder_test.go` | The transcript format itself, target-free: a hand-built event sequence through a fake target gives every line kind (plan, inserted step, flag change, normalised paths, snapshot with and without co-maintainers, prompts and their replies, abort, note, report, end); an unscripted or mismatched prompt, a bad confirm reply, an unused answer, a step outside the plan and a target error each fail with the transcript so far. |
| `internal/engine/engine_test.go` | The frozen API (E0), checked by the compiler: every `Session` method's and `Driver`'s signature as a typed func value, so a changed signature breaks the build. The closed sets: the Event, Effect, Input and Command types listed in the test are exactly the ones the source marks (a `go/parser` scan), so a new type is picked up by the checks. No Event can carry a password: a reflection walk over every Event type, nested structs, slices, maps and pointers included, fails on a `Secret`, on a text field named like a password (`pw`, `passw…`, `secret`, `token`, `cred…`), and on any func, channel or interface field (events are plain data); a self-test proves the walk catches each of those and passes look-alikes (`Prompt`, `Upward`, `Bypass`). `Secret` prints `<redacted>` under every fmt verb and flag, nested in Commands and Effects, and through `encoding/json`. `Outcome.String` gives the transcript words. |
| `internal/engine/session_test.go`, `session_plan_test.go` | `Options.Validate` (the required `actions.Env` fields, a known `AuthFlow`). The Session (E1/E2) with the Driver played by hand (outside the package, a menu seam in `export_test.go`; a fake clock), every run held to `enginetest.CheckContract`. Start ignores an unknown action, a front end's own entry and a second action; a queue runs in order: `QueueStarted` (the plan as `PlanStep`s), `StepStarted` with its `StartStep`, `OutputChunk` per `StepOutput` (an OSC 52 write stripped, SGR kept), `StepDone` with its elapsed time, the next step; `Finished` with errs and elapsed, a `WriteLastRun` record, then idle. Stale inputs (an old or zero Seq, a stray probe answer or discovery) change nothing. Logging: FAIL always, OK only for `AlwaysLog`, DRYRUN in a dry run, the body the whole output. Step confirms: other IDs, a selection or a password for a confirm are ignored; optional "no" skips (SKIP logged for `AlwaysLog`), required "no" or cancel skips the rest and aborts (recorded cancelled when nothing ran, aborted otherwise); "yes" runs `OnAccept` as a `RunHook` before `StartStep`. Auth: a sudo queue is probed (cached → the gate → `QueueStarted` and the keep-alive; declined gate → cancelled, prefetch dropped, no plan; uncached → `NeedsPassword`, cancellable), a dry run and polkit never probe, a queue needing no root gets no gate; the consumed prefetch is dropped once. `NotGranted` is `AuthRefused` with a SKIP entry, counted in `Finished.Refused`, and the completed run is recorded `incomplete` with `steps_refused`. A discovery: `Discover` with `Options.Env` (DryRun copied in), its `Notice` plain and with no last-run record, its `Pending` run as a queue. Ticks, askpass hang-up, hyprpm kept last, and what isn't built yet panics (`Abort`, `SubmitPassword`, report/selection/root discoveries). Plan changes: `InsertNext` lands behind its step (events in order, the orphan confirm with `Direct`/`Cascade` quoted where unsafe), nothing from a failed result; `ConfirmNext` gates the next step (`PlanChanged`, `ConfirmAuditGate`, the commit only on "yes"), is dropped on the last step; `Commit` without it runs at once, even for a failed step; a result setting both is refused failing closed (step failed with the refusal in its output, nothing inserted, no commit, the gate still on the next step, with `Commits` false); no root step can be inserted after hyprpm; the downloads confirm lists its items. |
| `internal/engine/stub/stub_test.go` | The stub Driver, against a `FakeClock`: the hand-built `FullCleanup` script replays in order, event for event, with its three confirms answered, the clock lands on start + the script's waits, and the sequence obeys the engine's ordering contract (`enginetest.CheckContract`, shared with the Session's tests and `sessionTarget`: nothing names a step before `QueueStarted`, indexes match the plan as moved by `StepInserted`, one step at a time, output only for the running step, each step ends once and never as `StepDone{Aborted}`, a step's confirm names a step that hasn't run, `StepInserted` and `PlanChanged` only between steps — never while a step runs or its confirm is open — and never at a step that ran, prompt IDs unique, `Finished` last (only `PhaseChanged` and `StatusUpdated` after it) and every step settled; `enginetest/contract_test.go` checks the checker on hand-built breaches). Commands before `Start`, a `Start` mid-play, and answers to prompts that aren't open are ignored; each answer kind (confirm, cancel, password, selection, report) closes its prompt; the next `Start` replays from the top. `Abort` at a mid-step askpass prompt closes it, aborts the step with its elapsed time and finishes `aborted`; with no step started it finishes `cancelled`; during a cue's wait it aborts at once. `Send` never blocks with nobody reading (it returns false once the queue is full); `Close` returns whether the replay is idle or blocked publishing, is idempotent, closes `Events`, and `Send` after it is false. |
| `internal/proc/*_test.go` | Child processes, against real ones. `Start`: lines in order (stdout and stderr through one pipe), then exactly one done; a failure keeps its `*exec.ExitError`; a missing command is a nil `Child` and a done with the error; the `Child` carries the start time `/proc` gives and the child gets exactly `Spec.Env`, and with a nil `Env` never an inherited `SUDO_ASKPASS` (sudo, a sudo behind `env` or `sh -c`, a plain command, `Query`); a dry run starts nothing (a marker file proves it) and reports one `[dry-run] would run:` line; a child that leaves a background process holding its pipe is done within `WaitDelay`, with every line, its own exit status (a clean exit is a success) and no goroutine left. `Next`: batches what's buffered (at most `max` lines, in order, ending at the done, nothing left behind); released by quit. `Stop`: SIGINT first; a SIGINT-ignoring child is SIGKILLed after the grace; nil-safe; no goroutine left behind for a child blocked on a full channel or holding its pipe through a background process. `Query`: stdout on success and failure, the `ExitError` with stderr and a stderr-led message, ctx cancel kills. `Read` against the real `/proc` (a root process's exe is unreadable to a user). `TestNoSetsid`: a started child shares our session and process group, and a `go/parser` scan of `cmd/` (minus `cmd/archmaid-helper`) and `internal/` finds no `Setsid`/`Setpgid`/`Setctty`. |
| `teatest_test.go` + `testdata/` | Headless-program goldens for the menu and the services multi-select. |
| `golden_test.go` + `testdata/` | Direct `View()` goldens at 80×24, the model fed the engine's Events on a `fakeDriver`: the summary for each outcome (ok, issues, skipped, aborted); the up-front password popup over the menu (masked input, and re-opened with the attempts-left error); a confirm over the running checklist; a mid-run askpass prompt naming its requester; the log picker; the audit report; a notice; the discovering spinner. |
| `adapter_test.go` | The TUI as an engine front end, on a `fakeDriver`: Events → the model (askpass popup over the running step, its retry, lock and withdrawal; abort → the menu with what never ran skipped; a declined required step → the aborted summary; the report, its accept notice and dismissal; prompts, selection and the menu `Start`; the status line), keys → Commands (esc on askpass hangs up; abort confirm: n/esc/ctrl+c/enter-on-No resume and send nothing), the askpass prompt shown quoted and stripped of escapes, `forwardEvents`, and the binary's wiring: `newDriver` builds the real Driver and closing it closes Events, `startPrefetch` starts only the audit, `reportLog` keeps 20 errors and counts the rest. |
| `lastrun_test.go` | last-run.json through whole scenarios on the TUI: three wrong passwords → `incomplete` with every sudo step refused; an aborted Full Cleanup → `aborted`, every step from topgrade on skipped; a declined gate → `cancelled`; nothing before a run. The outcomes themselves are `internal/engine`'s. |
| `cmd/archmaid/e2e/*.tape` + `run.sh` + `shim.sh` | The real binary under `--dryrun`, one tape each, in a throwaway `HOME` with a shim dir first on `PATH` (every external command is a symlink to `shim.sh`, which only logs argv to a file and prints canned output; `run.sh` checks the shadowing first and that no mutating shim ran). `clear-cache`: asserts the `DRYRUN:` entry. `services`: toggles one of two shimmed units; the log names exactly that unit and `systemctl` was only asked to list units and `is-enabled`. `orphans`: Full Cleanup's confirm reads "2 … 3 total" (from the tape's `.txt`) and `orphans.log` has the dry-run removal. `abort`: ctrl+c on the blocked audit step, confirm; back on the menu, nothing logged. `bash cmd/archmaid/e2e/run.sh services abort` runs a subset; generated `cmd/archmaid/e2e/*.txt` is gitignored. |

## Manual verification

Run by hand, as root, before the polkit path ships. Unit tests can't see real
PAM. `archmaid-helper`'s own suite (`go test -race ./cmd/archmaid-helper/...`)
covers validation, argv, hashes, the denylist, locks and env scrubbing with a
fake runner. It can't answer these questions, which decide the GUI's design:

1. How many `pam_faillock` failures does one wrong password in a polkit dialog
   cost against Arch's `deny=3`? sudo costs 2 (see HANDOVER → two-stage auth).
2. Does cancelling the dialog count as a failure?
3. Is `auth_admin_keep` scoped to the requesting process (so one GUI process
   can reuse it, but a fresh shell can't)?
4. Does pkexec 127 pick an action by `exec.argv1`?

**Answered on 2026-10-04** (polkit/pkexec 127, scratch user, full report in
[`polkit-live-test.md`](polkit-live-test.md)):

1. **One** failure per wrong password: pkttyagent doesn't re-prompt, one dialog
   is one attempt. A successful authentication resets the count to 0.
2. **Yes**: a dismissed dialog (EOF or Ctrl-C) adds one failure.
3. **The requesting process**, when it has no login session: a second pkexec
   from the same parent skipped the prompt, a new parent was asked again.
   Still to re-check inside a real session (tty3 or the GUI), where polkit may
   tie it to the session instead.
4. **Yes**, but an action *without* argv1 on the same `exec.path` can win over
   an argv1-specific one. Our policy has none (every action carries its verb as
   argv1, and `TestPolicyMatchesVerbs` fails otherwise) — keep it that way.

Also seen: pkexec exits **127** for every refusal (wrong password, dismissed,
locked) — never 126; a locked account fails even with the right password and
pkttyagent prints "The account is locked due to 3 failed logins".

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
