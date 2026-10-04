# archmaid-helper

archmaid's root helper. It runs a fixed set of maintenance commands as root,
started through `pkexec`, with one polkit action per verb. The GUI uses it
because a GUI has no tty, so it can't share a sudo ticket. The TUI keeps
`sudo -A`; over SSH it is always sudo.

```
pkexec /usr/lib/archmaid/archmaid-helper <verb> [args]
```

- Installed at `/usr/lib/archmaid/archmaid-helper`, `0755 root:root`, **never
  setuid**. The path is a constant: polkit's `exec.path` names it, and the
  helper refuses to run as root from anywhere else.
- Go, stdlib only. The nested `go.mod` is temporary; a later phase moves the
  module to the repo root.

## Verbs

| Verb | Root command | Input / recheck | polkit action (`allow_active`) |
|---|---|---|---|
| `check-files` | `pacman -Qk` | none, read-only. Exit 1 from pacman means "missing files found", which is a result, not a failure | `…check-files` (keep) |
| `paccache N` | `paccache -r -k N` | `N` ∈ `1`–`9` | `…paccache` (keep) |
| `paccache-wipe` | `paccache -r -k 0` | none | `…paccache-wipe` (admin) |
| `plan-orphans` | `pacman -Qdtq`, then `pacman -Rs --print --print-format '%n %v' -- …` | read-only and **needs no root**. Emits a `plan` event with the hash | none (run it directly, not through pkexec) |
| `remove-orphans H` | `pacman -Rns --noconfirm -- …` | `H` =~ `^sha256:[0-9a-f]{64}$`. Recomputes the plan; a mismatch emits the new plan and exits **3**, removing nothing | `…remove-orphans` (admin) |
| `repair pkg…` | `pacman -S --noconfirm -- …` | 1–256 names =~ `^[a-z0-9@_+][a-z0-9@._+-]{0,127}$`; one bad name exits 2. Each package must be installed (`-Qq`), in a sync repo (`-Si`) and really missing files (`-Qk -- pkg`). Any other package is skipped and listed | `…repair` (keep) |
| `vacuum-journal D` | `journalctl --vacuum-time=D` | `D` ∈ `1week`, `2weeks`, `1month` | `…vacuum-journal` (keep) |
| `clean-tmp` | `systemd-tmpfiles --clean` | none | `…clean-tmp` (keep) |
| `disable-unit unit…` | `systemctl disable --now -- …` | 1–256 names =~ `^[A-Za-z0-9:_.@-]{1,250}\.service$`. A denylisted name or alias exits **4**, and the whole request is refused. A unit is skipped unless it is loaded, `UnitFileState=enabled` and `ActiveState` is `inactive`/`failed` | `…disable-unit` (admin) |
| `upgrade-repos` | `pacman -Syu --noconfirm` | none. Only the GUI uses it; the TUI keeps topgrade | `…upgrade-repos` (admin) |
| `full-cleanup …` | the sequence below | strict flags, see below | `…full-cleanup` (admin) |

Action ids are `org.archmaid.helper.<verb>` (see
`packaging/polkit/org.archmaid.helper.policy`). "keep" is `auth_admin_keep`,
"admin" is `auth_admin`.

The **orphan plan hash** is
`sha256("archmaid orphan plan v1\n" + "target <name>\n"… + "remove <name> <version>\n"…)`.
Both lists are sorted. It covers the direct orphans *and* the whole `-s`
cascade with versions, so a confirm given for one plan can't be spent on
another.

**Denylist** (`disable-unit`). Units whose names start with `systemd-`, `dbus`,
`polkit`, `getty@`, `NetworkManager`, `sshd`, `coolercontrold` or `ly@`, plus
`sddm`, `gdm`, `lightdm`, `ly`, `greetd` and `display-manager` (`.service`).
The check runs on the literal argument before any systemctl call. It runs again
on the unit's `Id` and `Names`, so a denied unit can't be reached through an
alias.

**full-cleanup** takes
`full-cleanup --keep N [--wipe] [--orphans sha256:…] --journal D`. Flags take
their value as the next argument, never `--flag=value`. Each flag may appear
once; unknown flags and positionals exit 2. The steps always run in this order:

1. `paccache -r -k N`
2. `paccache -r -k 0` (only with `--wipe`)
3. remove-orphans `H` (only with `--orphans`; a stale plan is skipped and reported, not run)
4. `journalctl --vacuum-time=D`
5. `systemd-tmpfiles --clean`
6. `pacman -Qk`

The steps are independent, so a failing step doesn't stop the rest. Result:
1 if any step failed, otherwise 3 if the orphan plan was stale, otherwise 0.
The GUI therefore shows one admin dialog for the whole cleanup, not about six.

Every name list is preceded by `--`. Every command is run by absolute path
(`/usr/bin/…`), never through PATH or a shell. stdin is `/dev/null`.

## Output: JSON lines, schema v1

stdout carries only JSON lines. Each is one object with `"v":1` and a type
`"t"`. Every invocation writes `start` first and exactly one `result` last,
including on usage errors and refusals. A failure also prints one plain line
on stderr.

```json
{"v":1,"t":"start","verb":"full-cleanup","version":"0.1.0"}
{"v":1,"t":"progress","step":1,"of":4,"label":"Cleaning package cache (keep 3)"}
{"v":1,"t":"line","stream":"out","text":"==> no candidate packages found for pruning"}
{"v":1,"t":"plan","targets":["a"],"items":["a 1.0-1","dep 2.1-3"],"total":2,"hash":"sha256:…"}
{"v":1,"t":"result","ok":true,"code":0,"child_exit":0,"skipped":[{"name":"x","reason":"no missing files"}]}
```

