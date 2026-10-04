# HANDOVER

The living orientation doc: where things are, the decisions you shouldn't undo
without reading why, how to live-test safely, and what's still open. It is
deliberately **not** a changelog — history is in `git log` (commit messages
carry the root-causing and verification), tests are in `TESTING.md`, features
are in `README.md`. Keep this file current-state only; rewrite entries rather
than appending dated ones.

## Status

Feature-complete: all nine menu actions work, with `--dryrun` for a safe end to
end. The gate is `scripts/check.sh`: race tests, vet, gofmt and coverage
floors (see `TESTING.md`). Verified live against a real Arch system: Full
Cleanup runs, the sudo/faillock flows, and the AUR audit against the real AUR.

## Code map (`tui/`, `package main`)

| File | Owns |
|---|---|
| `main.go` | package doc + `main()`: askpass dispatch, flag parsing (`parseArgs`: `--dryrun`, `--downloads`), `pruneLogs`, `tea.NewProgram` |
| `downloads.go` | "Delete old downloads": which folder (`resolveDownloadsDir`), what is old (`oldDownloads`, by ctime), and the in-process delete step (`deleteOldDownloads`) |
| `model.go` | screens, `menu`, the `model` struct, `initialModel`, `Init` |
| `update.go` | `Update`/`handleKey` — the single source of state transitions — plus queue, confirm and abort orchestration |
| `view.go` | `View` and every renderer; `popupLayer` is the one place that knows which screens are popups |
| `tasks.go` | `step`, `runStep` (streams a child's output), `waitForStream` (pumps it to the UI in batches), the step queues, sudo probe/auth/keep-alive, discovery commands, `editConfig` |
| `output.go` | the running step's output pane: full buffer for the log, capped tail for the viewport, follow-the-bottom |
| `audit.go` | the AUR audit: RPC lookup, classification, maintainer snapshot, report |
| `log.go` | log categories, per-run directories, `pruneLogs` |
| `askpass.go` | `SUDO_ASKPASS` helper + socket server |
| `overlay.go` | ANSI-aware popup compositor |
| `sanitize.go` | `Sanitize`: the one filter for untrusted text (command output, logs) |
| `status.go`, `theme.go` | the status line (native: statfs, MemAvailable from /proc/meminfo, a cache-dir walk — no child processes); the Tokyo Night styles |

## Decisions and landmines

Each of these was learned the hard way. The reasoning lives in the code
comments and `git log`; this is the "read before you touch it" list.

- **Steps run as plain children — never add `Setsid`.** sudo tickets are keyed to
  the tty *and* session. Per-child `Setsid` gave every internal sudo its own
  ticket (repeated popups during topgrade); a shared-pty attempt froze topgrade.
  Both are reverted in git. Plain children share the session, so the one primed
  ticket covers every step and everything topgrade/yay spawn.
- **Auth is two-stage (`authSudo`).** One wrong `sudo -S -v` burns *two*
  `pam_faillock` failures (the wrong password plus sudo's retry hitting EOF), and
  `sudo -A` burns two as well — with Arch's `deny=3`, one typo from lockout. So
  the password is validated with `su -s /usr/bin/true <user>` first
  (`suCheck`: single-shot, su's stack has no faillock on Arch, and `-s` keeps
  the user's shell startup files out of it, which `su -c true` didn't); only a
  known-correct password reaches sudo. The mid-run askpass popup does the same
  (`answerAskpass`): a password su rejects is never written to sudo's
  connection, the popup stays up with the attempts left, and the third failure
  cancels the request and skips the queue's remaining sudo steps, like a
  failed prime. A password with a line break is refused before su: sudo reads
  one line, so it would get a different password than su checked.
- **Nothing may prompt on the raw tty.** Sudo steps run `sudo -A`, and
  `SUDO_ASKPASS` routes any re-prompt back into the TUI as a popup. The password
  is passed straight through, never stored or logged. `sudo -n -v` is only the
  cache probe and the 60 s keep-alive.
- **`SUDO_ASKPASS` goes only to steps that use sudo.** It is never set in
  archmaid's own environment: `stepEnv` gives it to `needsSudo` steps (sudo
  steps and topgrade) and strips it from every other child, including a value
  inherited from the user's shell. The socket and wrapper live in a private
  0700 directory under `XDG_RUNTIME_DIR`. Without that variable askpass stays
  off — no `/tmp` fallback — and sudo steps run `sudo -n` instead of `-A`:
  with `SUDO_ASKPASS` unset, `sudo -A` falls back to sudo.conf's
  `Path askpass` (on this machine seahorse's GUI ssh-askpass) or exits with
  "no askpass program specified", so `-n` (the primed ticket, or a clean
  failure) is the form that never prompts anywhere. This does **not** close
  the threat for topgrade: everything it starts — yay, makepkg, a PKGBUILD's
  `build()` — inherits `SUDO_ASKPASS` and can reach the socket. The peer check
  below is the control there; full closure comes with the P6 repo/AUR split.
- **An askpass request is answered only from the running sudo step.** The
  server records each client's pid/uid (`SO_PEERCRED`) and start time;
  `handleAskpass` hangs up unless a `needsSudo` step is running, and
  `verifyAskpassPeer` requires: our uid; the same start time as at accept (pid
  not reused); `/proc/<pid>/exe` = `os.Executable()` (the wrapper execs us);
  a parent that is sudo; and a parent chain that reaches the step's child,
  matched on pid *and* the start time `runStepUntil` captured before anything
  could reap it. A user can't read sudo's `/proc/<pid>/exe` (setuid root; it
  needs ptrace access), so "is sudo" is: exe = `/usr/bin/sudo` when readable,
  else euid 0 and comm `sudo`. A user process can't get euid 0, but comm comes
  from the executed file's name, so a symlink named `sudo` to another
  setuid-root binary would pass; none of Arch's starts a user program as its
  child without authenticating first.
  Refusals leave a line in the step's output. The popup names the step, the
  real binary and pid of sudo's caller, and sudo's prompt only quoted and
  sanitized (`sudo -p` sets it). Residual risk, for P6: code already running
  under topgrade can run `sudo -A` itself with its own askpass, connect from
  it, keep the socket in a forked child and exec archmaid in place — the peer
  then checks out. The popup's caller line is what gives that away: genuine
  requests come from topgrade or yay, that one from whatever ran sudo inside
  the build (typically `/usr/bin/bash`).
- **Repair scans as the user first, and asks for sudo only to recheck what it
  flagged.** `scanRepairCandidates` lists the installed packages
  (`pacman -Qq`) and runs `pacman -Qkq -- <chunk>` as the user, in up to
  `repairScanWorkers` (min(cores, 16)) parallel chunks, exec'd directly. Nothing
  flagged is the "No broken packages" notice with no probe and no prompt.
  Otherwise the candidates are held as `scanAfterAuth`, authenticated like any
  sudo queue (one probe; the password prompt when uncached; esc drops the
  held recheck; three wrong passwords end in a notice), and rechecked with one
  `sudo -n pacman -Qkq -- <candidates>`; only what root confirms is offered for
  reinstalling. Don't drop the recheck: as the user, files in root-only dirs
  (`/boot` is 0700, `/var/lib/nfs/statd` is rpcuser's) fail `stat` and look
  missing, so amd-ucode and nfs-utils would be "repaired" on every run. And
  don't go back to scanning everything as root: the user scan can't miss a
  truly broken package (a missing file fails `stat` for anyone), and it's
  ~316 ms + ~50 ms for the recheck against ~850 ms for one root scan (see
  `TESTING.md` → Manual measurements). In both stages pacman's exit 1 is
  "found problems", a result; a failure with empty stdout is an error, never
  "nothing broken".
- **`hyprpm` runs last.** It drops the sudo ticket on exit, silently breaking the
  keep-alive; Full Cleanup runs topgrade with `--disable hyprpm` and appends its
  own final step.
- **Full Cleanup's order:** AUR audit → topgrade → `paccache -r` → optional
  `paccache -rk0` → orphan probe → (orphan removal, inserted by the probe) →
  `yay -Sc` → tmpfiles → journal vacuum → `pacman -Qkq` → hyprpm. Building the
  queue runs nothing; the only work `activate` starts is the audit prefetch,
  off the UI thread.
- **Orphans are found after the update, by an in-process step.** The probe
  (`findOrphans`) runs `pacman -Qdtq` once topgrade is done — an upgrade
  orphans packages of its own, and the list `-Rns --noconfirm` acts on must be
  the one the confirm showed — and, if there are any, inserts the removal right
  behind itself (`stepResult.insertNext`), with exactly those names. No
  orphans: a line saying so, no confirm. A failed query fails the probe and
  inserts nothing. Don't move the query back into `fullCleanupSteps`: it ran
  synchronously in `Update` (a 130–200 ms freeze) and went stale over
  topgrade.
- **The orphan confirm previews the `-Rns` cascade.** `-s` recursively removes a
  target's now-unneeded dependencies, so the real removal count can far exceed
  the `pacman -Qdtq` list (a real run confirmed 23 and removed 46). The probe
  previews it with `pacman -Rs --print` (`orphanRemovalPreview`; `-n` conflicts
  with `--print`; no root needed) and lists it in its output, under the
  confirm. Without the preview there's no removal: a failed `--print` fails the
  probe.
- **Logging policy.** Ordinary steps log only failures. `alwaysLog` steps log
  success too, and a `SKIP … (declined)` entry when their confirm is declined:
  anything that removes or overwrites something, topgrade (the step that changes
  the most), and the AUR audit. That keeps what those steps did auditable from
  archmaid's own log. Logs are split into
  `topgrade`/`orphans`/`cleanup`/`audit` files per run directory; the newest 5
  run directories are kept, `dryrun/` is a single reused slot, and files in the
  state-dir root (the AUR snapshot) are ignored by `pruneLogs`. Only `appendLog`
  creates a run directory (viewing logs must not, or it evicts a real run), and
  logs are private: directories 0700, files 0600.
- **Untrusted text goes through `Sanitize`.** Command output (topgrade, yay, AUR
  build scripts) can carry escapes that retitle the terminal, write the
  clipboard (OSC 52) or redraw what the user reads. Streamed output keeps only
  SGR colours (`SanitizeKeepSGR`); logs are plain text, both when written and
  when an older log is read back (`SanitizePlain`); AUR names and maintainers
  go through it too (`aurField`: plain, one line, capped). It also drops
  Unicode format characters (bidi overrides, zero-width). New paths that show foreign
  text should use it rather than a second filter.
- **The AUR audit diffs against a snapshot** because the RPC only exposes the
  *current* maintainer and co-maintainers, never their history. Consequence:
  the first audit only records a baseline. It is archmaid's only network call.
  A dry run reads the snapshot but never advances it.
- **Co-maintainers gate like maintainers.** A co-maintainer can push to the
  package's AUR git, so an *added* one is high (named in the gate: "foo:
  co-maintainer bob added"), a removed one a glance. The RPC omits
  `CoMaintainers` when there are none, so absent means none. The snapshot entry
  is `{"maintainer": …, "comaintainers": [sorted]}`; files from before this
  hold a bare `"maintainer"` string per package, which still loads (`snapEntry`
  with `hasCo` false). No co-maintainer record = a baseline for that field
  (no alarm, like a first audit), recorded by the next commit. Don't make it
  alarm instead: the upgrade would gate every package with a co-maintainer.
  Any other entry shape (null, a number, no `maintainer`) is corrupt.
- **Saving the snapshot is the acknowledgement, so it happens only on accept.**
  `performAudit` never writes; callers run a commit. The audit step hands it to
  topgrade's gate as `step.onAccept`: "yes" saves (logged `ACCEPT`), "no" or an
  abort drops it, so the same change gates the next run too. With nothing high
  it saves at once. The menu audit saves everything *except* the high findings
  straight away; `a` on the report accepts those. Don't move the save back
  into the audit: then one "no" spends the alarm for good.
- **A corrupt snapshot fails closed.** Only a missing file is "first audit";
  anything unreadable is a high finding that gates topgrade, and is only
  replaced on accept (moved aside as `aur-maintainers.json.corrupt-<time>`).
  Reading it as missing would silently re-baseline over a takeover. Writes are
  atomic (0600 temp + fsync + rename + dir fsync) under a non-blocking flock on
  `aur-maintainers.lock`; a held lock skips the save (logged `WARN`).
- **Full Cleanup and Run topgrade open with the audit, directly in front of
  topgrade** (one `auditStep`, gated identically). It runs
  as an in-process step (`step.run`, a `tea.Cmd` — not a shell command, so
  there's nothing to signal: an abort cancels the step's `ctx`, which ends the
  pacman query and the RPC at once, and `stepSeq` drops the late result). The
  whole audit has one deadline, `auditDeadline` (20 s for the query and every
  RPC chunk together), not a timeout per request. The lookup itself starts
  the moment the action is chosen: `activate` starts an `auditFetch` (a
  goroutine publishing its result on a `done` channel, owned by the model as
  `m.auditFetch`) so it runs while the password prompt or the gate is up, and
  the step awaits it instead of fetching again (one RPC per run). It only
  reads: the snapshot commit still comes from the step's result, so the gate
  semantics below are unchanged. Esc on the password prompt, a declined gate,
  an abort and the end of the run cancel it (`dropAuditFetch`); three wrong
  passwords don't, because the queue still runs and the audit step (no sudo)
  still reports from it. A
  high-severity finding (adoption, maintainer change, added co-maintainer) uses
  `stepResult.confirmNext` to put a named confirm in front of topgrade and make it
  optional, so "no" skips only the update and the cleanup carries on. An
  unreachable AUR fails the audit step but never gates. In-process steps run even
  in a dry run, so they must be read-only apart from what they gate on `dryRun`
  themselves.
- **Holding back a single risky AUR upgrade is yay's job, not archmaid's.** Full
  Cleanup runs `topgrade -y` → yay unattended, and archmaid never sees a PKGBUILD;
  the audit gate can only skip the whole update. The 72 h cooldown hook lives outside this repo, in
  `~/dotfiles/yay/.config/yay/init.lua` (stowed to `~/.config/yay/`), and needs
  stowing on any new machine. The audit's "pushed within 72 h" flag deliberately
  matches it.
- **The downloads folder is resolved, never assumed.** On the dev machine it is
  `~/Download`, `~/Downloads` doesn't exist, and `XDG_DOWNLOAD_DIR="$HOME/"` —
  so "just read the XDG dir" would clean the whole home folder.
  `resolveDownloadsDir` walks flag → `ARCHMAID_DOWNLOADS` → XDG → `~/Downloads`
  → `~/Download`, resolves symlinks, and refuses `$HOME`, `/` and ancestors of
  `$HOME` for every source; all but the flag must be strictly inside `$HOME`.
  Don't add a candidate that skips `checkDownloadsDir`.
- **Old downloads are judged by ctime, in Go — never `find -mtime … rm -rf`.**
  The old `find "$dir" -maxdepth 1` matched `$dir` itself (once its mtime aged,
  the whole folder went), and mtime lies: wget, `curl -R` and unzip back-date
  it. `oldDownloads` lists only the folder's entries; a directory counts only
  if nothing under it has a new ctime; nothing is followed (Lstat, a symlink is
  removed as the link). The delete is an in-process step that checks the
  folder is still the scanned inode, re-checks each entry right before
  removing it, and removes through an `os.Root` (hence `go 1.25`). Tests
  can't set ctimes: they move `downloadsNow` past a mark instead.
- **The downloads cleaner never crosses a mount point — never use
  `os.Root.RemoveAll` there.** RemoveAll descends into a filesystem mounted
  below the folder (a USB disk, sshfs, a bind mount of `~/Documents`) and
  empties it; those files carry their own old ctimes. `boundary` is the
  folder's `st_dev` plus every mount point inside it from
  `/proc/self/mountinfo` (a bind mount keeps the device, so `st_dev` alone
  misses it; an unreadable mount table fails closed). An entry that is or
  holds anything outside it is never old, and `removeTree` deletes bottom-up
  by hand, Lstat-checking each node against the boundary and opening each
  directory as a nested `os.Root` that must be the inode it Lstat'd.
  `TestDownloadsMountPoints` mounts tmpfs and bind mounts for real inside
  `unshare -rm` (skipped where that's unavailable); `statDev` and
  `mountinfoPath` are the seams for the rest.
- **Streamed output is batched, and the pane shows only a tail.** A step's
  child writes combined stdout+stderr to one ordered channel: every line, then
  exactly one done. The pump (`waitForStream`) blocks for the first message,
  then takes whatever else is already buffered — up to `streamBatchMax` (256)
  lines, stopping at the done — and delivers it as one `streamBatchMsg`
  stamped with the launch's `stepSeq`; `handleStream` applies the whole batch
  with one `SetContent`, or drops it if the seq is stale. A slow producer gets
  one-line batches; a flood gets full ones, so the UI catches up by itself. The
  full output still accumulates in `m.buf` (what `finishStep` logs, so
  `alwaysLog` steps keep everything); the viewport gets only the newest
  `maxVisibleLines` (2000) from `m.tail`. One update per line with the whole
  buffer re-split each time was quadratic: 5k lines cost ~1 s of CPU and
  ~1.8 GB of allocations, 20k lines ~5 s (`BenchmarkStreamView*`, now ~5 ms
  and ~21 ms; `ARCHMAID_PERF_GUARD=1 scripts/check.sh` fails if they
  regress past 3×, see `TESTING.md` → Perf guard). The pane follows new output only while it's at the bottom; a
  user who scrolled up stays put, and when the tail drops old lines their
  offset moves with the text. A step is done when its child exits: the pipe
  is read for at most `stepWaitDelay` (2 s, `exec.Cmd.WaitDelay`) after that,
  so a background process the child left holding stdout can't keep it
  "running" — no process-group change needed (see the `Setsid` landmine).
- **`m.bg` is one field, not a stack.** A popup that overwrites it with its own
  screen value makes `renderScreen` recurse forever; see `handleAskpass` in
  `update.go`.

## Live-testing safely

Unit tests can't cover real PAM, real pacman or a real terminal. When you need
to:

- Drive the real binary with VHS or tmux under a **scratch `HOME`** and never
  point it at the real `~/.local/state/archmaid` (see `e2e/run.sh`).
- Wrong-password flows: stay under faillock's `deny=3` — check
  `faillock --user $USER` first, pace attempts ~10 s apart so nothing is typed
  while auth is in flight, and expect the two-stage auth to burn nothing.
- A real Full Cleanup needs a human at the keyboard for the sudo password and the
  destructive confirms; don't automate it. `--dryrun` is the safe substitute.
- Check the result in `~/.local/state/archmaid/<run>/` (or View log), and use
  `/var/log/pacman.log` as the independent record of what pacman actually did.

## Open items

- Test gaps (e2e breadth, hand-only checks) are tracked in
  `TESTING.md` → Known gaps.
