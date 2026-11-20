# Live run of archmaid 1.0.0

> **Status.** Rewritten 2026-11-20 for v1.0.0 (092bf5a, `main` = `refactor/p0`).
> It was first written on 2026-10-04, before the merge to `main`; the first
> live runs have been done since (`docs/merge-to-main.md`), so this is now the
> checklist for the next one: a release candidate, or a change to privilege,
> the helper, the polkit policy or the GUI's dialogs. It follows today's code:
> one polkit dialog per Full Cleanup, one password per GUI run (the kept
> password and its PATH guard), `-fc` / `--full-cleanup`, System health and
> Held updates, the paru-or-yay AUR helper and the 1.0.0 version scheme. Each
> step names the source it was checked against; a step that code alone can't
> prove says so ("not checkable from code") and is the point of running it.

For the owner to run by hand on the Arch/Hyprland machine. No agent runs any
of this (CLAUDE.md rule 2). It is the first real run of:

- **Part A: the TUI** on the engine and the real Driver (sudo, askpass),
  built to a temp dir. Nothing is installed.
- **Part B: the root helper and its polkit policy**, installed from the
  `archmaid-git` package (an upgrade, if it is installed already).
- **Part C: the GUI** (`archmaid-gui`) on the real Driver: root steps through
  pkexec and one polkit request, topgrade's sudo through the GUI's password
  dialog, and one kept password for the run. Built to a temp dir.

Part C needs Part B. Part A needs neither, so you can stop after any part.

Everything is built from one commit in a separate worktree, `~/archmaid-live`
(S0): for the release, the commit tagged `v1.0.0`. Your `~/archmaid` checkout
stays as it is.

## Before you start

### What can change, and what can't

