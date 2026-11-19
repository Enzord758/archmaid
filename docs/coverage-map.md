# Coverage map

Every statement the tests leave uncovered, sorted by what it takes to cover
it, the exemption list for what can't be covered, the work packages that
turn the rest into tests, and the tooling that lets runs outside `go test`
count. Base: `3f8f0d0` (refactor/p0 = main, v0.8.1-beta; refreshed in #293). Machine-readable copies, one
entry per uncovered block: [`coverage-map.json`](coverage-map.json) and, for
category E, [`coverage-exempt.tsv`](coverage-exempt.tsv) with its reviewed
list [`coverage-exempt.md`](coverage-exempt.md). How the suite is run and
kept safe: [`TESTING.md`](TESTING.md) (its "Coverage from real runs" section
lists the scripts).

**The goal (owner, 2026-10-11): 100 % of reachable code**, i.e. covered /
(total - exempt) = 100 %, with the exempt list reviewed. `scripts/check.sh`
holds a floor on the raw core total (`MIN_TOTAL`) that is raised as coverage
rises, and prints the reachable figure next to it. `make e2e-cover` prints
raw and reachable per scope.

## Categories

| Cat | Means | Who runs it |
|---|---|---|
| A `unit` | an ordinary unit test with the existing shims and fakes reaches it; the test file is named per block | any dev, in the gate |
| B `e2e` | the real binary has to run: a new tape or check in `cmd/archmaid/e2e`, a headless start of the cover `archmaid-gui`, or a check of the cover `archmaid-helper` (as the user, or as uid 0 in an unprivileged user namespace; both are refused before anything runs) | any dev, in `make e2e` / `make e2e-cover` |
| C `vm` | real root, polkit, pkexec, sudo, pacman or systemd, a root process's `/proc`, or a real desktop session with a portal | the VM tester only, in `~/archmaid-vmtest` |
| D `owner` | the owner's own desktop, hardware or session that the VM lacks (the DMS shell (Quickshell) with the archmaid DMS widget plugin, Hyprland specifics, the NVIDIA GPU, the real AUR set, real network timing) | the owner, by hand |
| E `unreachable` | a defensive branch that can't happen given the code, or dead code. Each one is on the reviewed list [`coverage-exempt.md`](coverage-exempt.md) with a recommendation: `keep-exempt`, `make-testable` or `remove`, and a security flag | nobody: decide per block |

## How it was measured

```sh
COVER_GUI=1 COVER_OUT=$(mktemp -d) scripts/cover-report.sh
#   unit/  scripts/cover-unit.sh: go test ./... through scripts/cover-exec.sh (children count)
#   e2e/   cmd/archmaid/e2e/run.sh with E2E_COVER_DIR: the thirteen checks on a cover archmaid
#   gui/   gui/scripts/headless-cover.sh: the GUI's plain tests and every part of `gui/scripts/parts.sh` (the display suite)
#   merged/  scripts/cover-merge.sh of all three: summary.txt now has raw and reachable per scope
go run ./scripts/covblocks -json "$COVER_OUT/merged/profile.txt" > blocks.json
python3 -I scripts/covmap/classify.py blocks.json classified.json
python3 -I scripts/covmap/report.py classified.json "$COVER_OUT/merged/summary.txt" "$(git rev-parse HEAD)" docs
#   writes docs/coverage-map.json, docs/coverage-exempt.{tsv,md} and the generated regions of this file
```

The scopes: **core** is check.sh's set (`cmd/archmaid` + `internal/...`
without the test-only packages, the `MIN_TOTAL` floor), **testonly** is the
test-only packages (`internal/testenv`, `testtranscript`, `engine/enginetest`,
`privilege/privtest`, `helperclient/helpertest`: code that exists only for
tests, reported on its own line and in no floor), **helper** is `cmd/archmaid-helper/...` (the gate
doesn't measure it), and **gui** is the `gui/` module (the gate doesn't
measure it either). `gui/cmd/archmaid-gui` (6 statements) is linked by no
test, so it appears in no run's data; the map counts it in by hand.

What each kind of run adds, for core (statements 10275, test-only packages
excluded; measured at 3f8f0d0):

| Run | core |
|---|---|
| `check.sh` (`go test -coverprofile`, no children) | 97.3 % (9994/10275, the floor `MIN_TOTAL` 97.0 holds it) |
| `cover-unit.sh` (the same tests, plus the re-executed children: the TTY child running the real `tui.Run`, the askpass relay) | 97.3 % (10000/10275) |
| + the e2e tapes on the cover binary (`main`, the real startup) | 97.5 % (10019/10275) |
| + the GUI's tests (they drive `internal/engine`, `driver`, `frontend` and more through the GUI) | 97.5 % (10023/10275) |

testonly: 68.6 % (394/574), not part of core. helper: 93.6 % (1691/1806),
unit tests only. gui: 94.2 % (7919/8399, plus the 6 hand-counted statements of
`gui/cmd/archmaid-gui`) with the headless display tests (`make e2e-cover` with
`COVER_GUI=1`: 22 parts, four at a time, about 10 minutes in all).

Two things the numbers hide:

- **testenv's own checks never count.** `internal/testenv` is 49.0 %
  covered (157 of 308 statements uncovered), but most of it runs in every package
  of every test run: the orphan reaper, the trap report and the state-guard
  compare run in `TestMain` after `m.Run`, and the testing package writes its
  counters when `m.Run` returns. A direct unit test of those functions (inside
  `m.Run`) counts them; that is work package 1.
- **Test-only code is in the denominator.** `testenv`, `testtranscript`,
  `enginetest`, `privtest` and `helpertest` (574 statements, 394 covered)
  ship in no binary. Without them core is **97.5 %** (10023/10275). The gate
  could measure shipped code only (drop them from `COVERPKG`) and hold the
  test helpers to their own floor; that is a decision for the lead.

## Totals

Raw is covered / total; reachable is covered / (total - E), where E is the
exempt list (`coverage-exempt.md`). Both come out of `scripts/covblocks
-reachable -exempt docs/coverage-exempt.tsv`.

<!-- gen:totals -->
| Scope | Statements | Covered | Raw | Uncovered | A unit | B e2e | C vm | D owner | E exempt | Reachable (covered / (total - E)) |
|---|---|---|---|---|---|---|---|---|---|---|
| core | 10275 | 10023 | 97.5 % | 252 | 151 | 6 | 1 | 0 | 94 | 98.4 % (10023/10181) |
| helper | 1806 | 1691 | 93.6 % | 115 | 77 | 35 | 0 | 0 | 3 | 93.7 % (1691/1803) |
| gui | 8405 | 7919 | 94.2 % | 486 | 390 | 38 | 29 | 0 | 29 | 94.5 % (7919/8376) |
| testonly | 574 | 394 | 68.6 % | 180 | 111 | 0 | 0 | 0 | 69 | 78.0 % (394/505) |
| all | 21060 | 20027 | 95.0 % | 1033 | 729 | 79 | 30 | 0 | 195 | 95.9 % (20027/20865) |

Raw / reachable, cumulative:

| Scope | Now (unit + e2e + GUI) | after A | after A+B | after A+B+C | after A+B+C+D (= every reachable statement) |
|---|---|---|---|---|---|
| core | 97.5 % / 98.4 % | 99.0 % / 99.9 % | 99.0 % / 99.9 % | 99.0 % / 100.0 % | 99.0 % / 100.0 % |
| helper | 93.6 % / 93.7 % | 97.8 % / 98.0 % | 99.8 % / 100.0 % | 99.8 % / 100.0 % | 99.8 % / 100.0 % |
| gui | 94.2 % / 94.5 % | 98.8 % / 99.2 % | 99.3 % / 99.6 % | 99.6 % / 100.0 % | 99.6 % / 100.0 % |
| testonly | 68.6 % / 78.0 % | 87.9 % / 100.0 % | 87.9 % / 100.0 % | 87.9 % / 100.0 % | 87.9 % / 100.0 % |
| all | 95.0 % / 95.9 % | 98.5 % / 99.4 % | 98.9 % / 99.8 % | 99.0 % / 100.0 % | 99.0 % / 100.0 % |
<!-- /gen:totals -->

D is 0 statements: every line the owner's checks exercise is reachable by
A-C too. The owner's checks are about behaviour (below), not about the
number.

