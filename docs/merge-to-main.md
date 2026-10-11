# Merging `refactor/p0` into `main`: readiness review

For the owner, to decide whether and when to merge. Reviewed on 2026-10-05
against `origin/refactor/p0` at 6e6f70e and `origin/main` at 5585358. It
was rebased onto 99272a5; the commits after 6e6f70e are the CI fix for the
pty test, a CLAUDE.md note, a HANDOVER update and the security review
(`docs/security-review-merge.md`, which complements this document).
Nothing here was run on the real machine: the facts come from the code,
`git`, `go version -m`, `ls` and the pacman database files.

**Verdict: no-go today, go once section 5's "before the merge" items are
ticked.** The code is in good shape: the gate passes locally, `main` is an
ancestor of `refactor/p0` (0 commits on `main` that `refactor/p0` lacks,
305 the other way at 99272a5), so the merge is a fast-forward with no conflicts. What
blocks it is process: CI isn't green on the tip yet, and the GUI's
one-password change (decision F) isn't reviewed or integrated. Merging
before that would put on `main` a GUI that asks for topgrade's sudo password
separately from the polkit dialog.

## 1. What changes for you on merge

The merge itself changes nothing on the machine. Changes come from what you
install from `main` afterwards (section 2). Your machine today:

| Item | Now |
|---|---|
| `~/.local/bin/archmaid` | the **old** TUI, built from 83ecad2 (2026-09-23, `vcs.modified=true`), module `…/archmaid/tui` |
| `/usr/bin/archmaid`, `/usr/lib/archmaid/archmaid-helper`, policy, widget files, timer units | package `archmaid-git r357.2783217-1` (the live-run commit) |
| `archmaid-gui-git` | not installed |
| `~/.local/share/applications/archmaid.desktop` | `install.sh`'s entry, `Exec=kitty /home/Enzord/.local/bin/archmaid` |
| `archmaid-updates-check.timer` | not enabled |
| DMS plugin | files in `/etc/xdg/quickshell/dms-plugins/archmaid/`; whether it is enabled in DMS wasn't checked |
| `~/archmaid-live` | git worktree, detached at 2783217 |

### The TUI (from `main`'s old TUI to the new one)

The menu, keys and screens are the same; underneath it runs on the shared
engine and Driver. Changes you'll notice (details in `CHANGELOG.md`):

- `archmaid --help`/`-h` prints usage and exits 0. An unknown option exits 2.
  Neither starts the TUI. Nothing is deleted at startup any more.
- Full Cleanup looks for orphans **after** topgrade, and the confirm shows the
  whole `pacman -Rns` cascade ("N orphaned — M total"). Temp files are aged by
  `systemd-tmpfiles --clean` (10 days in `/tmp`, 30 in `/var/tmp`), not a
  2-day delete.
- Run topgrade also opens with the AUR audit and its gate. An *added*
  co-maintainer gates like a maintainer change. A corrupt snapshot gates
  instead of quietly starting over.
- Repair scans as you first and asks for sudo only if something looks broken,
  then rechecks that as root.
- Delete old downloads resolves the folder (`--downloads`,
  `$ARCHMAID_DOWNLOADS`, XDG, `~/Downloads`, `~/Download`), never `$HOME`,
  judges age by ctime, lists what it will delete and never crosses a mount.
- A mid-run sudo popup names the step and the real caller. Output is
  stripped of terminal escapes. No step child can read the TUI's terminal any
  more, which fixes the frozen-keys case with git over HTTPS and ssh.
- If the engine panics: "archmaid's engine stopped unexpectedly", and any key
  quits.

### The GUI (new: `archmaid-gui`, package `archmaid-gui-git`)

GTK4/libadwaita, with Overview, Run, Logs and Settings pages. Root steps go
through pkexec and the helper. At 6e6f70e, Full Cleanup's root steps are
**one** polkit dialog (decision E), and topgrade's sudo asks through the
GUI's own password dialog. `feat/gui-one-password` (decision F, not yet
merged) makes that one password per run. Without the GUI package, the DMS
widget opens the TUI in `$TERMINAL`.

### Files

`~/.local/state/archmaid/` (the layout is the same as the installed r357
package's; compared with `main`'s TUI):

