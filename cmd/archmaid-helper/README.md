# archmaid-helper

archmaid's root helper. It runs a fixed set of maintenance commands as root,
started through `pkexec`, with one polkit action per verb. The GUI uses it
because a GUI has no tty, so it can't share a sudo ticket. The TUI keeps
`sudo -A`; over SSH it is always sudo.

```
pkexec --disable-internal-agent /usr/lib/archmaid/archmaid-helper <verb> [args]
```

`--disable-internal-agent` is pkexec's own option (the helper never sees
it): with no polkit agent for the GUI, pkexec exits 127 ("No authentication
agent found") instead of starting its built-in text agent on the terminal
the GUI was started from, which fails anyway on polkit 126+ (see
`docs/polkit-keep-session-check.md`).

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
| `paccache-wipe` | `paccache -r -k 0` | none. The "Clear caches" menu action runs it when the package cache is ticked in its pick (which shows the user-side dry run's total and the warning); Full Cleanup does not (it keeps 3 versions, and `full-cleanup` takes no `--wipe`) | `…paccache-wipe` (admin) |
| `plan-orphans` | `pacman -Qdtq`, then `pacman -Rs --print --print-format '%n %v' -- …` | read-only and **needs no root**; run as root, it must still pass the install check (exit 4). Emits a `plan` event with the hash | none (run it directly, not through pkexec) |
| `remove-orphans H` | `pacman -Rns --noconfirm -- …` | `H` =~ `^sha256:[0-9a-f]{64}$`. Recomputes the plan right before `-Rns`; a mismatch emits the new plan and exits **3**, a pacman running then exits **5**, both removing nothing | `…remove-orphans` (admin) |
| `repair pkg…` | `pacman -S --noconfirm -- …` | 1–256 names =~ `^[A-Za-z0-9@_+][A-Za-z0-9@._+-]{0,127}$` (uppercase is allowed, as makepkg allows it for local builds); one bad name exits 2. Each package must be installed (`-Q`), in a sync repo (`-Si`), really missing files (`-Qk -- pkg`) and installed at exactly the version the first repo listing it has (`-Q` vs. `-Si` → `Version`), so a repair is never a partial upgrade or a downgrade. Any other package is skipped and listed, a version mismatch with the reason `repo has a different version (installed X, repo Y) — update the system first`. If nothing is left to reinstall and a version mismatch is among the reasons, it exits **3**. Then the whole transaction is checked: `pacman -Sp --noconfirm --print-format '%n %v' -- …` (stdout only) must list exactly the packages left, each at its installed version. A dependency it would install or upgrade too, a version that changed meanwhile or a package missing from it exits **3**, reinstalls nothing, and lists every package as skipped (the extra packages are named in the reasons and in `error`). Unparseable `-Sp` output or a failing `-Sp` exits 1. Note: the reinstall comes from the first repo (pacman.conf order) that lists the package, which then carries the installed version; that need not be the repo it was installed from. On CachyOS, a package installed from `core` is reinstalled from `cachyos-core-v4` when that repo lists the same version first: same version, that repo's build | `…repair` (admin) |
| `plan-install [--asdeps] pkg…` | `pacman -Si -- pkg` (each), `-Qu`, `-Q`, `-Sp --needed --noconfirm --print-format '%r %n %v' -- …`, `-Si -- repo/name…` | read-only and **needs no root** (as `plan-orphans`). 1–32 names (`MaxPkgNames`) =~ the name pattern, none twice; exit 2 otherwise. Each must be in a sync repo by exact name (groups and providers are not targets; exit 2). `--asdeps` is the one option token, first, and is part of the hash; it only changes the `-S` of `pkg-install`. Exits **3** (reason `updates-pending`, with `count`) when `pacman -Qu` is not empty (`N update(s) are waiting: update the system first`), or when a planned item is installed at another version (an upgrade; `names` = the item). Exits **4** (reason `conflict`, `names` = the item and the installed package) when a planned item `Replaces` or `Conflicts With` an installed package (`vim` replaced by `gvim`: remove the old one first). Exit 2 reasons: `not-found` (not in a sync repo), `bad-request`. Emits a `plan` event: `targets` = the names, `items` = `name version` of the whole transaction, dependencies included (the repo is in `rows[].repo`; the hash is over `repo name version`), plus the optional fields `mode` (`install`), `asdeps`, `rows` (`{name, version, old, repo, target, dl, inst}`, same order as `items`; sizes in bytes from `pacman -Si`, `old` always empty) and `warnings` (a plan above 30 items) | none |
| `pkg-install H [--asdeps] pkg…` | `pacman -S --needed --noconfirm [--asdeps] -- …` (sorted names) | `H` =~ the hash pattern. Under the helper lock and between two `db.lck` checks it recomputes the same plan (same vetting; a vetting failure exits with its own code) and requires the hash; a different plan emits the new plan and exits **3**, running nothing; a pacman running exits **5**. An empty plan (all up to date) exits 0 with nothing run | `…pkg-install` (admin, never keep) |
| `pkg-install-local H file…` | `pacman -U --noconfirm --color always -- <staged copies>` (build order) | `H` =~ the hash pattern; 1–64 files, none twice, each exactly `<base>/out/<name>.pkg.tar.zst\|xz` (`<base>` a package name, `<name>` strictly `pkgname-pkgver-pkgrel-arch`), **relative to the invoking user's `~/.cache/archmaid/aur`**. The user is `PKEXEC_UID` (never 0) and the home is that user's `/etc/passwd` entry, never an environment variable. See "Local (AUR) installs" below: each file is read **once** (path walked with `openat(O_NOFOLLOW\|O_DIRECTORY)`, every folder and the file checked on its fd: the user's, not group/other-writable, regular, 1 byte to 2 GiB), copied into a fresh root-only staging folder (`/var/cache/archmaid/staging/run-*`, 0700) while hashed; the hash of the staged copies must equal `H` (else exit **3**, reason `stale`, no plan event, nothing run); then `pacman -Qip`/`-Qlp` read each staged copy (logged), its name, version and architecture must be the file name's and an architecture pacman accepts, and the update, protected and conflict rules of `pkg-install` apply (a package may not provide a protected name either). Staging is removed on every exit, panic included, and what a killed run left is removed by the next one. Exits: **2** `bad-request`; **3** `updates-pending` or `stale` (also a file gone since the build); **4** `unsafe-file` (a symlink, a foreign owner, group/other-writable, not a regular file, too big, on the file's path), `bad-package`, `protected`, `conflict`; **5** `busy`; 1 pacman failed | `…pkg-install-local` (admin, never keep) |
| `plan-remove [-n] pkg…` | `pacman -Qq`, the protected list (see below), then `pacman -Rs --print --print-format '%n %v' -- …` (always `-Rs`: pacman refuses `-Rns` with `--print`; the cascade is the same) | read-only, no root. `-n` is the one option token, first; it selects `-Rns` (config files too) for the root run, and the hash mode `remove-n`. 1–32 names, each installed (exit 2). The **targets** are checked against the protected list before pacman is asked about the cascade; a protected target exits **4**, reason `protected`, `names` = the protected targets. pacman then refusing on dependencies ("could not satisfy dependencies" / "removing X breaks dependency 'Y' required by Z") exits **4**, reason `required-by`, `names` = the packages that need it (Z, parsed from those lines; empty when none can be parsed, and `error` says so). Any other pacman error is a plain failure (exit 1, no reason). A protected package only in the cascade exits **4**, reason `protected`, `names` = the protected ones found. Exit 2: `not-installed`. Emits a `plan` event: `items` = `name version` of everything `-Rs` removes, plus `mode` (`remove`), `with_config`, `rows` (`repo` empty, `inst` from `pacman -Qi`) and `warnings` (a cascade above 30 items). The hash covers mode, sorted targets and sorted items: `name version` for a remove | none |
| `pkg-remove H [-n] pkg…` | `pacman -Rs[n] --noconfirm -- …` (sorted names) | as `pkg-install`: recompute, hash, stale exits **3** (reason `stale`) with the new plan, busy **5** (reason `busy`). Never `-Rc` or `-Rdd` | `…pkg-remove` (admin, never keep) |
| `vacuum-journal D` | `journalctl --vacuum-time=D` | `D` ∈ `1week`, `2weeks`, `1month` | `…vacuum-journal` (keep) |
| `clean-tmp` | `systemd-tmpfiles --clean` | none | `…clean-tmp` (keep) |
| `disable-unit unit…` | `systemctl disable --now -- …` | 1–256 names =~ `^[A-Za-z0-9:_@][A-Za-z0-9:_.@-]{0,249}\.service$` (never a leading `-` or `.`). A denylisted name, alias, sleep hook or `Also=` unit, a running `Also=` unit, or a trigger that would start the unit again exits **4**, and the whole request is refused (see "Companions and triggers"). A unit is skipped unless it is loaded, `UnitFileState=enabled` and `ActiveState` is `inactive`/`failed` | `…disable-unit` (admin) |
| `full-cleanup …` | the sequence below | strict flags, see below | `…full-cleanup` (admin) |

**Result `reason`, `names`, `count`** (package verbs; additive, `v` stays 1): `bad-request` (2), `not-found` (2), `not-installed` (2), `updates-pending` (3, `count`), `stale` (3), `conflict` (4), `protected` (4), `required-by` (4), `unsafe-file` (4, pkg-install-local), `bad-package` (4, pkg-install-local), `busy` (5; also set by every verb's helper-lock busy). A plain failure (exit 1) has none. `docs/design/packages-contract.md` section 3 is the front end's side.

**Protected list** (`protected.go`, for the remove verbs). Never removed, as a target or as part of the cascade (exit 4, naming the package): `base`, `filesystem`, `glibc`, `bash`, `coreutils`, `util-linux`, `systemd`, `systemd-libs`, `pacman`, `archlinux-keyring`, `sudo`, `sudo-rs`, `polkit`, the stock kernels, `mkinitcpio`, `dracut`, the bootloaders (`grub`, `limine`, `refind`, `syslinux`, `efibootmgr`), anything named `archmaid*`, and, asked of pacman each run, the owner of every `/usr/lib/modules/*/pkgbase` (the running kernel and every installed one) and everything `base` depends on. A failing lookup refuses (fail closed).

**Colour.** Every pacman above also gets a fixed `--color` (before any
`--`), so the run output can show pacman's colour without anything the
helper parses ever holding an escape. A pacman whose output is only
relayed gets `--color always`: `-Rns` (remove-orphans), `-S` (repair), `-S --needed` (pkg-install), `-Rs[n]` (pkg-remove),
`-Sc` and `-Qk` (full-cleanup's parts); its `line` events keep the SGR
sequences (see Output). A pacman whose output the helper parses gets
`--color never`: every query (`-Qq`, `-Qdtq`, `-Rs --print`, `-Q`, `-Si`,
`-Qk -- pkg`, `-Sp`, `-Qu`, `-Qqo`, `-Rs[n] --print`) and the `check-files` verb's own `-Qk`, whose summary
lines make the `check` event. paccache colours only on a terminal. The
relayed systemd calls (`systemd-tmpfiles --clean`, `journalctl --vacuum-time`,
`systemctl disable --now`) get `SYSTEMD_COLORS=1` in their own environment
(`CallEnv`); every other call, `systemctl show`/`cat` above all, stays on
plain `ChildEnv`, because `disable-unit` parses `systemctl cat`, which with
colour on wraps `[Install]` and every
directive in SGR. The values are constants; the caller has no say.

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
`full-cleanup --keep N [--uninstalled] [--orphans sha256:…] --journal D`. Flags take
their value as the next argument, never `--flag=value`. Each flag may appear
once; unknown flags and positionals exit 2. The steps ("parts") always run in
this order, each named by the verb it stands for (`clean-uninstalled` has a name of its own):

1. `remove-orphans`: remove-orphans `H` (only with `--orphans`; a stale plan is skipped and reported, not run). First, right after the lock check, while the confirmed plan is freshest
2. `paccache`: `paccache -r -k N`
3. `clean-uninstalled`: `pacman -Sc --noconfirm` (only with `--uninstalled`): the cached packages of what is no longer installed (pacman.conf's `CleanMethod`) and unused sync databases. It is the root half of `yay -Sc`, which the GUI runs here instead of through sudo, after the orphan removal so the orphans' packages go too. It is the one part that is no verb of its own (no polkit action): it only ever runs under `full-cleanup`'s dialog. Before `-Sc`, holding pacman's own `db.lck`, it removes the empty `download-XXXXXX` folders pacman left in the cache (after an interrupted download, and after every transaction that downloaded nothing), which `-Sc` would otherwise report one red `error: could not open file …` line each for (see "Download leftovers" below)
4. `clean-tmp`: `systemd-tmpfiles --clean`
5. `vacuum-journal`: `journalctl --vacuum-time=D`
6. `check-files`: `pacman -Qk --color always` (relayed only, no `check` event here; the `check-files` verb's own `-Qk` runs with `--color never`)

The parts are independent, so a failing part doesn't stop the rest. Each
part's `progress` events carry `part`, and the part ends with a `done` event
(its own outcome), before the next part's first `progress`. Result: 1 if any
part failed, otherwise 3 if the orphan plan was stale, otherwise 0. The GUI
runs Full Cleanup's root steps as this one verb, so the whole cleanup shows
one admin dialog, and maps each part back to its own checklist row
(`internal/helperclient`). The order is part of the protocol: the client
checks it.

**Download leftovers.** pacman 7 downloads as its `DownloadUser` into a
`download-XXXXXX` folder (libalpm's `mkdtemp`) in the first writable cache
dir, and removes it only at the end of an actual download. So an
interrupted download leaves it behind, and so does every transaction that
had nothing to download: in libalpm 7.1, `download_files` (`sync.c`) makes
the folder before it looks for files to fetch and, finding none, only frees
its name. yay's `pacman -U` of a built AUR package, or a `-Syu` whose
packages were all cached, leaves one each, so Full Cleanup's own topgrade
leaves fresh ones on most runs. The `clean-uninstalled` part removes such a
folder, as root, only when all of this holds, and leaves everything else
alone:

- it is a direct child of a cache dir `pacman-conf CacheDir` names
  (`/usr/bin/pacman-conf`, pacman's own reading of `pacman.conf`; never a
  path from the caller), an absolute path, at most 16 of them;
- that cache dir, opened with `O_NOFOLLOW|O_DIRECTORY`, is owned by the
  helper's uid (root) and not group- or other-writable, so only root
  (pacman) can have made an entry in it;
- the name is exactly `download-` plus six ASCII letters or digits;
- the helper holds **pacman's own lock** while it removes: it creates
  `db.lck` in `pacman-conf DBPath` (default `/var/lib/pacman/db.lck`, see
  "The lock's path" below) exactly as libalpm does (`_alpm_handle_lock`:
  `open(O_WRONLY|O_CREAT|O_EXCL|O_CLOEXEC, 0000)`, nothing written in it),
  so no pacman that locks can run meanwhile; if the lock exists, pacman is
  running, nothing is touched and the lock is left as it is ("Left pacman's
  download folders alone: busy: pacman is running");
- under that lock, `fstatat(AT_SYMLINK_NOFOLLOW)` on the open cache dir
  shows a directory (not a symlink, not a file), unchanged for 2 minutes by
  the later of its mtime and its ctime (which, unlike mtime, no `touch` can set back);
- and `unlinkat(AT_REMOVEDIR)` on the open cache dir succeeds: rmdir
  semantics, so a folder that is not empty stays, and nothing is ever
  removed recursively.

*The lock and the 2-minute floor.* The hold is as short as it can be:
pacman-conf runs, and every cache dir is opened, checked and read, before
the lock; under it there are only `fstatat` and `unlinkat` on those open
dirs (no command, no output, since a write to the client's pipe can
block), and the lock is removed right after, on every path: a return, an
error or a panic (a deferred release). The release removes the path only
while it is still the file the helper made (same device and inode), so a
lock someone else made meanwhile stays. The helper catches and drops
`SIGINT`, `SIGTERM`, `SIGHUP`, `SIGQUIT` and the rest for its whole run
(see Hardening), so no signal a user or a dying GUI can send ends it inside
the hold. Left are `SIGKILL` and `SIGSTOP`, which can't be caught: root's, and the
user's too, since `pkexec` leaves the helper in archmaid-gui's user cgroup
(`cgroup.kill`, `cgroup.freeze`, `systemctl --user` stopping the scope, a
logout, systemd-oomd); also the OOM killer, the shutdown's final kill, a Go
runtime fatal error and a power loss, inside a window of microseconds per
folder. pacman, which holds the same lock for minutes, has the same
exposure, and so does `pacman -Sc` right after the tidy, for seconds. On an
NFS or FUSE cache dir, `fstatat` and `unlinkat` under the lock can block. A lock left that way is pacman's
usual stale `db.lck`: once no pacman is running, `sudo rm` it (the line
names the path). A failed release says so in its own line.

*The lock's path.* Every `db.lck` check (the start check of the verbs that
run pacman, the orphan removal's two rechecks, the tidy's busy test and its
hold) uses one path, `<pacman-conf DBPath>/db.lck`: `/usr/bin/pacman-conf
DBPath`, a fixed absolute argv, read once per run before the first check
that needs it. The answer must be one absolute path without control
characters; it goes through `filepath.Clean`. If pacman-conf fails or prints
anything else, the helper falls back to `/var/lib/pacman/db.lck` and prints
one plain line saying so. It does not refuse: the lock is a guard against a
pacman the helper can't see, not a precondition (pacman, paccache and the
removals take their own locks), so a broken pacman-conf shouldn't stop every
cleanup, and the fallback guards what the helper guarded before it read the
path. The tidy takes the lock only when the lock's folder is root's alone
(owned by root, not group- or other-writable); otherwise it skips with a
note ("not taking pacman's lock …"). A lock that already exists is still
honoured as busy there: a lock someone else made is still a possible
pacman.

The lock alone would allow dropping the age rule for every folder libalpm
makes inside a transaction (`download_files` runs in `alpm_trans_commit`,
which requires the lock). Not every folder is made that way, though:
`pacman -U <url>` fetches through `alpm_fetch_pkgurl` *before* its
`trans_init` (`src/pacman/upgrade.c`), so it makes its folder in the cache
with no `db.lck`; so does a libalpm user with another `DBPath` and the same
cache (`checkupdates -d` run as root), and pacman itself if run
with another `--dbpath` than `pacman.conf`'s. Such a folder is empty only between its
`mkdtemp` and the first `.part` file, which libalpm's built-in curl opens before the
transfer starts (`curl_add_payload`): milliseconds, or seconds if looking
up the download user is slow. With an `XferCommand` (`curl … -o %o`) the
file appears only when data arrives, so a lockless `pacman -U <url>` can
keep an empty folder for over 2 minutes. The 2-minute floor covers the
built-in case with a wide margin, and if it is ever beaten, one download fails before pacman changes
anything. A leftover from earlier in the same run is still removed, as soon
as it is 2 minutes old.

Each folder removed is one line of the part's output ("Removed …"), and so
is each candidate left alone, with the reason, so the red line `-Sc` still
prints for it has its explanation above it. A folder that isn't empty holds
an interrupted download's partial files, and its line says how to delete
it by hand: once no pacman is running (no `db.lck`), `sudo rm -r --
'<the folder>'`, printed only when the whole path is plain ASCII
`[A-Za-z0-9/._+-]` (the line is sanitized, so any other path would be
pasted as a different one; then the line just says to delete it by hand). None of this changes the part's result, which is `-Sc`'s.
The front ends' preview doesn't show the tidy: the step shows `pacman -Sc
--noconfirm`, and a dry run names the part.

Not done, a note for later: the cache dirs are opened by path, and only
the last component refuses a symlink (`O_NOFOLLOW`). `openat2` with
`RESOLVE_NO_SYMLINKS` would refuse one anywhere in the path; every parent
of a cache dir is root's to change anyway, so it isn't needed here.

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
DCS, APC, PM, SOS and the short ESC forms), with one exception: a `line`
event's `text` keeps well-formed SGR sequences (`ESC [`, parameters of
only digits, `;` and `:`, final `m`: colour and text style), so pacman's
`--color always` output keeps its colour; a cursor move, an erase, a
private CSI, a title or hyperlink is still removed there. A client must
still filter `text` itself (archmaid's keeps only SGR, without conceal,
and drops it under `NO_COLOR`; its logs keep none). C0/C1 control characters and DEL
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
| 3 | state changed since the user confirmed (orphan plan hash mismatch, or the pkg-install/pkg-remove plan, or the built files of pkg-install-local), pending updates, or an upgrade before an install, or `repair` has nothing it may reinstall because the repos have a different version, or reinstalling would pull in or upgrade other packages (update the system first) |
| 4 | refused: an unsafe or unreadable local package file (`unsafe-file`, `bad-package`), a replace/conflict with an installed package, a protected package in a removal, pacman refusing a removal, denylisted unit, a running `Also=` unit or trigger (or an enabled trigger left behind), not root, no `PKEXEC_UID`/`SUDO_UID`, wrong install path/ownership/mode, or a devhelper build run as root |
| 5 | busy: `/run/archmaid-helper.lock` is held, or pacman's `db.lck` (in `pacman-conf DBPath`, default `/var/lib/pacman/db.lck`) exists (checked only for verbs that run pacman/paccache mutations; `full-cleanup`'s download-leftovers tidy also takes `db.lck` itself for a moment, see "Download leftovers") |

## Hardening

In order, before anything else:

1. Capture `PKEXEC_UID` (else `SUDO_UID`), then clear the whole environment
   and set `PATH=/usr/bin LC_ALL=C TERM=dumb`. Every child again gets exactly
   that environment, so it is cleared twice. (`TERM` stays `dumb` although the
   output is coloured: pacman's colour comes from `--color`, and systemd's
   tools, whose output `disable-unit` parses, must stay plain; see Verbs, "Colour".) Also `umask 022`, `chdir /`, and
   every inherited fd above 2 is closed. The Go runtime's own netpoller fds
   (eventpoll/eventfd) can't be told apart from inherited ones of the same
   kind, so those are set close-on-exec instead of closed.
2. `SIGPIPE`, `SIGINT`, `SIGHUP`, `SIGTERM`, `SIGQUIT`, `SIGTSTP`, `SIGTTIN`
   and `SIGTTOU` are caught and dropped for the whole run, so the helper always
   finishes (it is never stopped either) and writes its `result` line. If the
   GUI dies, writes to stdout just fail with `EPIPE` and the helper keeps
   draining the child, so pacman is never killed mid-transaction. A Ctrl-C,
   Ctrl-\, Ctrl-Z or a closed terminal (`pkexec` from a shell) is dropped the
   same way. `SIGKILL` and `SIGSTOP` can't be caught, and not only root can send them: `pkexec` leaves the helper in archmaid-gui's user cgroup, so the user's `cgroup.kill`/`cgroup.freeze`, a logout or systemd-oomd reach it too. They are caught
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
3. Privilege, for every verb except the `plan-*` ones: euid must be 0;
   `PKEXEC_UID` or `SUDO_UID` must be a decimal uid; `/proc/self/exe` must be
   exactly `/usr/lib/archmaid/archmaid-helper`. That file and every parent up
   to `/` must be root-owned, not group- or other-writable, and not a symlink.
   The binary must be a regular file without setuid/setgid. The `plan-*` verbs
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
- Local (AUR) installs (`pkg-install-local`). The one verb that runs `pacman -U` on files from a user-writable place, so the rule is: **root reads a user path once, and trusts only what it has copied and hashed.** The user's home comes from `/etc/passwd` for `PKEXEC_UID`; the path to each file is fixed in shape (`<base>/out/<name>`, matched by strict patterns, so no `..`, absolute path or empty component can exist) and walked component by component with `openat(O_NOFOLLOW|O_DIRECTORY)`, each folder and the file checked with `fstat` on the fd that was opened (never on a name): no symlink anywhere, every folder from the home down the user's and not group- or other-writable, the folders above the home root's or the user's and not writable by others, the file regular, the user's, not group/other-writable, 1 byte to 2 GiB (FIFOs are opened non-blocking and refused). The bytes go through one copy into a root-owned 0700 folder under `/var/cache/archmaid/staging` (on disk, not `/run`: a package can be 2 GiB and `/run` is RAM) while being hashed; swapping the path afterwards changes nothing, because root holds the open file, and editing the file in place shows up in the hash. The hash `H` the user confirmed after the build (names, sizes and contents, `PkgLocalHash`) must equal the hash of the staged copies, which are read back from the root-only folder to catch a faulty copy. Only then is anything parsed, and by pacman itself (`pacman -Qip`, the parser that will install it: no second tar reader to disagree with it), whose output is sanitized into the log. `.PKGINFO` must say the package, version and architecture of the file name; protected packages (and packages providing a protected name) and anything that replaces or conflicts with an installed package are refused; pending updates block it as for `pkg-install`. pacman then runs on the staged copies only, under the helper lock, between two `db.lck` checks, with no `--overwrite`, `-dd` or `--noscriptlet`. What it does not do: it cannot tell a good package from a malicious one. A package installs files anywhere and runs its scriptlets as root, unsigned: the review before the build and the user's consent are the protection, the polkit action is admin every time and says so, and the log shows `.PKGINFO`, whether there is an `.INSTALL` and the file list.
- Package install and remove (`pkg-install`, `pkg-remove`). The caller picks
  names, never options: names are validated, an install target must exist in a
  sync repo, a remove target must be installed, a removal never touches the
  protected list, and the root verb takes the hash of the plan the user
  confirmed and recomputes it under the lock (stale: exit 3, nothing run). An
  install is refused during pending updates, for an upgrade, and when a package
  replaces or conflicts with an installed one, so `--noconfirm` can never answer
  "replace?" or "remove?" for the user. Both actions are admin, never keep: a
  package's scriptlets run as root, so a silent re-run would be root for the
  whole session. The polkit prompt cannot show names; archmaid's own confirm is
  the consent, the prompt the authentication.
- Environment and fd attacks on the root process (`LD_PRELOAD`, `PATH`,
  `GCONV_PATH`, `IFS`, leaked fds). Control and escape sequences in output
  reaching a terminal or GUI.
- A `pkexec` or `pkttyagent` earlier on the user's `PATH`. archmaid-gui
  starts `/usr/bin/pkexec` and `/usr/bin/pkttyagent` by absolute path
  (`internal/sysbin`), never from `PATH`, so neither the password polkit
  asks for nor the helper call can be routed through a stand-in.
- A helper copied or symlinked elsewhere, or installed with weak permissions.
  It refuses to run as root unless it is the root-owned file at the fixed
  path.

**Does not protect against**

- Root, or anyone who can already write to `/usr`. The admin password itself.
  A compromised pacman, paccache, systemd or mirror.
- What a legitimate admin-approved action does. `pkg-install-local` installs, as root, whatever the user built and confirmed, unsigned, scriptlets included. `pkg-install` installs, as root, whatever the configured repos serve (scriptlets and hooks included), with pacman's own signature checks and provider choice (the first provider, under `--noconfirm`); `pkg-remove` removes what the user confirmed. `repair` reinstalls whatever
  the repos serve at the installed version, and `disable-unit` can still
  disable a non-denylisted unit the user depends on. (System updates are not a
  helper verb: topgrade is archmaid's only update path, in the TUI and the
  GUI alike.)
- Denial of service by holding the lock or `db.lck`. (A helper killed with `SIGKILL` inside its download-leftovers tidy leaves `db.lck` behind, like a killed pacman; see "Download leftovers".)

## Development

From the repo root (the module root):

```sh
go test -race ./cmd/archmaid-helper/...                             # the helper's tests
make gate                                                           # the whole gate (scripts/check.sh), helper included
go build -tags devhelper -o /tmp/ah ./cmd/archmaid-helper && /tmp/ah plan-orphans   # dev build, as yourself
```

All seams are on `helper.Options`: `Runner`, `Euid`, `CallerUID`, `Exe`,
`Stat`, `LockPath`, `PacmanDBLock` and `Dev`, and, unexported, the
download-leftovers tidy's clock and a probe called under pacman's lock. The tests use a fake runner that
records argv and never executes anything. The only real processes in the tests
are `/bin/sh`, `/usr/bin/env`, `/usr/bin/cat` and the test binary re-executed,
which exercise the exec runner, the process hardening and the signal handling.

A **devhelper build** (`-tags devhelper`) runs from any path as a normal user
and skips the install check. It **refuses to run as root**, so it can never
stand in for the real helper. Never package it.

Live polkit and faillock behaviour can't be unit-tested. See
`docs/TESTING.md` → "Manual verification".