## The path to 100 % of reachable

1. **A (work packages 1-6):** unit tests and headless GUI scenarios, each
   block names its test file. The biggest areas are `internal/testenv` (about 110
   direct tests of the post-`m.Run` checks), `gui/internal/ui` (new headless
   scenarios) and the helper's fake-runner error paths.
2. **B (work packages 7-8):** the helper's `main` and `hardenProcess`
   (a cover-helper check in `run.sh`, the "helper check" below), the GUI
   binary's entry (a headless start of the cover `archmaid-gui`, real and
   `--stub` with each script), and `tui.Run`'s error exit and the editor
   handover.
3. **C (30):** the VM tester's runs (below).
4. **E ([`coverage-exempt.md`](coverage-exempt.md)):** leave the denominator
   by being listed. `make-testable` entries (the shared atomic-write helper,
   the downloads TOCTOU hook) move to A once the seam lands; `remove` entries
   are deleted. Every `keep-exempt` entry is a branch someone reviewed and
   decided to keep.

## Work packages

Every A and B statement is in exactly one package. Gain is statements over
the all-scope total (21060 with the hand-counted GUI main), in percentage
points of raw coverage. "Security code" counts the statements in
`cmd/archmaid-helper`, `internal/privilege`, `internal/helperclient`,
`internal/ttyguard`, `gui/internal/polkitagent`, `gui/internal/realdriver`,
`internal/downloads`, `internal/driver/keep.go` and the askpass code: Dev 3
reviews those tests, seams and removals. The packages touch disjoint files,
so they can run in parallel; 1 is a prerequisite for nothing but is the
largest single gain.

<!-- gen:workpackages -->
| # | Package | Statements | Gain (all) | Role | Security code | Covers |
|---|---|---|---|---|---|---|
| 1 | **Test infrastructure** (A) | 111 | +0.5 pts | architecture | no | testenv's post-`m.Run` checks (orphan reaper, trap report, state-guard compare) called directly inside a test, the recorder's fake targets, the contract checker's own failure paths. By package: `testenv` 91, `engine/enginetest` 15, `testtranscript` 5 |
| 2 | **Helper and privilege edge cases** (A) | 95 | +0.4 pts | security | 95 stmts: Dev 3 reviews | the helper's fake-runner/Stat/Exe error paths and lock edges, helperclient's malformed and failing helper output, privilege's askpass relays, faillock and kept-password failures. By package: `cmd/archmaid-helper/helper` 77, `helperclient` 14, `privilege` 4 |
| 3 | **Core: TUI keys, driver, engine, actions, logstore** (A) | 85 | +0.4 pts | architecture (driver/keep.go and the askpass bits: security) | no | per-screen key handling and Finished/PromptClosed edges in the TUI, the driver's teardown, hang-up and agent paths, combined-queue edges, discovery outputs, the log store's error paths, the health checks' and the AUR build's fake-seam failures, pkgdb and report edges. By package: `health` 28, `aurbuild` 17, `report` 11, `pkgdb` 6, `driver` 5, `pkgdb/pkgdbtest` 5 |
| 4 | **Leaf packages and the polkit agent** (A) | 89 | +0.4 pts | performance-QA (polkitagent, realdriver, downloads: security) | 45 stmts: Dev 3 reviews | audit/downloads write and scan error paths, proc argv forms, sanitize, sysstat and vt edges, polkitagent and realdriver against fake agents. By package: `gui/polkitagent` 27, `proc` 23, `gui/realdriver` 14, `audit` 13, `vt` 6, `downloads` 4 |
| 5 | **GUI logs, timeline and signals pages** (A) | 36 | +0.1 pts | GUI | no | new headless scenarios: log page states (none, unreadable, empty), the timeline, signals mid-run, the failed-step view, logview and overview edges. By package: `gui/ui` 28, `gui/overview` 8 |
| 6 | **GUI windows, dialogs, settings, run view** (A) | 313 | +1.4 pts | GUI | no | window/dialog/settings-page scenarios, the run view's SGR and ring edges, settings.Save error paths, shell and usercache cells, the Packages page (pkgview) and health sheet (healthview) states, nullbroadway's protocol errors. By package: `gui/ui` 166, `gui/pkgview` 70, `gui/scripts/nullbroadway` 43, `gui/healthview` 22, `gui/settings` 9, `gui/runview` 3 |
| 7 | **E2E: the cover-helper check** (B) | 35 | +0.1 pts | performance-QA with Dev 3's review (helper) | 35 stmts: Dev 3 reviews | main and hardenProcess of the helper binary: the run.sh check of the cover helper as the user and as uid 0 in an unprivileged user namespace (both refused before anything runs). By package: `cmd/archmaid-helper` 34, `cmd/archmaid-helper/coverdir` 1 |
| 8 | **E2E: binary entries** (B) | 44 | +0.2 pts | GUI (the TUI tape: architecture) | no | archmaid-gui's main, `--stub` with each script (headless), the TUI's error exit and the editor handover on a pty tape. By package: `gui/ui` 32, `cmd/archmaid` 6, `gui/cmd/archmaid-gui` 6 |
| | **A and B together** | 808 | +3.8 pts | | | |
<!-- /gen:workpackages -->

## The 10 most important VM (C) checks

The statement count of C is small because the code is built on seams; the
VM is where the real thing is checked, and with the cover build its runs now
count too.

1. pkexec + the installed helper's root verbs end to end (`full-cleanup`,
   `remove-orphans`, `repair`, `disable-unit`): `CheckInstall` passing on the
   real `/usr/lib/archmaid` path, `ExecRunner` on the real pacman/systemctl.
2. `helperclient.PlanOrphans`: the installed helper's `plan-orphans`, as the
   user, on the real `pacman -Qdtq` / `-Rs --print` (the one C statement in core).
3. A refused, cancelled and locked polkit dialog through the real
   `pkttyagent` (`gui/internal/polkitagent`): "Not authorised" rows, and
   `privilege.FaillockStatus` on the real `/run/faillock` tally.
4. topgrade's sudo through archmaid's askpass relay with the real sudo:
   `Request.Verify` walking the real `/proc` chain (sudo's exe is root's).
5. The TUI's sudo path: two-stage `Authenticate` against the real su/sudo,
   the keep-alive, `DropSudoTicket` at exit.
6. Open folder / Open log / Choose downloads folder in the GUI through
   `GtkFileLauncher`/`GtkFileDialog` and the VM desktop's portal
   (`ui/launcher.go`, 29 of the 30 C statements).
7. A real Full Cleanup (paccache, journal vacuum, `systemd-tmpfiles --clean`,
   the orphan removal) and its logs and `last-run.json`.
8. Repair with a really broken package: the root recheck and the reinstall.
9. Disable unused services on real units (the helper's `Also=` vetting on
   real unit files).
10. The cover helper's counters under pkexec (`coverdir.Pick` on a real
    root-owned dir; proven here only as uid 0 in a user namespace).

## The 10 most important owner (D) checks

1. The archmaid DMS widget plugin in the owner's DMS shell (Quickshell): the
   pills, the popout's sections, the re-read on open, opening the GUI or the
   TUI (QML/JS: not in Go's coverage; `packaging/dms` has its own Go tests of the data scripts).
2. `archmaid-gui` on the NVIDIA GPU: cairo by default, `GSK_RENDERER=vulkan`,
   first frame and RAM against the budgets.
3. Hyprland: the window's app id matching the `.desktop` entry and icon,
   started from the launcher (which sets no `GOCOVERDIR`: such a run counts nothing).
4. The AUR audit against the owner's real AUR set and the real AUR (the gate
   wording, real network timing against the deadline).
5. `auth_admin_keep` inside the owner's real session
   (`docs/polkit-keep-session-check.md`).
6. The updates timer and its pill on the owner's real repos and AUR set.
7. The downloads cleanup on the owner's real folder and mounts (USB, sshfs,
   bind mounts: `Scan`'s boundary).
8. Edit topgrade config with the owner's `$EDITOR` and terminal.
9. A real Full Cleanup with the installed package (`docs/first-live-run.md`).
10. The yay cooldown hook (`~/dotfiles/yay`, outside this repo).

## Collecting coverage

