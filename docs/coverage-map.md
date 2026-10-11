# Coverage map

Every statement the tests leave uncovered, sorted by what it takes to cover
it, and the tooling that lets runs outside `go test` count. Base:
`dad922a` (origin/refactor/p0). Machine-readable copy, one entry per
uncovered block: [`coverage-map.json`](coverage-map.json). How the suite is
run and kept safe: [`TESTING.md`](TESTING.md) (its "Coverage from real runs"
section lists the scripts).

**Pending:** `internal/actions`, `internal/engine`, `internal/logstore`,
`internal/driver`, `internal/tui`, `internal/frontend/audit.go` and
`packaging/dms` are being reworked by Dev 3's discovery change. Their 96
uncovered statements are counted below as "pending" and get classified after
that change lands. (`internal/engine/stub` and `internal/engine/enginetest`
are classified.)

## Categories

| Cat | Means | Who runs it |
|---|---|---|
| A `unit` | an ordinary unit test with the existing shims and fakes reaches it; the test file is named per block | any dev, in the gate |
| B `e2e` | the real binary has to run: a new tape or check in `cmd/archmaid/e2e`, a headless start of the cover `archmaid-gui`, or a check of the cover `archmaid-helper` (as the user, or as uid 0 in an unprivileged user namespace; both are refused before anything runs) | any dev, in `make e2e` / `make e2e-cover` |
| C `vm` | real root, polkit, pkexec, sudo, pacman or systemd, a root process's `/proc`, or a real desktop session with a portal | the VM tester only, in `~/archmaid-vmtest` |
| D `owner` | the owner's own desktop, hardware or session that the VM lacks (the DMS shell (Quickshell) with the archmaid DMS widget plugin, Hyprland specifics, the NVIDIA GPU, the real AUR set, real network timing) | the owner, by hand |
| E `unreachable` | a defensive branch that can't happen given the code (the reason is given per block), or dead code. Each block says delete or keep it with a `// unreachable: <why>` note; none is applied yet | nobody: decide per block |

## How it was measured

```sh
COVER_GUI=1 COVER_OUT=$(mktemp -d) scripts/cover-report.sh
#   unit/  scripts/cover-unit.sh: go test ./... through scripts/cover-exec.sh (children count)
#   e2e/   cmd/archmaid/e2e/run.sh with E2E_COVER_DIR: the nine checks on a cover archmaid
#   gui/   gui/scripts/headless-cover.sh: the GUI's plain tests and every headless display group
#   merged/  scripts/cover-merge.sh of all three
go run ./scripts/covblocks -json "$COVER_OUT/merged/profile.txt" > blocks.json
python3 -I scripts/covmap/classify.py blocks.json classified.json
python3 -I scripts/covmap/report.py classified.json "$COVER_OUT/merged/summary.txt" docs/coverage-map.json tables.md
```

The scopes: **core** is check.sh's set (`cmd/archmaid` + `internal/...`,
the `MIN_TOTAL` floor), **helper** is `cmd/archmaid-helper/...` (the gate
doesn't measure it), and **gui** is the `gui/` module (the gate doesn't
measure it either). `gui/cmd/archmaid-gui` (6 statements) is linked by no
test, so it appears in no run's data; it is counted in by hand.

What each kind of run adds, for core:

| Run | core |
|---|---|
| `check.sh` today (`go test -coverprofile`) | 91.6 % (5132/5602) |
| `cover-unit.sh` (the same tests, plus the re-executed children: the TTY child running the real `tui.Run`, the askpass relay) | 92.0 % (5166/5610) |
| + the e2e tapes on the cover binary (`main`, the real startup) | 92.8 % (5207/5610) |
| + the GUI's tests (they drive `internal/engine`, `driver`, `frontend` and more through the GUI) | 92.9 % (5212/5610) |

(5610 against 5602: `internal/testenv/cover.go`, new here, is 8 statements.)
helper: 87.3 % (774/886), unit tests only. gui: 33.1 % from its plain tests
alone, 92.4 % with the headless display tests.

Two things the numbers hide:

- **testenv's own checks never count.** `internal/testenv` is 36.9 %
  covered (145 statements uncovered), but most of it runs in every package
  of every test run: the orphan reaper, the trap report and the state-guard
  compare run in `TestMain` after `m.Run`, and the testing package writes its
  counters when `m.Run` returns. A direct unit test of those functions (inside
  `m.Run`) counts them; that's why they are A.
- **Test-only code is in the denominator.** `testenv`, `testtranscript`,
  `enginetest`, `privtest` and `helpertest` (477 statements, 312 covered)
  ship in no binary. Without them core is **95.4 %** (4900/5133) today. The
  gate could measure shipped code only (drop them from `COVERPKG`) and hold
  the test helpers to their own floor; that's a decision for the lead.

## Totals

| Scope | Statements | Covered now | Uncovered | A unit | B e2e | C vm | D owner | E unreachable | pending Dev 3 |
|---|---|---|---|---|---|---|---|---|---|
| core | 5610 | 5212 (92.9 %) | 398 | 221 | 3 | 1 | 0 | 77 | 96 |
| helper | 886 | 774 (87.3 %) | 112 | 59 | 52 | 0 | 0 | 1 | 0 |
| gui | 4139 | 3826 (92.4 %) | 313 | 215 | 39 | 23 | 0 | 36 | 0 |
| all | 10635 | 9812 (92.2 %) | 823 | 495 | 94 | 24 | 0 | 114 | 96 |

