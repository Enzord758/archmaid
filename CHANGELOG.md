# Changelog

All notable changes to archmaid are recorded here, in the style of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Each bullet ends with
the commit that made the change; the range is `5585358..refactor/p0` (tip 8540b78).

## [Unreleased]

### Read this first

- `archmaid --help` now prints help and exits 0, and unknown options now exit 2 instead of being ignored (13eb21a).
- History rotation now keeps 5 runs including the current one, instead of 5 old ones plus the current one (420733b, 13eb21a).

### Security

- Output from topgrade, yay and makepkg is stripped of terminal escapes (title changes, OSC 52 clipboard writes, cursor moves) before it reaches the pane and the logs; older logs are shown plain too (5fa1b8b).
- The sanitizer also drops bidi overrides, zero-width marks, SGR conceal, U+2028/2029 and Hangul fillers (9809765, 9a9cda0).
- The AUR audit fails closed: an unreadable or corrupt `aur-maintainers.json` is a high finding instead of a silent "first audit", and the bad file is kept (4265bb7).
- The AUR snapshot is written atomically (0600 temp file, fsync, rename) under a lock (4265bb7).
- AUR RPC replies are capped at 8 MiB, results for packages nobody asked about are dropped, and maintainer names are sanitized before display and logging (fb691b5, 9809765).
- A newly added AUR co-maintainer now triggers the gate, not just a changed maintainer (0ed25f2).
- `SUDO_ASKPASS` goes only to steps that use sudo, lives in a private 0700 directory under `XDG_RUNTIME_DIR` (askpass is off without it), and sudo steps run `sudo -n` (f03acdf).
- The askpass popup answers only the running sudo step, after checking the connecting process (uid, start time, executable, sudo parent, descendant of the step), and names the step and sudo's caller (09c192e, 48fa52e).
- A password typed into a mid-run askpass popup is checked with `su` before it reaches sudo, so two typos no longer burn the pam_faillock budget (deny=3) (d71ef0f).
- The `su` check no longer starts the login shell (`su -s /usr/bin/true`), so `~/.zshenv` is not read (d71ef0f).
- Logs are written 0600 in 0700 directories; looser permissions from older versions are tightened on the next write (c2bbb80).
- Package and unit names are passed after `--`, so a name starting with `-` cannot become a pacman or systemctl flag (dcbcef1).
- "Delete old downloads" refuses `$HOME`, `/`, any ancestor of `$HOME`, and the passwd home even when `$HOME` points elsewhere or is unusable (ab7ceec, ef94e67).
- "Delete old downloads" deletes through `os.Root`, so a swapped-in symlink cannot escape the folder (a7a8478).
- "Delete old downloads" never crosses a mount point (USB disk, sshfs, bind mount) below the folder, and re-checks every node's age as it removes it (002567f, ef94e67).
- `--downloads <value>` starting with `-` is an error (exit 2) instead of silently swallowing the next flag, e.g. `--dryrun` (ef94e67).
- sudo is refused when it could prompt on the terminal: `proc` only starts a sudo whose first argument is `-n` or `-A`, and a sudo without an explicit environment does not inherit `SUDO_ASKPASS` (0e1be6e).
- The askpass popup answers only a request that passed `Verify`; any other request is refused before `su` runs, and only the askpass server can create a request (1832e53).
- Passwords containing a line break are refused by `privilege` itself, and su's lockout line is sanitized and clipped (c423d4d).
- Safe defaults: `sanitize` defaults to plain (SGR only on request), `logstore` writes only its four categories and refuses a missing or relative state dir, AUR error text is sanitized and capped, an unparsable euid reads as unknown rather than root (c423d4d).
- The downloads home guard always includes the process's own `$HOME` and the passwd home, even when the caller passes none; with no home known, `--downloads` is refused (7de100c).
- The AUR audit records unacknowledged high findings in `audit-state.json` for the widget (f5e4def).

### Fixed

