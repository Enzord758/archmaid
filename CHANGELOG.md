# Changelog

All notable changes to archmaid are recorded here, in the style of
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Each bullet ends with
the commit that made the change; the range is `5585358..v2.0.0` (554 commits, tip 4fdc62b).

## [Unreleased]

### Changed

- **"Delete entire package cache" is a menu action of its own, no longer a step of Full Cleanup** (owner's decision, 2026-10-18). Full Cleanup already trims the cache to the 3 newest versions, so it has one step and one confirm fewer: 8 steps in the TUI (9 with hyprpm), and in the GUI the root call covers 5 steps, not 6. The new action is in the TUI menu and the GUI Overview (not in the bar widget). It first runs `paccache -dk0` as you, a read-only dry run, and its confirm (default No) shows how much it frees and how many package files, with the warning that you can't reinstall or downgrade those packages without internet; only a yes runs `paccache -rk0` as root (sudo in the TUI, the existing `paccache-wipe` helper verb through polkit in the GUI, so the polkit policy is unchanged). An empty cache or a failing dry run ends in a notice, recorded under the new log category "Package cache check".
- `archmaid-helper full-cleanup` no longer takes `--wipe` (it exits 2 as for any unknown flag); `paccache-wipe` stays a verb of its own.

### Added

- **A run report at the end of a cleanup (#188).** The TUI's summary screen now shows what the run changed: packages upgraded (with how many came from the AUR), installed and removed, the space each cleaning step freed and the total, what the package cache still holds (and that "Delete entire package cache" in the menu frees it), and a "Needs your attention" list: a restart (the running kernel was replaced, or systemd, mesa or the NVIDIA driver were upgraded), new `.pacnew`/`.pacsave` files, a high AUR audit finding. Package changes come from `/var/log/pacman.log` since the run started (another pacman run at the same time counts too); sizes are measured as the user before and after each step, and a folder it can't read says "unknown". A new package `internal/report` makes it, the Driver adds it (`engine.RunReport`, opt-in through `Deps.RunReport`; the TUI sets it, the GUI does not yet). AUR updates held back by the 72 h yay cooldown are not shown: no reliable source after the run (`docs/HANDOVER.md`, "The run report").
- `last-run.json` gets a `summary` object (`upgraded`, `aur`, `freed_bytes`, `attention`) when the run has something to report; the schema stays `v:1`, older files simply lack it. `state.js` reads it; the widget's QML does not use it yet.
- `testenv` also traps `pacman-conf` and points `ARCHMAID_PACMAN_LOG` and `ARCHMAID_MODULES_DIR` at nothing, so no test reads the machine's pacman log.

## [2.0.0] - 2026-10-18

The refactor/p0 work, merged into `main` (fast-forward, `main` = `refactor/p0` = 4fdc62b). The package version stays `rN.sha` from `pkgver()`; the tag `v2.0.0` marks this release.

### Read this first

- **The TUI is new underneath.** It now runs on the engine and the real Driver, and the old queue logic is deleted. It behaves the same (all 31 transcript goldens, the view goldens and the 9 e2e checks are identical), and it has been run live, by the owner and in the test VM (`docs/first-live-run.md`) (4a2fb23, 1fe2148, 7652f35).
- **Install both packages from `main`, together:** `archmaid-git` (the TUI, helper, polkit policy, widget and updates timer; a complete install on its own) and `archmaid-gui-git` (the GUI, which needs the exact `archmaid-git` of the same build). Build and install: README → Install, `packaging/arch/README.md`. Both are installed on the owner's machine.
- **Only if you still have an old `install.sh` build:** remove `~/.local/bin/archmaid` with `~/archmaid/install.sh --uninstall && hash -r` (README → First run). It comes first on `PATH`, prunes run folders at every start and rewrites the new AUR snapshot in the old format. A fresh install needs nothing of this.
- **One password per GUI run is in this release** (`internal/privilege/kept.go`, `gui/internal/polkitagent`): the first password you give is kept for the run and answers the later sudo and polkit prompts; the dialog and the Proceed sheet say so, or say why it is not kept. Under polkit, Full Cleanup's root steps are one `full-cleanup` helper call with one dialog.
- **The GUI (`archmaid-gui`) has been run live**, on the owner's desktop and in the test VM. Only the GUI shows in the app launcher; the TUI has no desktop entry and no icon. The DMS widget opens the GUI when it is installed.
- After updating, restart the widget's timer: `systemctl --user restart archmaid-updates-check.timer`. On a first install enable it (`systemctl --user enable --now archmaid-updates-check.timer`) and add the widget in DMS (`docs/dms-widget.md`).
- If the engine stops unexpectedly (an internal panic), the TUI now shows "archmaid's engine stopped unexpectedly: ..." and any key quits; the full reports go to stderr. Ctrl-C always gets you out, also from a password check that never answers (5de7e0c, 73b22cd).
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

- The DMS widget's pending-updates check now counts AUR updates too and runs every 30 min (was hourly): `archmaid-updates-check` runs `yay -Qua` (as the user, no sudo, stdin from /dev/null, 120 s timeout) next to `checkupdates` and writes `updates-cache.json` schema v2 (`count`, `repo`, `aur`, `aur_held`; `aur` is null when yay is missing or fails). AUR updates younger than 72 h (the yay cooldown hook, `audit.Cooldown`) are left out of the count and shown as held in the popout ("9 repo · 3 AUR (2 held, <72h)"). The widget still reads v1 files. Restart the timer after updating: `systemctl --user restart archmaid-updates-check.timer`.
- GUI: the six Overview action cells now light up in the accent colour on hover (a little stronger while pressed, and on keyboard focus), so they read as buttons; the last-run cell and the sidebar are unchanged.
- Packaging: only the GUI shows in the app launcher. `archmaid.desktop` (the TUI) now has `NoDisplay=true`; the `archmaid` command still starts the TUI.
- Packaging: the TUI now has no desktop entry at all (it was hidden before). The package no longer installs `archmaid.desktop`, `install.sh` no longer writes one and removes a stale one of its own (on install and `--uninstall`), and `ARCHMAID_TERMINAL` is gone. Only the GUI has an entry; run `archmaid` from a shell. `scripts/check.sh` guards it.
- Packaging: the TUI has no icon either. Nothing used the icon name `archmaid`, so the package and `install.sh` no longer install `apps/archmaid.svg` (the GUI keeps its app-id copy); `install.sh` removes an older copy on install and `--uninstall`, only if it is a regular file identical to `packaging/archmaid.svg`. `scripts/check.sh` guards it.
- Docs: the GUI speed budgets (first frame, idle RSS, throughput, zero drops) are measured by hand on the owner's desktop with `ARCHMAID_GUI_PERF=1`, CI checks correctness only; the merge checklist no longer counts "four jobs" (core and gui come in pairs, one of each runs).
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

- `archmaid-helper`: a root-side verb engine (`check-files`, `paccache`, `paccache-wipe`, `plan-orphans`, `remove-orphans`, `repair`, `vacuum-journal`, `clean-tmp`, `disable-unit`, `full-cleanup`) with JSON-lines v1 output, exit codes 0-5, an install-location check and a hardened process; its tests need no root. The TUI and the GUI call it through polkit and `helperclient` as of the later entries (a3a30a0, dcf6afc, 5eb3fbb).
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
- `internal/engine`: the frozen engine API (Session, Input, Output, Event, Effect, Driver, `Secret` that never prints) for the TUI and the GUI; the Session was only an API at this point (it is complete as of the later entries). `internal/engine/stub` is a scripted Driver with a ready-made Full Cleanup run (b295907).
- `actions.Env.Validate` names every required field left nil; the TUI refuses to start on an incomplete Env, and `Report.Accept` replaces an untyped `Audit` (1dc3241).
- CLAUDE.md: rules for agents (never start archmaid against the real environment, no privileged commands, write only in the worktree, bound every background process, hand in through the gate) (24fc249, 0a4bb37, 8540b78).
- `internal/testenv` puts traps for `su`, `sudo`, `pkexec`, `runuser`, `faillock` and `pkttyagent` first on PATH; a test that reaches one fails its package. `TestEveryTestPackageRunsUnderTestenv` and `TestNoAbsolutePrivilegedExec` keep new packages wired (f932158).
- The helper's verbs and the `steps.Verb*` constants are checked against each other, and any step after hyprpm that needs root fails the tests (4e19c69).
- `scripts/state-guard.sh`: `check.sh` and `e2e/run.sh` fail if the real `~/.local/state/archmaid` changes; the e2e harness traps pkexec, runuser and faillock and runs the binary under a scratch `XDG_STATE_HOME` (b19256f, 9613503).
- `make bench` / `scripts/bench-compare.sh` and a CI `bench` job comparing base and HEAD with benchstat; documented in `docs/TESTING.md` (5135b92, 6a4794c, 67dd62e).

### Added (since 8540b78)

- The engine is complete: `engine.Session` runs queues, authentication (password, faillock, polkit), mid-run askpass for the running sudo step only, Abort from a step, a prompt or a wait, discovery pick lists and reports, last-run records and all 31 scenarios; confirms are labelled by what they do and the gate confirm previews its plan (faaa289, 63417ba, beedf24, 2bbd4e7, 042a2ec, 956606a, 71db9a3, d9a55e0).
- `internal/driver`: the real `engine.Driver` around a Session (sudo path), used by the TUI and the GUI (26a4c91).
- `helperclient`: runs a helper step through `pkexec` and maps its JSON lines to engine Inputs (af11008, 8d036dc). Under polkit, Repair rechecks its candidates through the helper's `check-files`, which now takes package names and ends in a check event (bf97a0a, 916a88d, b732a88, 771310c).
- Under polkit, orphans are removed from the plan the user saw: the step carries the plan's hash and `remove-orphans` is run from it, and the client's plan is tied to the helper's own (a508c72, c8452ec, 2a71f74, 3b647b9).
- `archmaid-gui`, a GTK4/libadwaita front end in `gui/`: run page, decision dialogs for every prompt kind, overview, logs and a Settings page (downloads folder, dry run), output in the dank16 colours, steps an abort left shown as not run, and "Waiting for authorisation..." on polkit steps. It runs on the real Driver with polkit and its own askpass relay; `--stub` keeps the scripted stub. It renders with cairo unless `GSK_RENDERER` is set (5e5fa7a, fb01c7b, b5f40e6, 8735472, 258b789, 1bd7257, 2e5d39a, bf9dad0, 67dc330, 4d5ba8c).
- Split packaging: `archmaid-git` (TUI, helper, policy, widget, timer; no GUI toolkit) and `archmaid-gui-git` (GUI, desktop entry, AppStream data, icon; depends on gtk4, libadwaita, libgirepository and the same archmaid-git). The DMS widget opens `archmaid-gui` when it is on PATH and falls back to the TUI (afc0991, 4a1ca95).
- Test infrastructure: the contract checker is stricter (AuthWaiting before its AuthResolved, plan changes after their step's StepDone, every Session Output checked as it comes); `sessionTarget` and `driverTarget` replay every scenario through the Session and the real Driver; an askpass test goes through the TUI's real socket into the real Driver; `e2e` has 9 checks (tapes for repair, downloads, the declined gate and `--help`/bad flag were added); the gate keeps its full race-test output when it fails; the headless GUI harness and a `gui` CI job (9777208, 1660c04, 880a299, d4c9705, 5cef060, 20e36d0, f422a39, 3bc1119, 0fc75f2, 392a05e, 563d046).
- `docs/polkit-keep-session-check.md`: a live check of `auth_admin_keep` inside a real session, with an optional part on a pkexec killed in the dialog (e1125c5, 8fcd6da).

### Changed (since 8540b78)

- `cmd/archmaid` is a thin main (command line, askpass relay, building the Session and Driver); the TUI lives in `internal/tui`, with its goldens. No packages under `internal/` or `cmd/` may import charmbracelet directly (7652f35).
- The TUI's model is derived from engine Events and no longer runs actions itself; behaviour goes into the engine, never back into `update.go` (65ac47c, 4a2fb23, e5e1e82).
- The menu audit stops with the Driver: quitting during "Audit AUR packages" no longer waits up to 20 s or saves a snapshot afterwards (629d448).
- Under polkit, a missing helper reads "archmaid-helper is not installed at <path>" instead of looking like a refused password (ea97314).
- `proc` strips an inherited `SUDO_ASKPASS` from every child without an explicit environment, and wrapped or combined-flag sudo is held to the same check, dry run too (534fa34, a1979d2).
- Engine `Options.Validate` checks the Env the way `New` will set it up (81f458d).
- The benchmarks changed shape with the move to the engine: the perf-guard baselines need re-measuring (see the notes below).

### Security (since 8540b78)

- The root helper no longer lets a started helper be killed or cut off by the client: after its start line, `helperclient` never kills pkexec or closes its output, keeps the real result and reports "stopped, but the root helper had already started and finished" (47db488).
- The helper and its client refuse unit names starting with `-` or `.`, and a test ties the two name checks together (5cb4af1, 3b97572).
- `remove-orphans` checks that pacman is idle right before the orphan recompute and again before `pacman -Rns`, and skips the removal (exit 5) otherwise (fbe2da5).
- Askpass requests are verified inside the real Driver (peer uid, start time, running sudo step) and are tested through the real socket; the password appears in no Event, report or log (f422a39).
- The TUI stops its askpass server on an error exit, so the socket and wrapper are not left behind (a51e782).

### Fixed (since 8540b78)

- A Session panic no longer leaves the TUI stuck on the running screen, and a password check that never resolves can be left with Ctrl-C (5de7e0c, 73b22cd).
- `proc.Stop`'s escalation goroutine exits once the child is reaped (8145c66).
- The e2e shim appends one whole line per write (1134f45).
- Tests now pin: a flood past `MaxLines` drops from the events, never from the log; the last-run outcome precedence (failed over incomplete) (c52e72e, c9539fb).

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

### GUI (since f46230a)

- **One polkit dialog per Full Cleanup** (decision E). The first live GUI run showed five dialogs. Now, under polkit, Full Cleanup's root steps (orphan removal, paccache, the optional wipe, tmpfiles, journal, integrity) run as one `archmaid-helper full-cleanup` call with one dialog. It is asked after topgrade, the orphan list and the cache-wipe confirm. Each step still shows its own row. If you refuse the dialog, all those rows read "Not authorised" and the run goes on (10666d8, f981d59, 4cd82e5, f7a8307, 0f83f65, 9dfd68a).
- **Under polkit, only topgrade asks for a password besides the dialog.** `pacman -Sc` (cached packages no longer installed) is now a part of the helper call (`--uninstalled`). The AUR-cache step is `yay -Sc --aur`, which cleans yay's build folder as you, without sudo (499d168, 746361f, a96182a, 6e6f70e; replaces 34cbce7).
- Steps no longer share the terminal the GUI was started from, so sudo can never prompt on that shell. Stop and Close end each step's whole process group (655fdb0, 60110ea, 3465a0d).
- Ctrl-C in the starting terminal, SIGTERM and closing that terminal all act as Stop and Quit: the run is aborted and recorded. A second signal kills everything the run started and exits at once (23f1f7b, 0f685e9, f43b87d, 76eec79).
- Closing the window while polkit's dialog is up stops and quits without asking, because the question would open under that dialog (e4010dc).
- Every start of an action gets its own run folder, and the AUR lookup runs while the Proceed dialog is up (e29a5fd).
- If the engine stops unexpectedly, the window leaves the running state and says so, and the next run starts on a fresh engine (97a4c09).
- The stub runs only with `--stub`, under a "Demo" banner, with "(stub)" in the title and its own app id (865124d).
- Fixed: clicking Authenticate could send an empty password, and "Stop and Quit" could leave the window open. The dialogs now act on the button you pressed, not on the dialog closing (bc5646b).
- Fixed: an answer retried because the engine was busy could reach the next run's prompt (ddf2098).
- Fixed: a dialog's list could scroll its title out of view in a short window (6df69df).
- A password dialog that the engine withdraws is wiped. sudo's prompt is cut to 48 characters, as in the TUI (3be3eed).
- A downloads folder read from `gui.json` is checked like a typed one (0de93ac).
- The end of a run reads the same way `last-run.json` judges it, and counts read "1 step", not "1 steps" (5a881d6, 4f22bed).

### TUI (since f46230a)

- **No step can read the TUI's terminal any more.** topgrade's `git pull` over HTTPS (also ssh and pinentry-tty) opened the terminal itself and read the same keys as the TUI. The TUI looked frozen, Ctrl-C couldn't abort, and `y` became git's username. The TUI now keeps its terminal to itself while it runs, so such a step fails at once instead (9d8ed41, 2783217).

### Internal and tests (since f46230a)

- `internal/frontend` holds the code both front ends share. The layering check now also keeps gotk4 out of `internal/` (2bebf67, ccaa96d, ea61927).
- `testenv` traps every system-changing tool (topgrade, yay, pacman, systemctl…), not only the privileged ones, and kills any process a test leaves running and fails that package (36318d0).
- GUI harness: a stand-in helper file, every Driver closed, the display suite split into four parts, checks for one polkit dialog and one askpass prompt, and every row line a run's events produce (5023db9, a6b22f9, e00141d, 08ecf61, bab48c6, fb436ff, 9dfd68a, 6e6f70e).
- Test fixes: the transcripts wait up to 30 s for a step to reach its shim; the pty test runs without `CI` and `NO_COLOR`; a fake AUR in a cancelled-fetch test can no longer hang Close (e71424d, 4d3b11d, 7f623af).

### Docs (since f46230a)

- `docs/first-live-run.md`: your guide to the first live run (5ae223d, 56fce2c, 0acb411).
- `docs/merge-to-main.md` (readiness review and checklist) and `docs/security-review-merge.md` (91f3f46, 99272a5).
- `docs/design/p8-workbench-spec.md`: the spec for the GUI redesign (d0bd9f9).
- The packaging README's scratch-build recipe, HANDOVER's open items brought up to date, and CLAUDE.md rules for tests and the test VM (17e4d42, e0f71d1, 0c4c15d, 95c2918, b45da6e, 5407fd4).

### Notes for the maintainer

- The live polkit/faillock test (`packaging/polkit/LIVE-TEST-AGENT.md`) was run against a scratch user (`docs/polkit-live-test.md`). `auth_admin_keep` is kept for the scope of the agent that asked: answered for the graphical session by the owner on 2026-10-11/12 (`docs/polkit-keep-session-check.md`, Results); the optional tty3 part is open.
- Pick a license: the repo has none, the PKGBUILD has `LicenseRef-unknown` and a TODO to install `LICENSE`. `namcap` has not been run on the packages.
- The first audit after upgrading rewrites `aur-maintainers.json` in the new format.
- Enable the widget after installing the package: `systemctl --user enable --now archmaid-updates-check.timer`, then enable "archmaid" in DMS Settings, Plugins and add it in Dank Bar (`docs/dms-widget.md`).
- The GUI's speed budgets are measured by hand (`ARCHMAID_GUI_PERF=1`, `docs/TESTING.md`); CI checks correctness only. The perf-guard baselines were taken before E7b changed the benchmarks' shape and want re-measuring (`make perf`, `make bench`).
- The merge itself is recorded in `docs/merge-to-main.md`.

[Unreleased]: https://github.com/Enzord758/archmaid/compare/v2.0.0...HEAD
[2.0.0]: https://github.com/Enzord758/archmaid/compare/5585358...v2.0.0