| File | On `main` | After |
|---|---|---|
| `<YYYYMMDD-HHMM>/{topgrade,orphans,cleanup,audit}.log` | same | 0700/0600 enforced; 5 runs kept **including** the current one; rotated at a real run's first log write, never at startup |
| `dryrun/` | same | also holds `dryrun/last-run.json` |
| `aur-maintainers.json` | `{"pkg": "maintainer"}` | `{"pkg": {"maintainer": …, "comaintainers": […]}}`, written atomically. **Yours is already in the new format.** The old format still loads |
| `aur-maintainers.lock` | — | new (flock for the snapshot) |
| `aur-maintainers.json.corrupt-<time>` | — | only when you accept a corrupt snapshot |
| `last-run.json` | — | new: how the last action ended (`ok`/`failed`/`incomplete`/`aborted`/`cancelled`) |
| `audit-state.json` | — | new: unacknowledged high AUR findings, for the widget |
| `updates-cache.json` | — | new, only when the timer is enabled |

`~/.config/archmaid/gui.json`: the GUI's Settings (dry run, downloads
folder), 0600. It exists already, from the live run. `~/.config/topgrade.toml`
is handled as before.

### Packages (`packaging/arch/PKGBUILD`, split)

- **`archmaid-git`**: `/usr/bin/archmaid` (the TUI), `/usr/lib/archmaid/archmaid-helper`
  (0755 root, never setuid, run only through pkexec), the polkit policy (9 actions,
  one per helper verb), `archmaid.desktop` + icon, the DMS widget in
  `/etc/xdg/quickshell/dms-plugins/archmaid/`, the `archmaid-updates-check`
  user timer (not enabled), `/usr/share/doc/archmaid/helper.md`. It depends on
  `pacman-contrib`, `fakeroot`, `polkit` and `systemd`.
- **`archmaid-gui-git`**: `/usr/bin/archmaid-gui`, its `.desktop` file, the
  AppStream metainfo and an icon under the app id. It depends on the
  **exact** `archmaid-git` of the same build (`=$pkgver-$pkgrel`), so install
  and upgrade the two together.

### The old `~/.local/bin/archmaid`: yes, it still shadows `/usr/bin/archmaid`

`~/.local/bin` is the first entry on your PATH (`/usr/bin` is the fifth), and
`which -a archmaid` lists `~/.local/bin/archmaid` first. Your `~/.local`
desktop entry also shadows the package's (same desktop id), and it runs the
old binary by its full path. The widget's terminal fallback runs whichever
`archmaid` comes first on quickshell's PATH. Running the old build now is
**harmful**, not just stale:

1. It prunes the run history to 5 folders at **every** start, `--help`
   included (the 2026-10-04 incident).
2. It reads `aur-maintainers.json` as `map[string]string`. Your file is in
   the new object format, so the old build sees "no baseline". It silently
   treats the audit as a first one and overwrites the snapshot in the old
   format: the co-maintainer records are gone, and a takeover since your
   last audit would be accepted without a gate. This is exactly the failure
   the new code's fail-closed snapshot prevents.
3. It writes no `last-run.json` or `audit-state.json`, so the widget goes stale.

**What to do:** remove it right after the merge, before you start
`archmaid` from a shell again: `~/archmaid/install.sh --uninstall` (it
removes `~/.local/bin/archmaid`, the `~/.local` icon and the desktop entry),
then `hash -r` (or open a new shell). Check that `command -v archmaid`
prints `/usr/bin/archmaid`. Don't run `install.sh` again afterwards: it
would put a copy back in `~/.local/bin` and shadow the package. Use the
package only.

## 2. Packaging after the merge

- **Default fragment `branch=main`** (PKGBUILD:46). Right for after the
  merge: a plain `makepkg` in a scratch copy then clones GitHub's `main`, so
  it needs the merge **pushed**. Today it would build the old `main`, which
  has no `cmd/`, so it fails.
- **pkgver** = `r<rev-list --count HEAD>.<short hash>`: r357 installed;
  6e6f70e is r361, 99272a5 is r365. A fast-forward keeps those numbers,
  and a `--no-ff` merge commit adds one. Either way pacman sees an upgrade
  over r357. The count grows only along one history: never build a
  branch that isn't based on what you installed (a rebased branch can give
  a lower count).