| Scope | Now (unit + e2e + GUI) | after A | after A+B | after A+B+C | after A+B+C+D | + pending, if all reachable |
|---|---|---|---|---|---|---|
| core | 92.9 % | 96.8 % | 96.8 % | 96.9 % | 96.9 % | 98.6 % |
| helper | 87.3 % | 94.0 % | 99.8 % | 99.8 % | 99.8 % | 99.8 % |
| gui | 92.4 % | 97.6 % | 98.5 % | 99.1 % | 99.1 % | 99.1 % |
| all | 92.2 % | 96.9 % | 97.7 % | 98.0 % | 98.0 % | 98.9 % |

D is 0 statements: every line the owner's checks exercise is reachable by
A–C too. The owner's checks are about behaviour (below), not about the
number.

## The path to 100 %

1. **A (495 statements):** unit tests, each block names its test file.
   Biggest: `gui/internal/ui` 143 (new headless scenarios), `internal/testenv`
   123 (direct tests of the post-`m.Run` checks), `cmd/archmaid-helper/internal/helper`
   59 (fake-runner error paths). This alone takes **all** from 92.2 % to 96.9 %.
2. **B (94):** the helper's `main` and `hardenProcess` (52: a cover-helper
   check in `run.sh`, as the "helper check" below), the GUI binary's entry
   (39: a headless start of the cover `archmaid-gui`, real and `--stub`
   with each script), and `tui.Run`'s error exit (3).
