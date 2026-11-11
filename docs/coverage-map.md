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
#   e2e/   cmd/archmaid/e2e/run.sh with E2E_COVER_DIR: the thirteen checks on a cover archmaid
#   gui/   gui/scripts/headless-cover.sh: the GUI's plain tests and every headless display group
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

What each kind of run adds, for core (statements 5425, test-only packages
excluded; measured at the work-package-3 commit):

| Run | core |
|---|---|
| `check.sh` (`go test -coverprofile`, no children) | 96.3 % (5229/5425) |
| `cover-unit.sh` (the same tests, plus the re-executed children: the TTY child running the real `tui.Run`, the askpass relay) | 96.3 % (5229/5425) |
| + the e2e tapes on the cover binary (`main`, the real startup) | 97.0 % (5266/5425) |
| + the GUI's tests (they drive `internal/engine`, `driver`, `frontend` and more through the GUI) | 97.1 % (5268/5425) |

testonly: 66.0 % (323/489), not part of core. Before work package 3 core was
95.4 % raw / 96.5 % reachable on this basis (the pass-2 map's 92.9 % /
94.3 % had these 489 statements in it).

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
| core | 6267 | 6106 | 97.4 % | 161 | 91 | 3 | 1 | 0 | 66 | 98.4 % (6106/6201) |
| helper | 1106 | 1044 | 94.3 % | 62 | 8 | 51 | 0 | 0 | 3 | 94.6 % (1044/1103) |
| gui | 5341 | 5178 | 96.9 % | 163 | 71 | 39 | 22 | 0 | 31 | 97.5 % (5178/5310) |
| testonly | 585 | 399 | 68.2 % | 186 | 109 | 0 | 0 | 0 | 77 | 78.5 % (399/508) |
| all | 13299 | 12727 | 95.6 % | 572 | 279 | 93 | 23 | 0 | 177 | 96.9 % (12727/13122) |

Raw / reachable, cumulative:

| Scope | Now (unit + e2e + GUI) | after A | after A+B | after A+B+C | after A+B+C+D (= every reachable statement) |
|---|---|---|---|---|---|
| core | 97.4 % / 98.4 % | 98.8 % / 99.9 % | 98.9 % / 99.9 % | 98.9 % / 100.0 % | 98.9 % / 100.0 % |
| helper | 94.3 % / 94.6 % | 95.1 % / 95.3 % | 99.7 % / 100.0 % | 99.7 % / 100.0 % | 99.7 % / 100.0 % |
| gui | 96.9 % / 97.5 % | 98.2 % / 98.8 % | 99.0 % / 99.5 % | 99.4 % / 100.0 % | 99.4 % / 100.0 % |
| testonly | 68.2 % / 78.5 % | 86.8 % / 100.0 % | 86.8 % / 100.0 % | 86.8 % / 100.0 % | 86.8 % / 100.0 % |
| all | 95.6 % / 96.9 % | 97.7 % / 99.1 % | 98.4 % / 99.8 % | 98.6 % / 100.0 % | 98.6 % / 100.0 % |
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
| 1 | **Test infrastructure** (A) | 109 | +0.8 pts | architecture | no | testenv's post-`m.Run` checks (orphan reaper, trap report, state-guard compare) called directly inside a test, the recorder's fake targets, the contract checker's own failure paths. By package: `testenv` 91, `engine/enginetest` 13, `testtranscript` 5 |
| 2 | **Helper and privilege edge cases** (A) | 8 | +0.0 pts | security | 8 stmts: Dev 3 reviews | the helper's fake-runner/Stat/Exe error paths and lock edges, helperclient's malformed and failing helper output, privilege's askpass relays, faillock and kept-password failures. By package: `cmd/archmaid-helper/helper` 8 |
| 3 | **Core: TUI keys, driver, engine, actions, logstore** (A) | 4 | +0.0 pts | architecture (driver/keep.go and the askpass bits: security) | no | per-screen key handling and Finished/PromptClosed edges in the TUI, the driver's teardown, hang-up and agent paths, combined-queue edges, discovery outputs, the log store's error paths. By package: `driver` 3, `logstore` 1 |
| 4 | **Leaf packages and the polkit agent** (A) | 128 | +0.9 pts | performance-QA (polkitagent, realdriver, downloads: security) | 53 stmts: Dev 3 reviews | audit/downloads write and scan error paths, proc argv forms, sanitize, sysstat and vt edges, polkitagent and realdriver against fake agents. By package: `vt` 32, `proc` 28, `gui/polkitagent` 27, `gui/realdriver` 14, `audit` 13, `downloads` 12 |
| 5 | **GUI logs, timeline and signals pages** (A) | 7 | +0.0 pts | GUI | no | new headless scenarios: log page states (none, unreadable, empty), the timeline, signals mid-run, the failed-step view, logview and overview edges. By package: `gui/ui` 7 |
| 6 | **GUI windows, dialogs, settings, run view** (A) | 23 | +0.1 pts | GUI | no | window/dialog/settings-page scenarios, the run view's SGR and ring edges, settings.Save error paths, shell and usercache cells. By package: `gui/ui` 20, `gui/settings` 3 |
| 7 | **E2E: the cover-helper check** (B) | 51 | +0.3 pts | performance-QA with Dev 3's review (helper) | 51 stmts: Dev 3 reviews | main and hardenProcess of the helper binary: the run.sh check of the cover helper as the user and as uid 0 in an unprivileged user namespace (both refused before anything runs). By package: `cmd/archmaid-helper` 50, `cmd/archmaid-helper/coverdir` 1 |
| 8 | **E2E: binary entries** (B) | 42 | +0.3 pts | GUI (the TUI tape: architecture) | no | archmaid-gui's main, `--stub` with each script (headless), the TUI's error exit and the editor handover on a pty tape. By package: `gui/ui` 33, `gui/cmd/archmaid-gui` 6, `cmd/archmaid` 3 |
| | **A and B together** | 372 | +2.7 pts | | | |
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
| `cmd/archmaid-helper` |  | 50 |  |  | 1 |
| `cmd/archmaid-helper/internal/coverdir` |  | 1 |  |  |  |
| `cmd/archmaid-helper/internal/helper` | 8 |  |  |  | 2 |
| `cmd/archmaid` |  | 3 |  |  | 3 |
| `gui/cmd/archmaid-gui` |  | 6 |  |  |  |
| `gui/internal/polkitagent` | 27 |  |  |  | 15 |
| `gui/internal/realdriver` | 14 |  |  |  |  |
| `gui/internal/runview` |  |  |  |  | 1 |
| `gui/internal/settings` | 3 |  |  |  | 6 |
| `gui/internal/ui` | 27 | 33 | 22 |  | 9 |
| `internal/audit` | 13 |  |  |  | 17 |
| `internal/downloads` | 12 |  |  |  | 15 |
| `internal/driver` | 3 |  |  |  | 2 |
| `internal/engine` |  |  |  |  | 1 |
| `internal/engine/enginetest` | 13 |  |  |  |  |
| `internal/engine/stub` |  |  |  |  | 1 |
| `internal/frontend` |  |  |  |  | 1 |
| `internal/helperclient/helpertest` |  |  |  |  | 2 |
| `internal/helperclient` |  |  | 1 |  |  |
| `internal/logstore` | 1 |  |  |  | 8 |
| `internal/privilege` |  |  |  |  | 8 |
| `internal/privilege/privtest` |  |  |  |  | 1 |
| `internal/proc` | 28 |  |  |  | 1 |
| `internal/sysstat` | 2 |  |  |  |  |
| `internal/testenv` | 91 |  |  |  | 74 |
| `internal/testtranscript` | 5 |  |  |  |  |
| `internal/ttyguard` |  |  |  |  | 4 |
| `internal/tui` |  |  |  |  | 5 |
| `internal/vt` | 32 |  |  |  |  |
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
| `main.go:25-26, 27-29, 30-34, 35-36, 37-37` | `main` | 26 | B | main only runs in the binary: a cover helper started as the user with a root verb is refused before it runs anything (verified under a scratch HOME); the root path is the VM's | e2e (new helper check) (security: Dev 3) |