| Changes (real runs only) | Doesn't change |
|---|---|
| A full system upgrade by topgrade (repo, AUR, and whatever else your topgrade config enables) | sudoers, PAM, `/etc/security/faillock.conf`, polkit rules |
| Package cache thinned to 3 versions (`paccache -r`), and `pacman -Sc` in the GUI's root call: packages no longer installed leave the cache. The whole cache goes only if you tick it in Clear caches, and the guide never does | `~/.local/bin` and any `install.sh` build; `install.sh` isn't used |
| Orphaned packages removed, **only** if you say yes at a confirm that lists them | Your Downloads folder, `~/.cache` (the dry runs only print what Clear caches would do), services (those actions aren't run) |
| The AUR helper's build cache cleaned (`paru -Sc` or `yay -Sc`), `/tmp` and `/var/tmp` aged per tmpfiles.d, journal entries older than 2 weeks deleted (gone for good) | No unit is enabled; no setuid file is installed |
| Hyprland plugins updated (`hyprpm update`), if hyprpm is installed | The DMS shell: the widget files are installed by Part B, but nothing shows until you enable the plugin (optional, last) |
| `~/.local/state/archmaid`: new run folders (rotation keeps the 5 newest), `last-run.json` overwritten by a run that did something, the AUR snapshot and `audit-state.json` written; `health.json` by the GUI after each System health run; `health-ignore.json` only if you press Ignore | |
| `~/.cache/archmaid/arch-news.xml`: the Arch news feed's cache (3 h), written by any System health run, which the TUI starts at every start | |
| S0: a new git worktree, `~/archmaid-live` | `~/archmaid`'s working tree and branch |
| Part B: the `archmaid-git` package (files listed in B2) | |
| Part C: `~/.config/archmaid/gui.json` (the GUI's settings: dry run, downloads folder, glass look, blur) | |

The dry runs change nothing but the `dryrun/` folder under the state dir.
They still run the AUR audit (one network request to the AUR) and the orphan
query (`pacman -Qdtq`, read only). **Every TUI start** also runs the System
health checks in the background (read only: the Arch news feed, one AUR
request, your first mirror's `lastsync`, and local tools such as `systemctl`
and `coredumpctl`), so a dry run is not offline. The checks change nothing
except the news cache above.

<!-- source: internal/actions/queues.go (fullCleanupSteps, polkitFullCleanup,
orphanRemoval, aurCacheStep); internal/helperclient/helperclient.go
(PaccacheKeep = 3, JournalSpan = 2weeks); internal/logstore (KeepRuns = 5,
dryrun slot, lastRunPath, WriteLastRun drops a run in which nothing ran);
internal/tui/health.go startupChecks; internal/health (news.go cache path,
ignore.go, reportfile.go); gui/internal/ui/health_real.go (WriteReportFile);
gui/internal/settings (gui.json fields); HANDOVER landmines "Logging policy",
"State files", "AUR audit"; packaging/arch/README.md "What it installs" -->

### Time

| Part | Time |
|---|---|
| Setup (this section) | 10 min |
| A: the TUI | 30–60 min (topgrade is most of it) |
| B: helper and policy | 15–25 min (makepkg builds the GUI too: several minutes of cgo) |
| C: the GUI | 35–70 min (a cold GUI build takes several minutes; topgrade again) |
| Finish, results table | 10 min |
| Rollback (when you want it) | 5 min |

### If something goes wrong, and one open question

- **If archmaid's engine stops on its own** (an internal error; a bug to
  report), the TUI shows "archmaid's engine stopped unexpectedly: …" and any
  key quits; the details follow on the terminal (stderr). The GUI shows the
  same text in a toast and the status bar, closes its dialogs, marks the
  running row "Stopped" and the pending ones "Not run", and its controls come
  back. Either way, write down the text and send the stderr file back.
- **If it ever stops responding anyway** (the TUI ignores keys, or the GUI
  window is gone but the process hasn't exited after 30 s), from another
  terminal:

  ```sh
  pkill archmaid
  pgrep -a 'archmaid|topgrade|paru|yay|pacman|paccache'
  ```

  `pkill archmaid` also matches `archmaid-gui`. An "Operation not permitted"
  for `archmaid-helper` is expected: you can't signal the root helper, and it
  always finishes its verb (HANDOVER: "A Stop after the helper's start line
  can't stop it"). If the TUI's terminal is left garbled, type `reset`. Then
  write down what you saw and send it back.
- **Unanswered: does a pkexec killed while its dialog is up cost a faillock
  failure?** Polkit's source says yes (HANDOVER: "That cancel still costs one
  faillock row"), but no live run has measured it
  (`docs/polkit-keep-session-check.md`, Part C). Closing the GUI while polkit's
  dialog is up is Stop and Quit at once, which kills that pkexec
  (`Window.closeRequest`). This guide never presses Stop or closes the GUI
  while a polkit dialog is open.

<!-- source: internal/tui/adapter.go engineStopped, view.go ("any key to
quit; the details follow on the terminal"), TestEngineStoppedMidRun;
gui/internal/runview/model.go EngineStopped, Model.Stop, Row.Subtitle;
gui/internal/ui/window.go engineStopped, statusText; stopped_test.go,
stopped_harness_test.go -->

<!-- not gaps any more: the GUI makes a fresh Driver, so a fresh log store,
per Start (window.go startAction, realdriver package doc, TestRunDirs), and
prefetches the AUR audit behind the gate (Deps.Prefetch, TestAuditPrefetch) -->

### The faillock budget and the stop rule

pam_faillock locks your account after **3** failures (deny=3) for 10 minutes.
A wrong polkit password costs **1** failure, and so does a **cancelled**
polkit dialog. A correct password resets the count to 0. archmaid's own
password prompts (the TUI's, and the GUI's sudo and polkit dialogs) cost
**nothing** when wrong: it checks the password with `su` first (no faillock
on Arch) and only a correct one reaches sudo or polkit. A wrong password typed
at `sudo` itself in a terminal costs 1–2.

**This guide makes at most 2 deliberate wrong or cancelled attempts:**

1. A4, optional: one wrong password in the TUI. Expected cost: **0** rows.
2. C4b: one cancelled polkit dialog, in Clear caches (it deletes nothing).
   Expected cost: **1** row, reset by the next correct password.

**Stop rule**

- If `faillock` ever shows more rows than this guide expects at that
  point, or you mistype a password anywhere (sudo, polkit, a login): skip
  every remaining deliberate failure. Mark it "skipped: stop rule" in the
  table.
- With **2 or more rows**, the next failure locks the account. Type no
  password and cancel no dialog until 15 minutes have passed since the
  newest row (`fail_interval` = 900 s). Then check `faillock` again. If a
  polkit dialog is open then, leave it open; the GUI row waits on "Waiting
  for authorisation…". Don't press Stop or close the window either (see the
  open question above). If the dialog closes by itself, write that down.
- Never press Stop or close archmaid while a polkit dialog is up.
- If the account does lock: the GUI shows "Not authorised · account locked
  until HH:MM" on each root row without opening a dialog. Wait until then,
  and don't try `sudo` or a tty login meanwhile.

<!-- source: docs/polkit-live-test.md (1 failure per wrong polkit password,
cancel = 1, success resets, su has no faillock); HANDOVER "Auth is two-stage",
"Live-testing safely" and "pkexec: one exit code for every refusal, and a cancel
costs a failure" (the cancel costs a row, no way around it; decided 2026-10-12);
internal/privilege/faillock.go defaults (deny 3, unlock_time 600, fail_interval
900); gui/internal/runview/model.go Row.Subtitle (lock text);
TestGUIRealDriver real-locked -->

### Setup

Open two terminals in your graphical session. Terminal 1 runs archmaid;
terminal 2 runs the checks.

**S0. A worktree for the live run.** Give the live run its own detached
worktree of the commit under test, so `~/archmaid` stays as it is (terminal 1):

```sh
cd ~/archmaid && git fetch origin --tags && git worktree add --detach ~/archmaid-live v1.0.0
```

For a later commit use `origin/main` (or the sha) instead of `v1.0.0`. If the
tag does not exist yet, use `origin/main`: at the release, `main` is the
commit the tag will name. This changes nothing in `~/archmaid`'s working
tree; Rollback removes the worktree again. Every path below is
`~/archmaid-live`, not `~/archmaid`.

**S1. The commit.** In terminal 1:

```sh
cd ~/archmaid-live
git status --short
git log -1 --format='%H %s'
git describe --tags --dirty
```

`git status --short` must print nothing (makepkg builds only committed
state). Write down the hash: every build in this guide is of this commit.
`git describe` prints `v1.0.0` on the release commit, or `v1.0.0-N-g<hash>` N
commits past it: that is what the builds below show as their version.

**S1b. What is installed now** (terminal 2; write the answers down, Rollback
needs them):

```sh
pacman -Q archmaid-git archmaid-gui-git
command -v archmaid archmaid-gui
type -a sudo su pkexec
```

`pacman -Q` says `error: package '…' was not found` for one that isn't
installed. `command -v` tells which `archmaid` a plain `archmaid` runs: this
guide always runs its own builds by full path. `type -a` should list only
`/usr/bin/…`. Anything else (a `sudo` in `~/.local/bin`, say) trips the GUI's
PATH guard in Part C: the kept password is then off for the run, which is the
guard working, and C4 says what you will see instead.

<!-- source: internal/version (Format, describeRE); packaging/arch/PKGBUILD
_version; internal/driver/pathguard.go (PreviewKeepOff, ShadowNames in
internal/privilege/pathguard.go); the installed state is not checkable from
code -->

**S2. A scratch folder for the builds and the notes** (terminal 1; keep this
terminal open, `$T` lives in it):

```sh
T=$(mktemp -d) && echo "$T"
```

Write down the path.

**S3. Back up the state folder.** The rotation keeps the 5 newest run
folders, and this guide makes up to 3 new ones (A4, C4, C5), so the oldest
go:

```sh
B=~/archmaid-state-backup-$(date +%Y%m%d-%H%M) && cp -a ~/.local/state/archmaid "$B" && ls "$B"
ls -la --time-style=full-iso ~/.local/state/archmaid > "$T/state-0.txt"
```

**S4. faillock first** (terminal 2):

```sh
faillock --user "$USER"
```

Count the rows under the `When Type Source Valid` header. **It must be 0.**
If it isn't, wait until they are older than 15 minutes.

**S5. Nothing else is using pacman, and polkit's `pkttyagent` exists:**

```sh
ls /var/lib/pacman/db.lck
ls -l /usr/bin/pkttyagent
```

The first must say "No such file or directory". The second must exist (it is
part of the `polkit` package). The GUI registers `pkttyagent` for its own
process, so its polkit prompt comes up in the GUI's own dialog, not in the
desktop's. Without it, the GUI falls back to your desktop's polkit agent
(`pgrep -af 'polkit.*agent'` shows one if it is running) and says so in its
status line; with neither, the root steps end "Not authorised".

## Part A: the TUI

### A1. Build (terminal 1)

```sh
cd ~/archmaid-live && V=$(git describe --tags --dirty) && go build -trimpath -ldflags "-X github.com/Enzord758/archmaid/internal/version.Version=$V" -o "$T/archmaid" ./cmd/archmaid && go version -m "$T/archmaid" | grep vcs
"$T/archmaid" --version; echo "rc=$?"
```

The `-ldflags` is what the package build sets (`packaging/arch/PKGBUILD`
`_version`), so `--version` reads the same as the installed one will.
Expected: `vcs.revision=` the S1 hash and `vcs.modified=false`; then
`archmaid 1.0.0` on the release commit, or `archmaid 1.0.0 + N commits
(<hash>)` N commits past it, rc 0 (`--version` only prints; it reads and
writes nothing). Run it by its full path, `"$T/archmaid"`: a plain
`archmaid` is whatever S1b showed.

<!-- source: internal/version (Format); cmd/archmaid/main.go (errVersion);
packaging/arch/PKGBUILD -->

### A2. `--help` and a typo

```sh
"$T/archmaid" --help; echo "rc=$?"
"$T/archmaid" --dryrn; echo "rc=$?"
ls -la --time-style=full-iso ~/.local/state/archmaid | diff "$T/state-0.txt" -
```

Expected: the usage text (`Usage: archmaid [-fc] [--dryrun] [--downloads
<folder>] | --health-report | --version`, then the options `-fc,
--full-cleanup`, `--dryrun`, `--downloads <folder>`, `--health-report`,
`-h, --help` and `--version`), `rc=0`; then `archmaid: unknown option
"--dryrn"` and `Try 'archmaid --help'.`, `rc=2`; and **no output from
`diff`**: neither start touched the state folder. (Builds from before
2026-10-04 pruned the run history even on `--help`.) `--health-report` is not
tried here: it writes `health.json` (a read-only check, but a write).

<!-- source: cmd/archmaid/main.go (usage, startup, parseArgs);
TestStartupDeletesNothing; HANDOVER "Nothing deletes run history at startup" -->

### A3. Dry run: Full Cleanup end to end

```sh
"$T/archmaid" --dryrun -fc 2>"$T/tui-dry-stderr.txt"; echo "rc=$?"; cat "$T/tui-dry-stderr.txt"
```

`-fc` (or `--full-cleanup`) is the same as picking the first menu entry: it
starts the engine's Full Cleanup at once, so the gate is the first thing on
screen. (`-fc` is one token; `-f` and `-c` alone are unknown options.)

1. **Start:** no menu first. Declining the gate in step 2 would land on the
   menu, as after picking Full Cleanup by hand; try that once if you like
   (press `n`, look at the menu, then start again).
2. **Gate:** a "⚠ Proceed with full cleanup?" popup with Yes/No. **No
   password prompt**: a dry run never asks for sudo. Press `y`.
3. **Running screen:** a checklist of 9 steps, hyprpm last: AUR audit,
   topgrade, package cache, orphans, AUR cache, temp
   files, logs, package integrity, Hyprland plugins. Below it the output
   pane, and the hint `↑/↓ scroll output · ctrl+c abort`.
   - The AUR audit runs for real (one AUR request): "N foreign package(s)
     checked against the AUR: …", or "No foreign … packages installed". If
     it flags a high finding, a named confirm appears before topgrade.
     Either answer is safe here: a dry run never saves the snapshot.
   - Every command step prints `[dry-run] would run: …` (topgrade as
     `topgrade -y --no-retry --disable hyprpm`) and ticks ✔ at once.
   - Orphans: the real, read-only query. Either "No orphaned packages.", or
     the list plus "pacman -Rns would remove M package(s):" and a confirm
     "Remove N orphaned package(s) …?". `y` is safe here: it only prints
     `[dry-run] would run: sudo pacman -Rns --noconfirm -- …`.
   - The last row is "Updating Hyprland plugins…" (`hyprpm update`), when
     hyprpm is installed.
   - AUR cache: `[dry-run] would run: <paru or yay> -Sc --noconfirm --color
     always`, whichever helper your topgrade config names or is installed
     (paru is looked for first); with neither, a note that there is no AUR
     helper and nothing to clean.
4. **Summary:** "Completed" ("1 step(s) skipped" if you declined the
   orphans) under the checklist and a "Summary" block (packages upgraded,
   freed space and what needs attention; a dry run changed nothing), hint `l view log · enter back to menu · q quit`. Press `l`, open a
   category or two: the entries say `DRYRUN: …`. `esc` back, `enter` to the
   menu, `q` to quit. `rc=0`, stderr empty.

Check (terminal 2):

```sh
ls -la ~/.local/state/archmaid/dryrun/
cat ~/.local/state/archmaid/dryrun/last-run.json; echo
ls -la --time-style=full-iso ~/.local/state/archmaid | diff "$T/state-0.txt" -
```

Expected: the dry-run slot holds the category logs (`audit.log`,
`topgrade.log`, `cleanup.log`, `orphans.log`; this run's entries appended at
the end) and a `last-run.json` with `"action":"fullcleanup"`,
`"outcome":"ok"`, `"dry_run":true`. The `diff` shows only the `dryrun` line
(and the `.`/`total` lines): **no new run folder, no `last-run.json` in the
root.**

<!-- source: internal/tui/testdata/transcripts/fullcleanup_clean.dryrun.golden;
internal/engine/queue.go plan() (a dry run never probes; the gate);
internal/actions/queues.go; internal/logstore lastRunPath; view.go viewRunning,
viewSummary, summary.go; cmd/archmaid/main.go parseArgs (-fc); README.md
"Start at Full Cleanup"; cmd/archmaid/e2e/orphans.tape -->

### A3b. Dry run: Clear caches

```sh
"$T/archmaid" --dryrun 2>"$T/tui-dry-caches-stderr.txt"; echo "rc=$?"; cat "$T/tui-dry-caches-stderr.txt"
```

1. **Menu:** "Clear caches" (second entry, after Full Cleanup); press enter.
   archmaid measures `~/.cache` and runs `paccache --nocolor -dk0` as you,
   a read-only dry run of the package cache (no sudo, no password).
2. **The pick:** two rows, nothing ticked: "~/.cache — <size>" and
   "Entire package cache — <size> (<n> files)" with the line "Can't
   reinstall or downgrade those packages without internet" under it. A row
   with nothing in it is greyed out with the reason. Write down both sizes.
   Press `esc`: back to the menu, nothing ran, no password was asked.
3. Run it again, tick `~/.cache` (space) and press enter: under `--dryrun`
   it only prints `[dry-run] would run: bash -c rm -rf "$HOME/.cache/"*`,
   and no password is asked. Then tick the package cache: it prints
   `[dry-run] would run: sudo paccache -rk0`. (Never tick a row without
   `--dryrun` here: it deletes it.) Two empty caches show a notice instead
   of a pick.

### A3c. System health and Held updates (read only)

```sh
"$T/archmaid" --dryrun 2>"$T/tui-health-stderr.txt"; echo "rc=$?"; cat "$T/tui-health-stderr.txt"
```

1. **Menu:** just above "Edit topgrade config" there are two entries that are
   the TUI's own: "System health" and "Held updates". Once the start-up
   checks have finished (a few seconds, up to 15), a label says what needs
   attention: "System health · 2 things need attention" and "Held updates ·
   3 held". A calm machine shows the plain names.
2. **System health:** enter. A spinner "Checking the system…" (only if the
   report is older than 5 minutes), then the title line with "Nothing needs
   attention" or "N things need attention" and the check time, then
   "Needs your attention" (the look rows) first and the groups System,
   Packages and Hardware after it: eleven checks in all (failed services,
   restart, config files to merge, Arch news, disk space, package lock,
   clock sync, crashes, AUR packages, drive health, mirror). `?` marks a check
   that could not tell, which is not a fault. Keys: `↑/↓` move, `c` copies
   the row's command (OSC 52, and prints it), `o` prints its link (no
   browser is started), `i` ignores a look row, `u` undoes it, `r` re-runs,
   `esc` back. **Press no `i`** unless you mean it: `--dryrun` leaves the
   ignore list alone, but this guide has no use for it.
3. **Held updates:** enter. "Nothing is held back now.", or the AUR updates
   held for the 72 h cooldown and the packages an AUR audit holds, each with
   the command to run yourself after reading its PKGBUILD (`<paru or yay> -S
   <package>`). The TUI never installs anything here. The cooldown list is
   shown only while `updates-cache.json` is under 24 h old, so with the timer
   not enabled it is normally empty: that is correct, not a bug.
4. `q` to quit. `rc=0`, stderr empty.

Check (terminal 2): the state folder is as after A3 plus nothing:

```sh
ls -la --time-style=full-iso ~/.local/state/archmaid | diff "$T/state-0.txt" -
ls -la ~/.cache/archmaid/arch-news.xml
```

Expected: the `diff` shows only the `dryrun` line (as after A3); the news
cache exists (0600) unless archlinux.org was unreachable. Whether the rows
are *right* about your machine (a restart you know is due, a `.pacnew` you
know of) is for you to judge: the code only proves they come from the
documented sources.

<!-- source: internal/tui/health.go (tuiEntries, menu, startupChecks,
menuLabel, healthHint, healthSummary, healthStale = 5 min), held.go (heldBody,
heldKey); internal/health (eleven checks, titles in health.go, news cache);
internal/held (CacheFresh); cmd/archmaid/README.md; docs/design/health.md.
Not checkable from code: that the findings match your machine -->

### A4. One real Full Cleanup

Terminal 1:

```sh
sudo -k
START_A=$(date +%Y-%m-%dT%H:%M)
"$T/archmaid" 2>"$T/tui-stderr.txt"; echo "rc=$?"; cat "$T/tui-stderr.txt"
```

`sudo -k` asks for nothing; it drops this terminal's sudo ticket so the
password step really shows.

What to watch, in order:

1. **The password step.** Full Cleanup → "🔒 sudo access required" with a
   password field and `enter submit · esc cancel`. Uncached, this prompt
   **is** the gate: no separate "Proceed" popup follows.
   - *Optional, deliberate failure 1 of 2:* type a wrong password and
     press enter. Expected: "authenticating…", then "Incorrect password —
     2 attempts left". In terminal 2, `faillock --user "$USER"` must still
     show **0 rows**. If it shows any, apply the stop rule. Wait about 10 s.
   - Type the right password. Expected: the checklist (10 steps) starts.
2. **The AUR audit, then topgrade.** topgrade's output streams in the pane.
   A high audit finding puts a named confirm in front of topgrade: `n` skips
   only the update and the cleanup goes on.
3. **Askpass during topgrade.** Normally **no** popup: the ticket from step 1
   is kept alive every 60 s. (The TUI has no kept password; that is the
   GUI's, Part C. Its one password primes sudo's ticket, and archmaid's sudo
   wrapper makes any sudo that topgrade or the AUR helper start by name ask
   through this popup, never on the tty.) If sudo asks anyway, a "🔒 sudo access
   required" popup appears over the output with three lines: `step`,
   `caller` (the real binary that ran sudo) and sudo's own `prompt`, quoted,
   on one line and clipped to 48 characters (a longer one ends in `…`).
   Answer only if the caller is topgrade, paru or yay; otherwise `esc` (topgrade's
   sudo then fails; sudo counts the hang-up as a failed attempt, so check
   `faillock` afterwards and apply the stop rule) and write down the caller
   line.
4. **Package cache:** `paccache -r` runs (keeps 3 versions). Full Cleanup
   asks nothing more about the cache: deleting the entire cache is the
   row of the menu action "Clear caches" (A3b).
5. **The orphan confirm.** The probe's output lists the orphans and
   "pacman -Rns would remove M package(s):" with the full cascade. The
   confirm says "Remove N orphaned package(s) — M total once dependencies
   cascade?" (or just N when nothing cascades). Read the list first. `y`
   removes exactly those (`sudo pacman -Rns --noconfirm -- …`); `n` skips.
   **If `gtk4`, `libadwaita`, `libgirepository`, `pkgconf` or `go` is in the
   list, answer `n`**: Part B and C build with them. Write down N, M and your
   answer.
6. AUR cache (`paru -Sc` or `yay -Sc`, as your helper), temp files, logs,
   package integrity. If `pacman -Qkq` finds
   missing files, it exits non-zero and that row shows ✖ "… failed". Copy
   its output from the pane (it is also in `cleanup.log`).
7. **hyprpm last.** "Updating Hyprland plugins…" is the final row and runs
   after every step that needs sudo (it drops the sudo ticket on exit).
8. **Summary:** "✔ Cleanup completed successfully", "Completed — 1 step(s)
   skipped" (a declined orphan removal) or "⚠ Completed with N issue(s)".
   Press `enter` for the menu and **stay in this TUI** for A5. That keeps A5
   in the same run folder.

Then in terminal 2, keep a copy before A5 overwrites it:

```sh
cp ~/.local/state/archmaid/last-run.json "$T/last-run-A4.json" && cat "$T/last-run-A4.json"; echo
```

Expected: `"action":"fullcleanup"`, `"dry_run":false`, `"outcome":"ok"`
(`"failed"` if a row failed), `steps_skipped` = the confirms you declined,
`steps_refused` 0.

<!-- source: internal/engine/queue.go plan/probed/authorized (the password
prompt doubles as the gate), auth.go wrongPassword;
internal/tui/testdata/transcripts/fullcleanup_uncached_password_ok.shim.golden,
fullcleanup_orphans_cascade_acceptall.shim.golden; view.go viewPassword,
viewAskpassFrom, viewSummary; HANDOVER landmines "hyprpm runs last", "Orphans
are found after the update", "The orphan confirm previews the -Rns cascade",
"An askpass request is answered only from the running sudo step";
internal/engine/queue.go record() -->

### A5. Abort mid-topgrade, and Ctrl-C

Still in the same TUI:

1. Choose "Run topgrade (system update)". If the sudo ticket is still cached
   it starts at once; otherwise the password prompt shows again (type the
   right password).
2. Wait until topgrade's output is streaming. The system was updated minutes
   ago, so pacman should have nothing to install. **Don't abort while pacman
   is installing** (`(3/12) upgrading …` lines); wait for the next section.
3. Press **Ctrl-C**. Expected: "⚠ Abort the running step?" with "Updating
   system with topgrade…", **No** preselected, and `y abort · n resume`.
   topgrade keeps running underneath.
4. Press `n`. Expected: back to the output; nothing stopped.
5. Press **Ctrl-C** again, then `y`. Expected: straight back to the menu.
   topgrade gets SIGINT, and SIGKILL 5 s later if it is still alive.
6. Terminal 2, about 10 s later:

   ```sh
   pgrep -a 'topgrade|paru|yay'; ls /var/lib/pacman/db.lck
   cat ~/.local/state/archmaid/last-run.json; echo
   ```

   Expected: no processes, "No such file or directory", and
   `"action":"topgrade"`, `"outcome":"aborted"`.
7. Back in the TUI, press `q` (Ctrl-C at the menu quits too). `rc=0`, and
   whatever stderr shows is part of the results.

Ctrl-C everywhere else, for reference: on a password prompt it cancels
(back to the menu, recorded `cancelled`); on an askpass popup it hangs up on
sudo; on the abort popup it resumes.

<!-- source: internal/tui/update.go handleKey (screenRunning ctrl+c →
abort confirm, screenAbortConfirm n/esc/ctrl+c/enter resume, screenMenu
q/ctrl+c quit), adapter.go engineKey/finished (abort → menu);
internal/engine/abort.go; internal/tui/tasks.go sigkillGrace = 5 s;
internal/tui/testdata/transcripts/fullcleanup_abort_topgrade.shim.golden;
cmd/archmaid/e2e/abort.tape -->

### A6. Where to check the results

```sh
ls -la --time-style=full-iso ~/.local/state/archmaid ~/.local/state/archmaid/*/ > "$T/state-A.txt"; cat "$T/state-A.txt"
awk -v t="[$START_A" '$0 >= t' /var/log/pacman.log > "$T/pacman-A.txt"; wc -l "$T/pacman-A.txt"
```

Run the `awk` line in terminal 1, where `$START_A` is set. Expected:

- **One new run folder** `YYYYMMDD-HHMM` (the minute this TUI started),
  `drwx------`, files `-rw-------`. If there were already 5, the oldest is
  gone (that's the rotation; it is in the S3 backup).
- In it: `audit.log` (`OK: AUR audit` twice, A4's and A5's, maybe
  `ACCEPT`); `topgrade.log` (`OK: Topgrade completed` with the whole
  output; A5's aborted topgrade leaves no entry); `cleanup.log` (`OK: Temp files cleaned per
  tmpfiles.d rules`, `OK: Old logs removed`, plus any failed step); `orphans.log` (`OK: Orphaned packages
  removed` with the output, or `SKIP: … (declined)`; nothing if there were
  no orphans). Steps that succeed and aren't in that list (paccache -r, the AUR
  helper's cache clean, the integrity check, hyprpm) log nothing.
- `last-run.json` in the root: A5's record (`topgrade`, `aborted`).
- `pacman-A.txt`: topgrade's upgrade lines, and if you removed orphans, a
  `[PACMAN] Running 'pacman -Rns --noconfirm -- …'` line followed by
  `[ALPM] removed` lines. Their count must equal M from the confirm.

<!-- source: HANDOVER "Logging policy" (only failures, AlwaysLog steps log OK
and SKIP), "Live-testing safely" (pacman.log as the independent record);
internal/logstore (Append format "[ts] KIND: label", 0700/0600, run stamp
20060102-1504); internal/tui/log.go (store fixed per process);
fullcleanup_abort_topgrade golden (no topgrade log on abort) -->

## Part B: the root helper and polkit

### B1. Build the packages (no root)

```sh
P=$(mktemp -d) && cp ~/archmaid-live/packaging/arch/PKGBUILD ~/archmaid-live/packaging/arch/archmaid.install "$P"/ && cd "$P" && \
ARCHMAID_REPO="file://$HOME/archmaid" ARCHMAID_FRAGMENT="commit=$(git -C ~/archmaid-live rev-parse HEAD)" makepkg -s
```

Run it in terminal 1, and write down `$P`. The scratch copy keeps `pkg/`
and `src/` out of the repo. Both build files are needed, since `install=`
names `archmaid.install`. makepkg clones `~/archmaid`, the repository the
worktree belongs to (it holds the S1 commit and the tags), checks out the
S1 commit and builds the TUI, the helper and the GUI. It runs `check()`
(the helper's, the DMS widget's and the GUI view model's tests). `-s`
installs missing makedepends with sudo: check them first and `-s` has
nothing to ask for.

```sh
pacman -Q git go gtk4 libadwaita libgirepository pkgconf
```

Every package must print a version. If one is missing, install it yourself
first, or build without `-s`, so that makepkg stops instead of asking for
root.

Expected: two files, `archmaid-git-r<N>.<hash>-1-x86_64.pkg.tar.zst` and
`archmaid-gui-git-…`, where `<hash>` is the first 7 characters of the S1
hash. (The package version stays `r<N>.<hash>`; the version that
`archmaid --version` shows is the `git describe` one, from the build's
`-ldflags`.)

### B2. Install `archmaid-git` (one sudo password)

```sh
sudo pacman -U "$P"/archmaid-git-r*.pkg.tar.zst
```

**If S1b showed `archmaid-gui-git` installed, install both packages in this
one command** (`"$P"/archmaid-gui-git-r*.pkg.tar.zst` after the first):
`archmaid-gui-git` depends on the exact `archmaid-git` of its own build, so
pacman refuses to replace one alone. Otherwise install the GUI package only
for the DMS widget step at the end.

Type the sudo password carefully. If sudo says "Sorry, try again", don't
retry: press Ctrl-C, run `faillock --user "$USER"` and apply the stop rule.
pacman asks `Proceed with installation? [Y/n]`. Its post-install note about
the DMS widget is for the optional step at the end; ignore it for now.

This installs (`packaging/arch/README.md` → "What it installs"):

- `/usr/lib/archmaid/archmaid-helper` (0755 root:root, **not setuid**)
- `/usr/share/polkit-1/actions/org.archmaid.helper.policy`
- `/usr/bin/archmaid`. If `~/.local/bin` comes first on your PATH and holds
  an old `install.sh` build, a plain `archmaid` is that build (S1b);
  `install.sh --uninstall` removes it (README → First run).
- The DMS widget files in `/etc/xdg/quickshell/dms-plugins/archmaid/`
  (inactive until you enable the plugin).
- The `archmaid-updates-check` user timer (not enabled) and its script.
- `/usr/share/doc/archmaid/helper.md`.

(The packages ship no TUI desktop entry. The GUI package has the only one,
`io.github.enzord758.archmaid.desktop`.)

### B3. Verify (no root)

```sh
pacman -Q archmaid-git
stat -c '%A %U:%G %n' /usr/lib/archmaid/archmaid-helper /usr/lib/archmaid /usr/lib /usr
pkaction | grep -c '^org\.archmaid\.helper\.'
pkaction --verbose --action-id org.archmaid.helper.paccache
pkaction --verbose --action-id org.archmaid.helper.remove-orphans
pkaction --verbose --action-id org.archmaid.helper.full-cleanup
pkaction | grep -i archmaid | grep -v '^org\.archmaid\.helper\.'
```

Expected:

- `archmaid-git r<N>.<hash>-1`, with the S1 hash.
- `-rwxr-xr-x root:root /usr/lib/archmaid/archmaid-helper` (no `s`), and
  `drwxr-xr-x root:root` for the three directories. The helper refuses to
  run as root otherwise (exit 4).
- `12`: check-files, paccache, paccache-wipe, remove-orphans, repair,
  pkg-install, pkg-install-local, pkg-remove, vacuum-journal, clean-tmp,
  disable-unit, full-cleanup.
- `paccache`: `implicit any: no`, `implicit inactive: no`,
  `implicit active: auth_admin_keep`, and the annotations
  `org.freedesktop.policykit.exec.path -> /usr/lib/archmaid/archmaid-helper`
  and `org.freedesktop.policykit.exec.argv1 -> paccache`.
- `remove-orphans` and `full-cleanup`: `implicit active: auth_admin` (ask
  every time; `auth_admin_keep` is only check-files, paccache,
  vacuum-journal and clean-tmp), argv1 `remove-orphans` and `full-cleanup`.
  `full-cleanup` is the one action Part C's Full Cleanup asks for.
- The last line prints **nothing**: no other archmaid action is installed
  (for example a keep-check action left over from
  `polkit-keep-session-check.md`).

If `pkaction` says "No action with action id", wait 10 s and try again.
polkitd picks up new files by itself. Don't restart polkit.

**B4 (optional, read only).** Run one of the helper's verbs that need no root,
`plan-orphans`, as yourself, exactly as the GUI's orphan probe does:

```sh
/usr/lib/archmaid/archmaid-helper plan-orphans; echo "rc=$?"
```

Expected: JSON lines, `{"v":1,"t":"start","verb":"plan-orphans",…}`, a
`plan` line (`targets`, `items`, `total`, `hash`) and a `result` line with
`"ok":true,"code":0`; `rc=0`.

**Uninstall:** see Rollback.

<!-- source: packaging/arch/PKGBUILD (install=archmaid.install, package_*,
pkgver r<count>.<short=7>); packaging/arch/README.md; packaging/polkit/
org.archmaid.helper.policy; cmd/archmaid-helper/README.md (Verbs, keep vs
admin, Hardening step 3, exit 4, JSON lines); TestPolicyMatchesVerbs;
internal/helperclient/plan.go (PlanOrphans runs the helper as the user) -->

## Part C: the GUI

### C1. Build (terminal 1)

```sh
cd ~/archmaid-live/gui && V=$(git describe --tags --dirty) && go build -trimpath -ldflags "-X github.com/Enzord758/archmaid/internal/version.Version=$V" -o "$T/archmaid-gui" ./cmd/archmaid-gui && go version -m "$T/archmaid-gui" | grep vcs
```

Expected: the S1 hash, `vcs.modified=false`. A cold build takes several
minutes (gotk4's cgo). The GUI must be the same commit as the installed
helper: they share the verbs and the orphan plan hash. (Instead of building,
`"$P"/pkg/archmaid-gui-git/usr/bin/archmaid-gui` is the same commit, from
makepkg, and so is an `archmaid-gui-git` you installed in B2.)

### C2. Launch from the terminal

```sh
sudo -k
START_C=$(date +%Y-%m-%dT%H:%M)
"$T/archmaid-gui" 2>"$T/gui-stderr.txt"; echo "rc=$?"; cat "$T/gui-stderr.txt"
```

`sudo -k` drops B2's sudo ticket for this terminal. The GUI's children have
no terminal, so there is no ticket for them anyway: every sudo that topgrade
starts has to ask, through the GUI's dialog. The window "archmaid" opens on
**Overview**: the status line, **Last Run** (A5's record: aborted), the
**Updates** card (with its Run topgrade button, and a "N held ›" link when AUR
updates are held), the **System health** cell and the **Actions**. A sidebar
(a view switcher in a narrow window) has Overview, Packages, Run, Logs and
Settings; Stop appears in the header while a run goes. Settings → About reads
`About: archmaid` and the version from A1 (`1.0.0`, or `1.0.0 + N commits
(<hash>)`).

Check that this is the real GUI, not the demo stub: the title is plain
"archmaid" (not "archmaid (stub)"), there is **no** banner "Demo — the stub,
changes nothing", and the status bar has no `STUB`. Only `--stub` starts the
stub; no environment variable does. If you see any of the three, close the
window and stop here.

<!-- source: gui/cmd/archmaid-gui/main.go; gui/internal/ui/app.go
(parseLaunch: --stub only; StubBanner, StubAppID), shell.go navPages;
window.go title, statusText; settings.go aboutText; internal/version;
gui/internal/overview (cards, updatescard, held); launch_test.go -->

### C2b. System health and Held updates in the GUI (read only)

1. **System health** cell on Overview ("Failed services, restart, Arch news,
   disk"): click it. A sheet "System health" lists the eleven checks, "Needs
   your attention" first, then System, Packages and Hardware; `Unknown` rows
   are checks that could not tell. It re-checks when opened on a result over a
   minute old. Each look row has "Copy command" (the command is in its
   tooltip and the toast), a news item has "Read on archlinux.org", and a look
   row has **Ignore** (a menu when it has several items). **Press no
   Ignore:** it writes `health-ignore.json`. Close the sheet.
2. The run just wrote `~/.local/state/archmaid/health.json` (terminal 2:
   `cat ~/.local/state/archmaid/health.json; echo`): `"v":1`, `"at"`,
   `"attention"` = the number of look rows, at most 16 `checks`.
3. **Updates** card: "Up to date" or "N updates waiting" (from the timer's
   `updates-cache.json`; "Not checked yet" while the timer has never run is
   correct). If it shows "N held ›", click it: the sheet "Held updates" lists
   the AUR updates held for the 72 h cooldown (when each is free) and the
   packages an AUR audit holds. **Press no "Review and update"**: that
   starts an AUR install through the Packages page. Close the sheet.

<!-- source: gui/internal/ui/health.go, health_real.go (WriteReportFile),
health_ignore.go, held.go (openHeld, heldIntro); gui/internal/healthview;
gui/internal/overview/cards.go (IDHealth, Desc), updatescard.go, held.go
(LinkText); internal/health/reportfile.go; docs/design/health.md. Not
checkable from code: that the findings match your machine -->

### C3. Dry run via Settings first

1. **Settings** → Runs → **Dry run** on ("Show what each action would do and
   change nothing"; the card says "Changes apply to the next run").
2. **Overview** → "Full Cleanup (Recommended)". The page switches to
   **Run**. A dialog asks to proceed with full cleanup and lists the plan
   (hyprpm last), with Cancel and **Proceed**. In a dry run its note says
   that nothing asks for a password. Press Proceed.
3. Expected: the status bar shows `DRY RUN`. **No polkit dialog, no password
   dialog, no "Waiting for authorisation…"**. topgrade's row prints
   `[dry-run] would run: topgrade -y --no-retry --disable hyprpm`. The root
   steps print the helper call once, as `[dry-run] would run: pkexec
   --disable-internal-agent /usr/lib/archmaid/archmaid-helper full-cleanup
   --keep 3 --journal 2weeks …` (with `--orphans <hash>` if you said yes to
   the orphan confirm, and `--uninstalled`), then one line per row, `[dry-run]
   part … of the one root-helper call`. Rows go to "Done · …". An orphan
   confirm, if any: either answer is safe. The status bar ends in "Finished:
   completed".
4. **Turn Dry run off again** (Settings). Check (terminal 2):

   ```sh
   cat ~/.config/archmaid/gui.json; echo; stat -c '%a' ~/.config/archmaid/gui.json
   cat ~/.local/state/archmaid/dryrun/last-run.json; echo
   ```

   Expected: no `"dry_run":true` in `gui.json` (`{}`, or the downloads folder
   or the Appearance settings only), mode `600`; the dry-run record's
   `finished` is now; the root `last-run.json` is unchanged (still A5's).

<!-- source: gui/internal/ui/settings.go ("Dry run", "Changes apply to the
next run."), gui/internal/settings (gui.json 0600; dry_run, glass, blur,
downloads; omitempty); internal/helperclient/dryrun.go (DryRun's lines);
TestGUIRealDriver real-dry; gui/internal/runview/gate.go (DryNoPassword);
internal/engine/queue.go authorized (the gate with Plan and YesLabel
"Proceed"); gui/internal/ui/dialogs.go; window.go statusText -->

### C3b. One deliberate cancel: Clear caches (deletes nothing)

Deliberate failure 2 of 2. It is a Clear caches run with only the package
cache ticked and its polkit dialog cancelled, so nothing is deleted. Check
first (terminal 2): `faillock --user "$USER"` shows **0 rows**, and Dry run is
off.

1. **Overview** → "Clear caches". It measures `~/.cache` and the package
   cache (read only, as you), then a pick opens with two rows, nothing
   ticked. Tick **only** "Entire package cache", and go on ("Clear").
2. archmaid's own dialog opens, **"Authorise archmaid's Root Steps"**, with
   "Authentication is required to remove all cached packages (polkit action
   org.archmaid.helper.paccache-wipe)." and a password field. The row says
   "Waiting for authorisation…". (If the desktop's own polkit dialog appears
   instead, the GUI had no `pkttyagent` and said so in its status line;
   cancel it, it costs the same row, and write it down.)
3. **Press Cancel.** Expected: the step ends **Skipped**, the run ends
   *cancelled* (the status bar and toast say so, as for a declined gate),
   nothing is deleted. Overview → Last Run is still A5's record: a run in
   which nothing ran leaves `last-run.json` alone.
4. Terminal 2: `faillock --user "$USER"` → **1 row**. That is the documented
   cost of cancelling a polkit dialog; nothing avoids it. C4's correct
   password resets it.

A Cancel in archmaid's own polkit dialog is not "Not authorised": the user
said no, so the rest of the run is skipped as behind a declined step.
("Not authorised" is a refusal archmaid cannot tell from a wrong password: the
desktop agent's cancel, no agent, a locked account.)

<!-- source: gui/internal/ui/dialogs.go (PolkitTitle, passwordText);
gui/internal/ui/polkit_messages.go; internal/engine/askpass.go cancelAskpass,
cancelledByUser, settleCancelled; internal/logstore WriteLastRun (NothingRan);
internal/actions/clearcaches.go (pkgCacheWipeStep: paccache-wipe, auth_admin);
packaging/polkit policy; HANDOVER "pkexec: one exit code for every refusal, and
a cancel costs a failure"; TestSessionPolkitCancelNothingRan,
TestServicesPolkitCancel (gui/internal/realdriver/cancel_test.go). The faillock
row is polkit's: not checkable from code -->

### C4. A real Full Cleanup: one password

In the same window: **Overview** → "Full Cleanup (Recommended)". Read the
Proceed sheet before you press Proceed. Its notes say how the root steps are
authorised: "N steps run as root, together, through one authorisation;
topgrade gets root through its own sudo. You type your password once, the
first time a step needs it; archmaid keeps it for this run only." If instead
it says "archmaid is not keeping your password for this run (…), so every
password prompt asks you", the PATH guard found something that could pose as
sudo (a file named `sudo`, `su`, `pkexec`, `doas`… ahead of the system's own
on the GUI's PATH, a relative PATH entry, or a sudo or pacman named in the
topgrade, paru or yay config); the reason is named. That is the guard working,
and the run then asks at every prompt (2 passwords instead of 1: write it
down). Press **Proceed**.

1. **AUR audit**, then **topgrade**. The audit's AUR lookup already ran
   while the Proceed sheet was up, so its row should finish at once. topgrade
   runs as you. When its sudo asks, the dialog "sudo Asks for Your Password"
   appears: "Asked by <binary>." plus `sudo says: "…"` (sudo's prompt, on one
   line, clipped to 48 characters), the note "archmaid keeps this password in
   memory for this run only. It answers topgrade's and the AUR helper's (paru
   or yay) sudo and archmaid's own polkit prompts. Any other program that asks
   shows this dialog with its name.", and Cancel / Authenticate. Check that the
   binary is `/usr/bin/topgrade`, then type the password. **This is the one
   password of the run.** A wrong one costs no faillock row (su checks it
   first); none is planned. Expected: no second sudo dialog from topgrade or
   the AUR helper it starts. A dialog naming some other caller (a sudo inside
   an AUR package's build, say) is not covered by the kept password: don't
   answer it unless you know what asked; cancel it and write the caller down.
2. **Orphans:** no dialog for the probe (`plan-orphans` runs as you). If it
   finds orphans, a confirm lists them with the cascade `pacman -Rns` would
   take. **Yes** or Skip. Same rule as A4 about `gtk4`, `libadwaita`,
   `libgirepository`, `pkgconf` and `go`. Yes puts the removal in the root
   call below (`--orphans <hash>`).
3. **The root call.** The rows package cache, uninstalled packages' cache
   (`pacman -Sc`), orphan removal (if you said yes), temp files, logs and
   package integrity run as **one** `pkexec … archmaid-helper full-cleanup`
   call, which polkit authorises once as `org.archmaid.helper.full-cleanup`.
   Expected: **no dialog**. The kept password answers polkit's prompt
   through archmaid's own agent; the rows say "Waiting for authorisation…"
   for a moment, then each starts and ends in turn. If a dialog "Authorise
   archmaid's Root Steps" does open ("Authentication is required to clean the
   package cache, remove orphaned packages, vacuum the journal, clean
   temporary files and check package files (polkit action
   org.archmaid.helper.full-cleanup)"), then either topgrade asked nothing,
   so this is the run's first prompt, or the password was not kept: type it,
   once, and note which. It is still one dialog for the whole call, and
   archmaid does not ask again for the rest of the run. (Once the call is
   authorised, Stop can't halt the parts still to come: the helper runs as
   root.)
4. **AUR cache** (`paru -Sc --aur` or `yay -Sc --aur`, as the user): no
   dialog and no sudo. **Watch terminal 1:** nothing should print there.
   If a `[sudo] password for …` prompt appears, don't type it: press
   **Stop** in the header (no polkit dialog is open then) and write it down.
5. **Hyprland plugins last**, usually no dialog.
6. Expected: the status bar shows "Finished: completed" and every row is
   "Done" (or "Skipped" for a declined orphan removal). **Overview → Last
   Run:** completed, nothing refused. **Logs:** the run folder's
   `cleanup.log` has the failures and the AlwaysLog steps (`OK: Temp files
   cleaned per tmpfiles.d rules`, `OK: Old logs removed`), `topgrade.log` the
   whole topgrade output.

Terminal 2:

```sh
cp ~/.local/state/archmaid/last-run.json "$T/last-run-C4.json" && cat "$T/last-run-C4.json"; echo
faillock --user "$USER"
```

Expected: `"action":"fullcleanup"`, `"outcome":"ok"` (`"failed"` if a row
failed), `"steps_refused":0`, `steps_skipped` = the confirms you declined,
`"dry_run":false`; faillock **0 rows** (C3b's row is reset by the correct
password).

<!-- source: gui/internal/ui/dialogs.go (KeptNote, KeptOffNote, passwordText,
askpassPromptMax = 48); gui/internal/runview/gate.go GateNotes;
internal/driver/pathguard.go (PreviewKeepOff, keepOffReason),
internal/privilege/pathguard.go (ShadowNames), configguard.go;
internal/driver/keep.go; gui/internal/realdriver (KeepPassword: true; topgrade's
sudo through the askpass Server; root steps through helperclient.Run/pkexec);
gui/internal/polkitagent (pkttyagent registered for the GUI's process; only
org.archmaid.helper.* actions, only the current user); internal/actions/queues.go
(polkitFullCleanup, findOrphansPolkit, aurCacheStep: --aur has no sudo);
internal/engine/combine.go; TestFullCleanupPolkitOneDialog
(internal/driver/combined_test.go); HANDOVER "The GUI keeps the run's
password", "One polkit dialog per Full Cleanup run", "Under polkit only
topgrade asks for a password". Not checkable from code: what your topgrade and
polkit setup actually does, and whether the kept password answers polkit's
prompt on this desktop (the owner's and the VM's earlier runs say so; the code
and its tests use shims) -->

### C5. Close mid-run: "Stop and Quit"

Same window: **Overview** → Updates card → "Run topgrade". Its confirm asks first, and may show a
callout with unread Arch news ("Read before updating"). This is a new run, so
a new password: type it at the sudo dialog if it asks (one password for this
run, with the kept note again). When topgrade's output is streaming, no dialog
is open and pacman isn't installing:

1. Close the window (your close keybind or the title bar). Expected: "Stop
   the run and quit?" / "The running step is stopped; what it already
   changed stays changed.", with **Keep Running** (the default) and **Stop and
   Quit** (red).
2. **Keep Running.** Expected: the dialog goes away and the output keeps
   coming.
3. Close again → **Stop and Quit.** Expected: the window disappears at once
   and the process exits within about 5 s. Terminal 1 prints `rc=` and
   the stderr file.
4. Terminal 2:

   ```sh
   pgrep -a 'archmaid|topgrade|paru|yay'; ls /var/lib/pacman/db.lck
   cat ~/.local/state/archmaid/last-run.json; echo
   ```

   Expected: nothing running, no `db.lck`, and `"action":"topgrade"`,
   `"outcome":"aborted"`. If the process is still there after 30 s, use the
   recovery under "If something goes wrong" (`pkill archmaid`) and note it.

<!-- source: gui/internal/ui/window.go closeRequest (texts, Keep Running
default, closes off the main thread; a close while polkit's dialog is up is Stop
and Quit at once: not used here); internal/driver/driver.go teardown(asked)
(Abort before stopping children); internal/driver/keep.go (the kept password is
wiped when the run ends however it ends); TestGUIRealDriver real-close;
HANDOVER "GUI: widgets only on the GTK main thread" -->

### C6. Check

```sh
ls -la --time-style=full-iso ~/.local/state/archmaid ~/.local/state/archmaid/*/ > "$T/state-C.txt"; cat "$T/state-C.txt"
awk -v t="[$START_C" '$0 >= t' /var/log/pacman.log > "$T/pacman-C.txt"; wc -l "$T/pacman-C.txt"
```

Expected: **two** new run folders, one per run, each named for the minute
its run started: C4's (its entries, as in C4.6) and C5's. The GUI makes a
fresh engine, and so a fresh log store, for every action it starts. C5's
aborted topgrade logs nothing, but its audit row does (`OK: AUR audit` in
`audit.log`), so its folder exists. If C4 and C5 started in the same minute,
they share one folder. C3's dry run went to `dryrun/`, not here, and C3b's
cancelled run wrote at most stub entries, which take no slot.
`pacman-C.txt` holds topgrade's lines and the orphan removal, if you
accepted it. The removal runs as root through the helper, so pacman.log has
`Running 'pacman -Rns --noconfirm -- …'` for it too; the count of
`[ALPM] removed` lines must equal the confirm's M. Also new in the state
folder: `health.json` (C2b).

### C7. The DMS widget (optional, only after A–C passed)

1. `sudo pacman -U "$P"/archmaid-gui-git-r*.pkg.tar.zst` (one sudo
   password), unless B2 installed it already. The widget opens `archmaid-gui`
   when it is on PATH. Without it, the widget runs `archmaid --full-cleanup`
   in `$TERMINAL` for "Run Full Cleanup", and the `archmaid` on DMS's PATH may
   be an old build.
2. Enable it as in `docs/dms-widget.md` → "Enable (owner)" (the timer, the
   plugin, the bar). The timer's first run also writes `updates-cache.json`
   and, through `archmaid --health-report`, `health.json`.
3. Expected: the pill is tinted as a warning: the last run (C5) is
   `aborted`. The widget warns on `aborted`, `cancelled` and `incomplete`
   alike. The popout names it, and has a **Health** row from `health.json`
   ("All good", or "N things need attention" with the titles). **Run Full
   Cleanup** opens the GUI.

<!-- source: docs/dms-widget.md (state files, Health row);
packaging/systemd/archmaid-updates-check (health_report);
cmd/archmaid/main.go runHealthReport; packaging/dms/archmaid/state.js
(--full-cleanup fallback); CHANGELOG 1.0.0 (#297). The widget's look on your
bar is not checkable from code -->

## Finish

Terminal 2:

```sh
faillock --user "$USER"
```

Expected: **0 rows**. Every correct password reset the count (C3b's one row
went with C4's password). Write the number down.

## Results (fill in)

Date: ______  Commit (S1): ______  `pacman -Q archmaid-git`: ______
Installed before (S1b): ______  Version shown (A1, `--version`): ______
faillock rows: S4 ___ / after A4 ___ / after C3b's cancel ___ / after C4 ___ / Finish ___

| Part/Step | Expected | Seen | OK? |
|---|---|---|---|
| A1 build | `vcs.revision` = S1 hash, not modified; `--version` = `archmaid 1.0.0` (or `1.0.0 + N commits (<hash>)`) | | |
| A2 `--help` / `--dryrn` | usage with `-fc`, `--health-report`, `--version`, rc 0 / unknown option, rc 2; `diff` empty | | |
| A3 dry run (`--dryrun -fc`) | gate first, no menu, no password; 9 rows (10 with the orphan removal), hyprpm last; `[dry-run] would run`; summary "1 step(s) skipped" if you declined | | |
| A3 state | only `dryrun/` changed; its `last-run.json` has `dry_run: true` | | |
| A3c TUI System health / Held updates | two menu entries; eleven checks, look rows first; held list or "Nothing is held back now."; `diff` as after A3; no key pressed that changes anything | | |
| A4 password | "🔒 sudo access required" is the gate | | |
| A4 wrong password (optional) | "Incorrect password — 2 attempts left"; faillock 0 rows | | |
| A4 askpass | no popup (or: caller = topgrade/paru/yay) | | |
| A4 orphan confirm | N = ___, M = ___, answer ___ | | |
| A4 hyprpm | last row, after every sudo step | | |
| A4 summary + last-run | outcome `ok` (or `failed` + which row) | | |
| A5 abort popup | No preselected; `n` resumes, nothing stopped | | |
| A5 abort | menu at once; no topgrade/paru/yay left; no `db.lck`; `topgrade`/`aborted` | | |
| A6 run folder | one new folder, 0700/0600; entries as listed | | |
| A6 pacman.log | removed count = M (if accepted) | | |
| B1 makepkg | 2 packages, hash = S1; makedepends all present | | |
| B3 helper file | `-rwxr-xr-x root:root`, no setuid | | |
| B3 pkaction | 12 actions; paccache keep; remove-orphans and full-cleanup admin; argv1 set; nothing else | | |
| B4 plan-orphans (optional) | start, plan, result ok; rc 0 | | |
| C1 build | `vcs.revision` = S1 hash; Settings → About shows the A1 version | | |
| C2 real GUI | title "archmaid", no demo banner, no `STUB` | | |
| C2b GUI System health / Held updates | sheet with eleven checks; `health.json` written; held sheet or no link; nothing ignored or installed | | |
| C3 dry run | `DRY RUN`, no polkit or password dialog; one `pkexec … full-cleanup` line; Finished: completed | | |
| C3 settings | dry run off; `gui.json` 600 | | |
| C3b polkit dialog | "Authorise archmaid's Root Steps" (archmaid's own, not the desktop's), paccache-wipe message; "Waiting for authorisation…" | | |
| C3b cancel | step Skipped, run cancelled, nothing deleted, `last-run.json` unchanged; faillock 1 row | | |
| C4 Proceed sheet | note "You type your password once…keeps it for this run only" (or: "not keeping", reason ___) | | |
| C4 askpass | "sudo Asks for Your Password", "Asked by" = ___ ; kept note shown; passwords typed in the whole run ___ (expected 1) | | |
| C4 root call | no dialog (or: one "Authorise archmaid's Root Steps", why ___); rows run one after another | | |
| C4 AUR cache | no dialog, nothing printed in terminal 1 (or: seen, stopped) | | |
| C4 end | "Finished: completed"; `ok`, `steps_refused` 0; faillock 0 rows | | |
| C5 close | "Stop the run and quit?"; Keep Running continues | | |
| C5 Stop and Quit | window gone, exits ≤ 5 s; nothing left; `topgrade`/`aborted` | | |
| C6 run folders | two new folders, C4's and C5's (one if both started in the same minute) | | |
| C7 widget (optional) | warning tint, popout with a Health row, opens the GUI | | |
| Finish faillock | 0 rows | | |

## What to send back

- The filled-in table, and anything that surprised you, in your words.
- `cat "$T"/state-0.txt "$T"/state-A.txt "$T"/state-C.txt` (the run-dir
  listings).
- `last-run.json` as it was after each part: `"$T"/last-run-A4.json`,
  `"$T"/last-run-C4.json`, the final `~/.local/state/archmaid/last-run.json`
  and `dryrun/last-run.json`.
- Everything archmaid printed on stderr after exit: `"$T"/tui-dry-stderr.txt`,
  `"$T"/tui-stderr.txt`, `"$T"/gui-stderr.txt` (empty is a result too).
- faillock before and after (S4, after A4, after C3b's cancel, after C4, Finish).
- The run folders' logs if a step failed or a count looks wrong, and
  `"$T"/pacman-A.txt` / `"$T"/pacman-C.txt` if the orphan count didn't
  match.
- `pacman -Q archmaid-git archmaid-gui-git` (before, S1b, and after), the S1 hash
  and `git describe --tags --dirty`.
- `~/.local/state/archmaid/health.json` after C2b.

## Rollback

Do it whenever you want the machine back as it was. Steps 1–2 need one sudo
password each, so type them carefully. They depend on what S1b found installed.

1. **If you did C7:** remove the widget from the bar, then
   `systemctl --user disable --now archmaid-updates-check.timer`.
2. **The packages.** If S1b showed **none** installed:

   ```sh
   sudo pacman -R archmaid-gui-git archmaid-git
   pkaction --action-id org.archmaid.helper.paccache; echo "rc=$?"
   ls /usr/lib/archmaid
   ```

   (Leave out `archmaid-gui-git` if it was not installed.) `pkaction` must
   fail ("No action with action id …", non-zero rc), and `/usr/lib/archmaid`
   must be gone. This also removes everything else B2 installed.

   If S1b showed `archmaid-git r<N>.<hash>` (and `archmaid-gui-git`)
   installed, there is no package to remove: to go back, build that earlier
   commit the way B1 does (`git -C ~/archmaid-live checkout --detach <hash>`,
   or a new worktree) and install **both** packages in one `sudo pacman -U`
   command. A package built this way is not kept in pacman's cache, so there
   is no older copy to downgrade to.
3. **The GUI's settings:** `rm ~/.config/archmaid/gui.json` and, if the folder
   is empty and did not exist before this guide, `rmdir ~/.config/archmaid`.
   The GUI also writes `~/.local/state/archmaid/health.json`, and
   `health-ignore.json` if you pressed Ignore: they are harmless, and the
   widget and the timer rewrite the first.
4. **The scratch folders**, once you've sent the results: `rm -rf "$T" "$P"`.
   Then the S0 worktree (`~/archmaid` itself stays as it is):

   ```sh
   cd ~ && git -C ~/archmaid worktree remove ~/archmaid-live
   ```

   If git refuses because the worktree has changes, look at
   `git -C ~/archmaid-live status` first; the builds above write nothing
   there.
5. **Other archmaid builds:** nothing to restore. This guide never touched
   `~/.local/bin/archmaid` or any other install, and S1b told you which
   `archmaid` a plain command runs. A build from before 2026-10-04 prunes the
   run history to 5 folders at every start, `--help` included (HANDOVER:
   "Nothing deletes run history at startup"): copy `~/.local/state/archmaid`
   again before starting one. To go back to the pre-test state folder as well:

   ```sh
   mv ~/.local/state/archmaid ~/.local/state/archmaid.live-run && cp -a "$B" ~/.local/state/archmaid
   ```

   (`$B` is from S3. In a new terminal, use the `~/archmaid-state-backup-…`
   path you saw then.)

<!-- source: packaging/arch/README.md (What it installs); docs/dms-widget.md
(Disable); HANDOVER "Nothing deletes run history at startup"; PKGBUILD
(archmaid-gui-git depends on the exact archmaid-git). What was installed before
this run is not checkable from code (S1b records it) -->
