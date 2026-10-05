# Security review: merging refactor/p0 into main

Scope: `origin/refactor/p0` at 4d3b11d against `origin/main` (5585358),
security side only. Dev 1's `docs/merge-to-main.md` has the general
checklist. The review is by reading the code and running the gate
(`scripts/check.sh`); nothing ran live. The one-password work
(`feat/gui-one-password`) is **not** part of this merge: it stays gated by
its own review (`review/one-password`, findings 1–8 there).

## Verdict: GO

Nothing in the merge needs fixing first. There are no HIGH findings. The
MEDIUM items are residual risks of the design that the owner should know
and accept, listed at the end. The doc fixes below are already made on
this branch.

## What becomes root-reachable on merge

Root is reached only through `pkexec /usr/lib/archmaid/archmaid-helper
<verb> …`, installed by the package, never setuid. The TUI reaches root
only through sudo, as on main.

| Verb | polkit | What it runs as root | Input it takes |
|---|---|---|---|
| `check-files` | auth_admin_keep | `pacman -Qk` (read-only) | nothing, or 1–256 installed package names |
| `paccache` | auth_admin_keep | `paccache -r -k N` | N in 1–9 |
| `vacuum-journal` | auth_admin_keep | `journalctl --vacuum-time=D` | 1week, 2weeks or 1month |
| `clean-tmp` | auth_admin_keep | `systemd-tmpfiles --clean` | nothing |
| `paccache-wipe` | auth_admin | `paccache -r -k 0` | nothing |
| `remove-orphans` | auth_admin | `pacman -Rns` of the plan, recomputed as root | the plan hash only |
| `repair` | auth_admin | `pacman -S` of broken packages at their installed version | names, vetted against `pacman -Sp` |
| `disable-unit` | auth_admin | `systemctl disable --now` | `*.service` names; denylist |
| `full-cleanup` | auth_admin | the fixed sequence (orphans, paccache, wipe, `pacman -Sc`, tmpfiles, journal, `pacman -Qk`) | strict flags only |
| `plan-orphans` | none | runs without root | nothing |

Checked:
- **Policy:** every action has `exec.path` = the install path and its own
  `exec.argv1`; `allow_any` and `allow_inactive` are `no`. The tests
  `TestPolicyMatchesVerbs` and `TestArgvMatchesPolicy` hold this.
- **Helper hardening:**
  - it refuses root unless it runs from the install path, in root-owned
    directories that are not group- or other-writable;
  - it scrubs its environment, sets umask 022 and cwd `/`, and closes
    inherited fds;
  - every child runs by absolute path, never through a shell, with
    `/dev/null` as stdin, in a session of its own;
  - its lock is `/run`, 0600 and `O_NOFOLLOW`.
- **Package:**
  - helper 0755 root, never setuid; the policy is 0644;
  - the install script only prints hints;
  - the user timer runs `checkupdates` as the user (`NoNewPrivileges`) and
    writes 0600 into a 0700 directory.
- **DMS widget:** it only reads three state files and launches
  `archmaid-gui` or a terminal with `archmaid` (an argv, never a shell). It
  never calls sudo or pkexec.

## Defaults checked

- **sudo never prompts on a terminal:** `proc` refuses any sudo whose first
  argument isn't `-n` or `-A`, also behind `env` or `sh -c`. `SUDO_ASKPASS`
  goes only to steps marked NeedsPriv and is stripped from every other
  child.
- **Askpass:** its socket and wrapper live in a private 0700 directory
  under `XDG_RUNTIME_DIR`, with no `/tmp` fallback. A request is answered
  only from the running sudo step's own process tree (uid, start time, exe,
  a sudo parent, the chain up to the step's child), and every password is
  checked with `su` before sudo sees it.
- **ttyguard:** while the TUI runs, its terminal is exclusive, so no step's
  child (git, ssh, pinentry) can open it and read keys. Root still can.
- **Detach and KillNow:** only the GUI sets them (`TestTUIDoesNotDetach`).
  The GUI's children have no terminal, and a second signal kills every
  step's process group.
- **Files:** logs and state are 0700/0600, and nothing under them is
  secret.
