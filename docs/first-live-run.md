# First live run of the new archmaid

> **Status (2026-10-05).** Written for `refactor/p0` as of 2026-10-04,
> before it was merged. Two parts are out of date for any later commit:
> **C4** describes one polkit dialog per root step, but since 0f83f65 the
> GUI's Full Cleanup runs all its root steps as **one** `full-cleanup`
> call with one dialog (asked after topgrade, the orphan list and the
> cache wipe; a cancel marks every root row "Not authorised", and
> `steps_refused` counts them all), and since a96182a the AUR-cache step
> is `yay -Sc --aur` (no sudo) with `pacman -Sc` inside the helper call.
> And after the merge, **S0/S1** build from `~/archmaid` on `main`, not
> from an `~/archmaid-live` worktree. Refresh C4 before the next GUI live
> run (`feat/gui-one-password` changes its password dialogs again).

For the owner to run by hand on the Arch/Hyprland machine. No agent runs any
of this (CLAUDE.md rule 2). It is the first real run of:

- **Part A: the TUI** on the engine and the real Driver (sudo, askpass),
  built to a temp dir. Nothing is installed.
- **Part B: the root helper and its polkit policy**, installed from the
  `archmaid-git` package.
- **Part C: the GUI** (`archmaid-gui`) on the real Driver: root steps through
  pkexec and the polkit dialog, topgrade's sudo through its password dialog.
  Built to a temp dir.

Part C needs Part B. Part A needs neither, so you can stop after any part.

Everything is built from `refactor/p0` in a separate worktree,
`~/archmaid-live` (S0). Your `~/archmaid` checkout stays on `main`, as it is.

## Before you start

### What can change, and what can't