- **`packaging/arch/README.md`'s recipe** was written for the live run
  (`W=~/archmaid-live`). It is right in substance for `main`: it builds the
  commit `W` names from `ARCHMAID_REPO=file://$HOME/archmaid`, and it needs
  `archmaid.install` copied along. This commit adds the after-merge
  variant: `W=~/archmaid` on a `main` you've updated with `git -C ~/archmaid
  pull --ff-only`. Or skip both variables and let it clone GitHub `main`.
- Install both packages in one transaction, because of the exact-version
  dependency:
  `sudo pacman -U "$P"/archmaid-git-r*.pkg.tar.zst "$P"/archmaid-gui-git-r*.pkg.tar.zst`.
- Not yet done: `namcap` on the packages; a license (`LicenseRef-unknown`, a
  `TODO` for LICENSE); `# Maintainer: TODO`. None of these blocks a private
  install.

## 3. Docs consistency

Line numbers are at 6e6f70e. "Fixed" means fixed in this commit, by a
factual correction only.

| Where | Stale statement | Status |
|---|---|---|
| `INSTALL.md:54-58` | logs are `<stamp>.log` for failed runs only, `dryrun.log`, "older than a week are pruned at startup" | fixed |
| `cmd/archmaid/README.md:108-109`, `:180-181` | run folders "pruned at/on startup" | fixed |
| `docs/HANDOVER.md:62` | "The TUI doesn't use it [the Session] yet (E7)" | fixed |
| `docs/HANDOVER.md:64` | "The GUI builds against [the stub] until the real Driver (E6)" | fixed |
| `docs/HANDOVER.md:246-247` | Repair's `NeedsRoot` "held as `model.needsRoot`" by the TUI (the engine does it now) | fixed |
| `docs/HANDOVER.md:403`, `:413` | "the planned DMS widget"; "`audit-state.json` is planned, not written yet" (line 458 describes it as written) | fixed |
| `docs/HANDOVER.md:455-457` | `RunRecord` doesn't carry `incomplete`/`steps_refused` yet (it does, since 96911ea; the open part is `withRefused` in `dms_test.go`) | fixed |
| `docs/HANDOVER.md:674-680` | "GUI on the real Driver: not yet run live" (line 307 cites the first live GUI run) | fixed (also by 0c4c15d) |
| `docs/HANDOVER.md:681-685` | "`proc.Child.Stop`'s SIGKILL timer outlives `Driver.Close`" (fixed by 8145c66: `escalateKill` returns once the child is reaped) | removed (also by 0c4c15d) |
| `docs/HANDOVER.md:686-688` | "`archmaid-gui-git` is not live-tested yet": reworded to what is true now (the package has never been installed) | fixed |
| `docs/TESTING.md:11` | "four short VHS tapes" (there are 8 plus a `--help` check) | fixed |
| `docs/TESTING.md:34` | layering: "nothing under `internal/` imports charmbracelet" | fixed |
| `docs/TESTING.md:180-185` | `real-refused`: "paccache's dialog… pkexec gets exactly the six helper verbs"; `real-locked`: "each helper row …locked until" (now one `full-cleanup` call) | fixed |
| `docs/TESTING.md:779` | "what isn't built yet panics (`Abort`, `SubmitPassword`, …)" | fixed |
| `docs/TESTING.md:787` | e2e row lists only 4 of the 9 checks | fixed |
| `docs/first-live-run.md:586-639` (C4) | one polkit dialog per root step, `yay -Sc` with sudo, `steps_refused` 1 | status note added at the top; C4 itself needs a rewrite after one-password |
| `docs/first-live-run.md:16-17`, `:136-146` | builds from an `~/archmaid-live` worktree of `refactor/p0` | covered by the status note |
| `packaging/arch/README.md:55-56` | the recipe names only `~/archmaid-live` | fixed (after-merge variant added) |
| `README.md:11-28` | install is only `install.sh` into `~/.local/bin`; no word on the packages, the GUI, the helper/polkit or the widget | **open**: a README rewrite for `main`, not a factual fix. Recommended before or right after the merge, with a warning not to mix `install.sh` with the package |
| `README.md:44-47` | "Sudo is handled in-TUI" (true for the TUI; the GUI's polkit path isn't mentioned) | open, same rewrite |
| `CHANGELOG.md:5` | "range `5585358..refactor/p0` (249 commits, tip f46230a)": the tip is 99272a5, 305 commits, with no entries for the one-dialog `full-cleanup` call, detached GUI children and signals, ttyguard, or the `yay -Sc --aur` split | **open**: needs a changelog refresh (a separate docs task) |
| `CHANGELOG.md:11`, `:13`, `:189`; `packaging/arch/README.md:39-44` | "not run live yet", "Don't install `archmaid-gui-git` yet" | open: **your call** after the live check in section 5 |