- Aborting a running step leaked three goroutines and a pipe per step (15 after 5 aborts); they are now released (e46021c).
- Every finished action or editor return started another permanent 4 s status loop, and the sudo keep-alive doubled the same way; there is now one of each (c974117).
- Opening View log no longer creates an empty run directory that pushed real history out of the kept runs (c2bbb80).
- Declining the AUR gate no longer "spends" the alarm: the snapshot is saved only when you accept, so the same change gates again next time (b258af4).
- Late output from an aborted step can no longer appear in, or finish, the next step (caf81b3).
- An aborted AUR audit now stops at once instead of running on for up to 15 s per request (ff6a515).
- "Delete old downloads" deleted the Downloads folder itself once it was a month old; the folder is never a candidate now (a7a8478).
- Downloads age uses ctime, so a fresh download that `wget`, `curl -R` or `unzip` stamped with an old mtime is no longer deleted (a7a8478).
- The downloads folder is found when it is `~/Download` or `XDG_DOWNLOAD_DIR` is `$HOME/` (ab7ceec).
- An abort during the downloads deletion is still written to `cleanup.log` (62b7e2c).
- Services discovery no longer mistakes the leading "●" of a not-loaded unit for a unit name (bdf4027).
- A line over 1 MiB no longer leaves the child blocked on a full pipe (e46021c).
- A step whose child left a background process holding its output pipe no longer stays "running" after it exits; the pipe is read for at most 2 s more (d7c3eb4).
- Orphans are found after topgrade, so ones the upgrade leaves behind are included and the count in the confirm is current; Enter no longer freezes 130-200 ms (eeba326).
- Repair's scan timeout no longer reads as the audit's 20 s deadline, and notices honour the output tail (32c01a8).
- The helper's `disable-unit` no longer refuses bluetooth or avahi-daemon because of their D-Bus aliases (614f49b).
- The helper's `plan-orphans` accepts orphans with uppercase names (acad283).
- `archmaid --help` used to start the TUI and, through the startup rotation, delete the oldest real run folder. Nothing is deleted at startup now; `TestStartupDeletesNothing` guards it (13eb21a).
- The history rotation never deletes the run folder of the run that is writing, even if the clock was set back (420733b).
- The log store is safe to use from any goroutine, and tests wait for every audit prefetch they start (d4e9935, 6c7918f).

### Changed

- `archmaid --help` (or `-h`) now prints the usage and exits 0. Before, it started the full TUI. An unknown option or a stray argument now exits 2 with a message instead of being ignored (13eb21a).
- History rotation now keeps 5 runs **including the current one**: you see 5 run folders, not 5 old ones plus the current one (6 before). The rotation happens when a real run writes its first log entry, not at every start (420733b, 13eb21a).
- A dry run, `--help`, a bad option and a start that ends at the menu no longer delete anything from the run history (13eb21a).
- A run in which sudo gave up part-way (three wrong passwords) is now recorded as `incomplete`, not `ok`, with `steps_refused` in `last-run.json` (additive, still `"v":1`) (96911ea).
- An aborted run is recorded with the running step as Aborted and the later steps as skipped, so `steps_skipped` is no longer 0 (6c82bd5).
- When a step result is refused, the step it was meant to gate (for example topgrade behind an AUR finding) still asks for a confirm instead of running unasked, and a refused result no longer logs a false "aborted" (61771a2).
- The AUR audit never saves a snapshot from a failed, cancelled or empty audit, which used to wipe the baseline (79f5bca).
- The root helper no longer has the `upgrade-repos` verb or its polkit action; topgrade stays the only update path (f4bb885).
- Full Cleanup cleans `/tmp` and `/var/tmp` with `sudo systemd-tmpfiles --clean`, so your tmpfiles.d rules decide instead of a blanket 2-day delete; the step reads "Temp files cleaned per tmpfiles.d rules" (a5988bd).
- "Delete old downloads" picks the folder from `--downloads <dir>` (or `=<dir>`), `$ARCHMAID_DOWNLOADS`, `XDG_DOWNLOAD_DIR`, `~/Downloads`, `~/Download`; the notice and confirm name it, and a bad `--downloads` is reported, not skipped (ab7ceec).
- "Delete old downloads" lists the entries it will delete (ten, then "...and N more"), writes what was deleted and kept to `cleanup.log`, and a dry run reports `would delete: <name>` (a7a8478).
- The AUR audit gate also runs before standalone "Run topgrade", with the same confirm (default No) (33870bc).
- The AUR gate commits on accept; in the menu audit, high findings keep their old maintainer until you press `a` on the report (b258af4).
- Repair scans first as your user and only asks for a password if something looks broken; if nothing is, it ends with "No broken packages found." without touching sudo, and a root recheck drops false positives such as `amd-ucode` under the 0700 `/boot` (ae081b1).
- Repair no longer probes the sudo ticket twice (aa55868).
- Full Cleanup and "Run topgrade" start the AUR lookup while the password prompt is open (707be81).
- The status line shows available memory as "RAM avail:" instead of `MemFree` (d73c9e5).
- The integrity check uses `pacman -Qkq`, so only broken files are printed (a58ed79).
- The output pane shows the newest 2000 lines and no longer jumps to the bottom when you have scrolled up; the full output still goes to the log (1f2b703).
- Building needs Go 1.25 for `os.Root` (a7a8478).
- The install script builds with `-trimpath -ldflags='-s -w'` (50525fd).