| Changes (real runs only) | Doesn't change |
|---|---|
| A full system upgrade by topgrade (repo, AUR, and whatever else your topgrade config enables) | sudoers, PAM, `/etc/security/faillock.conf`, polkit rules |
| Package cache thinned to 3 versions (`paccache -r`); the whole cache only if you say yes | `~/.local/bin/archmaid` (the old build) and its `.desktop` entry; `install.sh` isn't used |
| Orphaned packages removed, **only** if you say yes at a confirm that lists them | Your Downloads folder, user cache, services (those actions aren't run) |
| AUR build cache cleaned (`yay -Sc`), `/tmp` and `/var/tmp` aged per tmpfiles.d, journal entries older than 2 weeks deleted (gone for good) | No unit is enabled; no setuid file is installed |
| Hyprland plugins updated (`hyprpm update`) | The DMS shell: the widget files are installed by Part B, but nothing shows until you enable the plugin (optional, last) |
| `~/.local/state/archmaid`: new run folders (rotation keeps the 5 newest), `last-run.json` overwritten, the AUR snapshot and `audit-state.json` written | |
| S0: a new git worktree, `~/archmaid-live` | `~/archmaid`'s working tree and its branch (`main`) |
| Part B: the `archmaid-git` package (files listed in B2) | |
| Part C: `~/.config/archmaid/gui.json` (the GUI's settings) | |

The dry runs change nothing but the `dryrun/` folder under the state dir.
They still run the AUR audit (one network request to the AUR) and the orphan
query (`pacman -Qdtq`, read only).

<!-- source: internal/actions/queues.go (fullCleanupSteps, orphanRemoval);
internal/helperclient/helperclient.go (PaccacheKeep = 3, JournalSpan = 2weeks);
internal/logstore (KeepRuns = 5, dryrun slot, lastRunPath);
HANDOVER landmines "Logging policy", "State files", "AUR audit";
packaging/arch/README.md "What it installs"; gui/internal/settings (gui.json) -->

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
  pgrep -a 'archmaid|topgrade|yay|pacman|paccache'
  ```

  `pkill archmaid` also matches `archmaid-gui`. An "Operation not permitted"
  for `archmaid-helper` is expected: you can't signal the root helper, and it
  always finishes its verb (HANDOVER: "A Stop after the helper's start line
  can't stop it"). If the TUI's terminal is left garbled, type `reset`. Then
  write down what you saw and send it back.
- **Unanswered: does a pkexec killed while its dialog is up cost a faillock
  failure?** (`docs/polkit-keep-session-check.md`, Part C.) This guide never
  presses Stop or closes the GUI while a polkit dialog is open.

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
polkit dialog. A correct password resets the count to 0. The TUI's password
prompts cost **nothing** when wrong: archmaid checks the password with `su`
first (no faillock on Arch) and only a correct one reaches sudo. A wrong
password typed at `sudo` itself in a terminal costs 1–2.

**This guide makes at most 2 deliberate wrong or cancelled attempts:**

1. A4, optional: one wrong password in the TUI. Expected cost: **0** rows.
2. C4: one cancelled polkit dialog. Expected cost: **1** row, reset by the
   next correct password.

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
cancel = 1, success resets, su has no faillock); HANDOVER "Auth is two-stage"
and "Live-testing safely" ("expect the two-stage auth to burn nothing");
internal/privilege/faillock.go defaults (deny 3, unlock_time 600, fail_interval
900); gui/internal/runview/model.go Row.Subtitle (lock text);
TestGUIRealDriver real-locked -->

### Setup

Open two terminals in your graphical session. Terminal 1 runs archmaid;
terminal 2 runs the checks.

**S0. A worktree for the live run.** Your `~/archmaid` checkout is on
`main`, the old code, and `refactor/p0` is checked out in another worktree,
so it can't be switched to there. Give the live run its own detached
worktree (terminal 1):

```sh
cd ~/archmaid && git fetch origin && git worktree add --detach ~/archmaid-live origin/refactor/p0
```

This changes nothing in `~/archmaid`'s working tree; Rollback removes the
worktree again. Every path below is `~/archmaid-live`, not `~/archmaid`.

**S1. The commit.** In terminal 1:

```sh
cd ~/archmaid-live
git status --short
git log -1 --format='%H %s'
```

`git status --short` must print nothing (makepkg builds only committed
state). Write down the hash: every build in this guide is of this commit,
and it is `origin/refactor/p0` as S0 fetched it.

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

**S5. Nothing else is using pacman, and a polkit agent runs:**

```sh
ls /var/lib/pacman/db.lck
pgrep -af 'polkit.*agent'
```

The first must say "No such file or directory". The second must show a
graphical agent (the keep-session check saw polkit-gnome's dialog in this
session). Without one, Part C's dialogs can't appear.

## Part A: the TUI

### A1. Build (terminal 1)

```sh
cd ~/archmaid-live && go build -trimpath -o "$T/archmaid" ./cmd/archmaid && go version -m "$T/archmaid" | grep vcs
```

Expected: `vcs.revision=` the S1 hash and `vcs.modified=false`. Run it by its
full path, `"$T/archmaid"`. A plain `archmaid` is still the old build in
`~/.local/bin`.

### A2. `--help` and a typo

```sh
"$T/archmaid" --help; echo "rc=$?"
"$T/archmaid" --dryrn; echo "rc=$?"
ls -la --time-style=full-iso ~/.local/state/archmaid | diff "$T/state-0.txt" -
```

Expected: the usage text (`Usage: archmaid [--dryrun] [--downloads
<folder>]` and its three options), `rc=0`; then `archmaid: unknown option
"--dryrn"` and `Try 'archmaid --help'.`, `rc=2`; and **no output from
`diff`**: neither start touched the state folder. (The old build pruned the
run history even on `--help`.)

<!-- source: cmd/archmaid/main.go (usage, startup, parseArgs);
TestStartupDeletesNothing; HANDOVER "Nothing deletes run history at startup" -->

### A3. Dry run: Full Cleanup end to end

```sh
"$T/archmaid" --dryrun 2>"$T/tui-dry-stderr.txt"; echo "rc=$?"; cat "$T/tui-dry-stderr.txt"
```

1. **Menu:** "Full Cleanup (Recommended)" is the first entry; press enter.
2. **Gate:** a "⚠ Proceed with full cleanup?" popup with Yes/No. **No
   password prompt**: a dry run never asks for sudo. Press `y`.
3. **Running screen:** a checklist of 10 steps, hyprpm last: AUR audit,
   topgrade, package cache, entire package cache, orphans, AUR cache, temp
   files, logs, package integrity, Hyprland plugins. Below it the output
   pane, and the hint `↑/↓ scroll output · ctrl+c abort`.
   - The AUR audit runs for real (one AUR request): "N foreign package(s)
     checked against the AUR: …", or "No foreign … packages installed". If
     it flags a high finding, a named confirm appears before topgrade.
     Either answer is safe here: a dry run never saves the snapshot.
   - Every command step prints `[dry-run] would run: …` (topgrade as
     `topgrade -y --no-retry --disable hyprpm`) and ticks ✔ at once.
   - "Delete ENTIRE package cache? (can't reinstall without internet)":
     press `n`.
   - Orphans: the real, read-only query. Either "No orphaned packages.", or
     the list plus "pacman -Rns would remove M package(s):" and a confirm
     "Remove N orphaned package(s) …?". `y` is safe here: it only prints
     `[dry-run] would run: sudo pacman -Rns --noconfirm -- …`.
   - The last row is "Updating Hyprland plugins…" (`hyprpm update`).
4. **Summary:** "Completed — 1 step(s) skipped" (2 if you also declined the
   orphans), hint `l view log · enter back to menu`. Press `l`, open a
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
viewSummary; cmd/archmaid/e2e/orphans.tape -->

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
   is kept alive every 60 s. If sudo asks anyway, a "🔒 sudo access
   required" popup appears over the output with three lines: `step`,
   `caller` (the real binary that ran sudo) and sudo's own `prompt`, quoted,
   on one line and clipped to 48 characters (a longer one ends in `…`).
   Answer only if the caller is topgrade or yay; otherwise `esc` (topgrade's
   sudo then fails; sudo counts the hang-up as a failed attempt, so check
   `faillock` afterwards and apply the stop rule) and write down the caller
   line.
4. **Package cache:** `paccache -r` runs. "Delete ENTIRE package cache?":
   `n` is the recommended answer. `y` deletes every cached package file.
5. **The orphan confirm.** The probe's output lists the orphans and
   "pacman -Rns would remove M package(s):" with the full cascade. The
   confirm says "Remove N orphaned package(s) — M total once dependencies
   cascade?" (or just N when nothing cascades). Read the list first. `y`
   removes exactly those (`sudo pacman -Rns --noconfirm -- …`); `n` skips.
   **If `gtk4`, `libadwaita`, `libgirepository`, `pkgconf` or `go` is in the
   list, answer `n`**: Part B and C build with them. Write down N, M and your
   answer.
6. AUR cache, temp files, logs, package integrity. If `pacman -Qkq` finds
   missing files, it exits non-zero and that row shows ✖ "… failed". Copy
   its output from the pane (it is also in `cleanup.log`).
7. **hyprpm last.** "Updating Hyprland plugins…" is the final row and runs
   after every step that needs sudo (it drops the sudo ticket on exit).
8. **Summary:** "✔ Cleanup completed successfully", "Completed — 1 step(s)
   skipped" (the declined cache wipe) or "⚠ Completed with N issue(s)".
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
   pgrep -a topgrade; pgrep -a yay; ls /var/lib/pacman/db.lck
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
  output; A5's aborted topgrade leaves no entry); `cleanup.log` (`SKIP:
  Entire package cache removed (declined)`, `OK: Temp files cleaned per
  tmpfiles.d rules`, `OK: Old logs removed`, plus any failed step); `orphans.log` (`OK: Orphaned packages
  removed` with the output, or `SKIP: … (declined)`; nothing if there were
  no orphans). Steps that succeed and aren't in that list (paccache -r, yay,
  the integrity check, hyprpm) log nothing.
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
worktree belongs to (it carries the `refactor/p0` branch), and checks out the
S1 commit and builds the TUI,
the helper and the GUI. It runs `check()` (the helper's, the DMS widget's
and the GUI view model's tests). All makedepends were installed on
2026-10-04, so `-s` shouldn't need sudo.

Expected: two files, `archmaid-git-r<N>.<hash>-1-x86_64.pkg.tar.zst` and
`archmaid-gui-git-…`, where `<hash>` is the first 7 characters of the S1
hash.

### B2. Install `archmaid-git` (one sudo password)

```sh
sudo pacman -U "$P"/archmaid-git-r*.pkg.tar.zst
```

Type the sudo password carefully. If sudo says "Sorry, try again", don't
retry: press Ctrl-C, run `faillock --user "$USER"` and apply the stop rule.
pacman asks `Proceed with installation? [Y/n]`. Its post-install note about
the DMS widget is for the optional step at the end; ignore it for now.

This installs (README → "What it installs"):

- `/usr/lib/archmaid/archmaid-helper` (0755 root:root, **not setuid**)
- `/usr/share/polkit-1/actions/org.archmaid.helper.policy`
- `/usr/bin/archmaid`. `~/.local/bin` comes first on your PATH, so a plain
  `archmaid` is still the old build.
- The icon. (The package no longer ships a TUI desktop entry.)
- The DMS widget files in `/etc/xdg/quickshell/dms-plugins/archmaid/`
  (inactive until you enable the plugin).
- The `archmaid-updates-check` user timer (not enabled) and its script.
- `/usr/share/doc/archmaid/helper.md`.

Install `archmaid-gui-git` **only** for the DMS widget step at the end.

### B3. Verify (no root)

```sh
pacman -Q archmaid-git
stat -c '%A %U:%G %n' /usr/lib/archmaid/archmaid-helper /usr/lib/archmaid /usr/lib /usr
pkaction | grep -c '^org\.archmaid\.helper\.'
pkaction --verbose --action-id org.archmaid.helper.paccache
pkaction --verbose --action-id org.archmaid.helper.remove-orphans
pkaction | grep -i archmaid | grep -v '^org\.archmaid\.helper\.'
```

Expected:

- `archmaid-git r<N>.<hash>-1`, with the S1 hash.
- `-rwxr-xr-x root:root /usr/lib/archmaid/archmaid-helper` (no `s`), and
  `drwxr-xr-x root:root` for the three directories. The helper refuses to
  run as root otherwise (exit 4).
- `9`: check-files, paccache, paccache-wipe, remove-orphans, repair,
  vacuum-journal, clean-tmp, disable-unit, full-cleanup.
- `paccache`: `implicit any: no`, `implicit inactive: no`,
  `implicit active: auth_admin_keep`, and the annotations
  `org.freedesktop.policykit.exec.path -> /usr/lib/archmaid/archmaid-helper`
  and `org.freedesktop.policykit.exec.argv1 -> paccache`.
- `remove-orphans`: `implicit active: auth_admin` (asks every time), argv1
  `remove-orphans`.
- The last line prints **nothing**: no other archmaid action is installed
  (for example a keep-check action left over from
  `polkit-keep-session-check.md`).

If `pkaction` says "No action with action id", wait 10 s and try again.
polkitd picks up new files by itself. Don't restart polkit.

**B4 (optional, read only).** Run the helper's one verb that needs no root,
as yourself, exactly as the GUI's orphan probe does:

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
cd ~/archmaid-live/gui && go build -trimpath -o "$T/archmaid-gui" ./cmd/archmaid-gui && go version -m "$T/archmaid-gui" | grep vcs
```

Expected: the S1 hash, `vcs.modified=false`. A cold build takes several
minutes (gotk4's cgo). The GUI must be the same commit as the installed
helper: they share the verbs and the orphan plan hash. (Instead of building,
`"$P"/pkg/archmaid-gui-git/usr/bin/archmaid-gui` is the same commit, from
makepkg.)

### C2. Launch from the terminal

```sh
sudo -k
START_C=$(date +%Y-%m-%dT%H:%M)
"$T/archmaid-gui" 2>"$T/gui-stderr.txt"; echo "rc=$?"; cat "$T/gui-stderr.txt"
```

`sudo -k` drops B2's sudo ticket for this terminal, so topgrade's sudo has
to ask through the GUI's dialog. The window "archmaid" opens on
**Overview**: the status line, **Last Run** (A5's record: aborted) and
**Actions**. The header has the view switcher (Overview, Run,
Logs, Settings) and Stop.

Check that this is the real GUI, not the demo stub: the title is plain
"archmaid" (not "archmaid (stub)"), there is **no** banner "Demo — the stub,
changes nothing", and the status bar has no `STUB`. Only `--stub` starts the
stub; no environment variable does. If you see any of the three, close the
window and stop here.

<!-- source: gui/cmd/archmaid-gui/main.go; gui/internal/ui/app.go
(parseLaunch: --stub only; StubBanner, StubAppID); window.go title,
statusText; launch_test.go -->

### C3. Dry run via Settings first

1. **Settings** → Runs → **Dry run** on ("Changes apply to the next run").
2. **Overview** → "Full Cleanup (Recommended)". The page switches to
   **Run**. A dialog asks to proceed with full cleanup and lists the plan
   (hyprpm last), with Cancel and **Proceed**. Press Proceed.
3. Expected: the status bar shows `DRY RUN`. **No polkit dialog, no password
   dialog, no "Waiting for authorisation…"**. Rows go to "Done · …". The
   cache-wipe confirm: press **Skip**. An orphan confirm, if any: either
   answer is safe. The status bar ends in "Finished: completed".
4. **Settings → Dry run off.** Check (terminal 2):

   ```sh
   cat ~/.config/archmaid/gui.json; echo; stat -c '%a' ~/.config/archmaid/gui.json
   cat ~/.local/state/archmaid/dryrun/last-run.json; echo
   ```

   Expected: no `"dry_run":true` in `gui.json` (`{}` or downloads only),
   mode `600`; the dry-run record's `finished` is now; the root
   `last-run.json` is unchanged (still A5's).

<!-- source: gui/internal/ui/settings.go ("Dry run", "Changes apply to the
next run."), gui/internal/settings (gui.json 0600, dry_run omitempty);
TestGUIRealDriver real-dry; internal/engine/queue.go authorized (the gate with
Plan and YesLabel "Proceed"); gui/internal/ui/dialogs.go (yes = YesLabel,
no = "Skip" for an Optional step); window.go statusText -->

### C4. A real Full Cleanup

In the same window: **Overview** → "Full Cleanup (Recommended)" → Proceed.

1. **AUR audit**, then **topgrade**. The audit's AUR lookup already ran
   while the Proceed dialog was up, so its row should finish at once. topgrade
   runs as you. When its sudo asks, a dialog "sudo Asks for Your Password"
   appears: "Asked by <binary>." plus sudo's prompt on one line, clipped to
   48 characters (a longer one ends in `…`), with Cancel / Authenticate.
   Check that the binary is topgrade's or yay's, then type the password. It
   may ask again later in a long topgrade (polkit runs have no sudo
   keep-alive). A wrong one here is checked with `su` and costs no faillock
   row; one isn't planned.
2. **Package cache, the polkit dialog:** the row says "Waiting for
   authorisation…" and the status bar "Waiting for authorisation". Your
   polkit agent's dialog shows "Authentication is required to remove old
   cached package versions"; under Details, `org.archmaid.helper.paccache`.
   If anything else shows, don't type: write it down.
   - **Deliberate failure 2 of 2: press Cancel.** Expected: the row says
     **"Not authorised"** and **the run goes on** with the next step.
     Terminal 2: `faillock --user "$USER"` → **1 row**.
3. "Delete ENTIRE package cache?": press **Skip**. No dialog.
4. **Orphans:** no dialog for the probe (`plan-orphans` runs as you). If it
   finds orphans, the confirm lists them and the cascade. **Yes** brings a
   polkit dialog "…remove orphaned packages and their unneeded
   dependencies" (`org.archmaid.helper.remove-orphans`, asks every time):
   **authenticate**. That resets faillock to 0. Skip is fine too. Same rule
   as A4 about `gtk4` and friends.
5. **AUR cache** (`yay -Sc`): no dialog expected. **Watch terminal 1:** this
   step doesn't get the askpass dialog. If a `[sudo] password for …` prompt
   appears there, don't type it: press **Stop** in the header (no polkit
   dialog is open then) and write it down.
6. **Temp files, logs, package integrity:** one polkit dialog each (clean-tmp,
   vacuum-journal, check-files). Each is its own polkit action, so each asks
   once. **Authenticate each.** The first correct password resets faillock
   to 0. With 2 rows, the rows would show "Waiting for authorisation… · one
   more failure locks your account": then apply the stop rule.
7. **Hyprland plugins last**, no dialog.
8. Expected: the status bar shows "Finished: completed". The package cache row
   says "Not authorised", the cache-wipe row "Skipped", the rest "Done".
   **Overview → Last Run:** "Last run incomplete: root steps refused (1)".
   **Logs:** the run folder's `cleanup.log` has `SKIP: Old package cache
   cleaned (privilege not granted)`.

Terminal 2:

```sh
cp ~/.local/state/archmaid/last-run.json "$T/last-run-C4.json" && cat "$T/last-run-C4.json"; echo
faillock --user "$USER"
```

Expected: `"action":"fullcleanup"`, `"outcome":"incomplete"`,
`"steps_refused":1`, `steps_skipped` ≥ 2 (the refused step and the declined
wipe), `"dry_run":false`; faillock **0 rows**.

<!-- source: gui/internal/realdriver package doc (topgrade's sudo through the
askpass Server; root steps through helperclient.Run/pkexec; the AUR audit's
prefetch behind the gate, TestAuditPrefetch); gui/internal/ui/dialogs.go
passwordText (askpassPromptMax = 48);
internal/engine/auth.go (AuthPolkit: faillock read before each helper step,
AuthWaiting until granted or NotGranted), queue.go stepExited (NotGranted →
AuthRefused, "SKIP … (privilege not granted)"), record() (incomplete);
gui/internal/runview/model.go Row.Subtitle; gui/internal/ui/dialogs.go
passwordDialog; gui/internal/overview Summarize; packaging/polkit policy
messages; TestGUIRealDriver real-refused, real-askpass; cmd/archmaid-helper/
README.md ("keep is per action"); internal/actions/queues.go (yay -Sc has no
NeedsPriv; privilege.Sudo.Env strips SUDO_ASKPASS from other steps) -->

### C5. Close mid-run: "Stop and Quit"

Same window: **Overview** → "Run topgrade (system update)". Answer the sudo
dialog if it asks. When topgrade's output is streaming, no dialog is open
and pacman isn't installing:

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
   pgrep -a 'archmaid|topgrade|yay'; ls /var/lib/pacman/db.lck
   cat ~/.local/state/archmaid/last-run.json; echo
   ```

   Expected: nothing running, no `db.lck`, and `"action":"topgrade"`,
   `"outcome":"aborted"`. If the process is still there after 30 s, use the
   recovery under "If something goes wrong" (`pkill archmaid`) and note it.

<!-- source: gui/internal/ui/window.go closeRequest (texts, Keep Running
default, closes off the main thread); internal/driver/driver.go teardown(asked)
(Abort before stopping children); TestGUIRealDriver real-close; HANDOVER "GUI:
widgets only on the GTK main thread" -->

### C6. Check

```sh
ls -la --time-style=full-iso ~/.local/state/archmaid ~/.local/state/archmaid/*/ > "$T/state-C.txt"; cat "$T/state-C.txt"
awk -v t="[$START_C" '$0 >= t' /var/log/pacman.log > "$T/pacman-C.txt"; wc -l "$T/pacman-C.txt"
```

Expected: **two** new run folders, one per run, each named for the minute
its run started: C4's (its entries, as in C4.8) and C5's. The GUI makes a
fresh engine, and so a fresh log store, for every action it starts. C5's
aborted topgrade logs nothing, but its audit row does (`OK: AUR audit` in
`audit.log`), so its folder exists. If C4 and C5 started in the same minute,
they share one folder. C3's dry run went to `dryrun/`, not here.
`pacman-C.txt` holds topgrade's lines and the orphan removal, if you
accepted it. The removal now runs as root through the helper, so it is
logged as `Running 'pacman -Rns --noconfirm -- …'` too.

### C7. The DMS widget (optional, only after A–C passed)

1. `sudo pacman -U "$P"/archmaid-gui-git-r*.pkg.tar.zst` (one sudo
   password). The widget opens `archmaid-gui` when it is on PATH. Without
   it, the widget runs `archmaid` in `$TERMINAL`, and the `archmaid` on
   DMS's PATH may be the old build.
2. Enable it as in `docs/dms-widget.md` → "Enable (owner)" (the timer, the
   plugin, the bar).
3. Expected: the pill is tinted as a warning: the last run (C5) is
   `aborted`. The widget warns on `aborted`, `cancelled` and `incomplete`
   alike. The popout names it. **Run Full Cleanup** opens the GUI. It was
   never shown in the live bar before, so look at it once.

## Finish

Terminal 2:

```sh
faillock --user "$USER"
```

Expected: **0 rows**. Every correct password reset the count. Write the
number down.

## Results (fill in)

Date: ______  Commit (S1): ______  `pacman -Q archmaid-git`: ______
faillock rows: S4 ___ / after A4 ___ / after C4's cancel ___ / Finish ___

| Part/Step | Expected | Seen | OK? |
|---|---|---|---|
| A1 build | `vcs.revision` = S1 hash, not modified | | |
| A2 `--help` / `--dryrn` | usage, rc 0 / unknown option, rc 2; `diff` empty | | |
| A3 dry run | gate only, no password; 10 rows, hyprpm last; `[dry-run] would run`; summary "1 step(s) skipped" | | |
| A3 state | only `dryrun/` changed; its `last-run.json` has `dry_run: true` | | |
| A4 password | "🔒 sudo access required" is the gate | | |
| A4 wrong password (optional) | "Incorrect password — 2 attempts left"; faillock 0 rows | | |
| A4 askpass | no popup (or: caller = topgrade/yay) | | |
| A4 orphan confirm | N = ___, M = ___, answer ___ | | |
| A4 hyprpm | last row, after every sudo step | | |
| A4 summary + last-run | outcome `ok` (or `failed` + which row) | | |
| A5 abort popup | No preselected; `n` resumes, nothing stopped | | |
| A5 abort | menu at once; no topgrade/yay left; no `db.lck`; `topgrade`/`aborted` | | |
| A6 run folder | one new folder, 0700/0600; entries as listed | | |
| A6 pacman.log | removed count = M (if accepted) | | |
| B1 makepkg | 2 packages, hash = S1 | | |
| B3 helper file | `-rwxr-xr-x root:root`, no setuid | | |
| B3 pkaction | 9 actions; paccache keep, remove-orphans admin; argv1 set; nothing else | | |
| B4 plan-orphans (optional) | start, plan, result ok; rc 0 | | |
| C1 build | `vcs.revision` = S1 hash | | |
| C2 real GUI | title "archmaid", no demo banner, no `STUB` | | |
| C3 dry run | `DRY RUN`, no polkit or password dialog; Finished: completed | | |
| C3 settings | dry run off; `gui.json` 600 | | |
| C4 askpass | "sudo Asks for Your Password", "Asked by" = ___ ; times asked ___ | | |
| C4 paccache dialog | "Waiting for authorisation…"; policy message, `org.archmaid.helper.paccache` | | |
| C4 cancel | "Not authorised", run goes on; faillock 1 row | | |
| C4 next dialogs | authenticated: ___ (which); faillock back to 0 | | |
| C4 yay -Sc | no sudo prompt in terminal 1 (or: seen, stopped) | | |
| C4 end | "Finished: completed"; Last Run "incomplete … (1)"; `incomplete`, `steps_refused` 1 | | |
| C5 close | "Stop the run and quit?"; Keep Running continues | | |
| C5 Stop and Quit | window gone, exits ≤ 5 s; nothing left; `topgrade`/`aborted` | | |
| C6 run folders | two new folders, C4's and C5's (one if both started in the same minute) | | |
| C7 widget (optional) | warning tint, popout, opens the GUI | | |
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
- faillock before and after (S4, after A4, after C4's cancel, Finish).
- The run folders' logs if a step failed or a count looks wrong, and
  `"$T"/pacman-A.txt` / `"$T"/pacman-C.txt` if the orphan count didn't
  match.
- `pacman -Q archmaid-git` and the S1 hash.

## Rollback

Do it whenever you want the machine back as it was. Steps 1–2 need one sudo
password each, so type them carefully.

1. **If you did C7:** remove the widget from the bar, then
   `systemctl --user disable --now archmaid-updates-check.timer` and
   `sudo pacman -R archmaid-gui-git`.
2. **The helper and the policy:**

   ```sh
   sudo pacman -R archmaid-git
   pkaction --action-id org.archmaid.helper.paccache; echo "rc=$?"
   ls /usr/lib/archmaid
   ```

   `pkaction` must fail ("No action with action id …", non-zero rc), and
   `/usr/lib/archmaid` must be gone. This also removes everything else B2
   installed.
3. **The GUI's settings:** `rm ~/.config/archmaid/gui.json && rmdir
   ~/.config/archmaid` (the folder didn't exist before this guide).
4. **The scratch folders**, once you've sent the results: `rm -rf "$T" "$P"`.
   Then the S0 worktree (`~/archmaid` itself stays as it is):

   ```sh
   cd ~ && git -C ~/archmaid worktree remove ~/archmaid-live
   ```

   If git refuses because the worktree has changes, look at
   `git -C ~/archmaid-live status` first; the builds above write nothing
   there.
5. **The old binary:** nothing to restore. This guide never touched
   `~/.local/bin/archmaid`, and `command -v archmaid` still finds it.
   **But that build (commit 83ecad2, 2026-09-23) prunes the run history to
   5 folders at every start, `--help` included**, a bug fixed since. Copy
   `~/.local/state/archmaid` again before you start it. To go back to the
   pre-test state folder as well:

   ```sh
   mv ~/.local/state/archmaid ~/.local/state/archmaid.live-run && cp -a "$B" ~/.local/state/archmaid
   ```

   (`$B` is from S3. In a new terminal, use the `~/archmaid-state-backup-…`
   path you saw then.)

<!-- source: packaging/arch/README.md (What it installs); docs/dms-widget.md
(Disable); `go version -m ~/.local/bin/archmaid` read on 2026-10-04:
vcs.revision 83ecad2, vcs.time 2026-09-23, and `git grep pruneLogs 83ecad2`
shows the prune in main(); HANDOVER "Nothing deletes run history at startup" -->
