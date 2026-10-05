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
- Go, stdlib only. It is part of the repo's one Go module (`go.mod` at the
  root), but imports nothing outside the standard library — not even the
  TUI's `internal/` packages.

## Verbs

| Verb | Root command | Input / recheck | polkit action (`allow_active`) |
|---|---|---|---|
| `check-files [pkg…]` | `pacman -Qk`, or with names `pacman -Qk -- …` | read-only. Exit 1 from pacman means "missing files found", which is a result, not a failure. With no names it checks every package. With names (Repair's root recheck of the user scan's candidates) it takes 1–256 of them, each =~ the `repair` name pattern (so none starts with `-`), none twice, and each installed (`pacman -Qq`); anything else exits 2 before `-Qk` runs. Either way it then emits one `check` event (`checked`, `broken`) before the result: with names, a name without a summary line is skipped (`could not check its files`); without, it lists every package pacman printed a summary for. Any pacman exit but 0/1 exits 1 with no `check` event | `…check-files` (keep) |
| `paccache N` | `paccache -r -k N` | `N` ∈ `1`–`9` | `…paccache` (keep) |
| `paccache-wipe` | `paccache -r -k 0` | none | `…paccache-wipe` (admin) |
| `plan-orphans` | `pacman -Qdtq`, then `pacman -Rs --print --print-format '%n %v' -- …` | read-only and **needs no root**; run as root, it must still pass the install check (exit 4). Emits a `plan` event with the hash | none (run it directly, not through pkexec) |
| `remove-orphans H` | `pacman -Rns --noconfirm -- …` | `H` =~ `^sha256:[0-9a-f]{64}$`. Recomputes the plan right before `-Rns`; a mismatch emits the new plan and exits **3**, a pacman running then exits **5**, both removing nothing | `…remove-orphans` (admin) |
| `repair pkg…` | `pacman -S --noconfirm -- …` | 1–256 names =~ `^[A-Za-z0-9@_+][A-Za-z0-9@._+-]{0,127}$` (uppercase is allowed, as makepkg allows it for local builds); one bad name exits 2. Each package must be installed (`-Q`), in a sync repo (`-Si`), really missing files (`-Qk -- pkg`) and installed at exactly the version the first repo listing it has (`-Q` vs. `-Si` → `Version`), so a repair is never a partial upgrade or a downgrade. Any other package is skipped and listed, a version mismatch with the reason `repo has a different version (installed X, repo Y) — update the system first`. If nothing is left to reinstall and a version mismatch is among the reasons, it exits **3**. Then the whole transaction is checked: `pacman -Sp --noconfirm --print-format '%n %v' -- …` (stdout only) must list exactly the packages left, each at its installed version. A dependency it would install or upgrade too, a version that changed meanwhile or a package missing from it exits **3**, reinstalls nothing, and lists every package as skipped (the extra packages are named in the reasons and in `error`). Unparseable `-Sp` output or a failing `-Sp` exits 1. Note: the reinstall comes from the first repo (pacman.conf order) that lists the package, which then carries the installed version; that need not be the repo it was installed from. On CachyOS, a package installed from `core` is reinstalled from `cachyos-core-v4` when that repo lists the same version first: same version, that repo's build | `…repair` (admin) |
| `vacuum-journal D` | `journalctl --vacuum-time=D` | `D` ∈ `1week`, `2weeks`, `1month` | `…vacuum-journal` (keep) |
| `clean-tmp` | `systemd-tmpfiles --clean` | none | `…clean-tmp` (keep) |
| `disable-unit unit…` | `systemctl disable --now -- …` | 1–256 names =~ `^[A-Za-z0-9:_@][A-Za-z0-9:_.@-]{0,249}\.service$` (never a leading `-` or `.`). A denylisted name, alias, sleep hook or `Also=` unit, a running `Also=` unit, or a trigger that would start the unit again exits **4**, and the whole request is refused (see "Companions and triggers"). A unit is skipped unless it is loaded, `UnitFileState=enabled` and `ActiveState` is `inactive`/`failed` | `…disable-unit` (admin) |
| `full-cleanup …` | the sequence below | strict flags, see below | `…full-cleanup` (admin) |