### Performance

- `BenchmarkViewRunning` guards the running-screen render at a fixed 600 us; `BenchmarkActivateFullCleanup` (~5 us) guards a 1 ms limit so choosing Full Cleanup never runs pacman on the UI thread again (869bfd0, 7c712bc, 9332d02).
- Status line (every 4 s): nine forked processes (~6 ms each) replaced by native reads, about 28 us and no children (261fd0f).
- Streamed output: 5k lines 976 ms -> 5.4 ms (1.79 GB -> 15 MB allocated); 20k lines 5.16 s -> 21 ms (9.7 GB -> 63 MB) (1f2b703).
- Repair scan: ~850 ms for a root `pacman -Qk` over 1422 packages -> 316 ms as user with 16 workers, plus ~50 ms for the root recheck (ae081b1).
- Services discovery: 430 ms for `systemctl list-unit-files` -> 3 ms for `list-units` plus one `is-enabled` (bdf4027).
- Stripping a 5k-line coloured log takes 0.29 ms (5fa1b8b).
- Binary size 12.4 MB -> 8.3 MB (50525fd).
- The orphan queries (130-200 ms) no longer run inside Update when choosing Full Cleanup (eeba326).

### Added

- `archmaid-helper`: a root-side verb engine (`check-files`, `paccache`, `paccache-wipe`, `plan-orphans`, `remove-orphans`, `repair`, `vacuum-journal`, `clean-tmp`, `disable-unit`, `full-cleanup`) with JSON-lines v1 output, exit codes 0-5, an install-location check and a hardened process; its tests need no root. The TUI does not call it yet (a3a30a0, dcf6afc, 5eb3fbb).
- Helper `disable-unit` refuses firewall, MAC/audit, NVIDIA sleep, sleep-target, bus, serial-getty and power units (firewalld, nftables, apparmor, auditd, tlp, upower ...), checking names, D-Bus aliases and `[Install]` (Alias, WantedBy, RequiredBy, UpheldBy, Also) the way systemd parses it (88412f7, 614f49b, 35ea7ba).
- Helper `disable-unit` also refuses when an `Also=` companion is still active or a running or enabled socket/timer/path would restart the unit (54f4b66).
- Helper `repair` reinstalls a package only at its installed version and only when the whole transaction is exactly the broken set, so it can never cause a partial upgrade (b0ad2cd, 34db02f).
- Helper children run in their own session, and the helper catches (does not ignore) terminal signals, so it always writes its result line while pacman keeps default signal handling (f084504, 799403f; replaces e8e863e).
- polkit policy `org.archmaid.helper.policy`, one action per root verb: `check-files`, `paccache`, `vacuum-journal`, `clean-tmp` use `auth_admin_keep`; `repair`, `paccache-wipe`, `remove-orphans`, `disable-unit`, `full-cleanup` use `auth_admin`; never for inactive or remote sessions. The README documents what `auth_admin_keep` allows (1f8d70d, 52ae9d3).
- `packaging/arch/PKGBUILD` (`archmaid-git`) installing `/usr/bin/archmaid`, `/usr/lib/archmaid/archmaid-helper` (never setuid), the policy, an icon and `archmaid.desktop`, plus a packaging README (397c321, cb3ab76).
- `last-run.json` in the state dir records how the last run ended (ok, failed, aborted, cancelled, incomplete) and its action; a dry run writes its own copy (fff5b1d).
- A transcript harness and 31 characterization transcripts of every menu action, recorded end to end against shims (8bfc04e, 6760abf, d53580b).
- Rendered-frame goldens for the summary, popups, password prompt, log picker, audit report and askpass popup (efbc4f1, 09c192e).
- Askpass tests (coverage 0% -> ~98% at the time) (75bce79).
- Four end-to-end VHS tapes (clear-cache, services, orphans, abort) run against shimmed commands (f2a6f49).
- `scripts/check.sh` gate: race tests three times, vet, gofmt, a layering check and coverage floors (89.1% total, 80% for `askpass.go`) (e58563a, 91fb0bf).
- A root `Makefile` (`make all`, `gate`, `e2e`, `perf`) and a GitHub Actions workflow running them, with the core job as an unprivileged user (530845d, 7d42232, 3df53de).
- Perf guard: `ARCHMAID_PERF_GUARD=1` (or `make perf`) fails the gate if the stream or status benchmarks exceed 3x their baseline (60c5761).
- `TestNoSetsid` guards that TUI children share archmaid's session, which sudo's ticket depends on (48265e5).
- `packaging/polkit/LIVE-TEST-AGENT.md` and a TESTING.md plan for the manual polkit/pam_faillock check (2161d33, 283772c).
- `docs/polkit-live-test.md`: results of the live polkit/faillock run: one failure per wrong password and per cancelled dialog, pkexec exits 127 for every refusal, and an action without `exec.argv1` can capture calls meant for the verb actions (ours has none; `TestPolicyMatchesVerbs` enforces it) (15d4d4a).
- `docs/dms-widget.md`: design of the DMS bar widget, with the owner's decisions recorded (7106f85, b321ec9).
- DMS bar widget `archmaid` (`packaging/dms/archmaid/`): a pill with the pending-update count, tinted red for an unacknowledged high AUR finding or a failed run and orange for incomplete, aborted or cancelled runs or a last run older than 14 days; a popout with the details and Run Full Cleanup / Log folder buttons. It only reads the state files (f1655bc).
- User timer `archmaid-updates-check.timer` (no root): runs `checkupdates` 3 min after login and hourly and writes `updates-cache.json` in the state dir. The package installs the widget to `/etc/xdg/quickshell/dms-plugins/archmaid/`, the units to `/usr/lib/systemd/user/` (41b3c9c, dc856e3). Enable steps are in `docs/dms-widget.md` (e22c4f8).
- `privilege.FaillockStatus(user, now)` reads pam_faillock's tally and config (read-only, runs nothing) so a front end can refuse to prompt a locked account and warn before the last allowed attempt (d718dd0).
- `internal/engine`: the frozen engine API (Session, Input, Output, Event, Effect, Driver, `Secret` that never prints) for the TUI and the GUI; the Session panics until E1. `internal/engine/stub` is a scripted Driver with a ready-made Full Cleanup run (b295907).
- `actions.Env.Validate` names every required field left nil; the TUI refuses to start on an incomplete Env, and `Report.Accept` replaces an untyped `Audit` (1dc3241).
- CLAUDE.md: rules for agents (never start archmaid against the real environment, no privileged commands, write only in the worktree, bound every background process, hand in through the gate) (24fc249, 0a4bb37, 8540b78).
- `internal/testenv` puts traps for `su`, `sudo`, `pkexec`, `runuser`, `faillock` and `pkttyagent` first on PATH; a test that reaches one fails its package. `TestEveryTestPackageRunsUnderTestenv` and `TestNoAbsolutePrivilegedExec` keep new packages wired (f932158).
- The helper's verbs and the `steps.Verb*` constants are checked against each other, and any step after hyprpm that needs root fails the tests (4e19c69).
- `scripts/state-guard.sh`: `check.sh` and `e2e/run.sh` fail if the real `~/.local/state/archmaid` changes; the e2e harness traps pkexec, runuser and faillock and runs the binary under a scratch `XDG_STATE_HOME` (b19256f, 9613503).
- `make bench` / `scripts/bench-compare.sh` and a CI `bench` job comparing base and HEAD with benchstat; documented in `docs/TESTING.md` (5135b92, 6a4794c, 67dd62e).