| t | fields |
|---|---|
| `start` | `verb`, `version`; `dev: true` only in a devhelper build |
| `progress` | `step`, `of` (1-based), `label` |
| `line` | `stream` (`out`/`err`), `text`: one line of child output (`\n`, `\r\n` or a lone `\r` ends a line; lines over 8 KiB are split) |
| `plan` | `targets` (orphans), `items` (`"name version"` of everything removed), `total` = `len(items)`, `hash` |
| `result` | `ok` (= `code == 0`), `code`, `child_exit` (exit status of the last root command run, `null` if none ran), `skipped` (`[{name, reason}]`, never `null`); `error` (string) when something went wrong |

All text is sanitized. Whole ANSI/VT escape sequences are removed (CSI, OSC,
DCS, APC, PM, SOS and the short ESC forms). C0/C1 control characters and DEL
are dropped, a tab becomes a space, and invalid UTF-8 becomes U+FFFD. Bidi
overrides and isolates and U+2028/2029 are dropped. Adding an optional field
is not a schema break; changing or removing one bumps `v`.

## Exit codes

| code | meaning |
|---|---|
| 0 | ok (including "nothing to do" and skips) |
| 1 | a root command failed, or an internal error |
| 2 | bad usage or input |
| 3 | state changed since the user confirmed (orphan plan hash mismatch) |
| 4 | refused: denylisted unit, not root, no `PKEXEC_UID`/`SUDO_UID`, wrong install path/ownership/mode, or a devhelper build run as root |
| 5 | busy: `/run/archmaid-helper.lock` is held, or `/var/lib/pacman/db.lck` exists (checked only for verbs that run pacman/paccache mutations) |

## Hardening

In order, before anything else:

1. Capture `PKEXEC_UID` (else `SUDO_UID`), then clear the whole environment
   and set `PATH=/usr/bin LC_ALL=C TERM=dumb`. Every child again gets exactly
   that environment, so it is cleared twice. Also `umask 022`, `chdir /`, and
   every inherited fd above 2 is closed. The Go runtime's own netpoller fds
   (eventpoll/eventfd) can't be told apart from inherited ones of the same
   kind, so those are set close-on-exec instead of closed.
2. `SIGPIPE` is ignored. If the GUI dies, writes to stdout just fail and the
   helper keeps draining the child, so pacman is never killed mid-transaction.
3. Privilege, for every verb except `plan-orphans`: euid must be 0;
   `PKEXEC_UID` or `SUDO_UID` must be a decimal uid; `/proc/self/exe` must be
   exactly `/usr/lib/archmaid/archmaid-helper`. That file and every parent up
   to `/` must be root-owned, not group- or other-writable, and not a symlink.
   The binary must be a regular file without setuid/setgid.
4. Validate input (exit 2), take the flock on `/run/archmaid-helper.lock`
   (exit 5), recompute state, then run.

## Threat model

**Protects against**

- A local user process, or a compromised GUI, that can call `pkexec` and talk
  to the helper. It can only pick a verb and pass validated arguments; polkit
  still demands the admin password. "keep" verbs are read-only or bounded and
  idempotent. Anything destructive or wide is "admin", so it prompts every
  time.
- Confused-deputy input: option injection (every name list follows `--`,
  regexes forbid a leading `-` on package names), `repo/pkg` syntax,
  arbitrary journal windows, `paccache -k 0` smuggled through `paccache`, and
  disabling critical units directly or through an alias.
- TOCTOU between confirm and act. remove-orphans recomputes the plan and
  requires the hash the user saw. repair and disable-unit recheck each target
  as root and skip anything that no longer qualifies.
- Environment and fd attacks on the root process (`LD_PRELOAD`, `PATH`,
  `GCONV_PATH`, `IFS`, leaked fds). Control and escape sequences in output
  reaching a terminal or GUI.
- A helper copied or symlinked elsewhere, or installed with weak permissions.
  It refuses to run as root unless it is the root-owned file at the fixed
  path.

**Does not protect against**

- Root, or anyone who can already write to `/usr`. The admin password itself.
  A compromised pacman, paccache, systemd or mirror.
- What a legitimate admin-approved action does. `upgrade-repos` installs
  whatever the repos serve, and `disable-unit` can still disable a
  non-denylisted unit the user depends on.
- Denial of service by holding the lock or `db.lck`.

## Development

```sh
cd cmd/archmaid-helper
go test -race ./... && go vet ./... && test -z "$(gofmt -l .)"   # the gate
go build -tags devhelper -o /tmp/ah . && /tmp/ah plan-orphans       # dev build, as yourself
```

All seams are on `helper.Options`: `Runner`, `Euid`, `CallerUID`, `Exe`,
`Stat`, `LockPath`, `PacmanDBLock` and `Dev`. The tests use a fake runner that
records argv and never executes anything. The only real processes in the tests
are `/bin/sh` and `/usr/bin/env`, which exercise the exec runner and the
process hardening.

A **devhelper build** (`-tags devhelper`) runs from any path as a normal user
and skips the install check. It **refuses to run as root**, so it can never
stand in for the real helper. Never package it.

Live polkit and faillock behaviour can't be unit-tested. See
`tui/TESTING.md` → "Manual verification".