Action ids are `org.archmaid.helper.<verb>` (see
`packaging/polkit/org.archmaid.helper.policy`). "keep" is `auth_admin_keep`,
"admin" is `auth_admin`.

**What "keep" allows.** After the user enters the admin password once, polkit
keeps the authorization for about five minutes. During that time **any process
in the same login session** can run that verb again through `pkexec` without a
prompt. polkit stores a process's temporary authorization against its logind
session when it has one, so assume the whole session, not just the GUI.
Question 3 in `docs/TESTING.md` is the live check of this. The authorization
covers the action, not the arguments: approving `paccache 3` also allows
`paccache 1`. A verb is "keep" only if it is acceptable for another process of
the session to re-run it with any argument it accepts:

| Verb | Re-run without a prompt can… | Why that's acceptable |
|---|---|---|
| `check-files [pkg…]` | read every package's file list again, or some installed packages' | read-only; the names can only narrow the full check, and must be installed packages |
| `clean-tmp` | apply the system's `tmpfiles.d` age rules again | no input; the same as the daily `systemd-tmpfiles-clean.timer`, and a second run finds nothing more |
| `paccache N` | thin the cache down to `N` ≥ 1 versions per package | the only input is one digit; the newest cached version always stays, `-k 0` is the separate admin verb `paccache-wipe`, and removed packages can be fetched again from a mirror or the Arch Linux Archive |
| `vacuum-journal D` | delete archived journal files older than `D` ≥ 1 week | the only input is one of three fixed windows. Deleted logs are gone for good, but the most recent week is never touched, so a process can't erase a trace of what it just did |

Nothing in the "keep" set takes free-form input; `check-files`' names are
held to installed packages. `repair` is **admin** even
though it rechecks every name, because it takes package names and runs a pacman
transaction: the packages' install scriptlets and pacman hooks run as root.
(It only reinstalls a package whose repo version equals the installed one,
and only when the transaction pulls in nothing else, so it can't be used for a
partial upgrade.) That is not something to allow
silently for five minutes. It costs nothing in the usual
flow, because `check-files` → `repair` already asks twice (keep is per action).

The **orphan plan hash** is
`sha256("archmaid orphan plan v1\n" + "target <name>\n"… + "remove <name> <version>\n"…)`.
Both lists are sorted. It covers the direct orphans *and* the whole `-s`
cascade with versions, so a confirm given for one plan can't be spent on
another.

**Denylist** (`disable-unit`). A unit is denied if:

- its name starts with `systemd-`, `polkit`, `getty@`, `serial-getty@`,
  `NetworkManager`, `sshd`, `coolercontrold` or `ly@`;
- its name without the type suffix is the system bus `dbus`, `dbus-broker` or
  `dbus-daemon`; one of the login managers `sddm`, `gdm`,
  `lightdm`, `ly`, `greetd`, `display-manager`; the security controls
  `firewalld`, `nftables`, `iptables`, `ip6tables`, `ufw`, `apparmor`,
  `auditd`; the sleep hooks `nvidia-suspend`, `nvidia-resume`,
  `nvidia-hibernate`, `nvidia-suspend-then-hibernate`; or the power and
  thermal managers `power-profiles-daemon`, `tlp`, `thermald`, `upower` (so
  `firewalld.socket` is denied as well as `firewalld.service`);
- it is wanted, required or upheld by a sleep unit: `sleep.target`,
  `suspend.target`, `hibernate.target`, `hybrid-sleep.target`,
  `suspend-then-hibernate.target` or the matching `systemd-*.service`.

