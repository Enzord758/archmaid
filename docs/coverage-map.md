# Coverage map

Every statement the tests leave uncovered, sorted by what it takes to cover
it, the exemption list for what can't be covered, the work packages that
turn the rest into tests, and the tooling that lets runs outside `go test`
count. Base: `8bf5d3a` (origin/refactor/p0). Machine-readable copies, one
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
#   e2e/   cmd/archmaid/e2e/run.sh with E2E_COVER_DIR: the nine checks on a cover archmaid
#   gui/   gui/scripts/headless-cover.sh: the GUI's plain tests and every headless display group
#   merged/  scripts/cover-merge.sh of all three: summary.txt now has raw and reachable per scope
go run ./scripts/covblocks -json "$COVER_OUT/merged/profile.txt" > blocks.json
python3 -I scripts/covmap/classify.py blocks.json classified.json
python3 -I scripts/covmap/report.py classified.json "$COVER_OUT/merged/summary.txt" "$(git rev-parse HEAD)" docs
#   writes docs/coverage-map.json, docs/coverage-exempt.{tsv,md} and the generated regions of this file
```

The scopes: **core** is check.sh's set (`cmd/archmaid` + `internal/...`,
the `MIN_TOTAL` floor), **helper** is `cmd/archmaid-helper/...` (the gate
doesn't measure it), and **gui** is the `gui/` module (the gate doesn't
measure it either). `gui/cmd/archmaid-gui` (6 statements) is linked by no
test, so it appears in no run's data; the map counts it in by hand.

What each kind of run adds, for core (statements 5918; the discovery change
and the new GUI work added about 300 since the first map):

| Run | core |
|---|---|
| `check.sh` (`go test -coverprofile`, no children) | 92.1 % |
| `cover-unit.sh` (the same tests, plus the re-executed children: the TTY child running the real `tui.Run`, the askpass relay) | 92.1 % (5455/5918) |
| + the e2e tapes on the cover binary (`main`, the real startup) | 92.8 % (5496/5918) |
| + the GUI's tests (they drive `internal/engine`, `driver`, `frontend` and more through the GUI) | 92.9 % (5501/5918) |

helper: 87.3 % (774/886), unit tests only. gui: 93.0 % (3924/4219) with the
headless display tests.

Two things the numbers hide:

- **testenv's own checks never count.** `internal/testenv` is 36.9 %
  covered (145 statements uncovered), but most of it runs in every package
  of every test run: the orphan reaper, the trap report and the state-guard
  compare run in `TestMain` after `m.Run`, and the testing package writes its
  counters when `m.Run` returns. A direct unit test of those functions (inside
  `m.Run`) counts them; that is work package 1.
- **Test-only code is in the denominator.** `testenv`, `testtranscript`,
  `enginetest`, `privtest` and `helpertest` (489 statements, 323 covered)
  ship in no binary. Without them core is **95.4 %** (5178/5429). The gate
  could measure shipped code only (drop them from `COVERPKG`) and hold the
  test helpers to their own floor; that is a decision for the lead.

## Totals

Raw is covered / total; reachable is covered / (total - E), where E is the
exempt list (`coverage-exempt.md`). Both come out of `scripts/covblocks
-reachable -exempt docs/coverage-exempt.tsv`.

<!-- gen:totals -->
| Scope | Statements | Covered | Raw | Uncovered | A unit | B e2e | C vm | D owner | E exempt | Reachable (covered / (total - E)) |
|---|---|---|---|---|---|---|---|---|---|---|
| core | 5918 | 5501 | 92.9 % | 417 | 323 | 5 | 1 | 0 | 88 | 94.3 % (5501/5830) |
| helper | 886 | 774 | 87.3 % | 112 | 59 | 52 | 0 | 0 | 1 | 87.4 % (774/885) |
| gui | 4225 | 3924 | 92.8 % | 301 | 208 | 39 | 22 | 0 | 32 | 93.5 % (3924/4193) |
| all | 11029 | 10199 | 92.4 % | 830 | 590 | 96 | 23 | 0 | 121 | 93.5 % (10199/10908) |

Raw / reachable, cumulative:

| Scope | Now (unit + e2e + GUI) | after A | after A+B | after A+B+C | after A+B+C+D (= every reachable statement) |
|---|---|---|---|---|---|
| core | 92.9 % / 94.3 % | 98.4 % / 99.8 % | 98.4 % / 99.9 % | 98.5 % / 100.0 % | 98.5 % / 100.0 % |
| helper | 87.3 % / 87.4 % | 94.0 % / 94.1 % | 99.8 % / 100.0 % | 99.8 % / 100.0 % | 99.8 % / 100.0 % |
| gui | 92.8 % / 93.5 % | 97.7 % / 98.5 % | 98.7 % / 99.4 % | 99.2 % / 100.0 % | 99.2 % / 100.0 % |
| all | 92.4 % / 93.5 % | 97.8 % / 98.9 % | 98.6 % / 99.7 % | 98.9 % / 100.0 % | 98.9 % / 100.0 % |
<!-- /gen:totals -->

D is 0 statements: every line the owner's checks exercise is reachable by
A-C too. The owner's checks are about behaviour (below), not about the
number.

## The path to 100 % of reachable

1. **A (work packages 1-6):** unit tests and headless GUI scenarios, each
   block names its test file. The biggest areas are `internal/testenv` (123
   direct tests of the post-`m.Run` checks), `gui/internal/ui` (new headless
   scenarios) and the helper's fake-runner error paths.
2. **B (work packages 7-8):** the helper's `main` and `hardenProcess`
   (a cover-helper check in `run.sh`, the "helper check" below), the GUI
   binary's entry (a headless start of the cover `archmaid-gui`, real and
   `--stub` with each script), and `tui.Run`'s error exit and the editor
   handover.
3. **C (23):** the VM tester's runs (below).
4. **E ([`coverage-exempt.md`](coverage-exempt.md)):** leave the denominator
   by being listed. `make-testable` entries (the shared atomic-write helper,
   the downloads TOCTOU hook) move to A once the seam lands; `remove` entries
   are deleted. Every `keep-exempt` entry is a branch someone reviewed and
   decided to keep.

## Work packages

Every A and B statement is in exactly one package. Gain is statements over
the all-scope total (11029 with the hand-counted GUI main), in percentage
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
| 1 | **Test infrastructure** (A) | 141 | +1.2 pts | architecture | no | testenv's post-`m.Run` checks (orphan reaper, trap report, state-guard compare) called directly inside a test, the recorder's fake targets, the contract checker's own failure paths. By package: `testenv` 123, `engine/enginetest` 13, `testtranscript` 5 |
| 2 | **Helper and privilege edge cases** (A) | 96 | +0.8 pts | security | 96 stmts: Dev 3 reviews | the helper's fake-runner/Stat/Exe error paths and lock edges, helperclient's malformed and failing helper output, privilege's askpass relays, faillock and kept-password failures. By package: `cmd/archmaid-helper/helper` 59, `helperclient` 21, `privilege` 16 |
| 3 | **Core: TUI keys, driver, engine, actions, logstore** (A) | 103 | +0.9 pts | architecture (driver/keep.go and the askpass bits: security) | 10 stmts: Dev 3 reviews | per-screen key handling and Finished/PromptClosed edges in the TUI, the driver's teardown, hang-up and agent paths, combined-queue edges, discovery outputs, the log store's error paths. By package: `tui` 43, `driver` 27, `actions` 15, `engine` 7, `logstore` 6, `frontend` 5 |
| 4 | **Leaf packages and the polkit agent** (A) | 79 | +0.7 pts | performance-QA (polkitagent, realdriver, downloads: security) | 52 stmts: Dev 3 reviews | audit/downloads write and scan error paths, proc argv forms, sanitize and sysstat edges, polkitagent and realdriver against fake agents. By package: `gui/polkitagent` 23, `downloads` 15, `gui/realdriver` 14, `audit` 13, `proc` 10, `sanitize` 2 |
| 5 | **GUI logs, timeline and signals pages** (A) | 85 | +0.7 pts | GUI | no | new headless scenarios: log page states (none, unreadable, empty), the timeline, signals mid-run, the failed-step view, logview and overview edges. By package: `gui/ui` 78, `gui/logview` 6, `gui/overview` 1 |
| 6 | **GUI windows, dialogs, settings, run view** (A) | 86 | +0.7 pts | GUI | no | window/dialog/settings-page scenarios, the run view's SGR and ring edges, settings.Save error paths, shell and usercache cells. By package: `gui/ui` 54, `gui/runview` 25, `gui/settings` 7 |
| 7 | **E2E: the cover-helper check** (B) | 52 | +0.4 pts | performance-QA with Dev 3's review (helper) | 52 stmts: Dev 3 reviews | main and hardenProcess of the helper binary: the run.sh check of the cover helper as the user and as uid 0 in an unprivileged user namespace (both refused before anything runs). By package: `cmd/archmaid-helper` 50, `cmd/archmaid-helper/coverdir` 1, `cmd/archmaid-helper/helper` 1 |
| 8 | **E2E: binary entries** (B) | 44 | +0.3 pts | GUI (the TUI tape: architecture) | no | archmaid-gui's main, `--stub` with each script (headless), the TUI's error exit and the editor handover on a pty tape. By package: `gui/ui` 33, `gui/cmd/archmaid-gui` 6, `cmd/archmaid` 3, `tui` 2 |
| | **A and B together** | 686 | +6.2 pts | | | |
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
   (`ui/launcher.go`, 23 of the 24 C statements).
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
scripts/cover-merge.sh -o "$(mktemp -d)" "$COVER_OUT/merged/covdata" "$out/var/lib/archmaid-cover" "$out/home/enzord/archmaid-cover"
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
| `cmd/archmaid-helper` |  | 50 |  |  | 1 |
| `cmd/archmaid-helper/internal/coverdir` |  | 1 |  |  |  |
| `cmd/archmaid-helper/internal/helper` | 59 | 1 |  |  |  |
| `cmd/archmaid` |  | 3 |  |  | 3 |
| `gui/cmd/archmaid-gui` |  | 6 |  |  |  |
| `gui/internal/logview` | 6 |  |  |  |  |
| `gui/internal/overview` | 1 |  |  |  |  |
| `gui/internal/polkitagent` | 23 |  |  |  | 18 |
| `gui/internal/realdriver` | 14 |  |  |  |  |
| `gui/internal/runview` | 25 |  |  |  |  |
| `gui/internal/settings` | 7 |  |  |  | 6 |
| `gui/internal/ui` | 132 | 33 | 22 |  | 8 |
| `internal/actions` | 15 |  |  |  |  |
| `internal/audit` | 13 |  |  |  | 17 |
| `internal/downloads` | 15 |  |  |  | 15 |
| `internal/driver` | 27 |  |  |  | 2 |
| `internal/engine` | 7 |  |  |  |  |
| `internal/engine/enginetest` | 13 |  |  |  |  |
| `internal/engine/stub` |  |  |  |  | 1 |
| `internal/frontend` | 5 |  |  |  |  |
| `internal/helperclient` | 21 |  | 1 |  |  |
| `internal/helperclient/helpertest` |  |  |  |  | 2 |
| `internal/logstore` | 6 |  |  |  | 8 |
| `internal/privilege` | 16 |  |  |  | 8 |
| `internal/privilege/privtest` |  |  |  |  | 1 |
| `internal/proc` | 10 |  |  |  | 1 |
| `internal/sanitize` | 2 |  |  |  |  |
| `internal/sysstat` | 2 |  |  |  |  |
| `internal/testenv` | 123 |  |  |  | 22 |
| `internal/testtranscript` | 5 |  |  |  |  |
| `internal/ttyguard` |  |  |  |  | 4 |
| `internal/tui` | 43 | 2 |  |  | 4 |
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
| `main.go:23-24, 25-27, 28-32, 33-34, 35-35` | `main` | 26 | B | main only runs in the binary: a cover helper started as the user with a root verb is refused before it runs anything (verified under a scratch HOME); the root path is the VM's | e2e (new helper check) (security: Dev 3) |

#### `cmd/archmaid-helper/internal/coverdir`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `coverdir.go:26-27` | `init` | 1 | B | only a cover helper running as root sets it: verified under unshare -r with a scratch HOME (coverage-map.md, helper check); in the VM every pkexec run does | e2e (new helper check) (security: Dev 3) |

#### `cmd/archmaid-helper/internal/helper`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `checks.go:32-33, 34-35, 36-37, 38-39, 40-40` | `LstatFile` | 7 | A | a plain os.Lstat wrapper: call it on a temp file, a missing path | cmd/archmaid-helper/internal/helper/checks_test.go (security: Dev 3) |
| `checks.go:82-83` | `CheckInstall` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `checks.go:124-125, 128-129, 135-135` | `acquireLock` | 3 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `checks.go:153-154` | `pacmanIdle` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `helper.go:67-75` | `DefaultOptions` | 1 | B | only main uses it: covered by any cover-helper run | e2e (new helper check) (security: Dev 3) |
| `helper.go:103-104` | `Verbs.func` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `helper.go:133-134, 150-151` | `Run` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `helper.go:191-191` | `session.lock` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `helper.go:206-207` | `session.step` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `helper.go:274-275, 284-285` | `runCheckFiles` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `helper.go:319-320` | `session.checkInstalled` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `helper.go:360-361` | `session.checkPackages` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `helper.go:424-425` | `runPlanOrphans` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `helper.go:463-464, 467-468, 484-485` | `session.removeOrphans` | 3 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `helper.go:532-533` | `runRepair` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `helper.go:554-555` | `session.repairable` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `helper.go:592-593` | `session.vetRepair` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `helper.go:693-693, 697-697` | `qkSummaries` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `helper.go:713-714` | `missingFiles` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `output.go:264-264` | `skipEscape` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `plan.go:45-46, 53-53, 68-69` | `ComputePlan` | 3 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `plan.go:99-99` | `firstLine` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `runner.go:85-85` | `exitStatus` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `runner.go:108-109, 112-113` | `ExecRunner.Stream` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `runner.go:151-151, 152-153, 154-154` | `SplitLines` | 3 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `runner.go:169-169, 170-170, 171-172, 173-173, 175-175` | `capped.Write` | 5 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `units.go:23-24, 38-39, 48-49, 77-78` | `runDisableUnit` | 4 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `units.go:112-113, 119-120, 153-154` | `session.vetInstall` | 3 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `units.go:174-175` | `session.vetTriggers` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `units.go:195-196` | `orUnknown` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `units.go:237-238, 240-241` | `session.unitState` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `units.go:271-272` | `session.readInstall` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |

#### `cmd/archmaid`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `main.go:63-66` | `main` | 3 | B | tui.Run's error exit: a new e2e run of the binary without a terminal (Bubble Tea can't open one), under the usual scratch HOME | cmd/archmaid/e2e/run.sh |

#### `gui/internal/logview`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `logview.go:53-54` | `Run.Subtitle` | 1 | A | a missing state dir, an unreadable one, a stray name and a DST-gap stamp, an empty run | gui/internal/logview/logview_test.go |
| `logview.go:64-65, 71-72, 86-86, 95-95` | `Runs` | 4 | A | a missing state dir, an unreadable one, a stray name and a DST-gap stamp, an empty run | gui/internal/logview/logview_test.go |
| `logview.go:119-120` | `Run.Read` | 1 | A | a missing state dir, an unreadable one, a stray name and a DST-gap stamp, an empty run | gui/internal/logview/logview_test.go |

#### `gui/internal/overview`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `overview.go:137-138` | `Summarize` | 1 | A | a dry-run last-run record | gui/internal/overview/overview_test.go |

#### `gui/internal/polkitagent`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `polkitagent.go:133-134` | `Agent.otherHolders` | 1 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go (security: Dev 3) |
| `polkitagent.go:170-171, 177-178, 235-238, 255-255, 256-257` | `Start` | 8 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go (security: Dev 3) |
| `polkitagent.go:268-268` | `discard` | 1 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go (security: Dev 3) |
| `polkitagent.go:289-290` | `Agent.Stop.func` | 2 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go (security: Dev 3) |
| `polkitagent.go:427-428, 453-454, 471-472, 482-482` | `Agent.drain` | 4 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go (security: Dev 3) |
| `polkitagent.go:495-496` | `Agent.sendEOF` | 1 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go (security: Dev 3) |
| `polkitagent.go:530-530` | `Prompt.Err` | 1 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go (security: Dev 3) |
| `polkitagent.go:572-574` | `Prompt.Answer.func` | 2 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go (security: Dev 3) |
| `polkitagent.go:600-600, 601-602, 603-603` | `Agent.waitNoEcho` | 3 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go (security: Dev 3) |

#### `gui/internal/realdriver`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `agent.go:57-58, 60-61, 70-71, 74-76` | `Wiring.helper.func` | 8 | A | a fake polkit agent that keeps ending or fails to start; no XDG_RUNTIME_DIR in Config | gui/internal/realdriver/agent_test.go (security: Dev 3) |
| `agent.go:95-96, 99-100, 103-104` | `Wiring.startAgent` | 3 | A | a fake polkit agent that keeps ending or fails to start; no XDG_RUNTIME_DIR in Config | gui/internal/realdriver/agent_test.go (security: Dev 3) |
| `realdriver.go:138-139, 143-144` | `Start` | 2 | A | a fake polkit agent that keeps ending or fails to start; no XDG_RUNTIME_DIR in Config | gui/internal/realdriver/agent_test.go (security: Dev 3) |
| `realdriver.go:215-216` | `Wiring.env` | 1 | A | Options() with no fake PlanOrphans picks helperclient.PlanOrphans (assignment only; running it is C) | gui/internal/realdriver/realdriver_test.go (security: Dev 3) |

#### `gui/internal/runview`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `ansi.go:102-103` | `ParseSGR` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `ansi.go:175-176, 178-178` | `applySGR` | 2 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `ansi.go:258-259` | `extColor.func` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `ansi.go:276-276` | `extColor` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `ansi.go:283-284, 294-295, 299-299` | `extColorSemi` | 3 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `gate.go:54-54` | `GateNotes` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `model.go:130-131` | `lockText` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `model.go:390-393` | `Model.Apply` | 3 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `model.go:538-538` | `Model.lastText` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `model.go:691-692, 703-705` | `Model.popRuns` | 3 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `palette.go:64-65, 66-67, 68-68` | `Dank16Path` | 4 | A | XDG_CACHE_HOME empty: the ~/.cache fallback, and HOME unset too (t.Setenv) | gui/internal/runview/palette_test.go |
| `palette.go:131-132` | `parseHex` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `scripts.go:89-90` | `FloodSteps` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `stepline.go:14-15` | `Model.StepLine` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `stepline.go:51-52` | `Model.seqLine` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |

#### `gui/internal/settings`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `settings.go:59-60, 66-67` | `Load` | 2 | A | no XDG_CONFIG_HOME nor HOME, an unreadable gui.json, a read-only config dir, a rename onto a directory, a bad downloads folder | gui/internal/settings/settings_test.go |
| `settings.go:83-84, 87-88, 91-92, 108-109` | `Save` | 4 | A | no XDG_CONFIG_HOME nor HOME, an unreadable gui.json, a read-only config dir, a rename onto a directory, a bad downloads folder | gui/internal/settings/settings_test.go |
| `settings.go:146-146` | `CheckDownloads` | 1 | A | no XDG_CONFIG_HOME nor HOME, an unreadable gui.json, a read-only config dir, a rename onto a directory, a bad downloads folder | gui/internal/settings/settings_test.go |

#### `gui/internal/ui`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `app.go:109-110, 111-113, 114-114, 115-116, 117-117` | `Main` | 7 | B | the binary's entry: start the cover archmaid-gui under headless.sh with a scratch HOME (a GUI e2e; the harness calls Run, not Main) | gui/scripts/headless.sh + cover archmaid-gui (new) |
| `app.go:175-176, 178-180, 182-182, 183-185, 186-186` | `mainReal` | 10 | B | the binary's entry: start the cover archmaid-gui under headless.sh with a scratch HOME (a GUI e2e; the harness calls Run, not Main) | gui/scripts/headless.sh + cover archmaid-gui (new) |
| `app.go:176-176` | `mainReal.func` | 1 | B | the binary's entry: start the cover archmaid-gui under headless.sh with a scratch HOME (a GUI e2e; the harness calls Run, not Main) | gui/scripts/headless.sh + cover archmaid-gui (new) |
| `app.go:192-192, 193-193, 193-193, 195-195, 196-196, 196-196, 206-206, 207-210` | `stubDriver.func` | 10 | B | archmaid-gui --stub with each ARCHMAID_GUI_SCRIPT, headless (the harness builds its stub drivers itself) | gui/scripts/headless.sh + cover archmaid-gui (new) |
| `app.go:198-198, 200-200, 202-202, 204-204, 212-212` | `stubDriver` | 5 | B | archmaid-gui --stub with each ARCHMAID_GUI_SCRIPT, headless (the harness builds its stub drivers itself) | gui/scripts/headless.sh + cover archmaid-gui (new) |
| `dialogs.go:42-42` | `Window.prompt` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `dialogs.go:56-57` | `Window.answer` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `dialogs.go:74-75, 86-87, 112-113` | `Window.confirmDialog` | 4 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `dialogs.go:335-335, 336-337, 338-339` | `Window.passwordDialog.func` | 4 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `dialogs.go:416-417` | `Window.selectionDialog` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `dialogs.go:446-447` | `Window.selectionDialog.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `failed.go:67-68` | `Window.failedRow` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `failed.go:90-91` | `Window.drawFailed` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `failed.go:131-131, 132-132, 133-134, 136-136, 153-153` | `runFolder` | 5 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `failed.go:170-171` | `Window.redrawHeaders` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `launcher.go:37-37, 38-39, 40-41, 46-46` | `gtkLauncher.launch` | 5 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:42-42, 43-44` | `gtkLauncher.launch.func` | 2 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:49-49` | `gtkLauncher.OpenFile` | 1 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:50-50` | `gtkLauncher.OpenFolder` | 1 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:53-54, 55-56` | `gtkLauncher.ChooseFolder` | 3 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:57-57, 58-59, 60-62, 63-63` | `gtkLauncher.ChooseFolder.func` | 6 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `logs.go:72-72, 73-75, 88-88, 100-100, 101-102, 115-116, 143-144, 151-152, 183-184, 208-208, 226-226` | `Window.newLogs.func` | 12 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `logs.go:241-242, 249-249, 251-251` | `entryClass` | 3 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `logs.go:270-273` | `Window.refreshLogs` | 3 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `logs.go:328-328, 329-329, 330-331, 337-341` | `Window.showRun` | 7 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `logs.go:357-357, 359-359` | `Window.openLog` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `logs.go:396-397` | `Window.toggleSection` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `overview.go:163-163` | `cardByID` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `overview.go:298-299` | `Window.lastRunFolder` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `overview.go:377-378` | `Window.refreshLastRun` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `overview.go:422-423` | `Window.updateOverview` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `settings.go:120-121` | `Window.newSettings` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `settings.go:206-206, 207-208` | `Window.topgradeCard.func` | 2 | C | Open folder / Choose folder through the real launcher (VM desktop); a fake launcher in the harness would make these A | VM desktop |
| `settings.go:216-219, 223-224` | `Window.topgradeCard` | 4 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `settings.go:231-233, 235-237` | `Window.openTopgradeConfig` | 4 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `settings.go:251-252` | `Window.logFolderCard.func` | 1 | C | Open folder / Choose folder through the real launcher (VM desktop); a fake launcher in the harness would make these A | VM desktop |
| `settings.go:266-267` | `Window.chooseDownloads` | 1 | C | Open folder / Choose folder through the real launcher (VM desktop); a fake launcher in the harness would make these A | VM desktop |
| `settings.go:297-298` | `Window.applyDownloads` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `settings.go:310-312` | `Window.saveSettings` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `shell.go:148-148, 153-153, 154-155` | `Window.buildShell.func` | 3 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `shell.go:207-208` | `Window.navKey` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `shell.go:218-219` | `Window.navPress` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `shell.go:281-282` | `Window.updateRunBadge` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `shot.go:58-59` | `savePNG` | 1 | A | screenshot to an unwritable path (build tag shots) | gui/internal/ui/harness_test.go |
| `signals.go:74-77` | `signals.run.func` | 3 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `signals.go:123-123` | `abortedOf` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `signals.go:127-130` | `closedCh` | 3 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `signals.go:138-139, 142-144` | `Window.quitOnSignal` | 3 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:119-119, 120-121, 129-130, 166-166, 167-168, 169-169, 177-177, 179-179, 181-181` | `Window.newRunPage.func` | 9 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:278-278` | `Window.rowOf` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:284-285, 289-290` | `Window.drawHeader` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:355-355` | `timeline.locate` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:362-363` | `timeline.sectionAt` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:401-402, 406-407` | `Window.insert` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:421-426` | `Window.sectionFor` | 6 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:433-434, 437-438` | `Window.wantOpen` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:490-491` | `Window.replaceList` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:526-527` | `Window.foldNow` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:539-542` | `Window.toggleStep` | 3 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:567-569` | `Window.output` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:695-696` | `fmtClock` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `usercache.go:31-32` | `userCacheDir` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `usercache.go:104-105, 108-109` | `Window.measureUserCache` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `usercache.go:126-127` | `Window.measureUserCache.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `window.go:137-137, 170-171` | `newWindow.func` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `window.go:244-245` | `Window.start` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `window.go:300-303` | `Window.send.func` | 3 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `window.go:315-315` | `pumpDone` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `window.go:362-364` | `Window.redraw` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `window.go:436-437, 460-461` | `Window.statusText` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `window.go:479-480, 491-492, 522-524` | `Window.closeRequest` | 4 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |

#### `internal/actions`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `downloads.go:43-45` | `discoverDownloads` | 2 | A | the downloads discovery's edge outputs: a Scan error notice, more than the listed items, an age-unknown or recently-changed or other-filesystem entry, nothing older than 30 days | internal/actions/downloads_test.go |
| `downloads.go:108-109, 114-114, 118-118, 128-128` | `downloadsChecked` | 5 | A | the downloads discovery's edge outputs: a Scan error notice, more than the listed items, an age-unknown or recently-changed or other-filesystem entry, nothing older than 30 days | internal/actions/downloads_test.go |
| `services.go:49-49` | `splitInactiveServices` | 1 | A | a unit listing with a blank line, systemctl failing on is-enabled, more than three skip rules | internal/actions/services_test.go |
| `services.go:81-83, 84-84, 85-86, 88-88` | `enabledServices` | 6 | A | a unit listing with a blank line, systemctl failing on is-enabled, more than three skip rules | internal/actions/services_test.go |
| `services.go:188-189` | `skipRules` | 1 | A | a unit listing with a blank line, systemctl failing on is-enabled, more than three skip rules | internal/actions/services_test.go |

#### `internal/audit`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `audit.go:91-92` | `Config.deadline` | 1 | A | Config with Deadline 0 gets DefaultDeadline | internal/audit/audit_test.go |
| `audit.go:215-216` | `fetch` | 1 | A | RPCURL with a control character fails http.NewRequest | internal/audit/audit_test.go |
| `result.go:88-89` | `Result.save` | 1 | A | a corrupt snapshot's high "maintainer snapshot" finding has no previous entry: commit it with holdHigh | internal/audit/snapshot_test.go |
| `result.go:244-244` | `Result.Note` | 1 | A | corrupt snapshot in a dry run | internal/audit/snapshot_test.go |
| `snapshot.go:181-182` | `WriteSnapshot` | 1 | A | MkdirAll under a regular file fails | internal/audit/snapshot_test.go |
| `snapshot.go:192-193, 198-199` | `WriteSnapshot` | 2 | A | a pre-created lock file in a 0500 dir: the lock opens, the corrupt file's rename aside / CreateTemp fail | internal/audit/snapshot_test.go |
| `snapshot.go:213-214` | `WriteSnapshot` | 1 | A | the snapshot path is a non-empty directory: rename over it fails | internal/audit/snapshot_test.go |
| `snapshot.go:229-230` | `lockSnapshot` | 1 | A | lock file can't be created in a 0500 dir | internal/audit/snapshot_test.go |
| `state.go:71-72` | `WriteState` | 1 | A | MkdirAll under a file / CreateTemp in a 0500 dir / rename onto a directory | internal/audit/state_test.go |
| `state.go:86-87` | `WriteState` | 1 | A | StatePath is a non-empty directory: the rename fails | internal/audit/state_test.go |
| `state.go:154-156` | `Result.writeState` | 2 | A | StatePath under a regular file: the WARN is logged | internal/audit/state_test.go |

#### `internal/downloads`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `downloads.go:81-81` | `Options.now` | 1 | A | Delete with Options.Now nil uses the real clock | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:106-107` | `homeDirs.func` | 1 | A | Options.Home an absolute path that doesn't exist (EvalSymlinks fails) | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:213-214, 236-237, 243-244` | `xdgDownloadDir` | 3 | A | user-dirs.dirs with a relative XDG_CONFIG_HOME and no home, $HOME with no home, a relative value | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:290-291` | `Scan` | 1 | A | a missing/0000 folder (OpenRoot), the mountinfoPath seam on a missing file, a 0300 folder (ReadDir) | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:299-300, 303-304` | `Scan` | 2 | A | the mountinfoPath seam on a missing file; a 0300 folder (ReadDir fails) | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:328-329` | `oldDownloads` | 1 | A | a folder that opens but can't be listed (0300) | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:359-360` | `entryIsOld.func` | 1 | A | an old directory holding an unreadable (0000) subdirectory counts as new | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:431-432` | `newBoundary` | 1 | A | mountinfoPath seam pointing at a missing file | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:492-493, 501-502` | `Delete` | 2 | A | the folder removed after the scan (OpenRoot fails); mountinfoPath seam on a missing file | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:591-592` | `remover.removeTree` | 1 | A | a file in an old 0500 subdirectory (clock moved past the chmod with Options.Now): Remove fails EACCES | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:646-647` | `PathErrCause` | 1 | A | Scan of a missing folder gives a *fs.PathError | internal/downloads/downloads_test.go (security: Dev 3) |

#### `internal/driver`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `driver.go:217-218` | `msg.hangUp` | 1 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go |
| `driver.go:434-435` | `Driver.readAskpass` | 1 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go |
| `driver.go:453-455, 475-477` | `Driver.loop` | 4 | A | a Session that panics on the local or inputs path: the Driver tears down | internal/driver/driver_test.go |
| `driver.go:549-550` | `Driver.handle` | 1 | A | a Session that panics on the local or inputs path: the Driver tears down | internal/driver/driver_test.go |
| `driver.go:635-637, 639-640, 642-644` | `Driver.teardown` | 5 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go |
| `effects.go:51-51, 58-59, 68-69` | `Driver.effect` | 3 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go |
| `effects.go:127-127` | `Driver.refresh` | 1 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go |
| `effects.go:207-208` | `Driver.pump` | 1 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go |
| `keep.go:108-109` | `Driver.readAgent` | 1 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go (security: Dev 3) |
| `keep.go:176-178, 181-184, 186-188` | `Driver.agentRequest.func` | 7 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go (security: Dev 3) |
| `keep.go:227-227` | `Driver.answerUser` | 1 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go (security: Dev 3) |
| `keep.go:252-253` | `requesterOf` | 1 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go (security: Dev 3) |

#### `internal/engine`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `combine.go:85-85, 102-102` | `Session.combineNext` | 2 | A | a combined queue whose parts are all declined, finish out of order, or exit without having run | internal/engine/combine_test.go |
| `combine.go:187-188, 191-192` | `Session.partDone` | 2 | A | a combined queue whose parts are all declined, finish out of order, or exit without having run | internal/engine/combine_test.go |
| `combine.go:215-216` | `Session.combinedExited` | 1 | A | a combined queue whose parts are all declined, finish out of order, or exit without having run | internal/engine/combine_test.go |
| `events.go:308-309` | `DiscoveryOutcome.String` | 1 | A | String of an out-of-range outcome | internal/engine/events_test.go |
| `repaircheck.go:95-96` | `nameList` | 1 | A | a combined queue whose parts are all declined, finish out of order, or exit without having run | internal/engine/combine_test.go |

#### `internal/engine/enginetest`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `contract.go:89-90, 91-92, 94-95` | `Checker.Check.func` | 3 | A | the checker's own failure paths: add the missing hand-built breaches | internal/engine/enginetest/contract_test.go |
| `contract.go:164-165, 179-180, 185-186, 200-201, 217-218, 223-223, 225-225, 227-227, 240-241, 246-247` | `Checker.Check` | 10 | A | the checker's own failure paths: add the missing hand-built breaches | internal/engine/enginetest/contract_test.go |

#### `internal/frontend`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `topgrade.go:51-52, 55-56, 58-59, 61-63` | `EnsureTopgradeConfig` | 5 | A | Lstat error other than not-exist (0000 parent), MkdirAll under a file, the file appearing between Lstat and OpenFile (O_EXCL), a 0500 dir | internal/frontend/topgrade_test.go |

#### `internal/helperclient`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `combined.go:144-145, 165-165` | `decoder.partProgress` | 2 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `combined.go:181-182, 186-187, 202-203` | `decoder.partDone` | 4 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `combined.go:221-221` | `decoder.combinedOutcome` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `decode.go:121-122` | `decoder.feed` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `decode.go:184-185` | `decoder.readCheck` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `decode.go:235-236` | `decoder.readResult` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `plan.go:94-95` | `CheckPlan` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `plan.go:132-133` | `PlanOrphans` | 1 | C | runs the installed /usr/lib/archmaid/archmaid-helper plan-orphans, which runs the real pacman -Qdtq/-Rs --print | VM (security: Dev 3) |
| `plan.go:142-143` | `planOrphans` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `plan.go:191-192` | `decoder.readPlan` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `plan.go:222-222` | `exitCode` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `run.go:99-100, 112-113` | `Run` | 2 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `run.go:189-189` | `helperInstalled` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `run.go:280-281` | `resultText` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `run.go:358-359` | `decoder.outcome` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go (security: Dev 3) |
| `run.go:374-374` | `exitText` | 1 | A | a fake helper killed by a signal | internal/helperclient/run_test.go (security: Dev 3) |
| `run.go:403-404` | `tail.Write` | 1 | A | a fake helper writing >4 KiB to stderr | internal/helperclient/run_test.go (security: Dev 3) |

#### `internal/logstore`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `lastrun.go:72-73, 78-79` | `Store.WriteLastRun` | 2 | A | MkdirAll under a regular file / CreateTemp in a 0500 dir | internal/logstore/lastrun_test.go |
| `logstore.go:259-260` | `Store.pruneDiscovery` | 1 | A | a store with an error, a run dir under a regular file, a log path that is a directory | internal/logstore/logstore_test.go |
| `logstore.go:346-347, 352-353` | `Store.Append` | 2 | A | a store with an error, a run dir under a regular file, a log path that is a directory | internal/logstore/logstore_test.go |
| `logstore.go:379-380` | `Store.Read` | 1 | A | a store with an error, a run dir under a regular file, a log path that is a directory | internal/logstore/logstore_test.go |

#### `internal/privilege`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `askpass.go:349-350, 354-355` | `Request.deliver` | 2 | A | net.Pipe relays that hang up or answer the wrong line; a harden func that fails | internal/privilege/askpass_test.go (security: Dev 3) |
| `askpass.go:378-379, 382-383, 386-387` | `ReadAnswer` | 3 | A | net.Pipe relays that hang up or answer the wrong line; a harden func that fails | internal/privilege/askpass_test.go (security: Dev 3) |
| `faillock.go:110-111` | `faillockStatus` | 1 | A | a tally path that is a directory, an unreadable faillock.conf, a scanner error (an over-long line) | internal/privilege/faillock_test.go (security: Dev 3) |
| `faillock.go:168-169` | `readTally` | 1 | A | a tally path that is a directory, an unreadable faillock.conf, a scanner error (an over-long line) | internal/privilege/faillock_test.go (security: Dev 3) |
| `faillock.go:201-202, 234-235` | `readFaillockConf` | 2 | A | a tally path that is a directory, an unreadable faillock.conf, a scanner error (an over-long line) | internal/privilege/faillock_test.go (security: Dev 3) |
| `kept.go:68-69, 71-73` | `KeptPassword.Keep` | 3 | A | unix.Mmap or Mlock failing: a re-executed test child with setrlimit(RLIMIT_AS / RLIMIT_MEMLOCK) lowered (needs Dev 3: it is the kept-password code) | internal/privilege/kept_test.go (security: Dev 3) |
| `kept.go:158-159, 162-163` | `CheckPasswordBytes` | 2 | A | a password with a line break; su's check failing (shim) | internal/privilege/kept_test.go (security: Dev 3) |
| `kept.go:187-188` | `Request.AnswerBytes` | 1 | A | a password with a line break; su's check failing (shim) | internal/privilege/kept_test.go (security: Dev 3) |
| `privilege.go:177-178` | `CheckPassword` | 1 | A | su shim missing/failing to start | internal/privilege/privilege_test.go (security: Dev 3) |

#### `internal/proc`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `proc.go:80-81` | `checkSudo` | 1 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `proc.go:144-144, 149-149, 153-153` | `envCommand` | 3 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `proc.go:422-423` | `Child.GroupStopped` | 1 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `proc.go:445-445` | `Child.escalateGroup` | 1 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `procfs.go:40-41, 44-45, 51-52, 57-58` | `readAt` | 4 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |

#### `internal/sanitize`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `sanitize.go:217-217, 227-228` | `escapeEnd` | 2 | A | CSI with intermediates, a CSI cut off before its final byte, OSC ended by ESC \ | internal/sanitize/sanitize_test.go |

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
| `orphans.go:143-144, 145-146, 147-147` | `readStat` | 4 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `orphans.go:173-174, 175-176, 177-178, 179-179, 180-181, 183-183` | `hasMarker` | 8 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `orphans.go:188-189, 190-191, 192-192` | `cmdline` | 4 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `testenv.go:164-165, 166-166, 167-167, 168-169, 170-171, 174-175, 176-178, 179-179, 180-181, 182-183, 184-184, 186-186, 187-188, 189-190, 191-191, 193-193, 194-196, 197-197, 198-199, 200-200` | `Run` | 31 | A | the post-m.Run checks (reap, trap report, guard compare): run every time, counted never (after the counters are written); a test calling Run on a fake M counts them | internal/testenv/trap_test.go |
| `testenv.go:208-209, 217-218` | `guardScript` | 2 | A | Run from a dir with no scripts/state-guard.sh above it; a guard script that fails | internal/testenv/trap_test.go |
| `testenv.go:231-232` | `guardSnapshot` | 1 | A | Run from a dir with no scripts/state-guard.sh above it; a guard script that fails | internal/testenv/trap_test.go |
| `testenv.go:240-242` | `guardCompare` | 2 | A | post-m.Run: counted only from a direct test | internal/testenv/trap_test.go |

#### `internal/testtranscript`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `recorder.go:69-70` | `NewRecorder` | 1 | A | fake targets failing Start/Next/Reply and an odd replace list | internal/testtranscript/recorder_test.go |
| `recorder.go:91-92, 106-107` | `Recorder.Run` | 2 | A | fake targets failing Start/Next/Reply and an odd replace list | internal/testtranscript/recorder_test.go |
| `recorder.go:198-198` | `Recorder.event` | 1 | A | fake targets failing Start/Next/Reply and an odd replace list | internal/testtranscript/recorder_test.go |
| `recorder.go:229-230` | `Recorder.reply` | 1 | A | fake targets failing Start/Next/Reply and an odd replace list | internal/testtranscript/recorder_test.go |

#### `internal/tui`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `adapter.go:251-251` | `model.applyEvent` | 1 | A | key handling per screen (menu quit, log picker, log, audit, notice, discovering, services, confirm), the editor name from VISUAL/EDITOR, overlay padding, a PromptClosed for the askpass, a Finished with an open askpass or a cancelled outcome | internal/tui/*_test.go |
| `adapter.go:318-319, 346-347` | `model.finished` | 2 | A | key handling per screen (menu quit, log picker, log, audit, notice, discovering, services, confirm), the editor name from VISUAL/EDITOR, overlay padding, a PromptClosed for the askpass, a Finished with an open askpass or a cancelled outcome | internal/tui/*_test.go |
| `adapter.go:467-467` | `model.engineKey` | 1 | A | key handling per screen (menu quit, log picker, log, audit, notice, discovering, services, confirm), the editor name from VISUAL/EDITOR, overlay padding, a PromptClosed for the askpass, a Finished with an open askpass or a cancelled outcome | internal/tui/*_test.go |
| `glue.go:84-85` | `waitForDownloads` | 2 | A | key handling per screen (menu quit, log picker, log, audit, notice, discovering, services, confirm), the editor name from VISUAL/EDITOR, overlay padding, a PromptClosed for the askpass, a Finished with an open askpass or a cancelled outcome | internal/tui/*_test.go |
| `overlay.go:24-25` | `overlay` | 1 | A | key handling per screen (menu quit, log picker, log, audit, notice, discovering, services, confirm), the editor name from VISUAL/EDITOR, overlay padding, a PromptClosed for the askpass, a Finished with an open askpass or a cancelled outcome | internal/tui/*_test.go |
| `overlay.go:50-51` | `compositeLine` | 1 | A | key handling per screen (menu quit, log picker, log, audit, notice, discovering, services, confirm), the editor name from VISUAL/EDITOR, overlay padding, a PromptClosed for the askpass, a Finished with an open askpass or a cancelled outcome | internal/tui/*_test.go |
| `status.go:47-48` | `plural` | 1 | A | key handling per screen (menu quit, log picker, log, audit, notice, discovering, services, confirm), the editor name from VISUAL/EDITOR, overlay padding, a PromptClosed for the askpass, a Finished with an open askpass or a cancelled outcome | internal/tui/*_test.go |
| `tasks.go:82-82` | `<package>` | 1 | B | the default execProcess hands the terminal to the editor: a pty tape with EDITOR set to a script (the unit tests swap the seam) | cmd/archmaid/e2e/run.sh |
| `tasks.go:99-100` | `handedTerminal.SetStdin` | 1 | B | the terminal handover to a real editor: a pty tape with EDITOR set to a script | cmd/archmaid/e2e/run.sh |
| `tasks.go:121-122` | `editConfig` | 2 | A | key handling per screen (menu quit, log picker, log, audit, notice, discovering, services, confirm), the editor name from VISUAL/EDITOR, overlay padding, a PromptClosed for the askpass, a Finished with an open askpass or a cancelled outcome | internal/tui/*_test.go |
| `tasks.go:123-125` | `editConfig.func` | 2 | A | key handling per screen (menu quit, log picker, log, audit, notice, discovering, services, confirm), the editor name from VISUAL/EDITOR, overlay padding, a PromptClosed for the askpass, a Finished with an open askpass or a cancelled outcome | internal/tui/*_test.go |
| `tasks.go:142-143, 147-147` | `editorName` | 2 | A | key handling per screen (menu quit, log picker, log, audit, notice, discovering, services, confirm), the editor name from VISUAL/EDITOR, overlay padding, a PromptClosed for the askpass, a Finished with an open askpass or a cancelled outcome | internal/tui/*_test.go |
| `tui.go:63-64` | `Run` | 1 | A | ttyguard.Hold refusing stdin (a closed or non-terminal fd in the TTY child) | internal/tui/tui_test.go |
| `tui.go:105-106` | `DropSudoTicket` | 1 | A | privilege.DropTicket failing (sudo shim) | internal/tui/tui_test.go |
| `update.go:69-69, 70-73, 74-74` | `model.Update` | 5 | A | key handling per screen (menu quit, log picker, log, audit, notice, discovering, services, confirm), the editor name from VISUAL/EDITOR, overlay padding, a PromptClosed for the askpass, a Finished with an open askpass or a cancelled outcome | internal/tui/*_test.go |
| `update.go:111-111, 112-113, 133-133, 135-135, 175-175, 176-177, 187-187, 189-189, 191-193, 198-200, 204-205, 209-209, 210-211, 218-218, 219-220` | `model.handleKey` | 19 | A | key handling per screen (menu quit, log picker, log, audit, notice, discovering, services, confirm), the editor name from VISUAL/EDITOR, overlay padding, a PromptClosed for the askpass, a Finished with an open askpass or a cancelled outcome | internal/tui/*_test.go |
| `update.go:238-238, 243-243` | `model.activate` | 2 | A | key handling per screen (menu quit, log picker, log, audit, notice, discovering, services, confirm), the editor name from VISUAL/EDITOR, overlay padding, a PromptClosed for the askpass, a Finished with an open askpass or a cancelled outcome | internal/tui/*_test.go |

#### `gui/cmd/archmaid-gui`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `main.go:all` | `main` | 6 | B | the GUI binary's main: in no run's data at all (no test links it); a headless start of the cover archmaid-gui counts it | gui/scripts/headless.sh + cover archmaid-gui (new) |

<!-- /gen:blocks -->