#### `cmd/archmaid-helper/internal/coverdir`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `coverdir.go:26-27` | `init` | 1 | B | only a cover helper running as root sets it: verified under unshare -r with a scratch HOME (coverage-map.md, helper check); in the VM every pkexec run does | e2e (new helper check) (security: Dev 3) |

#### `cmd/archmaid-helper/internal/helper`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `dblock.go:80-80` | `session.lockDirSafe` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `fullcleanup.go:148-149` | `runFullCleanup` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `leftovers.go:255-258` | `scanCacheDir` | 3 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `leftovers.go:286-287` | `removeLeftover` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `leftovers.go:337-338` | `plainPath` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |
| `leftovers.go:420-423` | `releasePacmanLock` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go (security: Dev 3) |

#### `cmd/archmaid`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `main.go:63-66` | `main` | 3 | B | tui.Run's error exit: a new e2e run of the binary without a terminal (Bubble Tea can't open one), under the usual scratch HOME | cmd/archmaid/e2e/run.sh |

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
| `realdriver.go:153-154, 158-159` | `Start` | 2 | A | a fake polkit agent that keeps ending or fails to start; no XDG_RUNTIME_DIR in Config | gui/internal/realdriver/agent_test.go (security: Dev 3) |
| `realdriver.go:227-228` | `Wiring.New` | 1 | A | a fake polkit agent that keeps ending or fails to start; no XDG_RUNTIME_DIR in Config | gui/internal/realdriver/agent_test.go (security: Dev 3) |
| `realdriver.go:246-247` | `Wiring.env` | 1 | A | Options() with no fake PlanOrphans picks helperclient.PlanOrphans (assignment only; running it is C) | gui/internal/realdriver/realdriver_test.go (security: Dev 3) |

#### `gui/internal/settings`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `settings.go:83-84, 108-109` | `Save` | 2 | A | no XDG_CONFIG_HOME nor HOME, an unreadable gui.json, a read-only config dir, a rename onto a directory, a bad downloads folder | gui/internal/settings/settings_test.go |
| `settings.go:146-146` | `CheckDownloads` | 1 | A | no XDG_CONFIG_HOME nor HOME, an unreadable gui.json, a read-only config dir, a rename onto a directory, a bad downloads folder | gui/internal/settings/settings_test.go |