The name check runs on the literal argument before any systemctl call. It runs
again on the unit's `Id` and `Names` (`systemctl show`), so a denied unit can't
be reached through an alias. The bus is matched by exact name, not by a `dbus`
prefix, because ordinary services carry D-Bus activation aliases
(`bluetooth.service` is also `dbus-org.bluez.service`, `avahi-daemon.service`
is `dbus-org.freedesktop.Avahi.service`). Such an alias neither causes nor
avoids a refusal: `dbus-org.freedesktop.login1.service` is still refused,
because its `Id` is `systemd-logind.service`. For each unit it is about to disable, the helper
then reads the `[Install]` section with `systemctl cat` (unit file plus
drop-ins). `systemctl show` has no `Also=` property, and its `WantedBy` lists
only loaded units, so it would miss `WantedBy=systemd-suspend.service`. Every
`Alias=`, `WantedBy=`/`RequiredBy=`/`UpheldBy=` and `Also=` value counts, and
list resets in drop-ins are ignored, so the helper reads too much rather than
too little. Lines are joined the way systemd joins them (a trailing `\`
continues a line; a comment line inside a continuation is skipped, not
joined). The helper splits values on whitespace only, so anything systemd
would read differently is refused (exit 4) rather than guessed at: an
`[Install]` value containing a quote, a backslash, `%` (a specifier) or `#`,
or an `[Install]` line without `=`. `systemctl disable` also disables the units named in `Also=`, so
those are checked the same way and their own `Also=` is followed too (at most
1024 units). An `Also=` unit's `[Install]` is read whenever its `LoadState`
isn't `not-found` (a masked one too), and failing to read it refuses. An
`Also=` value with a specifier (`%i`) or an odd name can't be checked, so it
is refused. Any hit refuses the whole request with exit 4.

**Companions and triggers** (`disable-unit`). The helper also refuses, with
exit 4 and the whole request, when disabling wouldn't do what the user was
shown:

- an `Also=` unit (transitively) is running: its `ActiveState` isn't
  `inactive` or `failed`. `disable --now` stops only the named units, so a
  failed `avahi-daemon.service` whose `Also=avahi-daemon.socket` is active
  would leave the socket listening, disabled, and starting the service again
  until the next boot;
- a unit it would disable has a `TriggeredBy` unit (`systemctl show`: a
  socket, timer, path, …) that is running, or that is enabled (`enabled*`) and
  not disabled by the same request. It would start the unit again.

It refuses rather than skips so that the result never silently differs from
the request: the error names the unit, and the caller can stop the companion
or trigger first, or drop that unit and ask again. A pair that is stopped and
disabled together (a failed service and its inactive socket named in `Also=`)
is allowed.

**full-cleanup** takes
`full-cleanup --keep N [--wipe] [--uninstalled] [--orphans sha256:…] --journal D`. Flags take
their value as the next argument, never `--flag=value`. Each flag may appear
once; unknown flags and positionals exit 2. The steps ("parts") always run in
this order, each named by the verb it stands for (`clean-uninstalled` has a name of its own):

1. `remove-orphans`: remove-orphans `H` (only with `--orphans`; a stale plan is skipped and reported, not run). First, right after the lock check, while the confirmed plan is freshest
2. `paccache`: `paccache -r -k N`
3. `paccache-wipe`: `paccache -r -k 0` (only with `--wipe`)
4. `clean-uninstalled`: `pacman -Sc --noconfirm` (only with `--uninstalled`): the cached packages of what is no longer installed (pacman.conf's `CleanMethod`) and unused sync databases. It is the root half of `yay -Sc`, which the GUI runs here instead of through sudo, after the orphan removal so the orphans' packages go too. It is the one part that is no verb of its own (no polkit action): it only ever runs under `full-cleanup`'s dialog
5. `clean-tmp`: `systemd-tmpfiles --clean`
6. `vacuum-journal`: `journalctl --vacuum-time=D`
7. `check-files`: `pacman -Qk`

The parts are independent, so a failing part doesn't stop the rest. Each
part's `progress` events carry `part`, and the part ends with a `done` event
(its own outcome), before the next part's first `progress`. Result: 1 if any
part failed, otherwise 3 if the orphan plan was stale, otherwise 0. The GUI
runs Full Cleanup's root steps as this one verb, so the whole cleanup shows
one admin dialog, and maps each part back to its own checklist row
(`internal/helperclient`). The order is part of the protocol: the client
checks it.

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
| `progress` | `step`, `of` (1-based), `label`; `part` (the part's verb) only in `full-cleanup` |
| `done` | `full-cleanup` only, ending one part: `step`, `of`, `part`, `ok` (= `code == 0`), `code` (0, 1 failed, 3 stale plan, 5 pacman running), `error` when not ok |
| `line` | `stream` (`out`/`err`), `text`: one line of child output (`\n`, `\r\n` or a lone `\r` ends a line; lines over 8 KiB are split) |
| `plan` | `targets` (orphans), `items` (`"name version"` of everything removed), `total` = `len(items)`, `hash` |
| `check` | `check-files` (with or without names): `checked` (the packages pacman printed a summary for), `broken` (those of them missing files); both arrays, never `null` |
| `result` | `ok` (= `code == 0`), `code`, `child_exit` (exit status of the last root command run, `null` if none ran), `skipped` (`[{name, reason}]`, never `null`); `error` (string) when something went wrong |

All text is sanitized. Whole ANSI/VT escape sequences are removed (CSI, OSC,
DCS, APC, PM, SOS and the short ESC forms). C0/C1 control characters and DEL
are dropped, a tab becomes a space, and invalid UTF-8 becomes U+FFFD.
Invisible characters that can reorder, hide or fake text are dropped, the same
set the TUI's sanitizer drops. These are Unicode format characters (category
Cf: bidi embeddings, overrides, isolates and marks, zero-width spaces and
joiners, U+2060–2064, the BOM), the whole tag block U+E0000–E007F, U+2028/2029
and the Hangul fillers U+115F, U+1160, U+3164 and U+FFA0. Adding an optional
field is not a schema break; changing or removing one bumps `v`.

## Exit codes

| code | meaning |
|---|---|
| 0 | ok (including "nothing to do" and skips) |
| 1 | a root command failed, or an internal error |
| 2 | bad usage or input |
| 3 | state changed since the user confirmed (orphan plan hash mismatch), or `repair` has nothing it may reinstall because the repos have a different version, or reinstalling would pull in or upgrade other packages (update the system first) |
| 4 | refused: denylisted unit, a running `Also=` unit or trigger (or an enabled trigger left behind), not root, no `PKEXEC_UID`/`SUDO_UID`, wrong install path/ownership/mode, or a devhelper build run as root |
| 5 | busy: `/run/archmaid-helper.lock` is held, or `/var/lib/pacman/db.lck` exists (checked only for verbs that run pacman/paccache mutations) |

## Hardening

In order, before anything else:

1. Capture `PKEXEC_UID` (else `SUDO_UID`), then clear the whole environment
   and set `PATH=/usr/bin LC_ALL=C TERM=dumb`. Every child again gets exactly
   that environment, so it is cleared twice. Also `umask 022`, `chdir /`, and
   every inherited fd above 2 is closed. The Go runtime's own netpoller fds
   (eventpoll/eventfd) can't be told apart from inherited ones of the same
   kind, so those are set close-on-exec instead of closed.
2. `SIGPIPE`, `SIGINT`, `SIGHUP`, `SIGTERM`, `SIGQUIT`, `SIGTSTP`, `SIGTTIN`
   and `SIGTTOU` are caught and dropped for the whole run, so the helper always
   finishes (it is never stopped either) and writes its `result` line. If the
   GUI dies, writes to stdout just fail with `EPIPE` and the helper keeps
   draining the child, so pacman is never killed mid-transaction. A Ctrl-C,
   Ctrl-\, Ctrl-Z or a closed terminal (`pkexec` from a shell) is dropped the
   same way. Only root can stop the helper, with `SIGKILL`. They are caught
   (`signal.Notify`), **not** ignored: a `SIG_IGN` disposition survives exec,
   so pacman's scriptlets and hooks would start with `SIGINT`/`SIGTERM`/`SIGHUP`
   ignored, a hook's `timeout` couldn't stop a hung child, and a script's
   `trap` would do nothing. A caught signal is reset to the default on exec,
   so every child starts clean. (One corner: in the background of a terminal
   set to `stty tostop`, the helper spins on its writes to that terminal until
   it is brought to the foreground, instead of being stopped.)
   **Every child runs in a session of its own** (`Setsid`), so nothing the
   terminal sends to its foreground process group, and no kill of the helper's
   process group, reaches pacman. The child has no controlling terminal and
   needs none: stdin is `/dev/null`, output goes through pipes. This is the
   opposite of the TUI, whose steps must *not* get `Setsid` because they run
   `sudo` and a sudo ticket is keyed to the tty and session
   (`docs/HANDOVER.md` → landmines). The helper is root already through pkexec
   and runs no sudo, so it has no ticket to lose. Don't copy either choice into
   the other program.
3. Privilege, for every verb except `plan-orphans`: euid must be 0;
   `PKEXEC_UID` or `SUDO_UID` must be a decimal uid; `/proc/self/exe` must be
   exactly `/usr/lib/archmaid/archmaid-helper`. That file and every parent up
   to `/` must be root-owned, not group- or other-writable, and not a symlink.
   The binary must be a regular file without setuid/setgid. `plan-orphans`
   needs no root, but if it does run as root (sudo, or pkexec's generic
   action), the install-path and ownership checks still apply.
4. Validate input (exit 2), take the flock on `/run/archmaid-helper.lock`
   (exit 5), recompute state, then run.

## Threat model

**Protects against**

- A local user process, or a compromised GUI, that can call `pkexec` and talk
  to the helper. It can only pick a verb and pass validated arguments; polkit
  still demands the admin password. "keep" verbs take no free-form input and
  are read-only or bounded (see "What keep allows"); any process of the
  session may repeat them for about five minutes after the user authenticates.
  Anything that runs a pacman transaction, or is destructive or wide, is
  "admin", so it prompts every time.
- Confused-deputy input: option injection (every name list follows `--`,
  regexes forbid a leading `-` on package names), `repo/pkg` syntax,
  arbitrary journal windows, `paccache -k 0` smuggled through `paccache`, and
  disabling critical units directly, through an alias or through `Also=`.
- TOCTOU between confirm and act. remove-orphans recomputes the plan and
  requires the hash the user saw, right before `-Rns` and between two checks
  that pacman isn't running (exit 5 if it is). repair and disable-unit recheck each target
  as root and skip anything that no longer qualifies; repair also skips a
  package whose repo version differs from the installed one and refuses a
  transaction (`-Sp`) that holds anything but the broken packages at their
  installed versions, so a refreshed sync database or a missing or outdated
  dependency can't turn a repair into a partial upgrade.
- Environment and fd attacks on the root process (`LD_PRELOAD`, `PATH`,
  `GCONV_PATH`, `IFS`, leaked fds). Control and escape sequences in output
  reaching a terminal or GUI.
- A helper copied or symlinked elsewhere, or installed with weak permissions.
  It refuses to run as root unless it is the root-owned file at the fixed
  path.

**Does not protect against**

- Root, or anyone who can already write to `/usr`. The admin password itself.
  A compromised pacman, paccache, systemd or mirror.
- What a legitimate admin-approved action does. `repair` reinstalls whatever
  the repos serve at the installed version, and `disable-unit` can still
  disable a non-denylisted unit the user depends on. (System updates are not a
  helper verb: topgrade is archmaid's only update path, in the TUI and the
  GUI alike.)
- Denial of service by holding the lock or `db.lck`.

## Development

From the repo root (the module root):

```sh
go test -race ./cmd/archmaid-helper/...                             # the helper's tests
make gate                                                           # the whole gate (scripts/check.sh), helper included
go build -tags devhelper -o /tmp/ah ./cmd/archmaid-helper && /tmp/ah plan-orphans   # dev build, as yourself
```

All seams are on `helper.Options`: `Runner`, `Euid`, `CallerUID`, `Exe`,
`Stat`, `LockPath`, `PacmanDBLock` and `Dev`. The tests use a fake runner that
records argv and never executes anything. The only real processes in the tests
are `/bin/sh`, `/usr/bin/env`, `/usr/bin/cat` and the test binary re-executed,
which exercise the exec runner, the process hardening and the signal handling.

A **devhelper build** (`-tags devhelper`) runs from any path as a normal user
and skips the install check. It **refuses to run as root**, so it can never
stand in for the real helper. Never package it.

Live polkit and faillock behaviour can't be unit-tested. See
`docs/TESTING.md` → "Manual verification".