## 4. Open items and residual risks

### Landmines that are accepted risks, not bugs (from `docs/HANDOVER.md`)

- **Askpass impersonation under topgrade.** Code already running inside
  topgrade (a PKGBUILD's `build()`) can pass the peer checks. The popup's
  "caller" line is the only tell. Topgrade stays the only update path.
- **An Abort can't stop the combined `full-cleanup` call** once its dialog is
  granted (it runs as root), and the polkit message is fixed text: the
  in-app confirms are what say whether orphans and the wipe are included.
- **Orphan-removal TOCTOU**: a window of milliseconds against a concurrent
  root pacman. `-Rns` still refuses to remove a required package.
- **The GUI password's last copy** is a Go string (E0 is frozen); accepted.
- **ttyguard doesn't cover root.** A SIGKILLed TUI leaves the terminal
  exclusive until it is closed.
- **sudo reached through a script or a variable** isn't seen by `proc`'s
  `ErrSudoMayPrompt` check.
- **Downloads**: a node swapped between its Lstat and its unlink.
- **GUI RAM on NVIDIA with Vulkan** (so cairo is the default).
- **The yay 72 h cooldown hook** lives in `~/dotfiles`, outside this repo.

### Still unverified live

- `auth_admin_keep` scope inside a real session (`docs/polkit-keep-session-check.md`).
  Answered on 2026-10-12: session scope in the owner's graphical session
  (G3 rc=0, G5 `unix-session`); HANDOVER has the scope rule.
- Whether a pkexec killed while its dialog is up costs a faillock failure.
  Answered: yes, one row (VM check Part C, K2), and so does any cancel at a
  polkit dialog once PAM is asking (HANDOVER).
- One-password (not merged yet). HANDOVER (0c4c15d) records that the VM
  runs have confirmed one polkit dialog per Full Cleanup and topgrade's
  askpass. The `archmaid-gui-git` package itself has never been installed on
  your machine.
- The DMS widget in the live bar (it has only loaded in a scratch quickshell).

### `git grep -n TODO`

- `packaging/arch/PKGBUILD:1` (Maintainer), `:23-27` (license, release
  fragment, checksum), `:140` (install LICENSE); `packaging/arch/README.md:114`
  and `CHANGELOG.md:185` say the same.
- `gui/internal/ui/window.go:163` `TODO(P6b-4)`: dragging the scrollbar or
  the keyboard doesn't stop the output from following.
- No TODO marker, but open: `packaging/dms/dms_test.go` `withRefused` should
  use the real `RunRecord` writer.

### Tests that don't run on CI

The gate runs `go test` without `-v`, so CI logs don't show skips. These
follow from the code and the CI images:

- `packaging/dms` `TestStateJS` skips without `qmltestrunner`, which neither
  CI image installs. **The widget's `state.js` is tested only locally**
  (it passes on the dev machine).
- `internal/downloads` `TestDownloadsMountPoints` skips without unprivileged
  user and mount namespaces (likely in the `archlinux` container; the fake
  mountinfo tests still run).
- The perf guard (`TestPerfGuard`) and the GUI's perf budgets are opt-in and
  never enforced in CI. The perf-guard baselines also need re-measuring
  after E7b (CHANGELOG:190).
- The known failing test: `internal/tui` `TestStepCannotReadTheTerminal`
  with `CI=true` (fixed by 4d3b11d, whose CI run was still queued when this
  was written).
- PKGBUILD `check()` runs only the helper's, the widget's and `runview`'s
  tests, and makepkg/namcap never run in CI.
- A flake seen locally during this review (load average ~5; it passed on the
  rerun): `internal/proc` `TestDetach/KillGroup_kills_the_group_at_once`,
  "the leftover went with the SIGINT". The test's
  `(trap '' INT; exec sleep 30) &` prints `holder $!` before the subshell
  has set its trap, so a `Stop` that comes early kills the leftover. That
  is a race in the test, not in `proc`. The test should wait until the
  trap is set (for example, the subshell echoes after `trap`). It can turn
  CI red the same way. In another gate run, the coverage pass hit
  `internal/tui` `TestTranscriptSession/fullcleanup_abort_sigkill.shim`:
  "the running step's child never reached its shim" after 30 s. That is the
  same class as the e71424d flake Dev 2 is already looking into.

## 5. Go/no-go checklist

Tick these in order. The merge and the pushes are yours; agents don't push
or merge.

**Before the merge**

- [ ] CI is green on all four jobs (core, e2e, bench, gui) for the
      `refactor/p0` tip: the `TestStepCannotReadTheTerminal` fix (4d3b11d)
      has passed, and the earlier gui/e2e failures (e71424d, 2783217) are
      confirmed as one-off flakes or fixed.
- [ ] `feat/gui-one-password` has passed its security review, its findings
      are fixed, and it is integrated into `refactor/p0`; `./scripts/check.sh`,
      `make e2e` and `make gui-test` pass, and CI is green on the new tip.
- [ ] A live check of that tip (Dev 5 in the VM, or you with a refreshed
      `docs/first-live-run.md` C4): one GUI Full Cleanup with **one** polkit
      dialog and one password, a cancelled dialog marking every root row
      "Not authorised", and one TUI Full Cleanup. Results written down.
- [ ] `CHANGELOG.md` refreshed to the tip. Optional: the README rewrite
      (section 3).
- [ ] Your decision on the GUI: install `archmaid-gui-git` now (the widget
      then opens it) or keep the TUI only. Update `CHANGELOG.md:13`/`:189`
      and `packaging/arch/README.md:39-44` to match.

**The merge**

- [ ] `git -C ~/archmaid fetch origin && git -C ~/archmaid switch main &&
      git -C ~/archmaid merge --ff-only origin/refactor/p0 && git -C ~/archmaid push origin main`
      (or `--no-ff` if you want a merge commit to mark the refactor).
- [ ] CI is green on `main`.

**Right after it**

- [ ] Remove the old build: `~/archmaid/install.sh --uninstall && hash -r`;
      `command -v archmaid` prints `/usr/bin/archmaid`. Do this before any
      `archmaid` start from a shell (section 1).
- [ ] Back up the state folder: `cp -a ~/.local/state/archmaid ~/archmaid-state-backup-$(date +%Y%m%d-%H%M)`.
- [ ] Build from `main` (`packaging/arch/README.md`, `W=~/archmaid`) and install:
      `sudo pacman -U "$P"/archmaid-git-r*.pkg.tar.zst` plus `archmaid-gui-git`
      in the same command if you chose the GUI. `pacman -Q archmaid-git`
      shows the `main` hash.
- [ ] `pkaction | grep -c '^org\.archmaid\.helper\.'` prints 9, and
      `stat -c '%A %U' /usr/lib/archmaid/archmaid-helper` prints `-rwxr-xr-x root`.
- [ ] Remove the live-run worktree: `git -C ~/archmaid worktree remove ~/archmaid-live`.
      Once you are happy, delete the old `~/archmaid-state-backup-2026100*`
      copies and the empty `~/archmaid-scratch`. Leave `~/archmaid-vmtest`
      (the VM harness).
- [ ] Optional: enable the timer and the widget (`docs/dms-widget.md` →
      "Enable (owner)").
- [ ] Housekeeping, with the lead: remove the merged agent worktrees under
      `~/archmaid/.claude/worktrees/` (about 100) and their branches. Keep
      `refactor/p0` until `main` has run for a while.