#### `gui/internal/ui`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `app.go:114-115, 116-118, 119-119, 120-121, 122-122` | `Main` | 7 | B | the binary's entry: start the cover archmaid-gui under headless.sh with a scratch HOME (a GUI e2e; the harness calls Run, not Main) | gui/scripts/headless.sh + cover archmaid-gui (new) |
| `app.go:180-181, 185-187, 189-189, 190-192, 193-193` | `mainReal` | 10 | B | the binary's entry: start the cover archmaid-gui under headless.sh with a scratch HOME (a GUI e2e; the harness calls Run, not Main) | gui/scripts/headless.sh + cover archmaid-gui (new) |
| `app.go:181-181` | `mainReal.func` | 1 | B | the binary's entry: start the cover archmaid-gui under headless.sh with a scratch HOME (a GUI e2e; the harness calls Run, not Main) | gui/scripts/headless.sh + cover archmaid-gui (new) |
| `app.go:206-206, 207-207, 207-207, 209-209, 210-210, 210-210, 220-220, 221-224` | `stubDriver.func` | 10 | B | archmaid-gui --stub with each ARCHMAID_GUI_SCRIPT, headless (the harness builds its stub drivers itself) | gui/scripts/headless.sh + cover archmaid-gui (new) |
| `app.go:212-212, 214-214, 216-216, 218-218, 226-226` | `stubDriver` | 5 | B | archmaid-gui --stub with each ARCHMAID_GUI_SCRIPT, headless (the harness builds its stub drivers itself) | gui/scripts/headless.sh + cover archmaid-gui (new) |
| `dialogs.go:129-130, 139-140` | `Window.confirmDialog` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `failed.go:176-177` | `recordFolder` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `launcher.go:37-37, 38-39, 40-41, 46-46` | `gtkLauncher.launch` | 5 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:42-42, 43-44` | `gtkLauncher.launch.func` | 2 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:49-49` | `gtkLauncher.OpenFile` | 1 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:50-50` | `gtkLauncher.OpenFolder` | 1 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:53-54, 55-56` | `gtkLauncher.ChooseFolder` | 3 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:57-57, 58-59, 60-62, 63-63` | `gtkLauncher.ChooseFolder.func` | 6 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `logs.go:157-158` | `Window.newLogs.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `overview.go:252-253` | `Window.stackButtons` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `overview.go:523-524` | `Window.refreshLastRun` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `settings.go:206-206, 207-208` | `Window.topgradeCard.func` | 2 | C | Open folder / Choose folder through the real launcher (VM desktop); a fake launcher in the harness would make these A | VM desktop |
| `settings.go:251-252` | `Window.logFolderCard.func` | 1 | C | Open folder / Choose folder through the real launcher (VM desktop); a fake launcher in the harness would make these A | VM desktop |
| `settings.go:266-267` | `Window.chooseDownloads` | 1 | C | Open folder / Choose folder through the real launcher (VM desktop); a fake launcher in the harness would make these A | VM desktop |
| `termcols.go:31-32, 36-37` | `Window.measureCols` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:135-136` | `Window.newRunPage.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `timeline.go:622-623, 624-626` | `Window.output` | 4 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `widedialog.go:158-159, 163-163` | `newWideDialog.func` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `widedialog.go:240-241` | `amDialog.naturalHeight` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `widedialog.go:310-313` | `amDialog.columns.func` | 4 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `widedialog.go:357-357` | `amDialog.response` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `widedialog.go:368-368` | `amDialog.ResponseLabel` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `widedialog.go:380-381` | `amDialog.SetResponseAppearance` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `widedialog.go:396-397, 401-401` | `amDialog.ResponseAppearance` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `widedialog.go:421-422` | `amDialog.DefaultResponse` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `window.go:147-147` | `newWindow.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |

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
| `downloads.go:299-300, 303-304` | `Scan` | 2 | A | the mountinfoPath seam on a missing file; a 0300 folder (ReadDir fails) | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:328-329` | `oldDownloads` | 1 | A | a folder that opens but can't be listed (0300) | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:359-360` | `entryIsOld.func` | 1 | A | an old directory holding an unreadable (0000) subdirectory counts as new | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:431-432` | `newBoundary` | 1 | A | mountinfoPath seam pointing at a missing file | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:501-502` | `Delete` | 1 | A | the folder removed after the scan (OpenRoot fails); mountinfoPath seam on a missing file | internal/downloads/downloads_test.go (security: Dev 3) |
| `downloads.go:591-592` | `remover.removeTree` | 1 | A | a file in an old 0500 subdirectory (clock moved past the chmod with Options.Now): Remove fails EACCES | internal/downloads/downloads_test.go (security: Dev 3) |