- **Supply chain:** Go modules are pinned by `go.sum`, and the package
  builds with `-mod=readonly`.

## Findings

**M1. The TUI's sudo ticket is shared with everything in its terminal
session (residual, by design).** The TUI primes sudo once and keeps the
ticket alive (60 s refresh) so that topgrade and yay don't ask again. For
the whole run, any process in that session can use sudo without a password,
including what topgrade starts. The ticket also outlives the TUI by sudo's
timeout in that terminal. This is the same exposure as `yay --sudoloop` in
a terminal. Option for the owner: `sudo -n -k` when the TUI exits, which
closes the after-run window. Not done here: it changes behaviour.

**M2. Passwords can reach a core dump (residual).**
- The typed password ends in Go strings (`engine.Secret`, the TUI's input
  field) that stay in the heap until reused. This is accepted in HANDOVER.
- Neither front end turns core dumps off, so a crash can put that memory
  into a systemd-coredump file, which the user can read with
  `coredumpctl`.
- Recommendation: `prctl(PR_SET_DUMPABLE, 0)` at startup in archmaid and
  archmaid-gui. It needs a test run first: it also makes the process's
  `/proc` entries root-owned.

**M3. Askpass trusts the step's process tree, and the dialog's caller line
is the only check (residual, accepted).** A `sudo -A` started by code under
topgrade, such as an AUR package's build script, passes the peer checks.
What the user sees is the dialog naming the caller (`/usr/bin/bash` instead
of topgrade or yay), and the user has to read it. This holds for both front
ends; HANDOVER's askpass landmine records it.

**L1. polkit keep actions.** After one authentication of `check-files`,
`paccache`, `vacuum-journal` or `clean-tmp`, other processes in the session
may run that same verb again for about five minutes without a prompt. The
effects are bounded and documented in the policy. The GUI itself only uses
`check-files`' keep (Repair's recheck). The scope inside a real login
session is still an open item (HANDOVER, `docs/polkit-keep-session-check.md`).

**L2. Widget text rendering.** The widget shows strings from its state
files (audit findings, last-run fields) in `StyledText`, which may treat
text that looks like markup as rich text. The files are archmaid's own,
written by the same user, and AUR-derived strings are sanitized before they
are stored, so this is defence in depth only. Recommendation:
`textFormat: Text.PlainText` on file-derived text.

**L3. PKGBUILD source.** It builds a git checkout of `branch=main` with
`SKIP` checksums. That is normal for a `-git` package. The release TODOs
already in the PKGBUILD (pin a tag or commit, real checksum, licence) must
be done before publishing.

**L4. ttyguard after SIGKILL.** A SIGKILLed TUI leaves its terminal
exclusive until the terminal is closed. Documented in HANDOVER.

**L5. Orphan removal TOCTOU.** A millisecond window remains in the helper's
orphan removal. Documented in HANDOVER, needs a concurrent root pacman, and
`-Rns` still refuses required packages.

**Docs, fixed on this branch (HANDOVER):**
- The Detach landmine said the GUI's AUR-cache step gets the askpass. Since
  `yay -Sc --aur` it runs without sudo; only topgrade asks.
- Open items said the GUI wasn't run live and named an `escalateKill`
  timer that outlived Close. Both are outdated: the GUI has run live, and
  `escalateKill` returns once the child is reaped.

## What the owner should know

1. **One password per run in the TUI unlocks sudo for the whole run.** The
   ticket is shared with everything topgrade starts, including AUR builds,
   and lasts a few minutes after the run in that terminal (M1).
2. **Read the askpass dialog's caller line.** A request from
   `/usr/bin/bash`, or anything that isn't topgrade or yay, is not
   archmaid's own (M3).
3. **The GUI asks twice per Full Cleanup:** topgrade's askpass and one
   polkit dialog. Nothing is stored. The one-password branch would change
   this; it is not in this merge and is under review.
4. **A crash can leave the typed password in a core dump.** Hardening is
   recommended (M2).
5. **Root comes only from the package's helper,** at a fixed path, through
   polkit. Every verb takes fixed or validated input, and destructive ones
   ask every time.