### Internal

- P1 layout: the TUI moves to `cmd/archmaid`, one Go module at the repo root (the helper's nested module is gone), TUI docs move to `docs/` (7ceabe9, 630e838, ba84e1e, b2f381d).
- P2 packages, no behaviour change: `internal/sanitize`, `internal/logstore`, `internal/sysstat`, `internal/downloads`, `internal/proc` (children: start, stream, stop, query, /proc reads) (61fcb32, 3c893b2, 40dec88, 8a1f649, 52a15be, 7e7f401, f90604e).
- Packages under `internal/` may not import charmbracelet; the gate enforces it (91fb0bf).
- Stream messages carry their step sequence; a `tickFn` seam lets tests drop timers (caf81b3, 8bfc04e).
- The AUR audit runs under one cancellable 20 s deadline and reads all its settings on the model's goroutine, which fixes data races under `-race` (ff6a515, 8316033).
- The audit fetch is a model-owned goroutine whose result the step awaits (707be81).
- Snapshot format is now `{"maintainer": ..., "comaintainers": [...]}`; the old bare-string format still loads without alarms (0ed25f2).
- Test infra: tests never touch the real home directory or state dir, and every package that can reach HOME runs under `testenv` (d5f1d56, d24b442, fff5b1d); askpass peer tests no longer depend on the real uid (5986bc9).
- P3 engine work: menu and queues are data in `internal/actions` and `internal/steps`; the transcript driver sits behind a `Target` interface; the AUR audit lives in `internal/audit`; sudo and askpass in `internal/privilege` (3ef4d38, 167f044, 2ffbeab, 7908ae7, 9332d02).
- CI uses Node 24 versions of its actions (vhs-action stays on node20 upstream) (521bfe6); the perf target's comment is corrected (8560d63); stale helper README notes are refreshed (74f72fa).

### Notes for the maintainer

- The live polkit/faillock test (`packaging/polkit/LIVE-TEST-AGENT.md`) has been run once, against a scratch user (see `docs/polkit-live-test.md`); still re-check that `auth_admin_keep` is scoped per process inside a real login session (tty3).
- Try the package with `makepkg -si` in `packaging/arch/` and run `namcap` on the result; the PKGBUILD has not been through either.
- Pick a license: the repo has none, the PKGBUILD has `LicenseRef-unknown` and a TODO to install `LICENSE`.
- The first audit after upgrading rewrites `aur-maintainers.json` in the new format.
- Still to check by you: `auth_admin_keep` scope in a real login session (above), the bench CI job (it has not run on GitHub yet) and the widget in the live bar (it has only been loaded in a scratch quickshell).
- Enable the widget after installing the package: `systemctl --user enable --now archmaid-updates-check.timer`, then enable "archmaid" in DMS Settings, Plugins and add it in Dank Bar (`docs/dms-widget.md`).