All inputs to one merge must come from **the same commit**: covdata is
matched by package and source, and blocks of two versions don't line up.

### The helper check (B; any dev, on this machine)

The cover helper may run here only as the CLAUDE.md rules allow: a scratch
`HOME`/`XDG_*`, a verb it refuses before running anything. Verified for this
map:

```sh
s=$(mktemp -d) && mkdir -p "$s/home" "$s/cov" && chmod 0755 "$s/cov"
go build -trimpath -cover -covermode=atomic -coverpkg=./cmd/archmaid-helper/... -tags archmaidcover \
  -ldflags "-X github.com/Enzord758/archmaid/cmd/archmaid-helper/internal/coverdir.Dir=$s/cov" \
  -o "$s/archmaid-helper" ./cmd/archmaid-helper
# as uid 0 in an unprivileged user namespace: refused ("running from …, not
# /usr/lib/archmaid/archmaid-helper"), exit 4; the counters land in the baked $s/cov
env -i HOME="$s/home" XDG_STATE_HOME="$s/home/.local/state" XDG_CONFIG_HOME="$s/home/.config" \
  XDG_CACHE_HOME="$s/home/.cache" PATH=/usr/bin PKEXEC_UID=1000 \
  timeout 20 unshare -r "$s/archmaid-helper" full-cleanup
# as the user with GOCOVERDIR: refused ("not running as root"), exit 4
env -i HOME="$s/home" ... GOCOVERDIR="$s/cov-user" timeout 20 "$s/archmaid-helper" full-cleanup
```

With the baked dir group-writable the helper refuses it, warns "GOCOVERDIR
not set" and writes nothing (checked too). The scratch home stayed empty.

### The VM tester (C)

pkexec starts the helper with a fixed environment, so `GOCOVERDIR` never
reaches it. A cover helper built with `COVER_HELPER_DIR` carries a directory
instead (`cmd/archmaid-helper/internal/coverdir`, linked only with the
`archmaidcover` tag): when it runs as root with no `GOCOVERDIR`, it writes
there, but only if the directory is no symlink, is owned by root and isn't
group- or other-writable. Runs as the user (`plan-orphans`) inherit
`GOCOVERDIR` like the other binaries.

On the host (building only; nothing runs):

```sh
bin=$(mktemp -d) && COVER_BIN=$bin COVER_HELPER_DIR=/var/lib/archmaid-cover make cover-build
```

In the VM, through `vm.sh` only:

```sh
V=~/archmaid-vmtest
$V/run.sh --reset && $V/run.sh --boot
# install archmaid-git and archmaid-gui-git of the SAME commit as usual
# (packaging/arch/README.md), then put the cover binaries over them:
$V/vm.sh put $bin/archmaid /tmp/archmaid
$V/vm.sh put $bin/archmaid-helper /tmp/archmaid-helper
$V/vm.sh put $bin/archmaid-gui /tmp/archmaid-gui
$V/vm.sh sudo 'install -m0755 -o root -g root /tmp/archmaid /usr/bin/archmaid &&
  install -m0755 -o root -g root /tmp/archmaid-helper /usr/lib/archmaid/archmaid-helper &&
  install -m0755 -o root -g root /tmp/archmaid-gui /usr/bin/archmaid-gui &&
  install -d -m0755 -o root -g root /var/lib/archmaid-cover'
$V/vm.sh ssh 'install -d -m0700 ~/archmaid-cover'
```

Run the checks from a terminal in the VM's session with
`GOCOVERDIR=$HOME/archmaid-cover archmaid` (or `archmaid-gui`); the helper
needs nothing, pkexec runs it and it writes to `/var/lib/archmaid-cover`.
Every process must **exit normally** (q in the TUI, close the GUI window): a
killed process writes no counters. A "GOCOVERDIR not set" line in topgrade's
output means sudo dropped the variable for archmaid's askpass relay; harmless,
only the relay's counters are lost. Then collect, merge, and reset:

```sh
$V/vm.sh sudo 'tar -C / -czf /tmp/archmaid-cover.tgz var/lib/archmaid-cover home/enzord/archmaid-cover && chown enzord /tmp/archmaid-cover.tgz'
out=$(mktemp -d) && $V/vm.sh get /tmp/archmaid-cover.tgz "$out/" && tar -C "$out" -xzf "$out/archmaid-cover.tgz"
scripts/cover-merge.sh -o "$(mktemp -d)" "$COVER_OUT/merged/covdata" "$out/var/lib/archmaid-cover" "$out/home/$USER/archmaid-cover"
$V/run.sh --reset
```

The cover binaries are not personal data; nothing goes the other way but the
covdata.

### The owner (D)

On the owner's machine the installed helper stays the packaged one (a cover
helper belongs in the VM only). The TUI and the GUI can run as cover builds
from a checkout of **the commit the installed package was built from** (the
GUI talks to that helper):

```sh
cd ~/archmaid && git checkout <the package's commit>
bin=$(mktemp -d) && COVER_BIN=$bin make cover-build
cov=$(mktemp -d /tmp/archmaid-cover-owner.XXXXXX)   # never under ~/.local/state/archmaid
GOCOVERDIR=$cov $bin/archmaid          # a real run, exactly like the installed archmaid
GOCOVERDIR=$cov $bin/archmaid-gui      # from a terminal: the launcher sets no GOCOVERDIR
scripts/cover-merge.sh -o "$(mktemp -d)" "$cov" <the unit/e2e/VM dirs of that commit>
```

These are real runs: they write the real logs and `last-run.json` like the
installed binaries do, and the gate's state guard will see them if a gate
runs at the same time. Quit normally (q, close the window) or the run counts
nothing.

## Packaging

A cover build is never installed by default. `packaging/arch/PKGBUILD`
builds plain binaries; `check.sh` fails if it names `-cover`, `archmaidcover`
or `GOCOVERDIR`, or if a normal build of the helper links `coverdir`.


## Per package

Statements still uncovered after unit + e2e + GUI, by category.

<!-- gen:packages -->
| Package | A | B | C | D | E |
|---|---|---|---|---|---|
| `cmd/archmaid-helper` |  | 34 |  |  | 1 |
| `cmd/archmaid-helper/internal/coverdir` |  | 1 |  |  |  |
| `cmd/archmaid-helper/internal/helper` | 77 |  |  |  | 2 |
| `cmd/archmaid` |  | 6 |  |  |  |
| `gui/cmd/archmaid-gui` |  | 6 |  |  |  |
| `gui/internal/healthview` | 22 |  |  |  |  |
| `gui/internal/overview` | 8 |  |  |  |  |
| `gui/internal/pkgview` | 70 |  |  |  |  |
| `gui/internal/polkitagent` | 27 |  |  |  | 15 |
| `gui/internal/realdriver` | 14 |  |  |  |  |
| `gui/internal/runview` | 3 |  |  |  | 1 |
| `gui/internal/settings` | 9 |  |  |  |  |
| `gui/internal/ui` | 194 | 32 | 29 |  | 13 |
| `gui/scripts/nullbroadway` | 43 |  |  |  |  |
| `internal/actions` | 4 |  |  |  |  |
| `internal/audit` | 13 |  |  |  | 17 |
| `internal/aurbuild` | 17 |  |  |  | 5 |
| `internal/aurhelper` | 4 |  |  |  |  |
| `internal/aursearch` | 1 |  |  |  |  |
| `internal/downloads` | 4 |  |  |  | 15 |
| `internal/driver` | 5 |  |  |  | 2 |
| `internal/engine` | 1 |  |  |  | 1 |
| `internal/engine/enginetest` | 15 |  |  |  |  |
| `internal/engine/stub` |  |  |  |  | 1 |
| `internal/frontend` | 1 |  |  |  |  |
| `internal/health` | 28 |  |  |  | 18 |
| `internal/helperclient/helpertest` |  |  |  |  | 2 |
| `internal/helperclient` | 14 |  | 1 |  |  |
| `internal/logstore` | 1 |  |  |  | 8 |
| `internal/pkgdb` | 6 |  |  |  |  |
| `internal/pkgdb/pkgdbtest` | 5 |  |  |  |  |
| `internal/privilege` | 4 |  |  |  | 17 |
| `internal/privilege/privtest` |  |  |  |  | 1 |
| `internal/proc` | 23 |  |  |  | 1 |
| `internal/report` | 11 |  |  |  |  |
| `internal/sysstat` | 2 |  |  |  |  |
| `internal/testenv` | 91 |  |  |  | 66 |
| `internal/testtranscript` | 5 |  |  |  |  |
| `internal/ttyguard` |  |  |  |  | 4 |
| `internal/tui` |  |  |  |  | 5 |
| `internal/version` | 1 |  |  |  |  |
| `internal/vt` | 6 |  |  |  |  |
<!-- /gen:packages -->

