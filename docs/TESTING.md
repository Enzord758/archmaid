# TESTING

How archmaid is tested, the rules that keep the suite safe and deterministic,
and how to add a test. For *what the app does* see `cmd/archmaid/README.md`; for design
decisions and open work see `HANDOVER.md`.

## Strategy

A bottom-heavy pyramid. Almost everything is a fast unit test against
`model.Update`; above that, whole actions are recorded as transcripts, a set of
rendered-frame goldens pins every screen and popup, and eight short VHS tapes
(plus a `--help`/bad-flag check) drive the real binary end to end.

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
layering check (only `internal/tui` may import charmbracelet, nothing under
`internal/` may import gotk4), then a
coverage run into a temporary profile (removed on exit) and enforces the floors
set at the top of the script: `MIN_TOTAL` for `cmd/archmaid` plus `internal/...` (the TUI is `internal/tui`)
together, without the test-only packages (`testenv`, `testtranscript`, `enginetest`, `privtest`, `helpertest`: they get their own `testonly` line and no floor; a floor keeps at least 0.2 points of margin under the measured figure, so a timing-dependent statement can't flake the gate), and `MIN_ASKPASS` for `internal/privilege/askpass.go` (matched by
that path suffix), which carries the sudo password. Per-file
coverage is computed from the profile itself. When coverage rises, raise the
floor in the same commit (to the figure the gate then measures, rounded down to one decimal); never lower one to make a change pass. The gate also prints the *reachable* figure, covered / (total - exempt), against the exempt list in [`coverage-exempt.md`](coverage-exempt.md); the goal is 100 % of reachable. Around all
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
`BenchmarkStreamView5k`, `BenchmarkStreamView20k`, `BenchmarkSystemStatusCache100` and
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
| `SystemStatusCache100` | 147.3 µs (637 allocs) | 441.9 µs (a fixture cache of 100 packages: the walk costs ~1.3 µs per cached file, so the real `/var/cache/pacman/pkg` measured the machine's cache, 28 µs just cleaned to 170 µs at 104 packages) |
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
- `SystemStatusCache100` allocs/op rose at all (significant; it is deterministic, 637 with its fixture cache);
- HEAD's best `ActivateFullCleanup` sample is over 1 ms or `ViewRunning` over
  600 µs (backstop caps, the guard's fixed limits, for slow drift that no single
  base-to-head comparison shows).

Noise shows as `~` and passes. It takes about 40 s plus the build. Output
(`old.txt`, `new.txt`, `benchstat.txt`/`.csv`) goes to `$BENCH_OUT`, a temp
dir by default; `BENCH_COUNT` changes the sample count. If a benchmark is new on
HEAD it has no base column and is checked only against its cap.

### Coverage from real runs (`make e2e-cover`, `make cover-build`)

The gate's coverage figure counts `go test` only. Every statement still
uncovered is listed and sorted (unit test, e2e, test VM, the owner, unreachable)
in [`coverage-map.md`](coverage-map.md), with the path to 100 % of reachable, the exemption list and the work packages that close the gap. To count runs
outside `go test`, everything below writes raw coverage data (a `GOCOVERDIR`
of `covmeta.*`/`covcounters.*` files) in atomic mode, so any mix of runs merges
into one number:

| Script | Writes covdata from |
|---|---|
| `scripts/cover-unit.sh DIR [PKG...]` | the root module's tests (`go test ./...`, every package under `testenv.Run` as always), through `scripts/cover-exec.sh`, so a test binary's re-executed children count too (plain `go test -cover` drops what they write) |
| `gui/scripts/headless-cover.sh DIR` | the GUI's plain tests and `headless.sh`'s display groups (private headless Broadway display, as `make gui-test`) |
| `E2E_COVER_DIR=DIR bash cmd/archmaid/e2e/run.sh` | the e2e tapes on a cover-built `archmaid` (each run's `GOCOVERDIR` inside its scratch dir, copied to DIR) |
| `scripts/cover-build.sh OUTDIR` (`make cover-build COVER_BIN=…`) | builds `archmaid`, `archmaid-helper`, `archmaid-gui` with `-cover`; each writes to `$GOCOVERDIR` when it exits normally (a SIGKILLed process writes nothing). For the test VM and the owner's measured runs (`coverage-map.md`, "Collecting coverage") |
| `scripts/cover-merge.sh [-o OUT] DIR...` | merges, then prints core (`cmd/archmaid` + `internal/...` without the test-only packages, check.sh's set), testonly, helper, gui and all, raw and reachable (`docs/coverage-exempt.tsv`); OUT holds the merged covdata, `profile.txt`, `summary.txt`, `uncovered.txt` and `coverage.html` |
| `make e2e-cover` (`scripts/cover-report.sh`) | unit + e2e merged; `COVER_GUI=1` adds the GUI; `COVER_OUT=dir` keeps everything |

`go run ./scripts/covblocks [-summary|-json|-src] PROFILE...` lists a
profile's uncovered blocks with their function (the raw material of the map).
Two measuring facts behind the scripts: a re-executed test binary gets
`GOCOVERDIR` but not `-test.gocoverdir`, so `testenv.Run` points the flag at
it (`internal/testenv/cover.go`; coverage only); and code that runs in
`TestMain` after `m.Run` (testenv's own checks) is never counted, because the
testing package writes its counters when `m.Run` returns.

A cover build is never what gets installed: `packaging/arch/PKGBUILD` builds
plain binaries, and `check.sh` fails if it names `-cover`, the
`archmaidcover` tag or `GOCOVERDIR`, or if a normal helper build links
`cmd/archmaid-helper/internal/coverdir` (the cover-only package that lets the
helper write its counters under pkexec). Run a cover binary like any archmaid
binary: under a scratch `HOME`/`XDG_*` on this machine (ground rule 8), with
`GOCOVERDIR` a scratch dir, never under `~/.local/state/archmaid`.

## GUI (`gui/`, its own module)

The GUI is a separate Go module, so nothing above builds it: the root gate,
`go test ./...` at the root and CI's `core` job never compile gotk4 (a cold
gotk4 build takes about 7 minutes). Two layers:

| Layer | Where | Needs |
|---|---|---|
| View-models and pump | `gui/internal/runview`, `overview`, `logview`, plain `go test` (`-race` is fine: they don't build gotk4) | nothing: no display, no GTK |
| The real window | `gui/internal/ui/harness_test.go`, through `gui/scripts/headless.sh` | gtk4 (its Broadway backend), libadwaita, dbus |

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
`go test ./...` in `gui/` never opens a window. `headless.sh` runs them on
GTK's Broadway backend (`GDK_BACKEND=broadway`, part of gtk4) against
`gui/scripts/nullbroadway`, a Broadway display server of our own that shows
nothing: no compositor, no browser, no web server. It listens on one Unix
socket in a fresh 0700 temp runtime dir (`BROADWAY_DISPLAY=:0` is
`broadway1.socket` there; GDK only takes a display number, so the ui
package links the socket into testenv's scratch runtime dir,
`display_test.go`), and `headless.sh` refuses to run if the server holds
any IP socket (checked from its fds against `/proc/net/{tcp,udp}{,6}`).
The script drops `WAYLAND_DISPLAY`, `DISPLAY` and
`HYPRLAND_INSTANCE_SIGNATURE`, and runs the command on a private D-Bus with
no service activation (no portal, at-spi or gvfs starts). The server and the
command run under `timeout` (`HEADLESS_TIMEOUT`, default 60 s; the suite takes
~7.5 min warm, so with no command it runs fourteen parts of at most ~40 s, each with its own display server (the coverage scenarios are one part, the wide dialogs and `TestGUIMinWidthToggle` one `TestGUIWideDialogsLong` another and `TestGUIWideDialogsLongResize` (~41 s with the one before it) a last one, on a 1600x1000 screen: `HEADLESS_WIDTH`, `HEADLESS_HEIGHT`); CI sets 600), so nothing outlives the run, and its cleanup trap removes the runtime dir whatever failed (`TestHeadlessServerFails`).

Why not `gtk4-broadwayd`, GTK's own server: with no browser connected it
answers each frame's roundtrip locally, and GDK then draws one frame a
second (`_gdk_broadway_roundtrip_notify` rate-limits a local reply), which
starves every scenario that waits for frames; with a browser (or a
stand-in that answers like one) connected, gtk 4.24's broadwayd aborts on
the app's frames (`broadway-output.c` `append_node_ops`: `node ==
reused_node` with the cairo renderer, `!reused_node->consumed` with
Broadway's own). `nullbroadway` keeps broadwayd's no-browser behaviour
(surfaces, configure notifies, focus, a modal surface focused on configure,
pointer grabs, the screen size sent on connect), answers every roundtrip as
a browser does, and drops render nodes and textures (closing each
texture's fd): `scripts/nullbroadway/server_test.go` checks the replies,
the event layout GDK reads, and that an unknown request (another gtk's
protocol) ends it with an error. Two things the backend change brought
into the harness: the child opens the display and handles its first events
before the app builds its window (`settleDisplay`: broadway's screen size
is an event, and a window presented before it is clamped to 1024x768),
and the checks that fail on any GTK warning let GDK Broadway's own
once-per-process "Duplicate call to gdk_frame_timings_submitted() with
different values" through on Broadway only (`broadwayNoise`). Screenshots
(`shot.go`) render with a cairo renderer of their own, so they look the
same on any backend. Scenarios:
Actions start from their Overview row against the stub's `Catalog`
(the page must switch to Run). `fullcleanup` (the stub run end to end, each
confirm answered through its dialog, its buttons labelled by `YesLabel` and
"Skip"/"Cancel"), `usercache-no` (a required step's confirm declined: the
step skipped, outcome aborted), `repair` (the menu as shown, a discovery
ending in a notice — shown as the toast and on the status line —, the
empty last run), `logs` (a scratch state dir with a
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
drop under `ARCHMAID_GUI_PERF=1`, a capacity budget; always: dropped + in view never exceeds the lines seen; the GUI process, not nullbroadway, is where the frame work goes), `flood-default` (unpaced, default pump), `flood-colour` (as `flood`,
every line in colour with seven SGR spans: bold, the 16 colours, 256-colour
and 24-bit; it also checks that every visible output row carries its Pango
attributes). Every scenario also checks
that `Close` was called on the Driver and that no goroutine is left after
`Run` returns (goleak, against the goroutines from before the app started).

The `cov-*` scenarios (`cover_harness_test.go`, `cover_harness2_test.go`; their
`TestGUICov*` tests are in `cover_scenarios_test.go`) are the coverage work's:
small children that drive one area through its real widgets and signals and
assert the outcome inside the child (`covEnv.ok`; a failed check is in the
report's `Errors`, which `child` turns into a test failure). They start 200 ms
after the first frame, feed events through `Window.apply` (the pump's own
entry), and close the window: dialogs (a question without a title, Enter in
the password entry, a withdrawn prompt, a selection cancelled, the window
closing under an open dialog), the quit question and a signal's quit, the
default Launcher and an engine that never takes a command, the sidebar
overlay's key and click controllers, the timeline (a click on a step's
header, the follow-the-bottom controllers, lines outside any step, a failed
step's actions), the Logs page (row, back, category toggle, search, section
activate, Open in Logs on another run, a run with no logs, a log removed or
unreadable since it was listed, an unreadable state dir), the Settings page
(unreadable and unwritable settings, a topgrade config that can't be made or
opened, a folder typed with spaces, no HOME) and a screenshot that can't be
taken (`TestGUIShots`, built with `-tags shots`). `cover_plain_test.go` and
`cover_plain2_test.go` are their plain-Go half (no display): a `Window`
built by hand with only the fields the code touches, the timeline's sections
over a bare `gio.ListStore`, `runFolder`, the status line, the user-cache
re-measure and a signal before the window exists.

The `real-*` scenarios (`TestGUIRealDriver`, its own part of the default
`headless.sh` run) drive the **real Driver** through `gui/internal/realdriver`
on the polkit path. Every privileged binary is a shim first on PATH, ahead
of testenv's traps: `pkexec` checks it got `--disable-internal-agent` then
the helper's install path (else it exits 126) and speaks the helper's JSON lines (start, a line, check-files' check event,
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
`real-refused` (Full Cleanup's one polkit dialog, the root steps'
`full-cleanup` call, refused after "Waiting for authorisation…" with the
last-attempt warning: every root row reads "Not authorised", the run goes
on with `yay -Sc`, and pkexec gets exactly one call, `full-cleanup`),
`real-locked` (a locked account: no pkexec at all; the first root row
reads "Not authorised · account locked until …", the rest "Not
authorised"), `real-askpass` and
`real-askpass-cancel` (the right password reaches the sudo shim; a cancel
doesn't, topgrade fails), `real-close` (the window closed mid-topgrade: the
topgrade shim's pid is gone and goleak is clean), `real-failed-decline`
(a Full Cleanup whose topgrade fails, then its Run topgrade again with
the gate declined: the failed run stays on the page, tagged "previous
run", and the status line says "Run cancelled, nothing ran", not the
failed run's end and line count; `TestStatusTextGateDeclined` is the
plain-Go half), `real-runs` (one window,
`KeepRuns`+1 real Run topgrade runs then a dry one, from the Overview row
each time, in a scratch state dir with a clock stepping a minute per
Driver: a fresh Driver per Start, one run directory per real run, the
oldest rotated out, the dry run in `dryrun/`, `last-run.json` in the
root), `real-colour` and `real-colour-fc` (`TestGUIRunColour`,
`colour_harness_test.go`, in the real Driver's part of `headless.sh`:
topgrade and yay shims that colour only when
their switch asks; at Run topgrade's end topgrade's own lines are drawn
with a Pango foreground and bold and the pacman lines plain, at Full
Cleanup's end yay ran with `--color always` and its `::` is drawn in a
colour; the step's pty is as wide as the Run view (`ui.TermCols`, 72
columns for 720 px) and the output's font draws block and box glyphs,
braille's count reported (#129);
`real-colour-end-1.png`, `real-colour-fc-end-1.png` with
`ARCHMAID_GUI_SHOTS`), and `real-services`, `real-services-none`, `real-services-error`
(`TestGUIRealServices`: Disable unused services from its Overview cell,
with a `systemctl` shim first on PATH: the fixture machine's
enabled-but-inactive units as the pick list shown, the two ticked disabled
by one `disable-unit` helper call; nothing found, and a `list-units` that
fails, each said in the toast and on the status line, nothing run). The plain-Go `gui/internal/realdriver` tests cover the relay, the Run
page's colour (`TestRunPageColour`: Run topgrade's Events through
`runview.Model`, its line a bold green span and Pango attributes, none
under `NO_COLOR`; `TestRunPageHelperColour`: a Full Cleanup whose
pkexec shim speaks the root helper's stream with a pacman line in SGR
colour, as the helper relays `--color always` output: the orphan row's
line coloured, no escape in any log or `last-run.json`, no span under
`NO_COLOR`), the pty (#129, `TestRunPagePty`: topgrade's child has
a terminal as stdout and stderr, none as stdin, no `/dev/tty`, `tty_nr` 0,
`TERM=xterm-256color` and `Config.TermCols` columns; its spinner, `\r`
bar and cursor-up rewrite end as their final lines on the page and in
`topgrade.log`),
`Options.Validate`, a dry Full Cleanup on the real Driver without a
display, the TUI's sudo wrapper kept out of the GUI
(`TestGUIGetsNoSudoWrapper`: its askpass Server writes none, and
topgrade gets the askpass with archmaid-gui's own `PATH`), the per-run directories and rotation (`TestRunDirs`, a topgrade
shim first on PATH), and the audit prefetch with a blocking fake
`Foreign` (no pacman, no AUR): it starts while the gate is up and the step
consumes it (one lookup), and a declined gate or `Close` cancels and
reaps it (`TestAuditPrefetch*`). `TestServicesPolkitCancel` runs Disable unused
services on the real Driver with archmaid's agent (the pkttyagent shim,
`agenttest.Shim`) and `pkexec`, `su` and `systemctl` shims: a Cancel in
archmaid's own polkit dialog ends cancelled with nothing refused in
`Finished`, `last-run.json`, the Overview's summary and the Run page's
text, while three wrong passwords and a cancel at the desktop's dialog stay
"not authorised".

`TestGUIMinWidth` (`minwidth_harness_test.go`, scenario `real-narrow`)
runs the real Driver's content at 360, 480 and 960 px wide (720 high):
the Overview with the real Full Cleanup plan (also scrolled to its
buttons and to its end, for the screenshots), its gate declined (and that
toast), Dry run first through to its end (the Run page with the plan's
rows and root tags, and the orphans dialog with the test VM's real plan:
15 orphans, 39 packages with the cascade, their real names and versions,
the dry run's banner; it must list the orphans once, "Removed with them
(24)"), the longest toasts, then the Logs and Settings pages.
On the Overview and the finished Run page it also checks where the tags
sit (`checkTags`, `narrowtags_harness_test.go`): under 760 px every plan
row's, step header's and action cell's tags on their own line under the
title, starting at the title's x; above it at the line's end, the same
distance from it on every row; no tag drawn taller than its own height;
the hero's Run and Dry run first stacked at its full width when narrow
(nothing under them but its padding), side by side when wide. The gate's
rail gets the same check by the dialog's own breakpoint
(`checkRailTags`: under the titles when its sheet is under 640 px, at
the line's end above), in `TestGUIMinWidth`, `TestGUIMinWidthStates` and
`TestGUIWideDialogs` (at least the real plan's 7 tagged steps).
`TestGUIMinWidthToggle` (`narrowtoggle_harness_test.go`, scenario
`narrow-toggle`) crosses the breakpoint both ways (960, 500, 960, 500 px, the
window resized live) with Run pages of 6, 12 and 18 steps started at each
width, so the step headers made while narrow (only rows beyond the most any
earlier run made are new widgets; the list view recycles the rest) come out
under their titles, and `checkTags` runs after each move; the Overview's
kicker follows too. `polkitagent/holders_real_test.go` runs the terminal-holder
check on the real `/proc`: a child holding the pty is found, a zombie and a
reaped child hold nothing.
`ARCHMAID_GUI_NARROW_WIDTHS=380,960` runs both min-width tests at other
widths, for screenshots (`ARCHMAID_GUI_SHOTS`).
At each moment the window's content (the toast overlay) must measure no
wider than the window, no mapped widget (a dialog's, a breakpoint bin's
content) may either, and the child's stderr must hold no GLib warning
(but the one it provokes on purpose, so a lost stderr can't pass it). A
failure names the widest widgets (`tooWide`); each dialog's line gives its
sheet's minimum (`dialog=`, libadwaita's "AdwFloatingSheet … exceeds
AdwBreakpointBin width" when over). Libadwaita lets a window
shrink to 360 px whatever its content asks; content wider than that is
cut off on the right and logged as "AdwToastOverlay … exceeds
AdwApplicationWindow width". Stub content can't stand in: the stub has no
plan, so the hero has no kicker or step list.

`TestGUIMinWidthStates` (`minwidth_states_harness_test.go`, scenario
`narrow-states`, 360 and 480 px, its own part of `headless.sh`) measures the
states that carry long text, fed to the window as events: a failed run with a
long error, a 90-letter word and a long path in step titles, rows and
output, a refused row ("no polkit agent is running..."; alone too, as the
VM showed it, its root tag beside a 3-line state line), a waiting and a
locked-out row (the run states' tags checked as in `TestGUIMinWidth`), a
long busy line and notice, a discovery result (nothing
found, failed) with long unit names, every dialog (the gate with its plan,
the step, orphans, downloads and audit-gate confirms, the downloads confirm
as a real scan's plan builds it with a Downloads folder's names, each
shown once, as its body, the TUI's text, drops the names its list shows
(`runview.BodyWithoutItems`), the three
password dialogs, the services pick, the audit report, the quit question;
the gate and the real downloads confirm with the dry run's banner, the
passwords with the kept-password note, each checked shown), Settings
with long values and errors, the last-run cell with a long issue and with
a title over two lines beside its chip (the chip keeps its own height), and the Logs
page (every category, long lines and the unreadable row). A new state that
carries user or system text (a path, a package or unit name, an error)
belongs there. The usual cause of a failure is a wrapping label whose wrap
mode is `WORD`: a word longer than the window is its minimum width, so
labels that can hold one use `WrapWordChar`.

`TestGUIWideDialogs` (`widedialogs_harness_test.go`, scenario
`real-wide`, its own part of `headless.sh` on a 1600x1000 headless screen,
since the screen bounds the window) opens every decision dialog at
1460x930 and at 380x720: the real Driver's Full Cleanup gate (declined),
then the orphans confirm with the VM's plan, the real downloads confirm,
a step confirm, the audit gate, the three password dialogs, the services
pick and the audit report, each fed as the engine sends it. It measures
each dialog's sheet (`dialogGeometry`: size, whether the gate's steps and
notes are side by side, how many scrolled windows have something to
scroll, the yes button in the window, nothing too wide). At 1460 the gate
must be at least 800 px wide, shorter than before (372x567 on d3b2b94),
side by side, nothing scrolling, its note's root count the rail's `root`
tags (`gateRootCounts`: topgrade is tagged `sudo`), the orphans' lists
with no line broken (`wrappedLists`, the VM's cascade); every other dialog wider than before
(`*WBefore`), the passwords wider than 372 px. At 380 everything fits
with its buttons in view and the gate stacked. With `ARCHMAID_GUI_SHOTS`
it writes `real-wide-<width>-<dialog>.png`. `TestGUICovDialogClose`
(`cov-dialog-close`) closes a wide confirm, pick list and report as
Escape does (`adw_dialog_close`) and a confirm under the window's close:
each sends its close response exactly once, and a clicked button's
dialog responds once. `TestGUICovDialogEdges` (`cov-dialog-edges`,
`dialogedges_harness_test.go`) holds the contract's edges: Escape twice
(in one main-loop turn and across two), a click while the dialog is
closing (its `closed` comes at the start of the close animation; a real
pointer no longer reaches the button then, the test clicks in code) and
a click then Escape, a confirm, pick list and report withdrawn while
open, a confirm withdrawn while closing and one withdrawn before it was
drawn (nothing sent, no dialog left on screen), the password
AlertDialog's order (closed, then the response; the typed password sent
whole, the entry wiped) for a click, Escape twice then a click, Enter and
a withdrawal, the window's close with a wide confirm open idle and
mid-run (the dialog closes with its close response, the window stays,
the next close asks), and a confirm arriving over the quit question
(Escape answers the confirm; Stop and Quit under it sends nothing for
it). For the wide dialogs it checks the dialog's own responses too, since
`Window.answer` drops a second answer for a forgotten ID. A mutant
without the once-guard fails it (no then yes), one without the `closed`
bridge too. `TestGUIWideDialogsLong` (`widelong_harness_test.go`,
scenario `wide-long`, its own part on the 1600x1000 output) opens a
20-step gate, 300 orphans with a 500-line cascade, 200 long file names, a
step confirm naming a long path, 80 services, a 40-finding report and an
askpass named by a long path at 1460x930, 1460x500, 620x500 and 380x500:
each sheet inside the window, its heading and buttons drawn inside it
and (a wide dialog's) in no scrolled window, something in its middle
scrolling in the short windows and only the middle (no list scrolling
inside it: a capped list shrank there instead, task #101), the 20-step
rail whole at 1460x930 and at least 6 of its steps shown in each low
window, the orphans' lines unbroken at 1460x930, nothing too wide, no GTK warning
(`wide-long-<w>x<h>-<dialog>.png` with `ARCHMAID_GUI_SHOTS`).
`TestGUIWideDialogsLongResize` (`widelongresize_harness_test.go`, scenario
`wide-resize`, a part of its own) leaves the orphans, downloads and report
dialogs open while the window goes 1460x930, 1460x500, 1460x930 and so
on, four times over: in each tall stage every list is capped again (max
content height and automatic policy back, scrolling by itself, the middle
not), in each low one every list is uncapped and only the middle scrolls.
A fitHeight without the re-cap on a new window size fails it at stage 2.
Plain Go: `TestStatusTextEndsBeforeQueue` and `TestStatusTextNoPolkitAgent`
(`statusbefore_test.go`: a cancelled up-front password, a discovery with
nothing to say, a notice, each dry or not, after a run: the status line
says how that action ended, no "Finished" and no lines of the run before;
`TestStatusTextWhileDiscovering` is skipped, a known bug: the previous
run's "Finished: completed" shows until the queue starts) and
`TestSudoStepIsOnlyTopgrade` (`overview/privtag_test.go`).
`TestGUISmallWindow` accepts a heading in no
scrolled window at all (`scrolled=-1`: the wide orphans dialog's heading
is in its top bar).

The stub is only ever `--stub`: `TestParseLaunch` (plain Go) checks that
`ARCHMAID_GUI_STUB` and any `ARCHMAID_GUI_SCRIPT` leave a launch without it
on the real Driver and that an unknown script is refused; the stub
scenarios check the banner, "(stub)" title and STUB status line, and
`real-dry` their absence. `TestGUIStubOwnInstance` runs unique instances
(flags 0) under a test-only app id on `headless.sh`'s private session bus:
with a real window up, a stub launch gets its own window, while a second
real launch hands its activation over.

```sh
cd gui
go test ./...                                     # runview; the display tests skip
scripts/headless.sh                               # + the display tests, headless
ARCHMAID_GUI_PERF=1 scripts/headless.sh           # + enforce the budgets below
GSK_RENDERER=broadway scripts/headless.sh         # Broadway's own renderer (default: cairo)
```

Broadway has no GL or Vulkan, so `GSK_RENDERER=vulkan` or `ngl` measures
nothing here; the Vulkan column below was measured on the headless weston
the suite used to run on.

With `ARCHMAID_GUI_PERF=1` the tests fail over budget: warm first frame
< 400 ms (the best of the runs after the first, measured from exec to the
frame clock's first `after-paint`), idle RSS < 125 MB, ≥ 5k lines/s for the
unpaced flood, main-thread work per flush p95 ≤ 4 ms (the time in the pump's
`apply`: model, list splice, queueing the scroll), RSS after 50k lines < 200 MB. They are
always measured and logged with `-v`. CI runs them without the variable.

### GUI measurements

Dev machine (Ryzen 7 7800X3D, RTX 4090 on driver 615.71, gtk4 4.22.5,
libadwaita 1.9.4, go1.27.1), headless weston, warm:

| Measure | Budget | `GSK_RENDERER=vulkan` (GTK's own default) | cairo (archmaid-gui's default) |
|---|---|---|---|
| First frame, warm | < 400 ms | 376–396 ms (one run 446) | 85–95 ms |
| Idle RSS (PSS, anon) | < 125 MB (120 until P8 row 13) | 280–290 MB (181–192, 92–103) | 110–122 MB (45–56, 24–36) |
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

**The table is weston's; Broadway measures its own transport.** Since #162
the suite runs on Broadway (no weston on the dev machine), where GDK
encodes every texture it uploads as a PNG on the main thread: with cairo
that is the whole window every frame. Same machine, gtk4 4.24.1, warm,
one run each (`TestGUIFirstFrameAndIdleRAM`, `TestGUIThroughput`):

| Measure | Budget | Broadway, cairo (the default) | Broadway, `GSK_RENDERER=broadway` |
|---|---|---|---|
| First frame, warm | < 400 ms | 140 ms | 131 ms |
| Idle RSS (PSS, anon) | < 125 MB | 132 MB (74, 43) | 125 MB (68, 36) |
| 50k unpaced, nothing dropped | ≥ 5k lines/s | ~8.5k lines/s | ~9.9k lines/s |
| Main-thread work per flush, p95 (max) | ≤ 4 ms | 0.60 ms (7.9) | 0.56 ms (6.8) |
| 10k lines/s paced: p95 (max), dropped | ≤ 4 ms | 0.53 ms (4.4), 0 | 0.48 ms (4.4), 0 |
| RSS after 50k lines | < 200 MB | 449 MB | 166 MB |
| Frame work, unpaced p95 | — | 62 ms | 54 ms |

The flush work, which the budgets are about, is the app's and matches;
the rate, the frame work and the RSS are Broadway's PNG uploads, so with
`ARCHMAID_GUI_PERF=1` the RSS budgets fail on Broadway with cairo. Measure
budgets against a real compositor (the owner's desktop, by hand) until a
decision on where the GUI's perf baseline lives.

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
| `make perf-baseline` | `scripts/perf-baseline.sh`: re-measures the baselines (TUI/sysstat/sanitize/runview benchmarks, `TestPerfGuard`, the GUI's first frame, idle RSS and throughput headless, stripped binary sizes) into `$PERF_OUT`'s `summary.txt`. It refuses (exit 3) unless the machine is quiet: load ≤ `PERF_MAX_LOAD` (an eighth of the CPUs), no `qemu-system`, no other gate or test run. `PERF_COUNT=1 PERF_FORCE=1` is a smoke test marked NOT A BASELINE. The Repair scan, services discovery and the installed GUI's idle CPU need the real system: measured by hand. |
| `make gui` | in `gui/`: build, vet, gofmt, `runview` tests under `-race`, `go test ./...` (display tests skip) |
| `make gui-test` | `gui`, then the display tests under `gui/scripts/headless.sh` (gtk4's Broadway backend on `nullbroadway`; no compositor) |
| `make e2e-cover` | `scripts/cover-report.sh`: the unit tests and the e2e tapes writing covdata, merged into one number (`COVER_GUI=1` adds the GUI's tests) — see "Coverage from real runs" |
| `make cover-build` | `scripts/cover-build.sh`: the three binaries built with `-cover` into `COVER_BIN` (default a temp dir), for the test VM and the owner's measured runs; never packaged |
| `make all` | `gate` — run before pushing |

## CI

`.github/workflows/ci.yml` runs on every push and pull request; a newer push to
the same ref cancels the older run. Paths are in the workflow's `env:` block.

| Job | Where | Runs |
|---|---|---|
| `core` | `public.ecr.aws/docker/library/archlinux:latest` container (the Docker official image, mirrored on ECR Public to avoid Docker Hub's pull rate limit) (some discovery tests expect Arch tooling) | `make all`: the gate (TUI and helper). Go build and module caches keyed on `go.sum` and the Go version. |
| `e2e` | `ubuntu-latest` + setup-go + vhs-action (vhs, ttyd, ffmpeg) | `make e2e`; uploads `cmd/archmaid/e2e/*.txt` as an artifact on failure. A tape whose VHS browser never started ("could not start browser", a runner flake) runs once more (`e2e/browser-retry.sh`, proved on a stub vhs by `browser-retry-selftest.sh` at the start of every run); any other failure fails at once. |
| `bench` | `ubuntu-latest` + setup-go, full history | `scripts/bench-compare.sh <base>` on the one runner: the PR's merge-base (or a push's previous SHA) against HEAD, benchstat-compared; fails on a significant slowdown (see "Bench compare"); uploads `benchstat` (old/new results, report) as an artifact, always. About 1–2 minutes. |
| `gui` | `public.ecr.aws/docker/library/archlinux:latest` container (the Docker official image, mirrored on ECR Public to avoid Docker Hub's pull rate limit) + gtk4, libadwaita, dbus, a font (no compositor) | `make gui-test` as an unprivileged user with `GSK_RENDERER=cairo` (no GPU): build, vet, the plain-Go tests, the display tests on the headless Broadway display; budgets not enforced. Build and module caches keyed on `gui/go.sum` + `go.sum` and the Go version (a cold gotk4 build is ~7 min on 16 threads, more on a runner). |

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
   `XDG_STATE_HOME`). Its scratch dirs (`archmaid-test-home*`, `archmaid-test-trap*`)
   are removed on every exit: by `Run` itself, and by the watchdog when the
   binary dies first (panic in a test, timeout, SIGKILL). `check.sh` and
   `gui/scripts/headless.sh` set `ARCHMAID_TESTENV_CREATED`, which every `Run`
   appends its dirs to, and fail through `scripts/testenv-leftovers.sh` if any
   still exists afterwards.
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
   allow-list of literals that are only compared, never run.
   **archmaid's own privileged programs** (`su`, `sudo`, `pkexec`,
   `pkttyagent`, `topgrade`) run by absolute `/usr/bin` path in the product
   (`internal/sysbin`; HANDOVER.md, "archmaid doesn't trust PATH for
   privilege"). `internal/testenv`'s `init` sets each back to its bare name
   in every process of a test binary, a re-exec'd child that never calls
   `Run` (the GUI harness's windows, the askpass relays) included, so the
   traps and a test's own shims decide exactly as before; `Run` fails the
   package if one is absolute when it starts. A test that proves a call
   site uses the absolute path points the variable at a shim of its own
   outside `PATH` (`testenv.UseSysbin`) with decoys first on `PATH`
   (`testenv.Decoys`), and must not run in parallel. The e2e harness
   builds the real binary with them bare (`-ldflags -X`, which
   `TestE2EBuildsBareSysbin` keeps complete; before any tape `run.sh`
   reads the values the binary links with `scripts/sysbinvals`, which
   reads the ELF file and runs nothing); `check.sh` fails a main package that links
   `internal/testenv` (its init would make the product trust `PATH` again)
   and a PKGBUILD, `install.sh` or cover build that names `sysbin`.
   `TestNoBarePrivilegedExec` keeps the bare names out of every exec call in
   product code, reading the program argument as the compiler folds it
   (`go/types`: a constant, a concatenation, a typed constant; also a
   variable assigned a bare name and a shell's `-c` script; its known
   limits are in HANDOVER.md's #131 landmine). archmaid-helper
   runs `/usr/bin/pacman` and friends by absolute path (as root, under
   pkexec); its verb tests use a fake `Runner` and never execute them, and
   only the `ExecRunner` tests run real, harmless binaries (`env`, `cat`,
   `sh`).
   **Programs archmaid's own package installs** and archmaid runs by
   absolute path (the widget's update check,
   `/usr/lib/archmaid/archmaid-updates-check`, which `driver.RefreshUpdates`
   runs when `systemctl --user start` fails) are variables in
   `internal/installed`; `testenv.Run` points each at a trap for the whole
   run, so reaching one FAILs the package with its real path in the report,
   on a machine with archmaid installed as on CI without it, whatever the
   test's `PATH`. `TestNoAbsoluteInstalledExec` rejects their literal paths
   anywhere but `internal/installed`. The hole this closes (#108):
   `TestTranscriptRealDriver` makes its shim dir the whole `PATH`, so
   `systemctl` was "not installed" and the real Driver fell back to the
   installed check, on the owner's machine the real script, 12 times a run
   (it died at once: with that `PATH` not even `mkdir` exists; on CI, without
   the script, the Driver's report failed the test instead). A test that
   builds a real Driver therefore fakes `Deps.RefreshUpdates`: the TUI's
   `TestMain` swaps `refreshUpdates` for every Driver its tests build
   (`TestDriverDepsRefreshUpdates`), the GUI's `realdriver` `fakes` and its
   UI harness set `Config.RefreshUpdates`. Only `internal/driver`'s refresh
   tests run the real one, against a `systemctl` shim and a script of their
   own in `installed.UpdatesScript`.
4. **No network.** The AUR RPC is an `httptest` server reached through the
   `aurRPCURL` var. `TestMain` sets `aurRPCURL` to a closed local port, so any
   test that reaches the audit without `fakeAUR` (choosing Full Cleanup or Run
   topgrade starts it at once, see `auditFetch`) fails fast instead of calling
   the real AUR. `internal/audit` has no default endpoint at all: `Run`
   refuses a `Config` without `RPCURL`, and its tests pass an `httptest` URL
   and a `Config.Foreign` func instead of a pacman shim. A fake AUR that
   holds a request must read the body first (`r.ParseForm()`): net/http only
   cancels `r.Context()` on a client hang-up once the body is consumed. And
   whatever else it waits on must be released by a cleanup registered after
   the server's, so it runs before `srv.Close` waits for the handler.
   Otherwise `srv.Close` hangs until the test binary times out.
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
| `refreshUpdates` | the widget's update-count refresh in `driverDeps` (`driver.RefreshUpdates` in the binary); `TestMain` sets a fake that counts its calls (`fakeRefreshes`) for every test |

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
| `note` | a notice popup's text, or the run's note (a right password's `AuthResolved` Detail: sudo keeps no reusable ticket, so each sudo asks through the popup), recorded before the plan |
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
  3-day rules, the launcher choice, and the popout's sections (`popout`: the outcome tag
  and its tint, `stamp`'s "Mon 5 Oct, 00:45 · 17 h ago", the notes, the updates and AUR
  lines, five findings and "+N more"); a name for every runnable action of
  `internal/actions` (`actionLabel`). Skipped when `qmltestrunner` is missing. It prefers
  `/usr/lib/qt6/bin/qmltestrunner`, because `/usr/bin/qmltestrunner` on this machine is a
  stale build that exits 1 silently.
- `TestUpdatesScript` (count, none, failure leaves no file, 0600/0700) and `TestManifest`.
- `TestWidgetTextIsPlain`: every `StyledText` in the widget sets `textFormat: Text.PlainText`.
- `TestPopoutReloadsOnOpen`: the popout re-reads the state files on every open (a
  `Connections` on its `parentPopout`'s `shouldBeVisible`; DMS loads the content once), and the
  bar keeps its 60 s reload timer. Both read the widget's source: it can't load outside DMS.
- Not in the gate: loading the widget in a real DMS. Do it **in the test VM** (DMS installed
  from the owner's dotfiles), never on the owner's machine: any quickshell instance there can
  open windows on the live desktop. In the VM, install `packaging/dms/archmaid` as a DMS
  plugin, put a scratch `last-run.json`, `audit-state.json` and `updates-cache.json` in the
  state dir (or let a real run write them), and check: both pills, the popout's LAST RUN /
  UPDATES / AUR sections with the outcome tag (OK primary, FAILED error, INCOMPLETE and
  ABORTED warning, CANCELLED muted), mono numbers, the findings, and both buttons. Then, with
  the popout closed, rewrite one state file (a new outcome): reopening must show it at once,
  not after the 60 s timer.

## Coverage map

What each file pins. Behaviour contracts, not test names — grep the file for
the names.

| File | Pins |
|---|---|
| `internal/sanitize/*_test.go` | `String` for every escape/control/format-character class in both modes, the invalid-UTF-8 and CR behaviour, and the no-copy fast path (with benchmarks); `OneLine`, `Clip`, `ClipTail`, `Field`, `QuoteName`. Pure unit tests, no UI. |
| `internal/logstore/logstore_test.go` | The store itself: paths and the dryrun slot, reading creates nothing and reports `ErrEmpty`, 0700/0600 modes fresh and fixed up, entry format, plain-text logs (written and read back), `Prune` keeps the newest runs and spares `dryrun`, strays and its own run; `Options.KeepRuns` rotates only when a real run's first `Append` creates its directory (the new run counted), a dry-run store never prunes. |
| `internal/logstore/wp3_test.go` | Work package 3: a record that can't be created (`/proc`, a path below a file) is an error, a broken store neither prunes nor creates anything, a log path that is a directory fails `Append` and `Read` (not `ErrEmpty`). |
| `internal/steps/steps_test.go` | `LogCategory` defaults to cleanup; `Command`/`Args` split `Argv` (nothing for an in-process step); `Result.Check` refuses `ConfirmNext` together with `InsertNext`, failed or not, and accepts each alone. |
| `cmd/archmaid-helper/verbs_test.go` | Step verbs and the helper's verb table in lockstep (the one package that can import both): every `steps.Verb*` constant, read from the source, is a helper verb; every non-empty `Verb` on a step the actions build is one too, including the steps only built at run time (Repair's reinstall after its root recheck, the orphan removal the probe inserts, the services disable, the downloads delete), reached by running each discovery and in-process step against shimmed `pacman`/`sudo`/`systemctl`. |
| `internal/actions/actions_test.go` | The menu table (unique IDs, `ByID`, each entry a queue, a discovery or a front-end entry, discoveries have a `Busy` label); Full Cleanup's step order with and without hyprpm, Run topgrade's; every queued step has a unique `ID` and the right helper `Verb`; `TestNoPrivilegedStepAfterHyprpm`: in no action's queue does a root step, or any in-process step (its result may insert a root step, as the orphan probe's does), follow hyprpm; `DisableUnits`; the audit report is the front end's (`Env.AuditReport`). |
| `internal/actions/repair_test.go`, `repair_err_test.go`, `services_test.go` | A Repair notice says "timed out" for a timeout, never the audit's deadline wording. The parsers: `parseBrokenPackages` (a package with several missing files once, stray lines ignored; fixture `testdata/pacman_Qkq.txt`), `chunkStrings`, `qkqOutput` (pacman's exit 1 with a report is a result; stderr or the exit status otherwise), `parseInactiveServices` on the captured `list-units` fixture (the `●` not-found row, masked, template instances, excluded names). The services discovery against a `systemctl` shim: a failing `list-units` is a "Couldn't list services: …" notice with systemctl's error, never "nothing found"; an empty one is the nothing-found notice. |
| `internal/actions/colour_test.go` | The colour switches (`colour.go`) in every queue, under sudo and polkit: topgrade's step has `Env` `CLICOLOR_FORCE=1`, yay's argv ends in `--color always`, no other step has an `Env` or the flag, and under `NO_COLOR` none has either. |
| `internal/actions/wp3_test.go` | Work package 3: the downloads record's rarer lines (a scan that fails, an entry kept though old, unlistable folder, age unknown behind a 0400 folder, the cut at `maxDetails`), blank/malformed unit lines, `skipRules` capped at three. |
| `main_test.go` | Dry-run safety: every Full Cleanup step, started as the Driver starts it, only reports what it would run. The services multi-select (toggle) and `actions.DisableUnits`, the parsers, the default log category, and the View-log picker (opens, returns where it came from, opens the chosen category). The queue, sudo, confirm and logging flows are `internal/engine`'s, with the TUI's side in `adapter_test.go` and the transcripts. |
| `gate_orphan_test.go` | Orphans: building the Full Cleanup queue and choosing it run no pacman (only the audit prefetch's `-Qqm`, off the UI thread); the in-process probe sits after topgrade and the cache steps, before the AUR cache step; it inserts the removal right behind itself only when orphans exist, with exactly the names it found (also as the step's `Items`, the cascade as its `Cascade`) and a confirm that previews the real `pacman -Rns` cascade total (both counts when they differ, plain count when equal), defaulting to No, a decline logged as SKIP (run through the TUI on the engine); none → a clear line and no confirm; a failing `-Qdtq` or `-Rs --print` fails the probe and inserts nothing; an abort on the real Driver kills the probe's pacman and inserts nothing. Temp files are cleaned with `sudo systemd-tmpfiles --clean`, not a find/rm. (The proceed-gate is `internal/engine`'s and the transcripts'.) |
| `activate_bench_test.go` | `BenchmarkActivateFullCleanup`: choosing Full Cleanup is the model's `Start` and the Session's `Start`, which builds the queue and asks for the probe and the prefetch, nothing else (guarded by `TestPerfGuard`, opt-in). `actions_glue_test.go` holds the helpers that build the real queues and discoveries through `actionEnv`. |
| `sudo_hardening_test.go` | Sudo steps, started as the Driver starts them (`runStep`), run as `sudo -A` (password via `SUDO_ASKPASS`, never the tty), or `sudo -n` when askpass isn't wired, and other commands are untouched; both topgrade steps need sudo. (The keep-alive is the Driver's.) |
| `auth_test.go` | `privileged().Authenticate`, the Driver's password check: a wrong password never reaches `sudo` and su runs as `su -s /usr/bin/true <user>`, a correct one mints the ticket. On the up-front prompt: enter sends the password and locks the input, every key is ignored until the engine answers, the re-ask unlocks it and shows its error (a lockout notice verbatim), and the popup shows "authenticating" meanwhile. |
| `stream_test.go` | The pane on the engine's `OutputChunk`s: it follows output only at the bottom: scrolled up, new chunks don't yank it, and when the tail drops old lines the line being read stays on top; a 20k-line step leaves the newest 2000 in order (the log gets all of it: the engine's). `BenchmarkStreamView5k`/`20k`: Update + View cost of a flood of 256-line chunks (guarded by `TestPerfGuard`, opt-in). |
| `perfguard_test.go` | Opt-in (`ARCHMAID_PERF_GUARD=1`): the streaming and status-line benchmarks stay under 3× the dev machine's baselines, choosing Full Cleanup under 1 ms and a Running frame (`BenchmarkViewRunning`, `view_bench_test.go`) under 600 µs; see Perf guard above. |
| `overlay_test.go` | Popups float, centered, over their background; ANSI-styled backgrounds keep their width; the abort dialog stays over the running screen. |
| `resize_test.go` | The output pane never overflows the terminal (running queue, log viewer, resize while a popup is open; the run's note above the checklist, at 40, 80 and 120 columns); an askpass prompt (the engine's `NeedsPassword`) over another popup keeps the real background; nested popups resolve to the right base screen. |
| `services_test.go` | Services discovery through `actions.Menu`: against a `systemctl` shim serving the `internal/actions/testdata/systemctl_list_*.txt` fixture system, the batched `is-enabled` with its per-unit fallback picks the enabled-but-inactive units, none is a notice, and an equivalence test runs the old bash pipeline against the same system to prove both select the same units. |
| `startup_test.go` | Startup deletes nothing (ground rule 8): `--dryrun` (with its own log and last-run writes), a plain start, `--help`/`-h` (usage on stdout, exit 0), an unknown option, a stray argument, a bad `--downloads` (exit 2) and the askpass relay each leave a state dir of `logKeepRuns`+1 runs intact, writing only under `dryrun/`; a real run rotates to `logKeepRuns` (itself included) on its first entry, not before. |
| `colour_test.go` | Run topgrade (sudo path) with a topgrade shim that colours only under `CLICOLOR_FORCE`, on the TUI's model and on the binary's real Driver: the pane shows the colour, under `NO_COLOR` it doesn't, and `topgrade.log` (timestamps aside) and `last-run.json` are the same plain text either way. |
| `log_hygiene_test.go` | The log through the TUI: opening every category in View log creates no run directory (it would take one of the five prune slots); what a step streamed (colours, a progress bar, an OSC 52 clipboard write), run through the TUI on the engine, shows with its colours only and lands in its log as plain text, and an old log still holding raw escapes is shown plain; a store rebuilt because `logDir` moved keeps the process's run-directory name, even a minute later (`logNow`), while entries keep the real time. The store itself (modes, pruning) is `internal/logstore`'s. |
| `ticks_test.go` | Ticks never multiply: the status refreshes after a finished queue (`Finished`) and after the editor are one-offs, not another 4 s loop. (The sudo keep-alive is the Driver's.) |
| `repair_test.go` | Repair's discovery against a `pacman`/`sudo` shim pair that flags different packages as the user and as root: the user scan (`pacman -Qq`, then `pacman -Qkq -- <chunk>` per worker) flags the same packages in the same order for 1, 2, 4, 16 or 100 workers, checks each installed package once and never runs sudo; the root recheck is one `sudo -n pacman -Qkq -- <candidates>` and only what root confirms is offered for reinstalling; without a ticket it is a couldn't-scan notice, never "nothing broken". The flow around it (probe, prompt, give-up notice) is `internal/engine`'s and the `repair_*` transcripts'. |
| `hyprpm_test.go` | `hyprpm` is disabled inside topgrade and runs as the last step, only when installed (it drops the sudo ticket on exit). |
| `coverage_test.go` | Thin spots: `editConfig` (creates defaults, never clobbers, via the `execProcess` seam); password masking in the rendered frame; every checklist glyph; the status line's tea glue only (the line carries its three labels, a `statusMsg` updates the header and schedules the next tick); all summary outcomes. `status_test.go` is just `BenchmarkSystemStatusCache100` for the perf guard (the status line on a 100-package fixture cache). |
| `typeahead_test.go` | Typed-ahead guard (#158): "hunter2" and Enter sent as the topgrade confirm is drawn (also right after the up-front password prompt) answer nothing, a trailing Enter inside the renewed window neither, a deliberate y or n after a pause does; the same for the audit report's `a`; ctrl+c on the confirm declines (No) at once, inside the window too, and on the audit report declines like esc and q (#175); `q` on the summary quits, Enter and Esc go back to the menu. Runs on a fake clock (`armNow`); the rest of the package runs with the guard off (`confirmArm = 0`). The e2e tapes sleep before answering a prompt. |
| `keys_wp3_test.go`, `edges_wp3_test.go` | Work package 3: every key of every screen (menu, confirm, summary, log picker and pager, audit scroll, notice, discovering, services), unknown messages, `Finished` with an askpass popup or a dismissed report, `PromptClosed`, overlay padding, the status tick through the `tickFn` seam, `execProcess` and `handedTerminal`, `editConfig` in a dry run, `editorName`, `DropSudoTicket` (against a shimmed `sudo`: the call, the failure said, nothing in a dry run) and `waitForDownloads`' slow path. |
| `internal/sysstat/sysstat_test.go` | The status line itself, against fixtures: the df/du-style and free-style size formatters, the rendered `Line`, `Read` spawning no child process (empty `PATH`), and `N/A` for every unreadable source. |
| `audit_test.go` | The AUR audit through the TUI, fully offline (the audit itself is `internal/audit`'s): an AUR maintainer with terminal escapes reaches neither the step output, the confirm, the report nor `audit.log` intact, while the raw value goes into the snapshot; a corrupt snapshot gates the step and offers `a` on the menu report, and is replaced (kept aside) only by the accepted commit; an end-to-end first-audit-then-takeover menu run, logged; a dry run never advances the snapshot; the notices (no foreign packages, an unreachable AUR, short enough for a popup); the menu audit saves all but the high findings and `a` on the report accepts those (q drops the accept, so they stay flagged), added co-maintainers included; an added co-maintainer gates the step by name until the commit records it; an old-format snapshot is a co-maintainer baseline for the step and the menu; the screen/notice flow. |
| `internal/audit/audit_test.go` | The audit itself, offline (an `httptest` AUR, `Config.Foreign` instead of pacman): severity classification (adoption/maintainer change/added co-maintainer = high; removed co-maintainer, orphaned, recent push, out of date, 0 votes, not-on-AUR = glance); `GateReason` naming maintainer changes and co-maintainers (sanitized, popup-sized) and gating a corrupt snapshot; local `-debug` companions skipped, high first; chunked POST lookups and RPC error handling, an 8 MiB response cap, results for names that weren't requested dropped; `sanitize.Field` at `FieldMax`; `Run` never saves, a first audit is a baseline and a takeover after a commit is high, in `Text` and `LogSummary`; `Commit` with `holdHigh` keeps the high findings' old entries, accepting saves them and logs `ACCEPT`, a dry run has no commit, a failed save is logged `WARN`; no foreign packages is `ErrNoForeign`, a failing lister or a missing setting fails the audit without an RPC; `ShortErr`. |
| `internal/audit/snapshot_test.go` | The maintainer snapshot: round-trip with co-maintainers sorted and deduplicated, entries kept for packages the AUR didn't report; an old-format file (maintainer strings only, or objects without `comaintainers`) loads with no co-maintainer record; every other shape, and an unreadable file, is `BaselineCorrupt`, never missing; written atomically at 0600 with no temp file left; a held lock skips the save (`ErrSnapshotLocked`); a corrupt snapshot fails closed, survives the holding save, and is kept aside as `.corrupt-<time>` by the accepting one; an old-format snapshot is a co-maintainer baseline until the commit records them, then an added one gates. |
| `internal/audit/cancel_test.go` | The one cancellable deadline, against a fake AUR that never answers: a cancel ends the RPC within 100 ms with no goroutine left; the deadline spans every chunk, not each request, and the foreign query too (a list cut short is the deadline, not `ErrNoForeign`). The prefetch: its result is `Run`'s and its commit writes the configured snapshot; `Cancel` and an aborted `Wait` each end a hanging lookup within 100 ms with `context.Canceled` and no goroutine left. |
| `fullcleanup_audit_test.go` | Full Cleanup's audit step: it's first, in-process, `AlwaysLog`, category `audit`; both topgrade steps are `AlwaysLog`. Run through the TUI on the engine (`startQueue`, `startAudit`): a maintainer change gates topgrade with a confirm naming the package(s), defaulting to No, before topgrade starts; declining skips only the update and logs `SKIP: … (declined)`; the report is in the output pane and `audit.log`. No foreign packages is a clean pass; an unreachable AUR fails the step without gating. In a dry run it still audits, logs exactly once, and leaves the snapshot alone, even when the gate is accepted. The step itself never saves the snapshot: with nothing high the engine commits it at once; behind a gate only "yes" saves it (logged `ACCEPT`), while "no" or a gate with no step after it leaves it, so the next run gates again. |
| `topgrade_audit_test.go` | Run topgrade's queue is [audit, topgrade], built from the same `auditStep`, and gated identically, run through the TUI on the engine (`startQueue`): a maintainer change puts a named confirm defaulting to No in front of topgrade; "no" skips the update (logged) and leaves the snapshot, so the next run gates again; "yes" runs it and saves the snapshot (logged `ACCEPT`); an unreachable AUR fails the audit without gating; a dry run gates but never saves. |
| `askpass_test.go` | The TUI's askpass glue: `startAskpassServer` under `XDG_RUNTIME_DIR` wires `askpassWrapper` (sudo steps get `-A`) without touching archmaid's own `SUDO_ASKPASS`, `stopAskpassServer` removes it and unwires; no `XDG_RUNTIME_DIR` leaves askpass unwired with no `/tmp` fallback; a real step child sees `SUDO_ASKPASS` only for a `NeedsPriv` step. `tty_sudo_test.go`: the Server also writes the sudo wrapper (0700 file in a 0700 dir), wired as `askpassSudoDir`, first on a `NeedsPriv` step's `PATH` only, removed and unwired on stop; `TestTUISudoByNameAsksInThePopup` runs the real TUI in a pty (as `TestStepCannotReadTheTerminal`) with a step whose program runs `sudo` by name twice, `sysbin.Sudo` a shim outside `PATH` and a decoy `sudo` on it: each call reaches the TUI's askpass popup, its answer is su-checked and reaches sudo, the decoy never runs and nothing prompts on the terminal; with `timestamp_timeout=0` (the shim's `-n -v` fails) the run goes on after the up-front password, shows its note, and asks once per sudo call. Answering, refusing and verifying a request are the Driver's and the Session's (`internal/driver`, `internal/engine`, `internal/privilege`); the popup is `adapter_test.go`'s. |
| `internal/privilege/*_test.go` | The package itself, against `su`/`sudo` shims (`shimAuth`), real unix sockets in `t.TempDir()` and a fake process table. The TUI's sudo wrapper (`sudowrap_test.go`, run for real against a stand-in sudo at an absolute path): `-A` and archmaid's `SUDO_ASKPASS` added exactly when the caller's sudo options leave them out (a table over `-A`/`-S`/`-n` long and short and in clusters, option values such as `-u -n` and `-C 3 -n`, `--`, the command's own flags, `-K`/`-V`/`-h`, unknown options), every argument passed through as given (spaces, quotes, `$`, backslashes, a newline, an empty one); it execs the absolute sudo fixed at write time (the real sudo's parent is the caller) and a decoy later on `PATH` never runs; `Listen` writes it as `bin/sudo`, both 0700, only with `SudoWrapper` and an absolute `sysbin.Sudo`, and `Close` removes it; `Env` puts its dir first on a `NeedsPriv` step's `PATH` only; `Authenticate` with an unreusable ticket is `NoTicketNote` and no error with the wrapper, `ErrNoTicket` without; `PathShadows` takes the wrapper, as written, for sudo's boundary, and a replaced, rewritten, copied or symlinked one, or one left after `Close`, for a shadow. `Sudo`: `Authenticate` runs `su -s /usr/bin/true <user>` with the password on stdin and never reaches sudo on a wrong one, then `sudo -S -v`; lockout notices from su or sudo come back verbatim; a cancelled ctx fails; `Probe`/`Refresh` are `sudo -n -v`; `Command` gives sudo `-A` only with a wrapper and needsPriv, else `-n`, leaves other argv alone and keeps the step's display; `Env` strips an inherited `SUDO_ASKPASS` and adds the wrapper only for privileged steps. Askpass: `RunClient` prints the answer and exits 0, exits 1 on an unanswered close or no socket; `Listen` keeps its socket and 0700 wrapper in a private 0700 directory under the runtime dir, never sets `SUDO_ASKPASS`, delivers prompts, and `Close` (twice, or on nil) removes the directory; no runtime dir is `ErrNoRuntimeDir` with no `/tmp` fallback, and a failed `Listen` leaves nothing behind; the wrapper's shell quoting survives quotes and `$(…)` in paths; a client that never sends its prompt doesn't block the next one and is hung up on at the read deadline, an oversized prompt or an unidentifiable peer is dropped, an undelivered request is hung up on at `Close`; the server reports the real `SO_PEERCRED` peer; the real wrapper end to end. `Request.Answer` writes the password only after su accepts it, never on a wrong one (connection left open), refuses a line break before su; `Cancel` hangs up unanswered. `Verify`, against a fake process table: the step's own sudo (via yay, or a direct `sudo -A` step) is accepted and its caller named; refused are a wrong uid, a gone or reused helper pid, a helper that isn't archmaid, a parent that isn't sudo (a user process named sudo, a root process with another name, a readable exe elsewhere), a chain that misses the step, breaks, loops or runs past `maxChain`, and a reused step or ancestor pid; outside tests `Verify` uses the real uid, binary and `/proc`, and `seam.SetSystem` swaps them only until restored. |
| `downloads_test.go` | The downloads glue: the notice names the folder, `--downloads` parsing, a whole run through the TUI (discovery → named confirm → "y" → summary, the deletion in `cleanup.log` once), and the delete step's own records on the ctx causes the Driver uses: an abort (plain `context.Canceled`) mid-deletion or after the run writes one `ABORT` entry naming what went, a taken result (`steps.ErrResultTaken`) writes none, and the record goes to the store captured when the step was set up even while `logDir` keeps moving (race clean under `-race`). The glue on its own is `internal/frontend`; the rest is `internal/downloads/downloads_test.go`. |
| `internal/frontend/*_test.go` | The glue both front ends share, on its own (the GUI's use of it): `AuditConfig`'s defaults and its files in the store's root, `ForeignPackages` parsing a shimmed `pacman -Qqm` (a failing one reads as none), `MenuAudit`'s notices (nothing to audit, an unreachable AUR, a stopped audit that writes no snapshot) and its report (drawn by `render` or plain, `Accept` only for a high finding, cleared once accepted), `AuditStep` (its fields, the gate and commit, the notices as output), `Prefetcher` (only the audit, config read once, one AUR request) and an `AuditFetch` cancelled while the AUR holds its request (the step fails at once and the request is hung up on); the delete step's records on the Driver's ctx causes (committed: none; abort mid-deletion or after the run: one `ABORT` entry naming what went; `ErrResultTaken`: none; dry run and an unendable ctx arm nothing), `StepAborted`, `DownloadsOptions`, and `Glue.Env` (keeps the front end's fields, the menu audit's config read once and the step's per queue, base `Downloads` wins). Under `testenv.Run`. |
| `internal/frontend/topgrade_errors_test.go` | Work package 3: `EnsureTopgradeConfig` when the directory can't be made, the file can't be opened, or the write fails (a zero `RLIMIT_FSIZE`; `created` says the empty file exists). |
| `internal/driver/colour_test.go` | A whole Full Cleanup on the real Driver (polkit): topgrade's child gets `CLICOLOR_FORCE=1`, yay `--color always`, the root helper's call neither and archmaid's own environment no `CLICOLOR_FORCE`; the shim's colour reaches the `OutputChunk`; under `NO_COLOR` neither child gets its switch. |
| `internal/driver/wp3_test.go` | Work package 3: what the teardown does after a Session panic on the inputs path and the local (hook result) path: the open askpass request hung up on, an in-flight prefetch cancelled, the keep-alive stopped (the panic is armed through the engine clock); a discovery cancelled by `Close`; a hung-up agent prompt; the askpass reader ending with its channel; an answer for a request already hung up; a prefetch shared by two steps. |
| `internal/downloads/downloads_test.go` | Which folder is cleaned: the order flag → `ARCHMAID_DOWNLOADS` → `XDG_DOWNLOAD_DIR` → `~/Downloads` → `~/Download`; `$HOME` and the passwd home refused however spelled (`"$HOME/"`, a symlink to it, `$HOME` behind a symlink), as are `/` and their ancestors; only the flag may leave `$HOME`, and a bad flag is an error, not a fall-through. What gets deleted (real ctimes, the clock moved via `Options.Now`): old files, folders and symlinks go, a symlink as the link; new files, a back-dated mtime and a folder with anything new deep inside stay; the folder itself is never a candidate; an entry changed or replaced between scan and delete is kept; a swapped folder deletes nothing; dry run only reports; the confirm lists ten names then "…and N more", unsafe names quoted. Never another filesystem: other devices, bind mounts from a fake mountinfo, a mount appearing after the scan, `removeTree` refusing to enter one, per-node re-checks, partial-delete reporting, hard links, and `TestDownloadsMountPoints`, which mounts for real under `unshare -rm` (skipped without unprivileged user namespaces). |
| `audit_cancel_test.go` | Aborting the audit step on the real Driver (`newDriver`) while the AUR sits on its request: the action finishes aborted within 100 ms, and once the Driver is closed no goroutine (connection, handler, pacman) is left behind. The deadline itself is `internal/audit/cancel_test.go`'s. |
| `audit_race_test.go` | Every audit setting is read on the model's goroutine (`currentAuditConfig`): a menu audit, a prefetch and an audit step run while the test rewrites `logDir`, `aurRPCURL`, `auditDeadline` and `dryRun`, and still finish against what they captured (race clean under `-race`); a commit run after `logDir` moved writes the snapshot the audit read and logs beside it. |
| `audit_prefetch_test.go` | A whole Full Cleanup through the TUI asks the AUR exactly once: the audit step uses the prefetched lookup, and the finished run drops it. When the engine starts and drops the prefetch is `internal/engine`'s; carrying it out `internal/driver`'s. Home of `reapAuditFetch` (ground rule 7). |
| `transcript_*_test.go` + `testdata/transcripts/` | Whole actions end to end against shims, through the TUI on a `syncDriver` (`TestTranscript`, `tuiEngineTarget`; `-update-transcripts` records through it), through the `syncDriver` alone (`TestTranscriptDriver`, `driverTarget`), through the real Driver wired as main wires it (`TestTranscriptRealDriver`, `tuiDriverDeps` = `driverDeps`) and through the engine's Session (`TestTranscriptSession`, `sessionTarget`): the same goldens byte for byte, every Session run also held to `enginetest.CheckContract` and its last-run record checked; a real downloads delete through the Session is logged once, with no ABORT entry, because the target releases the step's ctx with `steps.ErrResultTaken` — see Transcripts above and the scenario names. `syncdriver_test.go` is the tests' pull-mode Driver. |
| `internal/testtranscript/recorder_test.go` | The transcript format itself, target-free: a hand-built event sequence through a fake target gives every line kind (plan, inserted step, flag change, normalised paths, snapshot with and without co-maintainers, prompts and their replies, abort, note, report, end); an unscripted or mismatched prompt, a bad confirm reply, an unused answer, a step outside the plan and a target error each fail with the transcript so far. |
| `internal/engine/engine_test.go` | The frozen API (E0), checked by the compiler: every `Session` method's and `Driver`'s signature as a typed func value, so a changed signature breaks the build. The closed sets: the Event, Effect, Input and Command types listed in the test are exactly the ones the source marks (a `go/parser` scan), so a new type is picked up by the checks. No Event can carry a password: a reflection walk over every Event type, nested structs, slices, maps and pointers included, fails on a `Secret`, on a text field named like a password (`pw`, `passw…`, `secret`, `token`, `cred…`), and on any func, channel or interface field (events are plain data); a self-test proves the walk catches each of those and passes look-alikes (`Prompt`, `Upward`, `Bypass`). `Secret` prints `<redacted>` under every fmt verb and flag, nested in Commands and Effects, and through `encoding/json`. `Outcome.String` gives the transcript words. |
| `internal/engine/session_test.go`, `session_plan_test.go` | `Options.Validate` (the required `actions.Env` fields, a known `AuthFlow`). The Session (E1/E2) with the Driver played by hand (outside the package, a menu seam in `export_test.go`; a fake clock), every run held to `enginetest.CheckContract`. Start ignores an unknown action, a front end's own entry and a second action; a queue runs in order: `QueueStarted` (the plan as `PlanStep`s), `StepStarted` with its `StartStep`, `OutputChunk` per `StepOutput` (an OSC 52 write stripped, SGR kept), `StepDone` with its elapsed time, the next step; `Finished` with errs and elapsed, a `WriteLastRun` record, then idle. Stale inputs (an old or zero Seq, a stray probe answer or discovery) change nothing. Logging: FAIL always, OK only for `AlwaysLog`, DRYRUN in a dry run, the body the whole output. Step confirms: other IDs, a selection or a password for a confirm are ignored; optional "no" skips (SKIP logged for `AlwaysLog`), required "no" or cancel skips the rest and aborts (recorded cancelled when nothing ran, aborted otherwise); "yes" runs `OnAccept` as a `RunHook` before `StartStep`. Auth: a sudo queue is probed (cached → the gate → `QueueStarted` and the keep-alive; declined gate → cancelled, prefetch dropped, no plan; uncached → `NeedsPassword`, cancellable), a dry run and polkit never probe, a queue needing no root gets no gate; the consumed prefetch is dropped once. `NotGranted` is `AuthRefused` with a SKIP entry, counted in `Finished.Refused`, and the completed run is recorded `incomplete` with `steps_refused`. A discovery: `Discover` with `Options.Env` (DryRun copied in), its `Notice` plain and with no last-run record, its `Pending` run as a queue. Ticks, askpass hang-up, hyprpm kept last. Plan changes: `InsertNext` lands behind its step (events in order, the orphan confirm with `Direct`/`Cascade` quoted where unsafe), nothing from a failed result; `ConfirmNext` gates the next step (`PlanChanged`, `ConfirmAuditGate`, the commit only on "yes"), is dropped on the last step; `Commit` without it runs at once, even for a failed step; a result setting both is refused failing closed (step failed with the refusal in its output, nothing inserted, no commit, the gate still on the next step, with `Commits` false); no root step can be inserted after hyprpm; the downloads confirm lists its items. |
| `internal/engine/stub/stub_test.go` | The stub Driver, against a `FakeClock`: the hand-built `FullCleanup` script replays in order, event for event, with its three confirms answered, the clock lands on start + the script's waits, and the sequence obeys the engine's ordering contract (`enginetest.CheckContract`, shared with the Session's tests and `sessionTarget`: nothing names a step before `QueueStarted`, indexes match the plan as moved by `StepInserted`, one step at a time, output only for the running step, each step ends once and never as `StepDone{Aborted}`, a step's confirm names a step that hasn't run, `StepInserted` and `PlanChanged` only between steps — never while a step runs or its confirm is open — and never at a step that ran, prompt IDs unique, `Finished` last (only `PhaseChanged` and `StatusUpdated` after it) and every step settled; `enginetest/contract_test.go` checks the checker on hand-built breaches). Commands before `Start`, a `Start` mid-play, and answers to prompts that aren't open are ignored; each answer kind (confirm, cancel, password, selection, report) closes its prompt; the next `Start` replays from the top. `Abort` at a mid-step askpass prompt closes it, aborts the step with its elapsed time and finishes `aborted`; with no step started it finishes `cancelled`; during a cue's wait it aborts at once. `Send` never blocks with nobody reading (it returns false once the queue is full); `Close` returns whether the replay is idle or blocked publishing, is idempotent, closes `Events`, and `Send` after it is false. |
| `internal/engine/combine_edges_test.go`, `wp3_test.go`, `internal/engine/stub/close_wp3_test.go` | Work package 3: a `PartDone` that isn't the running part's, a call that exits cleanly before its later parts, a combined run whose every confirm is declined, `DiscoveryOutcome.String` out of range, the repair-check notice's three-names-then-ellipsis; the stub closed while it waits on a cue or on an answer. |
| `internal/ttyguard/*_test.go` | Against a real pty (a shell made its session leader, test code only): while held, a child can open neither `/dev/tty` nor the pty by name, an fd opened before still reads; `Pause` lets opens through until every resume; `Release` undoes it for good (also during a pause); a terminal someone else made exclusive is left so; a pipe or nil is a no-op. In `internal/tui`, `TestStepCannotReadTheTerminal` runs the real `Run` (this test binary again, `TestTTYChild`) as the session leader of a pty, with one step whose shell tries to open and read `/dev/tty`: it is refused, ctrl+c then y abort it, q quits, and the terminal is no longer exclusive afterwards (without the hold the shell opens the terminal and ctrl+c never reaches the TUI). `TestEditorGetsTheTerminal`: the editor's command runs with the hold paused and the hold is back after it. |
| `internal/proc/*_test.go` | Child processes, against real ones. `Start`: lines in order (stdout and stderr through one pipe), then exactly one done; a failure keeps its `*exec.ExitError`; a missing command is a nil `Child` and a done with the error; the `Child` carries the start time `/proc` gives and the child gets exactly `Spec.Env`, and with a nil `Env` never an inherited `SUDO_ASKPASS` (sudo, a sudo behind `env` or `sh -c`, a plain command, `Query`); a dry run starts nothing (a marker file proves it) and reports one `[dry-run] would run:` line; a child that leaves a background process holding its pipe is done within `WaitDelay`, with every line, its own exit status (a clean exit is a success) and no goroutine left. `Next`: batches what's buffered (at most `max` lines, in order, ending at the done, nothing left behind); released by quit. `Stop`: SIGINT first; a SIGINT-ignoring child is SIGKILLed after the grace; nil-safe; no goroutine left behind for a child blocked on a full channel or holding its pipe through a background process. `Query`: stdout on success and failure, the `ExitError` with stderr and a stderr-led message, ctx cancel kills. `Read` against the real `/proc` (a root process's exe is unreadable to a user). `ExtraEnv` (`extraenv_test.go`): added after a nil or explicit `Env`, wins over a name in it, never carries a `SUDO_ASKPASS`, never writes into the caller's `Env`. `TestNoSetsid`: a started child shares our session and process group, and a `go/parser` scan of `cmd/` (minus `cmd/archmaid-helper`) and `internal/` finds no `Setsid`/`Setpgid`/`Setctty`. |
| `teatest_test.go` + `testdata/` | Headless-program goldens for the menu and the services multi-select. |
| `golden_test.go` + `testdata/` | Direct `View()` goldens at 80×24, the model fed the engine's Events on a `fakeDriver`: the summary for each outcome (ok, issues, skipped, aborted); the up-front password popup over the menu (masked input, and re-opened with the attempts-left error); a confirm over the running checklist; a mid-run askpass prompt naming its requester; the log picker; the audit report; a notice; the discovering spinner. |
| `adapter_test.go` | The TUI as an engine front end, on a `fakeDriver`: Events → the model (askpass popup over the running step, its retry, lock and withdrawal; abort → the menu with what never ran skipped; a declined required step → the aborted summary; the report, its accept notice and dismissal; prompts, selection and the menu `Start`; the status line), keys → Commands (esc on askpass hangs up; abort confirm: n/esc/ctrl+c/enter-on-No resume and send nothing), the askpass prompt shown quoted and stripped of escapes, `forwardEvents`, and the binary's wiring: `newDriver` builds the real Driver and closing it closes Events, `startPrefetch` starts only the audit, `reportLog` keeps 20 errors and counts the rest. |
| `lastrun_test.go` | last-run.json through whole scenarios on the TUI: three wrong passwords → `incomplete` with every sudo step refused; an aborted Full Cleanup → `aborted`, every step from topgrade on skipped; a declined gate → `cancelled`; nothing before a run. The outcomes themselves are `internal/engine`'s. |
| `cmd/archmaid/e2e/*.tape` + `run.sh` + `shim.sh` | The real binary under `--dryrun`, one tape each, in a throwaway `HOME` with a shim dir first on `PATH` (every external command is a symlink to `shim.sh`, which only logs argv to a file and prints canned output; `run.sh` checks the shadowing first and that no mutating shim ran). `clear-cache`: asserts the `DRYRUN:` entry. `services`: toggles one of two shimmed units; the log names exactly that unit and `systemctl` was only asked to list units and `is-enabled`. `orphans`: Full Cleanup's confirm reads "2 … 3 total" (from the tape's `.txt`) and `orphans.log` has the dry-run removal. `abort`: ctrl+c on the blocked audit step, confirm; back on the menu, nothing logged. `repair-none`: the user scan, the "No broken packages found" notice, no sudo. `repair-found`: the root recheck as `sudo -n pacman -Qkq -- …`, the reinstall confirm declined and logged, nothing reinstalled. `downloads`: `--downloads` honoured, a year-old mtime isn't old (ctime), nothing deleted. `fullcleanup-gate-declined`: a declined gate runs only the audit prefetch's `pacman -Qqm` and leaves a `cancelled` record in `dryrun/` only. `help-and-bad-flag` (no tape): `--help` exits 0, `--bogus` exits 2, neither writes under the scratch `HOME`. `bash cmd/archmaid/e2e/run.sh services abort` runs a subset; generated `cmd/archmaid/e2e/*.txt` is gitignored. |

| `cmd/archmaid-helper/internal/coverdir/coverdir_test.go` | The cover helper's baked counter dir (cover builds only): used only by root with no `GOCOVERDIR`, and only a real, root-owned directory nobody else can write; a symlink, a file, a group- or world-writable or user-owned dir, a missing one and a user's own temp dir are refused. |
| `scripts/covblocks/main_test.go` | The uncovered-block lister: profiles merge with check.sh's rule (covered in any profile is covered), totals truncate like check.sh, malformed lines are refused, blocks are named after their function, method, closure or `<package>`. |
| `scripts/covblocks/exempt_test.go` | The exempt list: seven tab-separated fields, comments skipped, bad lines refused; exempt statements leave the denominator per scope, an entry that no longer names an uncovered block of the same size is stale and takes nothing out, and a scope the profile lacks is not stale. |

The uncovered statements themselves, sorted by what it takes to cover them:
[`coverage-map.md`](coverage-map.md).

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
   In the owner's graphical session it is the **session** (2026-10-11/12,
   `polkit-keep-session-check.md`): polkitd keeps it for the scope of the
   agent that asked (HANDOVER, "`auth_admin_keep` is kept for the scope of
   the agent that asked").
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