#### `internal/driver`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `outbox.go:148-148` | `outbox.redrawAfter` | 1 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go |
| `refresh.go:59-60` | `RefreshUpdates` | 1 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go |
| `refresh.go:68-69` | `trimOut` | 1 | A | a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe | internal/driver/*_test.go |

#### `internal/engine/enginetest`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `contract.go:89-90, 91-92, 94-95` | `Checker.Check.func` | 3 | A | the checker's own failure paths: add the missing hand-built breaches | internal/engine/enginetest/contract_test.go |
| `contract.go:164-165, 179-180, 185-186, 200-201, 217-218, 223-223, 225-225, 227-227, 240-241, 246-247` | `Checker.Check` | 10 | A | the checker's own failure paths: add the missing hand-built breaches | internal/engine/enginetest/contract_test.go |

#### `internal/helperclient`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `plan.go:132-133` | `PlanOrphans` | 1 | C | runs the installed /usr/lib/archmaid/archmaid-helper plan-orphans, which runs the real pacman -Qdtq/-Rs --print | VM (security: Dev 3) |

#### `internal/logstore`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `logstore.go:231-232` | `freeRun` | 1 | A | a store with an error, a run dir under a regular file, a log path that is a directory | internal/logstore/logstore_test.go |

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
| `pty.go:53-54, 70-72, 75-78, 80-83` | `openPty` | 9 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `pty.go:61-62` | `openPty.func` | 1 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `pty.go:95-96, 97-98, 99-99` | `peer` | 4 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |

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
| `testenv.go:280-281, 289-290` | `guardScript` | 2 | A | Run from a dir with no scripts/state-guard.sh above it; a guard script that fails | internal/testenv/trap_test.go |
| `testenv.go:303-304` | `guardSnapshot` | 1 | A | Run from a dir with no scripts/state-guard.sh above it; a guard script that fails | internal/testenv/trap_test.go |
| `testenv.go:312-314` | `guardCompare` | 2 | A | post-m.Run: counted only from a direct test | internal/testenv/trap_test.go |
| `watchdog.go:76-79` | `watchdog.stop` | 3 | A | post-m.Run in Run (counted never, after the counters are written); a direct call in a test counts | internal/testenv/trap_test.go |

#### `internal/testtranscript`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `recorder.go:69-70` | `NewRecorder` | 1 | A | fake targets failing Start/Next/Reply and an odd replace list | internal/testtranscript/recorder_test.go |
| `recorder.go:91-92, 106-107` | `Recorder.Run` | 2 | A | fake targets failing Start/Next/Reply and an odd replace list | internal/testtranscript/recorder_test.go |
| `recorder.go:198-198` | `Recorder.event` | 1 | A | fake targets failing Start/Next/Reply and an odd replace list | internal/testtranscript/recorder_test.go |
| `recorder.go:229-230` | `Recorder.reply` | 1 | A | fake targets failing Start/Next/Reply and an odd replace list | internal/testtranscript/recorder_test.go |

#### `internal/vt`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `vt.go:137-139` | `style.sgr` | 2 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:158-158, 160-160` | `writeColor` | 2 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:318-318, 320-320, 323-323` | `Screen.Write` | 3 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:391-391` | `Screen.controlInSeq` | 1 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:407-407` | `Screen.escByte` | 1 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:449-449, 451-451` | `Screen.stringByte` | 2 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:514-514, 516-516` | `Screen.param` | 2 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:524-525` | `atoi` | 1 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:570-571` | `Screen.bound` | 1 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:643-644` | `Screen.putASCII` | 1 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:672-673` | `Screen.putRune` | 1 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:728-728` | `Screen.eraseLine` | 1 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:899-900, 910-910, 914-914, 916-916, 918-918, 920-920, 922-922` | `Screen.sgr` | 7 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:976-976, 984-984` | `extColon` | 2 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:991-992, 1005-1005, 1007-1007` | `extSemicolon` | 3 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:1013-1014` | `indexed` | 1 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |
| `vt.go:1021-1022` | `rgbColor` | 1 | A | parser edges a byte sequence reaches (a CSI parameter past its cap, SGR 38/48 colours with missing or out-of-range parts in both the colon and the semicolon form, a control or string byte inside a sequence, the column bound, a style table at MaxStyles): a table test or FuzzScreen's corpus; vt is a pure function of its input (#129, Dev 1) | internal/vt/vt_test.go |

#### `gui/cmd/archmaid-gui`

| Lines | Function | Stmts | Cat | Why | Test home |
|---|---|---|---|---|---|
| `main.go:all` | `main` | 6 | B | the GUI binary's main: in no run's data at all (no test links it); a headless start of the cover archmaid-gui counts it | gui/scripts/headless.sh + cover archmaid-gui (new) |

<!-- /gen:blocks -->
