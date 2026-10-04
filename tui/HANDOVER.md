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
| `tasks.go` | `step`, `runStep` (streams a child's output), the step queues, sudo probe/auth/keep-alive, discovery commands, `editConfig` |
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
  the password is validated with `su <user> -c true` first (single-shot, and su's
  stack has no faillock on Arch); only a known-correct password reaches sudo.
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
  `build()` — inherits `SUDO_ASKPASS` and can reach the socket. Full closure
  comes with the P6 repo/AUR split.
- **`hyprpm` runs last.** It drops the sudo ticket on exit, silently breaking the
  keep-alive; Full Cleanup runs topgrade with `--disable hyprpm` and appends its
  own final step.
- **The orphan confirm previews the `-Rns` cascade.** `-s` recursively removes a
  target's now-unneeded dependencies, so the real removal count can far exceed
  the `pacman -Qdtq` list (a real run confirmed 23 and removed 46).
  `orphanRemovalTotal` previews it with `pacman -Rs --print` (`-n` conflicts with
  `--print`; no root needed).
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
  *current* maintainer, never adoption history. Consequence: the first audit only
  records a baseline. It is archmaid's only network call. A dry run reads the
  snapshot but never advances it.
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
  as an in-process step (`step.run`, a `tea.Cmd` — not a shell command, and not
  signalable; an abort just drops its late result via `stepSeq`). A
  high-severity finding (adoption or maintainer change) uses
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
  removing it, and removes via `os.Root.RemoveAll` (hence `go 1.25`). Tests
  can't set ctimes: they move `downloadsNow` past a mark instead.
- **`m.bg` is one field, not a stack.** A popup that overwrites it with its own
  screen value makes `renderScreen` recurse forever; see the `askpassMsg` case in
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