## Every uncovered block (A, B, C)

Line numbers are at the base commit. Blocks of one function with the same
reason are one row. The last column is the test file (or harness) the test
belongs in. The E blocks are in [`coverage-exempt.md`](coverage-exempt.md).

<!-- gen:blocks -->
#### `cmd/archmaid-helper`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `harden.go:17-19, 20-21, 22-22` | `hardenProcess` | 5 | B | runs in the harden test's child, but hardenProcess clears GOCOVERDIR before the test binary writes counters; the cover helper binary counts it (any run) | e2e (new helper check) (security: Dev 3) |
| `harden.go:28-29, 30-32` | `scrubEnv` | 4 | B | runs in the harden test's child, but hardenProcess clears GOCOVERDIR before the test binary writes counters; the cover helper binary counts it (any run) | e2e (new helper check) (security: Dev 3) |
| `harden.go:40-41, 42-43, 44-46, 47-48, 49-49, 51-52, 53-54, 56-56, 58-58` | `closeInheritedFDs` | 15 | B | runs in the harden test's child, but hardenProcess clears GOCOVERDIR before the test binary writes counters; the cover helper binary counts it (any run) | e2e (new helper check) (security: Dev 3) |
| `main.go:25-26, 27-29, 30-34, 35-36, 37-37` | `main` | 10 | B | main only runs in the binary: a cover helper started as the user with a root verb is refused before it runs anything (verified under a scratch HOME); the root path is the VM's | e2e (new helper check) (security: Dev 3) |

#### `cmd/archmaid-helper/internal/coverdir`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `coverdir.go:26-27` | `init` | 1 | B | only a cover helper running as root sets it: verified under unshare -r with a scratch HOME (coverage-map.md, helper check); in the VM every pkexec run does | e2e (new helper check) (security: Dev 3) |

#### `cmd/archmaid-helper/internal/helper`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `dblock.go:80-80` | `session.lockDirSafe` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `fullcleanup.go:142-143` | `runFullCleanup` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `leftovers.go:255-258` | `scanCacheDir` | 3 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `leftovers.go:286-287` | `removeLeftover` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `leftovers.go:337-338` | `plainPath` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `leftovers.go:420-423` | `releasePacmanLock` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkg.go:201-202, 214-215, 219-220, 222-223, 232-233` | `session.installPlan` | 5 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkg.go:275-276, 282-282` | `session.checkNoUpdates` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkg.go:293-294, 296-297, 302-303` | `session.installedVersions` | 3 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkg.go:321-322, 324-325` | `session.syncDetails` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkg.go:353-354, 357-358` | `parseSize` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkg.go:387-387` | `parseSyncInfo` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkg.go:459-460, 462-463, 485-486, 494-495, 521-522` | `session.removePlan` | 5 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkg.go:603-604, 635-636` | `session.pkgRun` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkglocal.go:159-160, 164-165` | `homeFromPasswd` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkglocal.go:195-195` | `session.checkWalkStat` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkglocal.go:218-218` | `openStep` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkglocal.go:236-237, 242-243, 260-262, 277-279` | `session.openUserFile` | 6 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkglocal.go:312-313, 318-318` | `checkRootDir` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkglocal.go:331-332, 339-340, 354-355, 357-359` | `session.newStaging` | 5 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkglocal.go:373-374, 382-382, 386-386` | `staging.copyIn` | 3 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkglocal.go:396-397, 402-403` | `staging.rehash` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkglocal.go:436-437` | `parseLocalInfo` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkglocal.go:478-479, 481-482, 491-492` | `session.allowedArches` | 3 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkglocal.go:502-503, 504-505, 506-506` | `session.machine` | 4 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkglocal.go:515-516, 519-520, 523-524, 532-533, 567-568` | `session.vetLocal` | 6 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkglocal.go:623-624, 664-665` | `runPkgInstallLocal` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `pkglocal.go:701-702, 709-710` | `session.caller` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `protected.go:53-53` | `session.kernelPkgbases` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `protected.go:77-78, 82-83, 92-93` | `session.protectedSet` | 3 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `protected.go:196-197, 199-200` | `runPlanProtected` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |

#### `cmd/archmaid`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `main.go:34-35, 42-44, 65-68` | `main` | 6 | B | tui.Run's error exit: a new e2e run of the binary without a terminal (Bubble Tea can't open one), under the usual scratch HOME | cmd/archmaid/e2e/run.sh |

#### `gui/internal/healthview`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `demo.go:15-15` | `Demo` | 1 | A | the sheet's model with a state or an ignore error no test sets yet | gui/internal/healthview/*_test.go |
| `demo.go:28-30, 31-32, 33-33, 34-35, 36-37` | `demo.Ignore` | 8 | A | the sheet's model with a state or an ignore error no test sets yet | gui/internal/healthview/*_test.go |
| `demo.go:41-45` | `demo.Unignore` | 4 | A | the sheet's model with a state or an ignore error no test sets yet | gui/internal/healthview/*_test.go |
| `demo.go:48-50, 51-52, 53-70, 71-72` | `demo.Run` | 8 | A | the sheet's model with a state or an ignore error no test sets yet | gui/internal/healthview/*_test.go |
| `demo.go:76-80` | `demo.UnreadNews` | 1 | A | the sheet's model with a state or an ignore error no test sets yet | gui/internal/healthview/*_test.go |

#### `gui/internal/overview`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `held.go:114-114` | `ParseHeld` | 1 | A | a dry-run last-run record | gui/internal/overview/overview_test.go |
| `held.go:134-135, 146-146` | `ReadHeld` | 2 | A | a dry-run last-run record | gui/internal/overview/overview_test.go |
| `held.go:159-160` | `clip` | 1 | A | a dry-run last-run record | gui/internal/overview/overview_test.go |
| `overview.go:88-89` | `Label` | 1 | A | a dry-run last-run record | gui/internal/overview/overview_test.go |
| `updates.go:74-75, 87-88` | `ReadUpdatesStamp` | 2 | A | a dry-run last-run record | gui/internal/overview/overview_test.go |
| `updatescard.go:157-157` | `cut` | 1 | A | a dry-run last-run record | gui/internal/overview/overview_test.go |