3. **C (24):** the VM tester's runs (below); then 98.0 %.
4. **pending (96):** classified after Dev 3's change; at most 98.9 %.
5. **E (114):** Go has no "ignore this branch" pragma, so raw statement
   coverage reaches 100 % only if these go. Suggested, per kind:
   - truly dead (a few: `audit` `packageNames`' empty case,
     `Entry.UnmarshalJSON`'s inner error, the stub's unknown-step panic):
     delete, or keep with `// unreachable:`;
   - the atomic-write pattern copied in `audit.WriteSnapshot`,
     `audit.WriteState`, `gui/internal/settings.Save` (and `logstore.WriteLastRun`,
     pending): one shared helper with an injectable file turns its
     write/fsync/close branches (about 25 statements) into A;
   - TOCTOU guards (`downloads` `removeContents`, `removeTree`'s Lstat,
     `checkDownloadsDir`): keep; a hook between the checks (like
     `Options.EntryHook`) would make them A;
   - syscalls that can't fail here (pty and ioctl in `polkitagent`, `prctl`,
     `setrlimit`, `SO_PEERCRED`, `flock`): keep with `// unreachable: <why>`
     and let `check.sh` leave blocks carrying that marker out of the
     denominator, so the floor reads "of reachable statements" and every
     exclusion is visible in review.

   Recommended target: **100 % of reachable statements**, with a reviewed E
   list, rather than 100 % raw.

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
   TUI (QML/JS: not in Go's coverage; `packaging/dms` is pending Dev 3).
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

| Package | A | B | C | D | E | pending |
|---|---|---|---|---|---|---|
| `cmd/archmaid-helper` |  | 50 |  |  | 1 |  |
| `cmd/archmaid-helper/internal/coverdir` |  | 1 |  |  |  |  |
| `cmd/archmaid-helper/internal/helper` | 59 | 1 |  |  |  |  |
| `cmd/archmaid` |  | 3 |  |  | 3 |  |
| `gui/cmd/archmaid-gui` |  | 6 |  |  |  |  |
| `gui/internal/logview` | 6 |  |  |  |  |  |
| `gui/internal/overview` | 1 |  |  |  |  |  |
| `gui/internal/polkitagent` | 23 |  |  |  | 18 |  |
| `gui/internal/realdriver` | 14 |  |  |  |  |  |
| `gui/internal/runview` | 21 |  |  |  | 4 |  |
| `gui/internal/settings` | 7 |  |  |  | 6 |  |
| `gui/internal/ui` | 143 | 33 | 23 |  | 8 |  |
| `internal/actions` |  |  |  |  |  | 1 |
| `internal/audit` | 13 |  |  |  | 17 |  |
| `internal/downloads` | 15 |  |  |  | 15 |  |
| `internal/driver` |  |  |  |  |  | 28 |
| `internal/engine` |  |  |  |  |  | 5 |
| `internal/engine/enginetest` | 12 |  |  |  |  |  |
| `internal/engine/stub` |  |  |  |  | 1 |  |
| `internal/frontend` | 5 |  |  |  |  |  |
| `internal/helperclient` | 21 |  | 1 |  |  |  |
| `internal/helperclient/helpertest` |  |  |  |  | 2 |  |
| `internal/logstore` |  |  |  |  |  | 13 |
| `internal/privilege` | 14 |  |  |  | 11 |  |
| `internal/privilege/privtest` |  |  |  |  | 1 |  |
| `internal/proc` | 10 |  |  |  | 1 |  |
| `internal/sanitize` | 2 |  |  |  |  |  |
| `internal/sysstat` | 1 |  |  |  |  |  |
| `internal/testenv` | 123 |  |  |  | 22 |  |
| `internal/testtranscript` | 5 |  |  |  |  |  |
| `internal/ttyguard` |  |  |  |  | 4 |  |
| `internal/tui` |  |  |  |  |  | 49 |

## Every uncovered block

Line numbers are at `dad922a`. Blocks of one function with the same reason are one row. For E the last column is the recommendation; otherwise it is the test file (or harness) the test belongs in. The pending packages are left out until Dev 3's change lands.

### `cmd/archmaid-helper`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `dev_off.go:9-9` | `devLockPath` | 1 | E | devLockPath is only called when devBuild, which is false in this build | keep: build-tag stub |
| `harden.go:17-19, 20-21, 22-22` | `hardenProcess` | 5 | B | runs in the harden test's child, but hardenProcess clears GOCOVERDIR before the test binary writes counters; the cover helper binary counts it (any run) | e2e (new helper check) |
| `harden.go:28-29, 30-32` | `scrubEnv` | 4 | B | runs in the harden test's child, but hardenProcess clears GOCOVERDIR before the test binary writes counters; the cover helper binary counts it (any run) | e2e (new helper check) |
| `harden.go:40-41, 42-43, 44-46, 47-48, 49-49, 51-52, 53-54, 56-56, 58-58` | `closeInheritedFDs` | 15 | B | runs in the harden test's child, but hardenProcess clears GOCOVERDIR before the test binary writes counters; the cover helper binary counts it (any run) | e2e (new helper check) |
| `main.go:23-24, 25-27, 28-32, 33-34, 35-35` | `main` | 26 | B | main only runs in the binary: a cover helper started as the user with a root verb is refused before it runs anything (verified under a scratch HOME); the root path is the VM's | e2e (new helper check) |

### `cmd/archmaid-helper/internal/coverdir`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `coverdir.go:26-27` | `init` | 1 | B | only a cover helper running as root sets it: verified under unshare -r with a scratch HOME (coverage-map.md, helper check); in the VM every pkexec run does | e2e (new helper check) |

### `cmd/archmaid-helper/internal/helper`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `checks.go:32-33, 34-35, 36-37, 38-39, 40-40` | `LstatFile` | 7 | A | a plain os.Lstat wrapper: call it on a temp file, a missing path | cmd/archmaid-helper/internal/helper/checks_test.go |
| `checks.go:82-83` | `CheckInstall` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `checks.go:124-125, 128-129, 135-135` | `acquireLock` | 3 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `checks.go:153-154` | `pacmanIdle` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `helper.go:67-75` | `DefaultOptions` | 1 | B | only main uses it: covered by any cover-helper run | e2e (new helper check) |
| `helper.go:103-104` | `Verbs.func` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `helper.go:133-134, 150-151` | `Run` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `helper.go:191-191` | `session.lock` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `helper.go:206-207` | `session.step` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `helper.go:274-275, 284-285` | `runCheckFiles` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `helper.go:319-320` | `session.checkInstalled` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `helper.go:360-361` | `session.checkPackages` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `helper.go:424-425` | `runPlanOrphans` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `helper.go:463-464, 467-468, 484-485` | `session.removeOrphans` | 3 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `helper.go:532-533` | `runRepair` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `helper.go:554-555` | `session.repairable` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `helper.go:592-593` | `session.vetRepair` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `helper.go:693-693, 697-697` | `qkSummaries` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `helper.go:713-714` | `missingFiles` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `output.go:264-264` | `skipEscape` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `plan.go:45-46, 53-53, 68-69` | `ComputePlan` | 3 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `plan.go:99-99` | `firstLine` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `runner.go:85-85` | `exitStatus` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `runner.go:108-109, 112-113` | `ExecRunner.Stream` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `runner.go:151-151, 152-153, 154-154` | `SplitLines` | 3 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `runner.go:169-169, 170-170, 171-172, 173-173, 175-175` | `capped.Write` | 5 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `units.go:23-24, 38-39, 48-49, 77-78` | `runDisableUnit` | 4 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `units.go:112-113, 119-120, 153-154` | `session.vetInstall` | 3 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `units.go:174-175` | `session.vetTriggers` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `units.go:195-196` | `orUnknown` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `units.go:237-238, 240-241` | `session.unitState` | 2 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |
| `units.go:271-272` | `session.readInstall` | 1 | A | fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \r | cmd/archmaid-helper/internal/helper/*_test.go |

### `cmd/archmaid`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `main.go:33-34` | `main` | 1 | E | prctl(PR_SET_DUMPABLE) on our own process doesn't fail | keep |
| `main.go:40-42` | `main` | 2 | E | tui.ActionEnv always fills every required field (Env.Validate is the guard for a future change) | keep: fail-closed startup check |
| `main.go:63-66` | `main` | 3 | B | tui.Run's error exit: a new e2e run of the binary without a terminal (Bubble Tea can't open one), under the usual scratch HOME | cmd/archmaid/e2e/run.sh |

### `gui/internal/logview`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `logview.go:53-54` | `Run.Subtitle` | 1 | A | a missing state dir, an unreadable one, a stray name and a DST-gap stamp, an empty run | gui/internal/logview/logview_test.go |
| `logview.go:64-65, 71-72, 86-86, 95-95` | `Runs` | 4 | A | a missing state dir, an unreadable one, a stray name and a DST-gap stamp, an empty run | gui/internal/logview/logview_test.go |
| `logview.go:119-120` | `Run.Read` | 1 | A | a missing state dir, an unreadable one, a stray name and a DST-gap stamp, an empty run | gui/internal/logview/logview_test.go |

### `gui/internal/overview`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `overview.go:137-138` | `Summarize` | 1 | A | a dry-run last-run record | gui/internal/overview/overview_test.go |

### `gui/internal/polkitagent`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `polkitagent.go:133-134` | `Agent.otherHolders` | 1 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go |
| `polkitagent.go:170-171, 177-178, 235-238, 255-255, 256-257` | `Start` | 8 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go |
| `polkitagent.go:181-182, 185-188` | `Start` | 4 | E | openPTY failing | keep |
| `polkitagent.go:247-250` | `Start` | 3 | E | TIOCEXCL on the pty just opened can't fail | keep |
| `polkitagent.go:268-268` | `discard` | 1 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go |
| `polkitagent.go:289-290` | `Agent.Stop.func` | 2 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go |
| `polkitagent.go:427-428, 453-454, 471-472, 482-482` | `Agent.drain` | 4 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go |
| `polkitagent.go:495-496` | `Agent.sendEOF` | 1 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go |
| `polkitagent.go:530-530` | `Prompt.Err` | 1 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go |
| `polkitagent.go:572-574` | `Prompt.Answer.func` | 2 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go |
| `polkitagent.go:595-596` | `Agent.waitNoEcho` | 1 | E | the termios read fails only on a closed pty | keep |
| `polkitagent.go:600-600, 601-602, 603-603` | `Agent.waitNoEcho` | 3 | A | a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject | gui/internal/polkitagent/polkitagent_test.go |
| `polkitagent.go:614-615, 620-621, 623-624` | `ioctl` | 3 | E | posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail | keep |
| `polkitagent.go:632-633, 636-638, 641-643, 646-648` | `openPTY` | 7 | E | posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail | keep |

### `gui/internal/realdriver`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `agent.go:57-58, 60-61, 70-71, 74-76` | `Wiring.helper.func` | 8 | A | a fake polkit agent that keeps ending or fails to start; no XDG_RUNTIME_DIR in Config | gui/internal/realdriver/agent_test.go |
| `agent.go:95-96, 99-100, 103-104` | `Wiring.startAgent` | 3 | A | a fake polkit agent that keeps ending or fails to start; no XDG_RUNTIME_DIR in Config | gui/internal/realdriver/agent_test.go |
| `realdriver.go:138-139, 143-144` | `Start` | 2 | A | a fake polkit agent that keeps ending or fails to start; no XDG_RUNTIME_DIR in Config | gui/internal/realdriver/agent_test.go |
| `realdriver.go:215-216` | `Wiring.env` | 1 | A | Options() with no fake PlanOrphans picks helperclient.PlanOrphans (assignment only; running it is C) | gui/internal/realdriver/realdriver_test.go |

### `gui/internal/runview`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `ansi.go:102-103` | `ParseSGR` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `ansi.go:175-176, 178-178` | `applySGR` | 2 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `ansi.go:258-259` | `extColor.func` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `ansi.go:276-276` | `extColor` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `ansi.go:283-284, 294-295, 299-299` | `extColorSemi` | 3 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `gate.go:54-54` | `GateNotes` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `model.go:130-131` | `lockText` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `model.go:374-377` | `Model.Apply` | 3 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `model.go:522-522` | `Model.lastText` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `model.go:649-650, 661-663` | `Model.popRuns` | 3 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `palette.go:64-65, 66-67, 68-68` | `Dank16Path` | 4 | E | os.UserHomeDir fails only with HOME unset, and XDG_CACHE_HOME empty too | keep |
| `palette.go:131-132` | `parseHex` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `scripts.go:89-90` | `FloodSteps` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `stepline.go:14-15` | `Model.StepLine` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |
| `stepline.go:51-52` | `Model.seqLine` | 1 | A | SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks | gui/internal/runview/*_test.go |

### `gui/internal/settings`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `settings.go:59-60, 66-67` | `Load` | 2 | A | no XDG_CONFIG_HOME nor HOME, an unreadable gui.json, a read-only config dir, a rename onto a directory, a bad downloads folder | gui/internal/settings/settings_test.go |
| `settings.go:83-84, 87-88, 91-92, 108-109` | `Save` | 4 | A | no XDG_CONFIG_HOME nor HOME, an unreadable gui.json, a read-only config dir, a rename onto a directory, a bad downloads folder | gui/internal/settings/settings_test.go |
| `settings.go:96-98, 100-102, 104-106` | `Save` | 6 | E | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO | keep |
| `settings.go:146-146` | `CheckDownloads` | 1 | A | no XDG_CONFIG_HOME nor HOME, an unreadable gui.json, a read-only config dir, a rename onto a directory, a bad downloads folder | gui/internal/settings/settings_test.go |

### `gui/internal/ui`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
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
| `dump.go:36-37, 39-40` | `NoCoreFiles` | 2 | E | setrlimit/prctl on our own process don't fail | keep |
| `dump.go:48-49` | `coreRlimit` | 1 | E | setrlimit/prctl on our own process don't fail | keep |
| `failed.go:67-68` | `Window.failedRow` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `failed.go:90-91` | `Window.drawFailed` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `failed.go:102-103` | `Window.againAction` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `failed.go:131-131, 132-132, 133-134, 136-136, 153-153` | `runFolder` | 5 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `failed.go:170-171` | `Window.redrawHeaders` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `launcher.go:37-37, 38-39, 40-41, 46-46` | `gtkLauncher.launch` | 5 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:42-42, 43-44` | `gtkLauncher.launch.func` | 2 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:49-49` | `gtkLauncher.OpenFile` | 1 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:50-50` | `gtkLauncher.OpenFolder` | 1 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:53-54, 55-56` | `gtkLauncher.ChooseFolder` | 3 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `launcher.go:57-57, 58-59, 60-62, 63-63` | `gtkLauncher.ChooseFolder.func` | 6 | C | GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one) | VM desktop |
| `logs.go:72-72, 73-75, 88-88, 100-100, 101-102, 115-116, 143-144, 151-152, 183-184, 208-208, 226-226` | `Window.newLogs.func` | 12 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `logs.go:241-242, 249-249, 251-251, 253-253` | `entryClass` | 4 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `logs.go:270-273, 275-278` | `Window.refreshLogs` | 6 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `logs.go:300-305` | `placeholder` | 5 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `logs.go:322-326, 328-328, 329-329, 330-331, 337-341` | `Window.showRun` | 11 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `logs.go:357-357, 359-359` | `Window.openLog` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `logs.go:396-397` | `Window.toggleSection` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `overview.go:163-163` | `cardByID` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `overview.go:298-299` | `Window.lastRunFolder` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `overview.go:373-374` | `Window.refreshLastRun` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `overview.go:417-418` | `Window.updateOverview` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `secret.go:30-31` | `editableBytes` | 1 | E | the password entry is always a GtkEditable with a buffer | keep |
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
| `style.go:39-40, 43-46` | `loadStyle.func` | 4 | E | the embedded CSS parses; only a future GTK could report errors | keep: it guards GTK upgrades |
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
| `window.go:133-133` | `newWindow.func` | 1 | C | the launcher's report callback: only a real launch failure (VM desktop) | VM desktop |
| `window.go:166-167` | `newWindow.func` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `window.go:239-240` | `Window.start` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `window.go:294-297` | `Window.send.func` | 3 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `window.go:309-309` | `pumpDone` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `window.go:334-335` | `Window.apply` | 1 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `window.go:356-358` | `Window.redraw` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `window.go:429-430, 453-454` | `Window.statusText` | 2 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |
| `window.go:472-473, 484-485, 515-517` | `Window.closeRequest` | 4 | A | a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt | gui/internal/ui/harness_test.go |

### `internal/audit`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `audit.go:91-92` | `Config.deadline` | 1 | A | Config with Deadline 0 gets DefaultDeadline | internal/audit/audit_test.go |
| `audit.go:215-216` | `fetch` | 1 | A | RPCURL with a control character fails http.NewRequest | internal/audit/audit_test.go |
| `result.go:88-89` | `Result.save` | 1 | A | a corrupt snapshot's high "maintainer snapshot" finding has no previous entry: commit it with holdHigh | internal/audit/snapshot_test.go |
| `result.go:186-186` | `packageNames` | 1 | E | only called with the gating (high) names, never none | keep: // unreachable: callers pass ≥1 name |
| `result.go:244-244` | `Result.Note` | 1 | A | corrupt snapshot in a dry run | internal/audit/snapshot_test.go |
| `snapshot.go:76-77` | `Entry.UnmarshalJSON` | 1 | E | b starts with a quote and the outer decoder already validated the JSON, so the string always unmarshals | keep: // unreachable: validated JSON string |
| `snapshot.go:177-178` | `WriteSnapshot` | 1 | E | json.MarshalIndent of map[string]Entry can't fail | keep: // unreachable: plain map marshals |
| `snapshot.go:181-182` | `WriteSnapshot` | 1 | A | MkdirAll under a regular file fails | internal/audit/snapshot_test.go |
| `snapshot.go:192-193, 198-199` | `WriteSnapshot` | 2 | A | a pre-created lock file in a 0500 dir: the lock opens, the corrupt file's rename aside / CreateTemp fail | internal/audit/snapshot_test.go |
| `snapshot.go:202-204, 206-208, 210-211` | `WriteSnapshot` | 5 | E | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam | keep: idiomatic error return, no seam |
| `snapshot.go:213-214` | `WriteSnapshot` | 1 | A | the snapshot path is a non-empty directory: rename over it fails | internal/audit/snapshot_test.go |
| `snapshot.go:217-218` | `WriteSnapshot` | 1 | E | opening the directory it just wrote into can't fail | keep |
| `snapshot.go:229-230` | `lockSnapshot` | 1 | A | lock file can't be created in a 0500 dir | internal/audit/snapshot_test.go |
| `snapshot.go:236-236` | `lockSnapshot` | 1 | E | flock(LOCK_EX|LOCK_NB) on an open regular file fails only with EWOULDBLOCK (handled) or EINTR | keep |
| `state.go:63-64` | `WriteState` | 1 | E | json.MarshalIndent of State can't fail | keep: // unreachable |
| `state.go:71-72` | `WriteState` | 1 | A | MkdirAll under a file / CreateTemp in a 0500 dir / rename onto a directory | internal/audit/state_test.go |
| `state.go:75-77, 79-81, 83-84` | `WriteState` | 5 | E | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam | keep |
| `state.go:86-87` | `WriteState` | 1 | A | StatePath is a non-empty directory: the rename fails | internal/audit/state_test.go |
| `state.go:90-91` | `WriteState` | 1 | E | opening the directory it just wrote into can't fail | keep |
| `state.go:154-156` | `Result.writeState` | 2 | A | StatePath under a regular file: the WARN is logged | internal/audit/state_test.go |

### `internal/downloads`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `downloads.go:48-49` | `PasswdHome` | 1 | E | user.Current fails only when the uid has no passwd entry | keep |
| `downloads.go:81-81` | `Options.now` | 1 | A | Delete with Options.Now nil uses the real clock | internal/downloads/downloads_test.go |
| `downloads.go:106-107` | `homeDirs.func` | 1 | A | Options.Home an absolute path that doesn't exist (EvalSymlinks fails) | internal/downloads/downloads_test.go |
| `downloads.go:180-181` | `checkDownloadsDir` | 1 | E | Stat after a successful EvalSymlinks fails only if the folder vanishes in between (race) | keep: TOCTOU guard |
| `downloads.go:213-214, 236-237, 243-244` | `xdgDownloadDir` | 3 | A | user-dirs.dirs with a relative XDG_CONFIG_HOME and no home, $HOME with no home, a relative value | internal/downloads/downloads_test.go |
| `downloads.go:290-291` | `Scan` | 1 | A | a missing/0000 folder (OpenRoot), the mountinfoPath seam on a missing file, a 0300 folder (ReadDir) | internal/downloads/downloads_test.go |
| `downloads.go:295-296` | `Scan` | 1 | E | Stat(".") of a root it just opened can't fail | keep |
| `downloads.go:299-300, 303-304` | `Scan` | 2 | A | the mountinfoPath seam on a missing file; a 0300 folder (ReadDir fails) | internal/downloads/downloads_test.go |
| `downloads.go:328-329` | `oldDownloads` | 1 | A | a folder that opens but can't be listed (0300) | internal/downloads/downloads_test.go |
| `downloads.go:359-360` | `entryIsOld.func` | 1 | A | an old directory holding an unreadable (0000) subdirectory counts as new | internal/downloads/downloads_test.go |
| `downloads.go:366-367` | `entryIsOld.func` | 1 | E | d.Info fails only if the node vanishes mid-walk (race) | keep: TOCTOU guard |
| `downloads.go:381-382` | `changedBefore` | 1 | E | on Linux every os FileInfo carries a *syscall.Stat_t and Stat of an open root can't fail | keep: // unreachable on Linux |
| `downloads.go:395-396, 399-400` | `dirIdentity` | 2 | E | on Linux every os FileInfo carries a *syscall.Stat_t and Stat of an open root can't fail | keep: // unreachable on Linux |
| `downloads.go:409-410` | `<package>` | 1 | E | on Linux every os FileInfo carries a *syscall.Stat_t and Stat of an open root can't fail | keep: // unreachable on Linux |
| `downloads.go:431-432` | `newBoundary` | 1 | A | mountinfoPath seam pointing at a missing file | internal/downloads/downloads_test.go |
| `downloads.go:492-493, 501-502` | `Delete` | 2 | A | the folder removed after the scan (OpenRoot fails); mountinfoPath seam on a missing file | internal/downloads/downloads_test.go |
| `downloads.go:576-577` | `remover.removeTree` | 1 | E | Lstat of a node just listed fails only if it vanishes (race) | keep: TOCTOU guard |
| `downloads.go:591-592` | `remover.removeTree` | 1 | A | a file in an old 0500 subdirectory (clock moved past the chmod with Options.Now): Remove fails EACCES | internal/downloads/downloads_test.go |
| `downloads.go:608-609, 612-613, 614-615, 618-619, 623-624` | `remover.removeContents` | 5 | E | OpenRoot/Lstat/Open/Readdirnames of a directory entryIsOld just walked fail, or it is swapped, only in a race; no seam between the checks | keep: TOCTOU guard |
| `downloads.go:638-638` | `inode` | 1 | E | on Linux every os FileInfo carries a *syscall.Stat_t and Stat of an open root can't fail | keep: // unreachable on Linux |
| `downloads.go:646-647` | `PathErrCause` | 1 | A | Scan of a missing folder gives a *fs.PathError | internal/downloads/downloads_test.go |

### `internal/engine/enginetest`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `contract.go:83-84, 85-86, 88-89` | `Checker.Check.func` | 3 | A | the checker's own failure paths: add the missing hand-built breaches | internal/engine/enginetest/contract_test.go |
| `contract.go:146-147, 161-162, 167-168, 182-183, 199-200, 205-205, 207-207, 209-209, 222-223` | `Checker.Check` | 9 | A | the checker's own failure paths: add the missing hand-built breaches | internal/engine/enginetest/contract_test.go |

### `internal/engine/stub`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `fullcleanup.go:93-93` | `builder.index` | 1 | E | the hand-built script only names its own steps | keep: programming-error panic |

### `internal/frontend`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `topgrade.go:51-52, 55-56, 58-59, 61-63` | `EnsureTopgradeConfig` | 5 | A | Lstat error other than not-exist (0000 parent), MkdirAll under a file, the file appearing between Lstat and OpenFile (O_EXCL), a 0500 dir | internal/frontend/topgrade_test.go |

### `internal/helperclient`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `combined.go:144-145, 165-165` | `decoder.partProgress` | 2 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go |
| `combined.go:181-182, 186-187, 202-203` | `decoder.partDone` | 4 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go |
| `combined.go:221-221` | `decoder.combinedOutcome` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go |
| `decode.go:121-122` | `decoder.feed` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go |
| `decode.go:184-185` | `decoder.readCheck` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go |
| `decode.go:235-236` | `decoder.readResult` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go |
| `plan.go:94-95` | `CheckPlan` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go |
| `plan.go:132-133` | `PlanOrphans` | 1 | C | runs the installed /usr/lib/archmaid/archmaid-helper plan-orphans, which runs the real pacman -Qdtq/-Rs --print | VM |
| `plan.go:142-143` | `planOrphans` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go |
| `plan.go:191-192` | `decoder.readPlan` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go |
| `plan.go:222-222` | `exitCode` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go |
| `run.go:99-100, 112-113` | `Run` | 2 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go |
| `run.go:189-189` | `helperInstalled` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go |
| `run.go:280-281` | `resultText` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go |
| `run.go:358-359` | `decoder.outcome` | 1 | A | a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH | internal/helperclient/*_test.go |
| `run.go:374-374` | `exitText` | 1 | A | a fake helper killed by a signal | internal/helperclient/run_test.go |
| `run.go:403-404` | `tail.Write` | 1 | A | a fake helper writing >4 KiB to stderr | internal/helperclient/run_test.go |

### `internal/helperclient/helpertest`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `helpertest.go:16-16` | `testOnly` | 1 | E | test-helper guards: panic outside a test binary or on an empty path | keep |
| `helpertest.go:29-29` | `UseHelperFile` | 1 | E | test-helper guards: panic outside a test binary or on an empty path | keep |

### `internal/privilege`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `askpass.go:119-122` | `Listen` | 3 | E | WriteFile of the wrapper into the 0700 dir just created can't fail | keep |
| `askpass.go:193-193` | `Server.serve` | 1 | A | net.Pipe relays that hang up or answer the wrong line; a harden func that fails | internal/privilege/askpass_test.go |
| `askpass.go:206-207, 213-214, 216-217` | `Server.peerOf` | 3 | E | SyscallConn/Control/SO_PEERCRED on an accepted unix conn don't fail | keep: // unreachable |
| `askpass.go:349-350, 354-355` | `Request.deliver` | 2 | A | net.Pipe relays that hang up or answer the wrong line; a harden func that fails | internal/privilege/askpass_test.go |
| `askpass.go:378-379, 382-383, 386-387` | `ReadAnswer` | 3 | A | net.Pipe relays that hang up or answer the wrong line; a harden func that fails | internal/privilege/askpass_test.go |
| `askpass.go:515-516` | `verifyPeer` | 1 | E | os.Executable fails only without /proc | keep |
| `faillock.go:110-111` | `faillockStatus` | 1 | A | a tally path that is a directory, an unreadable faillock.conf, a scanner error (an over-long line) | internal/privilege/faillock_test.go |
| `faillock.go:168-169` | `readTally` | 1 | A | a tally path that is a directory, an unreadable faillock.conf, a scanner error (an over-long line) | internal/privilege/faillock_test.go |
| `faillock.go:201-202, 234-235` | `readFaillockConf` | 2 | A | a tally path that is a directory, an unreadable faillock.conf, a scanner error (an over-long line) | internal/privilege/faillock_test.go |
| `kept.go:68-69, 71-73` | `KeptPassword.Keep` | 3 | E | mmap/mlock fail only under a tiny RLIMIT_MEMLOCK; the wipe check can't see non-zero bytes after the wipe | keep: defensive |
| `kept.go:136-137` | `KeptPassword.wipeLocked` | 1 | E | mmap/mlock fail only under a tiny RLIMIT_MEMLOCK; the wipe check can't see non-zero bytes after the wipe | keep: defensive |
| `kept.go:158-159, 162-163` | `CheckPasswordBytes` | 2 | A | a password with a line break; su's check failing (shim) | internal/privilege/kept_test.go |
| `kept.go:187-188` | `Request.AnswerBytes` | 1 | A | a password with a line break; su's check failing (shim) | internal/privilege/kept_test.go |
| `privilege.go:177-178` | `CheckPassword` | 1 | A | su shim missing/failing to start | internal/privilege/privilege_test.go |

### `internal/privilege/privtest`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `privtest.go:18-18` | `testOnly` | 1 | E | panics outside a test binary | keep |

### `internal/proc`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `detach.go:57-58` | `groupAlive` | 1 | E | Getpgid errors other than ESRCH (EPERM across namespaces) don't happen for our own children | keep |
| `proc.go:80-81` | `checkSudo` | 1 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `proc.go:144-144, 149-149, 153-153` | `envCommand` | 3 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `proc.go:422-423` | `Child.GroupStopped` | 1 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `proc.go:445-445` | `Child.escalateGroup` | 1 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |
| `procfs.go:40-41, 44-45, 51-52, 57-58` | `readAt` | 4 | A | sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files | internal/proc/*_test.go |

### `internal/sanitize`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `sanitize.go:217-217, 227-228` | `escapeEnd` | 2 | A | CSI with intermediates, a CSI cut off before its final byte, OSC ended by ESC \ | internal/sanitize/sanitize_test.go |

### `internal/sysstat`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `sysstat.go:112-112` | `meminfoKB` | 1 | A | a meminfo fixture without the key | internal/sysstat/sysstat_test.go |

### `internal/testenv`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `cover.go:24-25` | `coverChildren` | 1 | E | the early return is for runs without coverage, which count nothing | keep |
| `orphans.go:27-30, 31-32, 33-34, 35-35, 36-38, 39-39, 40-40, 41-43, 46-46, 47-48, 49-49, 51-52, 54-56, 57-58, 59-59` | `reapLeftovers` | 24 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `orphans.go:64-65, 66-67, 68-70, 71-72, 73-73` | `report` | 8 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `orphans.go:79-79, 80-82` | `reap` | 3 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `orphans.go:89-90, 91-92, 93-95, 96-97, 98-98, 100-101, 102-102, 104-104, 105-105, 107-107, 108-109, 111-111, 112-113, 127-128, 129-129, 130-130, 132-132, 133-134, 137-137` | `leftovers` | 25 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `orphans.go:114-114, 115-115, 116-117, 118-119, 120-120, 121-122, 123-123, 125-125, 136-136, 136-136` | `leftovers.func` | 11 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `orphans.go:143-144, 145-146, 147-147` | `readStat` | 4 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `orphans.go:173-174, 175-176, 177-178, 179-179, 180-181, 183-183` | `hasMarker` | 8 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `orphans.go:188-189, 190-191, 192-192` | `cmdline` | 4 | A | runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts | internal/testenv/orphan_test.go |
| `testenv.go:94-96, 100-102, 108-110, 115-117, 120-122, 125-127, 131-133, 137-139` | `Run` | 16 | E | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) | keep: fail closed |
| `testenv.go:164-165, 166-166, 167-167, 168-169, 170-171, 174-175, 176-178, 179-179, 180-181, 182-183, 184-184, 186-186, 187-188, 189-190, 191-191, 193-193, 194-196, 197-197, 198-199, 200-200` | `Run` | 31 | A | the post-m.Run checks (reap, trap report, guard compare): run every time, counted never (after the counters are written); a test calling Run on a fake M counts them | internal/testenv/trap_test.go |
| `testenv.go:208-209, 217-218` | `guardScript` | 2 | A | Run from a dir with no scripts/state-guard.sh above it; a guard script that fails | internal/testenv/trap_test.go |
| `testenv.go:231-232` | `guardSnapshot` | 1 | A | Run from a dir with no scripts/state-guard.sh above it; a guard script that fails | internal/testenv/trap_test.go |
| `testenv.go:240-242` | `guardCompare` | 2 | A | post-m.Run: counted only from a direct test | internal/testenv/trap_test.go |
| `testenv.go:252-253, 255-256, 264-265` | `InstallTraps` | 3 | E | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) | keep: fail closed |
| `testenv.go:288-289` | `newMarker` | 1 | E | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) | keep: fail closed |
| `testenv.go:298-299` | `setChildSubreaper` | 1 | E | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) | keep: fail closed |

### `internal/testtranscript`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `recorder.go:69-70` | `NewRecorder` | 1 | A | fake targets failing Start/Next/Reply and an odd replace list | internal/testtranscript/recorder_test.go |
| `recorder.go:91-92, 106-107` | `Recorder.Run` | 2 | A | fake targets failing Start/Next/Reply and an odd replace list | internal/testtranscript/recorder_test.go |
| `recorder.go:198-198` | `Recorder.event` | 1 | A | fake targets failing Start/Next/Reply and an odd replace list | internal/testtranscript/recorder_test.go |
| `recorder.go:229-230` | `Recorder.reply` | 1 | A | fake targets failing Start/Next/Reply and an odd replace list | internal/testtranscript/recorder_test.go |

### `internal/ttyguard`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `ttyguard.go:63-64` | `Hold` | 1 | E | TIOCGEXCL/TIOCEXCL on an fd IsTerminal just accepted fail only if it is closed concurrently | keep |
| `ttyguard.go:122-123` | `Exclusive` | 1 | E | TIOCGEXCL/TIOCEXCL on an fd IsTerminal just accepted fail only if it is closed concurrently | keep |
| `ttyguard.go:130-131, 136-137` | `ioctl` | 2 | E | TIOCGEXCL/TIOCEXCL on an fd IsTerminal just accepted fail only if it is closed concurrently | keep |

### `gui/cmd/archmaid-gui`

| Lines | Function | Stmts | Cat | Why | Test home / recommendation |
|---|---|---|---|---|---|
| `main.go:all` | `main` | 6 | B | the GUI binary's main: in no run's data at all (no test links it); a headless start of the cover archmaid-gui counts it | gui/scripts/headless.sh + cover archmaid-gui (new) |
