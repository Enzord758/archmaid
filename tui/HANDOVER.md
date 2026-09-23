# HANDOVER

The living orientation doc: where things are, the decisions you shouldn't undo
without reading why, how to live-test safely, and what's still open. It is
deliberately **not** a changelog — history is in `git log` (commit messages
carry the root-causing and verification), tests are in `TESTING.md`, features
are in `README.md`. Keep this file current-state only; rewrite entries rather
than appending dated ones.

## Status

Feature-complete: all nine menu actions work, with `--dryrun` for a safe end to
end. The gate is `go test -race ./...`, `go vet ./...` and `gofmt -l .` all
clean (see `TESTING.md`). Verified live against a real Arch system: Full
Cleanup runs, the sudo/faillock flows, and the AUR audit against the real AUR.

## Code map (`tui/`, `package main`)

| File | Owns |
|---|---|
| `main.go` | package doc + `main()`: askpass dispatch, flag parsing, `pruneLogs`, `tea.NewProgram` |
| `model.go` | screens, `menu`, the `model` struct, `initialModel`, `Init` |
| `update.go` | `Update`/`handleKey` — the single source of state transitions — plus queue, confirm and abort orchestration |
| `view.go` | `View` and every renderer; `popupLayer` is the one place that knows which screens are popups |
| `tasks.go` | `step`, `runStep` (streams a child's output), the step queues, sudo probe/auth/keep-alive, discovery commands, `editConfig` |
| `audit.go` | the AUR audit: RPC lookup, classification, maintainer snapshot, report |
| `log.go` | log categories, per-run directories, `pruneLogs` |
| `askpass.go` | `SUDO_ASKPASS` helper + socket server |
| `overlay.go` | ANSI-aware popup compositor |
| `status.go`, `theme.go` | the status line; the Tokyo Night styles |

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
- **`hyprpm` runs last.** It drops the sudo ticket on exit, silently breaking the
  keep-alive; Full Cleanup runs topgrade with `--disable hyprpm` and appends its
  own final step.
- **The orphan confirm previews the `-Rns` cascade.** `-s` recursively removes a
  target's now-unneeded dependencies, so the real removal count can far exceed
  the `pacman -Qdtq` list (a real run confirmed 23 and removed 46).
  `orphanRemovalTotal` previews it with `pacman -Rs --print` (`-n` conflicts with
  `--print`; no root needed).
- **Logging policy.** Ordinary steps log only failures; steps that remove or
  overwrite something are `alwaysLog` and log success too, so what a destructive
  step did is auditable from archmaid's own log. Logs are split into
  `topgrade`/`orphans`/`cleanup`/`audit` files per run directory; the newest 5
  run directories are kept, `dryrun/` is a single reused slot, and files in the
  state-dir root (the AUR snapshot) are ignored by `pruneLogs`.
- **The AUR audit diffs against a snapshot** because the RPC only exposes the
  *current* maintainer, never adoption history. Consequence: the first audit only
  records a baseline. It is archmaid's only network call. A dry run reads the
  snapshot but never advances it.
- **Holding back risky AUR upgrades is yay's job, not archmaid's.** Full Cleanup
  runs `topgrade -y` → yay unattended, and archmaid never sees a PKGBUILD or gets
  to veto an upgrade. The 72 h cooldown hook lives outside this repo, in
  `~/dotfiles/yay/.config/yay/init.lua` (stowed to `~/.config/yay/`), and needs
  stowing on any new machine. The audit's "pushed within 72 h" flag deliberately
  matches it.
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

- **Topgrade is not `alwaysLog`.** It's the step that changes the most (a full
  system upgrade) yet only logs on failure. Worth reconsidering now that the
  machinery exists.
- **Nothing schedules the AUR audit,** and it doesn't gate Full Cleanup
  (deliberate: that would add a network call to every cleanup, and archmaid can't
  hold an upgrade anyway).
- Test gaps (askpass round trip, missing goldens, e2e breadth) are tracked in
  `TESTING.md` → Known gaps.