#### `gui/internal/pkgview`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `clock.go:25-25` | `RealClock.AfterFunc` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `clock.go:76-77` | `FakeClock.Pending` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `controller.go:135-136` | `Controller.Review` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `detail.go:60-62` | `Search.Select` | 2 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `detail.go:81-82, 91-91, 96-97` | `Search.Select.func` | 3 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `detail.go:112-114` | `Search.ClearDetail` | 2 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `detail.go:150-151` | `BuildDetail.func` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `detail.go:174-175` | `BuildDetail` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `detail.go:204-204` | `repoText` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `detail.go:218-218` | `reasonText` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `format.go:13-14` | `Size` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `format.go:88-89` | `digits` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `format.go:113-113, 116-116, 117-118, 119-119, 140-140, 141-142, 143-143, 159-160` | `rpmVerCmp` | 8 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `installed.go:28-28` | `ViewTitle` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `installed.go:69-69` | `InstalledView.Filter` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `installed.go:73-74` | `InstalledView.all` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `list.go:24-24, 26-26, 28-28, 30-30` | `Mode.String` | 4 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `list.go:64-64` | `ErrKind` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `search.go:58-58, 58-58` | `Deps.fill.func` | 2 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `search.go:236-237` | `Search.runRepo` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `search.go:250-251` | `Search.runRepo.func` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `search.go:261-262` | `Search.runAUR` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `search.go:294-295` | `plainNote.func` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `search.go:299-300` | `plainNote` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `search.go:344-345, 348-349` | `withInstalled` | 2 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `search.go:376-377, 381-381, 382-383, 384-385, 386-387` | `Search.Refresh` | 6 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `search.go:402-405` | `withoutInstalled` | 3 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `sheet.go:179-179` | `Sheet.Req` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `sheet.go:182-182` | `Sheet.Prompt` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `sheet.go:202-203, 207-208` | `Sheet.Apply` | 2 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `sheet.go:267-268` | `Sheet.CanConfirm` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `sheet.go:335-336` | `Sheet.View` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `sheet.go:372-372` | `Sheet.fillReview` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `sheet.go:415-416` | `Sheet.notes` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `sheet.go:441-442` | `oneLine` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `sheet.go:449-450, 461-461, 465-465, 467-467, 469-469` | `RefusalHeading` | 5 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `sheet.go:475-476` | `joinAnd` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `sheet.go:484-484` | `RefusalText` | 1 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |
| `sheet.go:491-492, 495-496, 540-540, 542-542` | `RefusalTextFor` | 4 | A | a headless scenario of the Packages page: the key, click or state (search error, empty, held, a long list) it needs, or a plain model test | gui/internal/pkgview/*_test.go + gui/internal/ui scenarios |

#### `gui/internal/polkitagent`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `polkitagent.go:165-166, 175-176` | `fileIDOf` | 2 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go (security: Dev 3) |
| `polkitagent.go:287-288, 294-295, 302-305, 308-311, 358-361, 378-378, 379-380` | `Start` | 14 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go (security: Dev 3) |
| `polkitagent.go:391-391` | `discard` | 1 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go (security: Dev 3) |
| `polkitagent.go:412-413` | `Agent.Stop.func` | 2 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go (security: Dev 3) |
| `polkitagent.go:550-551, 576-577, 594-595, 605-605` | `Agent.drain` | 4 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go (security: Dev 3) |
| `polkitagent.go:618-619` | `Agent.sendEOF` | 1 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go (security: Dev 3) |
| `polkitagent.go:653-653` | `Prompt.Err` | 1 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go (security: Dev 3) |
| `polkitagent.go:695-697` | `Prompt.Answer.func` | 2 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go (security: Dev 3) |

#### `gui/internal/realdriver`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `agent.go:59-60, 62-63, 72-73, 76-78` | `Wiring.helper.func` | 8 | A | a fake polkit agent that keeps ending or fails to start; no XDG_RUNTIME_DIR in Config | gui/internal/realdriver/agent_test.go (security: Dev 3) |
| `agent.go:101-102, 105-106` | `Wiring.startAgent` | 2 | A | a fake polkit agent that keeps ending or fails to start; no XDG_RUNTIME_DIR in Config | gui/internal/realdriver/agent_test.go (security: Dev 3) |
| `realdriver.go:170-171, 175-176` | `Start` | 2 | A | a fake polkit agent that keeps ending or fails to start; no XDG_RUNTIME_DIR in Config | gui/internal/realdriver/agent_test.go (security: Dev 3) |
| `realdriver.go:246-247` | `Wiring.New` | 1 | A | a fake polkit agent that keeps ending or fails to start; no XDG_RUNTIME_DIR in Config | gui/internal/realdriver/agent_test.go (security: Dev 3) |
| `realdriver.go:265-266` | `Wiring.env` | 1 | A | Options() with no fake PlanOrphans picks helperclient.PlanOrphans (assignment only; running it is C) | gui/internal/realdriver/realdriver_test.go (security: Dev 3) |

#### `gui/internal/runview`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `summary.go:192-193` | `attentionRow` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `summary.go:252-252` | `Duration` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `summary.go:283-284` | `packagesBlock` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |

#### `gui/internal/settings`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `settings.go:132-133, 145-147, 149-151, 153-155, 157-158` | `Save` | 8 | A | no XDG_CONFIG_HOME nor HOME, an unreadable gui.json, a read-only config dir, a rename onto a directory, a bad downloads folder | gui/internal/settings/settings_test.go |
| `settings.go:195-195` | `CheckDownloads` | 1 | A | no XDG_CONFIG_HOME nor HOME, an unreadable gui.json, a read-only config dir, a rename onto a directory, a bad downloads folder | gui/internal/settings/settings_test.go |

#### `gui/internal/ui`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `app.go:137-138, 139-141, 142-142, 143-144, 145-145` | `Main` | 7 | B | the binary's entry: start the cover archmaid-gui under headless.sh with a scratch HOME (a GUI e2e; the harness calls Run, not Main) | gui/scripts/headless.sh + cover archmaid-gui (new) |
| `app.go:205-211, 213-213, 214-216, 217-217` | `mainReal` | 10 | B | the binary's entry: start the cover archmaid-gui under headless.sh with a scratch HOME (a GUI e2e; the harness calls Run, not Main) | gui/scripts/headless.sh + cover archmaid-gui (new) |
| `app.go:226-226` | `realConfig.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `app.go:244-244, 245-245, 245-245, 247-247, 248-248, 249-250, 260-260, 261-264` | `stubDriver.func` | 10 | B | archmaid-gui --stub with each ARCHMAID_GUI_SCRIPT, headless (the harness builds its stub drivers itself) | gui/scripts/headless.sh + cover archmaid-gui (new) |
| `app.go:252-252, 254-254, 256-256, 258-258, 266-266` | `stubDriver` | 5 | B | archmaid-gui --stub with each ARCHMAID_GUI_SCRIPT, headless (the harness builds its stub drivers itself) | gui/scripts/headless.sh + cover archmaid-gui (new) |
| `compositor.go:46-47, 48-49, 50-54` | `<package>` | 8 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `dialogs.go:139-140, 149-150` | `Window.confirmDialog` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `dialogs.go:569-571` | `Window.selectionDialog` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `failed.go:119-119` | `Window.runAgain` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `failed.go:184-185` | `recordFolder` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `glass.go:106-107` | `Window.applyGlass` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `glass.go:143-144` | `Window.dialogOpened` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `health.go:68-69` | `Window.startHealth.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `health.go:105-106` | `Window.healthDone` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `health.go:130-131` | `Window.renderHealthCell` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `health.go:159-160` | `Window.openHealth` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `health.go:203-203` | `Window.openHealth.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `health.go:333-334, 336-337` | `Window.openLink` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `health.go:364-365` | `Window.confirmWithNews.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `health.go:412-413` | `Window.newsCallout` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `health.go:421-421` | `Window.newsCallout.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `health_ignore.go:28-28` | `ignoreItems` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `health_ignore.go:153-154` | `Window.ignoreKeys` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `health_ignore.go:171-172` | `Window.ignoreKeys.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `health_real.go:41-44` | `realHealthSource.Run` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `health_real.go:78-79` | `realHealthSource.Unignore` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `health_real.go:88-89, 90-91, 92-92` | `realHealthSource.UnreadNews` | 4 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `held.go:44-45` | `Window.openHeld` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `held.go:139-140, 142-143, 148-149, 151-151, 153-153, 155-155, 157-157` | `Window.reviewHeld` | 10 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `held.go:165-167` | `Window.openHeldAudit` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `icons.go:47-48, 51-52` | `installIcons` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `icons.go:63-63` | `iconName` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `launcher.go:40-40, 41-42, 43-44, 49-49` | `gtkLauncher.launch` | 5 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:45-45, 46-47` | `gtkLauncher.launch.func` | 2 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:53-53, 54-55, 56-57, 62-62` | `gtkLauncher.OpenURI` | 5 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:58-58, 59-60` | `gtkLauncher.OpenURI.func` | 2 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:65-65` | `gtkLauncher.OpenFile` | 1 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:66-66` | `gtkLauncher.OpenFolder` | 1 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:69-70, 71-72` | `gtkLauncher.ChooseFolder` | 3 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:73-73, 74-75, 76-78, 79-79` | `gtkLauncher.ChooseFolder.func` | 6 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `logs.go:157-158` | `Window.newLogs.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `nothing.go:43-44` | `Window.showNothing` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `overview.go:197-198, 215-216` | `Window.fillHeroPlan` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `overview.go:304-305` | `Window.stackButtons` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `overview.go:608-609` | `Window.refreshLastRun` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages.go:70-70` | `pkgBackend.Abort` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages.go:76-76, 76-76` | `pkgBackend.UpdateSystem.func` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages.go:78-78` | `pkgBackend.UpdateSystem` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages.go:148-149` | `Window.newPackages` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages.go:193-194, 205-206` | `Window.newPackages.func` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages.go:279-279, 280-281` | `pkgPage.build.func` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages.go:457-458, 462-463` | `pkgPage.setNarrow` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages.go:469-470` | `pkgPage.rowActivated` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages.go:500-501` | `pkgPage.refresh` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages.go:538-539, 566-567` | `pkgPage.refreshList` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_demo.go:32-33` | `demoCatalog.Search` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_demo.go:158-159` | `demoAUR.Info` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_detail.go:68-69` | `pkgPage.refreshDetail` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_detail.go:93-94` | `repoLabel` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_detail.go:144-145` | `pkgPage.detailActions` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_detail.go:165-165` | `pkgPage.detailActions.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_detail.go:183-185, 187-188` | `pkgPage.now` | 3 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_detail.go:274-276, 278-279, 282-282` | `pkgPage.buildTray.func` | 4 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_rows.go:16-17, 21-21` | `pkgPage.dbLine` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_rows.go:36-37` | `pkgPage.fillRecent` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_rows.go:182-182` | `pkgPage.addButton.func` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_rows.go:216-216` | `pkgPage.offerClear.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_rows.go:229-230` | `pkgPage.add` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_rows.go:239-240, 251-252, 253-254, 255-257, 263-264` | `pkgPage.refreshInstalled` | 8 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_run.go:18-19, 27-27` | `Window.startPackages` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_run.go:22-22, 23-23, 24-25` | `Window.startPackages.func` | 3 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_run.go:57-58, 60-61` | `Window.pkgTake` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_run.go:79-81` | `Window.pkgTake.func` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_run.go:104-104, 105-106` | `pkgEngine.Abort` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_sheet.go:127-128` | `pkgPage.sheetFill.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_sheet.go:156-157, 166-166` | `pkgPage.sheetRows` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_sheet.go:278-278` | `pkgPage.reviewView` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_sheet.go:296-296, 297-298, 299-300` | `pkgPage.reviewView.func` | 3 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_sheet.go:335-335, 336-337, 338-338` | `shortCommit` | 3 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_wiring.go:60-61, 68-69, 81-81, 82-83` | `openRealPackages` | 4 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_wiring.go:99-100, 101-102, 103-103` | `openRealPackages.func` | 4 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_wiring.go:121-122` | `auditRPCURL` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_wiring.go:140-141` | `realPackages.wiring.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_wiring.go:155-156, 157-158, 159-159` | `pacmanPaths` | 4 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `packages_wiring.go:181-182` | `clipText` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `settings.go:226-226, 227-228` | `Window.topgradeCard.func` | 2 | C | Open folder / Choose folder through the real launcher (VM desktop); a fake launcher in the harness would make these A | VM desktop |
| `settings.go:271-272` | `Window.logFolderCard.func` | 1 | C | Open folder / Choose folder through the real launcher (VM desktop); a fake launcher in the harness would make these A | VM desktop |
| `settings.go:288-288` | `blurIndex` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `settings.go:329-330` | `Window.appearanceCard.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `settings.go:347-348` | `Window.chooseDownloads` | 1 | C | Open folder / Choose folder through the real launcher (VM desktop); a fake launcher in the harness would make these A | VM desktop |
| `shell.go:367-367` | `Window.updateStop` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `shot.go:102-103` | `nodeText` | 1 | A | screenshot to an unwritable path (build tag shots) | gui/internal/ui/harness_test.go |
| `summary.go:105-106` | `Window.updateSummary` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `summary.go:194-195, 199-200` | `Window.setSummaryNarrow` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `summary.go:352-355` | `Window.newPackagesColumn` | 3 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `termcols.go:31-32, 36-37` | `Window.measureCols` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:156-157, 166-166, 168-168, 170-170, 188-189, 263-264` | `Window.newRunPage.func` | 6 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:697-698, 699-701` | `Window.output` | 4 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:857-858` | `timeline.sectionOf` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:880-880` | `timeline.neighbour` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:893-894, 897-898` | `Window.focusStep` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:904-905, 910-910, 911-912, 913-913, 914-914, 916-916, 918-919, 919-919` | `Window.focusStep.func` | 11 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `updates_card.go:135-136` | `Window.renderUpdates` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `updates_card.go:216-217` | `Window.checkUpdates.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `updates_card.go:252-253` | `Window.refreshUpdates` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `widedialog.go:166-167, 176-176, 177-178, 183-183, 187-187` | `newWideDialog.func` | 5 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `widedialog.go:266-267` | `amDialog.naturalHeight` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `widedialog.go:345-348` | `amDialog.columns.func` | 4 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `widedialog.go:403-403` | `amDialog.ResponseLabel` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `widedialog.go:415-416` | `amDialog.SetResponseAppearance` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `widedialog.go:431-432, 436-436` | `amDialog.ResponseAppearance` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `widedialog.go:456-457` | `amDialog.DefaultResponse` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `window.go:213-213, 261-262` | `newWindow.func` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |

#### `gui/scripts/nullbroadway`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `main.go:22-25` | `main.func` | 3 | A | the test display server's own protocol errors and disconnects: a fake client sending a bad request | gui/scripts/nullbroadway/*_test.go |
| `main.go:28-30, 38-39` | `main` | 3 | A | the test display server's own protocol errors and disconnects: a fake client sending a bad request | gui/scripts/nullbroadway/*_test.go |
| `server.go:140-140` | `server.serve` | 1 | A | the test display server's own protocol errors and disconnects: a fake client sending a bad request | gui/scripts/nullbroadway/*_test.go |
| `server.go:154-154` | `<package>` | 1 | A | the test display server's own protocol errors and disconnects: a fake client sending a bad request | gui/scripts/nullbroadway/*_test.go |
| `server.go:181-182` | `server.disconnect` | 1 | A | the test display server's own protocol errors and disconnects: a fake client sending a bad request | gui/scripts/nullbroadway/*_test.go |
| `server.go:199-200, 206-207, 209-209` | `server.handle` | 3 | A | the test display server's own protocol errors and disconnects: a fake client sending a bad request | gui/scripts/nullbroadway/*_test.go |
| `server.go:229-230, 235-236` | `unixRights` | 2 | A | the test display server's own protocol errors and disconnects: a fake client sending a bad request | gui/scripts/nullbroadway/*_test.go |
| `server.go:272-273, 286-287, 290-291, 292-293, 294-295, 296-297, 298-299, 301-301, 303-304, 305-306, 307-308, 309-310, 311-311, 321-322, 327-328, 333-334, 341-342, 351-352, 359-360` | `server.request` | 24 | A | the test display server's own protocol errors and disconnects: a fake client sending a bad request | gui/scripts/nullbroadway/*_test.go |
| `server.go:420-422` | `server.focus` | 2 | A | the test display server's own protocol errors and disconnects: a fake client sending a bad request | gui/scripts/nullbroadway/*_test.go |
| `server.go:439-440` | `server.destroy` | 1 | A | the test display server's own protocol errors and disconnects: a fake client sending a bad request | gui/scripts/nullbroadway/*_test.go |
| `server.go:452-453` | `server.grabPointer` | 1 | A | the test display server's own protocol errors and disconnects: a fake client sending a bad request | gui/scripts/nullbroadway/*_test.go |
| `server.go:465-466` | `server.ungrabPointer` | 1 | A | the test display server's own protocol errors and disconnects: a fake client sending a bad request | gui/scripts/nullbroadway/*_test.go |

#### `internal/actions`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `clearcaches.go:73-74, 87-87` | `measureUserCache` | 2 | A | the cache measure/discovery with a fake FS that fails | internal/actions/*_test.go |
| `clearcaches.go:137-137` | `discoverClearCaches` | 1 | A | the cache measure/discovery with a fake FS that fails | internal/actions/*_test.go |
| `clearcaches.go:180-180` | `userSummary` | 1 | A | the cache measure/discovery with a fake FS that fails | internal/actions/*_test.go |

#### `internal/audit`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `audit.go:93-94` | `Config.deadline` | 1 | A | Config with Deadline 0 gets DefaultDeadline | internal/audit/audit_test.go |
| `audit.go:217-218` | `fetch` | 1 | A | RPCURL with a control character fails http.NewRequest | internal/audit/audit_test.go |
| `result.go:88-89` | `Result.save` | 1 | A | a corrupt snapshot's high "maintainer snapshot" finding has no previous entry: commit it with holdHigh | internal/audit/snapshot_test.go |
| `result.go:244-244` | `Result.Note` | 1 | A | corrupt snapshot in a dry run | internal/audit/snapshot_test.go |
| `snapshot.go:181-182` | `WriteSnapshot` | 1 | A | MkdirAll under a regular file fails | internal/audit/snapshot_test.go |
| `snapshot.go:192-193, 198-199` | `WriteSnapshot` | 2 | A | a pre-created lock file in a 0500 dir: the lock opens, the corrupt file's rename aside / CreateTemp fail | internal/audit/snapshot_test.go |
| `snapshot.go:213-214` | `WriteSnapshot` | 1 | A | the snapshot path is a non-empty directory: rename over it fails | internal/audit/snapshot_test.go |
| `snapshot.go:229-230` | `lockSnapshot` | 1 | A | lock file can't be created in a 0500 dir | internal/audit/snapshot_test.go |
| `state.go:71-72` | `WriteState` | 1 | A | MkdirAll under a file / CreateTemp in a 0500 dir / rename onto a directory | internal/audit/state_test.go |
| `state.go:86-87` | `WriteState` | 1 | A | StatePath is a non-empty directory: the rename fails | internal/audit/state_test.go |
| `state.go:154-156` | `Result.writeState` | 2 | A | StatePath under a regular file: the WARN is logged | internal/audit/state_test.go |

#### `internal/aurbuild`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `build.go:183-184, 190-191` | `Fetcher.Queue.func` | 2 | A | a fake fetcher/hasher that fails, an unreadable or oversized file in a reviewed package | internal/aurbuild/*_test.go |
| `build.go:283-284, 299-300` | `hashFile` | 2 | A | a fake fetcher/hasher that fails, an unreadable or oversized file in a reviewed package | internal/aurbuild/*_test.go |
| `fetch.go:314-315` | `cutUTF8` | 1 | A | a fake fetcher/hasher that fails, an unreadable or oversized file in a reviewed package | internal/aurbuild/*_test.go |
| `resolve.go:179-180` | `Resolve` | 1 | A | a fake fetcher/hasher that fails, an unreadable or oversized file in a reviewed package | internal/aurbuild/*_test.go |
| `resolve.go:213-214` | `Resolution.classify` | 1 | A | a fake fetcher/hasher that fails, an unreadable or oversized file in a reviewed package | internal/aurbuild/*_test.go |
| `resolve.go:347-348` | `newInstalledIndex` | 1 | A | a fake fetcher/hasher that fails, an unreadable or oversized file in a reviewed package | internal/aurbuild/*_test.go |
| `review.go:57-58` | `Fetcher.Review` | 1 | A | a fake fetcher/hasher that fails, an unreadable or oversized file in a reviewed package | internal/aurbuild/*_test.go |
| `review.go:106-107` | `readSmallFile` | 1 | A | a fake fetcher/hasher that fails, an unreadable or oversized file in a reviewed package | internal/aurbuild/*_test.go |
| `review.go:172-172, 174-175` | `readFiles` | 3 | A | a fake fetcher/hasher that fails, an unreadable or oversized file in a reviewed package | internal/aurbuild/*_test.go |
| `review.go:212-214` | `fillFile` | 2 | A | a fake fetcher/hasher that fails, an unreadable or oversized file in a reviewed package | internal/aurbuild/*_test.go |
| `reviewed.go:24-25` | `DefaultReviewed` | 1 | A | a fake fetcher/hasher that fails, an unreadable or oversized file in a reviewed package | internal/aurbuild/*_test.go |
| `srcinfo.go:113-114` | `ParseSrcinfo` | 1 | A | a fake fetcher/hasher that fails, an unreadable or oversized file in a reviewed package | internal/aurbuild/*_test.go |

#### `internal/aurhelper`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `aurhelper.go:157-158` | `Deps.TopgradeConfigPaths` | 1 | A | a topgrade config of an odd form, an AUR helper missing from PATH | internal/aurhelper/*_test.go |
| `aurhelper.go:306-306` | `yayBuildDir` | 1 | A | a topgrade config of an odd form, an AUR helper missing from PATH | internal/aurhelper/*_test.go |
| `aurhelper.go:349-349, 360-360` | `parseParuClone` | 2 | A | a topgrade config of an odd form, an AUR helper missing from PATH | internal/aurhelper/*_test.go |

#### `internal/aursearch`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `aursearch.go:95-95` | `Client.Search.func` | 1 | A | a response of an odd form | internal/aursearch/*_test.go |

#### `internal/downloads`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `downloads.go:303-304` | `Scan` | 1 | A | the mountinfoPath seam on a missing file; a 0300 folder (ReadDir fails) | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:328-329` | `oldDownloads` | 1 | A | a folder that opens but can't be listed (0300) | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:359-360` | `entryIsOld.func` | 1 | A | an old directory holding an unreadable (0000) subdirectory counts as new | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:591-592` | `remover.removeTree` | 1 | A | a file in an old 0500 subdirectory (clock moved past the chmod with Options.Now): Remove fails EACCES | internal/downloads/downloads_test.go (security: Dev 3) |

#### `internal/driver`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `outbox.go:148-148` | `outbox.redrawAfter` | 1 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go |
| `pathguard.go:118-118` | `configTool` | 1 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go |
| `refresh.go:61-62` | `RefreshUpdates` | 1 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go |
| `refresh.go:70-71` | `trimOut` | 1 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go |
| `runreport.go:78-79` | `Driver.reportFinish` | 1 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go |

#### `internal/engine/enginetest`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `contract.go:89-90, 91-92, 94-95` | `Checker.Check.func` | 3 | A | the checker's own failure paths: add the missing hand-built breaches | internal/engine/enginetest/contract_test.go |
| `contract.go:165-166, 180-181, 186-187, 201-202, 218-219, 224-224, 226-226, 228-228, 241-242, 249-250, 254-255, 259-260` | `Checker.Check` | 12 | A | the checker's own failure paths: add the missing hand-built breaches | internal/engine/enginetest/contract_test.go |

#### `internal/engine`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `packages.go:145-146` | `packagesQuestion` | 1 | A | a combined queue whose parts are all declined, finish out of order, or exit without having run | internal/engine/combine_test.go |

#### `internal/frontend`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `topgrade.go:85-86` | `EnsureTopgradeConfigWith` | 1 | A | Lstat error other than not-exist (0000 parent), MkdirAll under a file, the file appearing between Lstat and OpenFile (O_EXCL), a 0500 dir | internal/frontend/topgrade_test.go |

#### `internal/health`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `health.go:225-226` | `statFS` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `ignore.go:132-133, 141-142, 145-146` | `IgnoreStore.load` | 3 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `ignore.go:217-218` | `IgnoreStore.write` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `kernel_disk.go:27-29` | `checkRestart` | 2 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `news.go:218-219` | `readNewsCache` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `news.go:237-238` | `writeNewsCache` | 1 | A | the cache dir a symlink, a file or someone else's (ownedPrivate); a read-only parent | internal/health/cachehard_test.go |
| `news.go:395-396` | `httpGetLimited` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `packages.go:51-52` | `localForeign` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `packages.go:70-71` | `aurInfo` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `packages.go:265-266` | `readHeld` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `packages.go:281-281` | `heldIn` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `packages.go:331-332` | `auditSeen` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `packages.go:410-411` | `checkMirror` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `packages.go:508-508` | `firstMirror` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `pacmanlog.go:34-36` | `openLogTail` | 2 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `pacmanlog.go:74-74` | `LastFullUpgrade` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `pacmanlog.go:107-108` | `pacFiles` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `system.go:70-71, 78-79` | `listProcsAt` | 2 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `system.go:99-100` | `procHidden` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `system.go:134-135` | `inGroup` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `system.go:144-145` | `readUptime` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `system.go:288-288` | `parseCoredumps` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |
| `units.go:113-114` | `journalFor` | 1 | A | a fake seam (Query, FS, HTTPGet, a temp dir) that fails, a file that is unreadable or hostile | internal/health/*_test.go |

#### `internal/helperclient`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `pkgplan.go:127-127, 131-131` | `CheckPkgPlan` | 2 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `pkgplan.go:220-221` | `modeName` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `pkgplan.go:232-233` | `PlanPackages` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `pkgplan.go:254-255, 257-258` | `planPackages` | 2 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `pkgplan.go:329-330, 361-362` | `decoder.readPkgPlan` | 2 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `plan.go:132-133` | `PlanOrphans` | 1 | C | runs the installed /usr/lib/archmaid/archmaid-helper plan-orphans, which runs the real pacman -Qdtq/-Rs --print | VM (security: Dev 3) |
| `protected.go:27-28` | `Protected` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `protected.go:37-38, 40-41` | `protectedNames` | 2 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `protected.go:77-78, 90-91` | `decoder.readProtected` | 3 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |

#### `internal/logstore`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `logstore.go:247-248` | `freeRun` | 1 | A | a store with an error, a run dir under a regular file, a log path that is a directory | internal/logstore/logstore_test.go |

#### `internal/pkgdb`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `open.go:131-132` | `repoNames` | 1 | A | a corrupt or truncated sync db / desc, an unreadable repo dir; pkgdbtest's own helper errors | internal/pkgdb/*_test.go |
| `open.go:240-241` | `decompress` | 1 | A | a corrupt or truncated sync db / desc, an unreadable repo dir; pkgdbtest's own helper errors | internal/pkgdb/*_test.go |
| `satisfy.go:59-59, 61-61, 63-63, 65-65` | `meets` | 4 | A | a corrupt or truncated sync db / desc, an unreadable repo dir; pkgdbtest's own helper errors | internal/pkgdb/*_test.go |

#### `internal/pkgdb/pkgdbtest`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `pkgdbtest.go:31-32, 36-37, 42-43` | `WriteSync` | 3 | A | a corrupt or truncated sync db / desc, an unreadable repo dir; pkgdbtest's own helper errors | internal/pkgdb/*_test.go |
| `pkgdbtest.go:82-83, 85-86` | `write` | 2 | A | a corrupt or truncated sync db / desc, an unreadable repo dir; pkgdbtest's own helper errors | internal/pkgdb/*_test.go |

#### `internal/privilege`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `configguard.go:109-109, 116-117, 121-122` | `configRisk` | 3 | A | a sudoers/config line with an odd form (empty, comment-only, a quote that never closes): a table row each | internal/privilege/configguard_test.go (security: Dev 3) |
| `configguard.go:144-145` | `cleanValue` | 1 | A | a sudoers/config line with an odd form (empty, comment-only, a quote that never closes): a table row each | internal/privilege/configguard_test.go (security: Dev 3) |

#### `internal/proc`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `lines.go:114-114, 117-118` | `emitLines` | 3 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `proc.go:85-86` | `checkSudo` | 1 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `proc.go:149-149, 154-154, 158-158` | `envCommand` | 3 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `proc.go:387-388` | `Runner.Start` | 1 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `proc.go:475-476` | `Child.GroupStopped` | 1 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `proc.go:498-498` | `Child.escalateGroup` | 1 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `procfs.go:40-41, 44-45, 51-52, 57-58` | `readAt` | 4 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `pty.go:73-74, 90-92, 98-101` | `openPty` | 6 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `pty.go:81-82` | `openPty.func` | 1 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `pty.go:111-112` | `<package>` | 1 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `pty.go:127-128` | `peer` | 1 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |

#### `internal/report`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `collector.go:93-94` | `runningKernel` | 1 | A | an unreadable pacman.log, a walk that hits an unreadable entry, odd versions | internal/report/*_test.go |
| `collector.go:105-105` | `userCacheDir` | 1 | A | an unreadable pacman.log, a walk that hits an unreadable entry, odd versions | internal/report/*_test.go |
| `measure.go:75-75, 80-80, 84-85` | `walk` | 3 | A | an unreadable pacman.log, a walk that hits an unreadable entry, odd versions | internal/report/*_test.go |
| `measure.go:96-98` | `addFile` | 2 | A | an unreadable pacman.log, a walk that hits an unreadable entry, odd versions | internal/report/*_test.go |
| `pacmanlog.go:69-70, 86-87` | `ReadLog` | 2 | A | an unreadable pacman.log, a walk that hits an unreadable entry, odd versions | internal/report/*_test.go |
| `pacmanlog.go:227-227` | `versionLess` | 1 | A | an unreadable pacman.log, a walk that hits an unreadable entry, odd versions | internal/report/*_test.go |
| `restart.go:110-110` | `KernelCheck` | 1 | A | an unreadable pacman.log, a walk that hits an unreadable entry, odd versions | internal/report/*_test.go |

#### `internal/sysstat`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `sysstat.go:113-113` | `meminfoKB` | 1 | A | a meminfo fixture without the key | internal/sysstat/sysstat_test.go |
| `sysstat.go:146-146` | `diskBytes` | 1 | A | a meminfo fixture without the key | internal/sysstat/sysstat_test.go |

#### `internal/testenv`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `orphans.go:27-30, 31-32, 33-34, 35-35, 36-38, 39-39, 40-40, 41-43, 46-46, 47-48, 49-49, 51-52, 54-56, 57-58, 59-59` | `reapLeftovers` | 24 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `orphans.go:64-65, 66-67, 68-70, 71-72, 73-73` | `report` | 8 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `orphans.go:79-79, 80-82` | `reap` | 3 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `orphans.go:89-90, 91-92, 93-95, 96-97, 98-98, 100-101, 102-102, 104-104, 105-105, 107-107, 108-109, 111-111, 112-113, 127-128, 129-129, 130-130, 132-132, 133-134, 137-137` | `leftovers` | 25 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `orphans.go:114-114, 115-115, 116-117, 118-119, 120-120, 121-122, 123-123, 125-125, 136-136, 136-136` | `leftovers.func` | 11 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `orphans.go:173-174, 175-176, 177-178, 179-179, 180-181, 183-183` | `hasMarker` | 8 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `orphans.go:188-189, 190-191, 192-192` | `cmdline` | 4 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `testenv.go:289-290, 298-299` | `guardScript` | 2 | A | Run from a dir with no scripts/state-guard.sh above it; a guard script that fails | internal/testenv/trap_test.go |
| `testenv.go:312-313` | `guardSnapshot` | 1 | A | Run from a dir with no scripts/state-guard.sh above it; a guard script that fails | internal/testenv/trap_test.go |
| `testenv.go:321-323` | `guardCompare` | 2 | A | post-m.Run: counted only from a direct test | internal/testenv/trap_test.go |
| `watchdog.go:76-79` | `watchdog.stop` | 3 | A | post-m.Run in Run (counted never, after the counters are written); a direct call in a test counts | internal/testenv/trap_test.go |

#### `internal/testtranscript`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `recorder.go:69-70` | `NewRecorder` | 1 | A | fake targets failing Start/Next/Reply and an odd replace list | internal/testtranscript/recorder_test.go |
| `recorder.go:91-92, 106-107` | `Recorder.Run` | 2 | A | fake targets failing Start/Next/Reply and an odd replace list | internal/testtranscript/recorder_test.go |
| `recorder.go:201-201` | `Recorder.event` | 1 | A | fake targets failing Start/Next/Reply and an odd replace list | internal/testtranscript/recorder_test.go |
| `recorder.go:232-233` | `Recorder.reply` | 1 | A | fake targets failing Start/Next/Reply and an odd replace list | internal/testtranscript/recorder_test.go |

#### `internal/version`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `version.go:27-28` | `String` | 1 | A | a build without module info | internal/version/*_test.go |

#### `internal/vt`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `vt.go:459-459` | `Screen.stringByte` | 1 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:530-530, 532-532` | `Screen.param` | 2 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:540-541` | `atoi` | 1 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:586-587` | `Screen.bound` | 1 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:688-689` | `Screen.putRune` | 1 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |

#### `gui/cmd/archmaid-gui`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `main.go:all` | `main` | 6 | B | the GUI binary's main: in no run's data at all (no test links it); a headless start of the cover archmaid-gui counts it | gui/scripts/headless.sh + cover archmaid-gui (new) |

<!-- /gen:blocks -->
