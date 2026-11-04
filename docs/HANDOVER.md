# HANDOVER

The living orientation doc: where things are, the decisions you shouldn't undo
without reading why, how to live-test safely, and what's still open. It is
deliberately **not** a changelog — history is in `git log` (commit messages
carry the root-causing and verification), tests are in `TESTING.md`, features
are in `cmd/archmaid/README.md`. Keep this file current-state only; rewrite entries rather
than appending dated ones.

## Status

Feature-complete: all ten menu actions work, with `--dryrun` for a safe end to
end. The gate is `scripts/check.sh`: race tests, vet, gofmt and coverage
floors (see `TESTING.md`). Verified live against a real Arch system: Full
Cleanup runs, the sudo/faillock flows, and the AUR audit against the real AUR.

## Code map (`cmd/archmaid/` `package main`, and `internal/tui/` `package tui`)

`cmd/archmaid` is a thin main; everything else in this table is
`internal/tui` (moved in P5 T1), the only package that may import
charmbracelet (`check.sh` layering, which also bars every package under
`cmd/` from importing it directly).

| File | Owns |
|---|---|
| `cmd/archmaid/main.go` | package doc + `main()`: `startup` (the `--askpass` relay to `privilege.RunClient`, and `parseArgs`, which returns the `flags`: `--dryrun`, `--downloads`, `--version`, `--help`/`-h`; anything else exits 2 — it writes and deletes nothing), then through `internal/tui`'s API: `tui.Configure` with the flags, `engine.New` with `tui.ActionEnv()`, `tui.StartAskpass`, the Driver from `tui.NewDriver` (handed the askpass server's requests), `tui.Run`; after the program `Close`, then `tui.Finish` (the Driver's reports to stderr, `waitForDownloads`) |
| `tui.go` | the package doc and the API main calls: `Configure`, `ActionEnv`, `StartAskpass`/`StopAskpass`, `NewDriver`, `Run` (the model on the Driver, `forwardEvents` into `tea.NewProgram`), `Finish` |
| `adapter.go` | the TUI as an engine front end (E7): `newDriver` (`driver.New` with `driverDeps`: `privileged()`, `logs()`, `sigkillGrace`, `stepWaitDelay`, the askpass requests, `startPrefetch` and `driverReports.add`; the tests' `tuiDriverDeps` is the same), `forwardEvents` (then `engineStoppedMsg` once Events closes: a Driver that closed on its own — a Session panic — gets a notice with the newest report's first line, and any key quits), `engineState` (the Driver and the open prompts' IDs), `applyEvents` (every Event → the model's state: checklist, screen, prompt, output pane, summary), `engineKey` (a key on a prompt → a Command: confirm, password, selection, report, abort), `keyHeld`/`armKeys` (typed-ahead guard: a confirm or an audit report drops every key for `confirmArm`, 400 ms, after it is drawn or after the askpass popup over it closes, and each dropped key (not ctrl+c, which is never held, does not renew, and declines the confirm or audit at once) renews the window, so a password typed ahead of its prompt, its Enter included, never answers the next prompt — the topgrade confirm, default No, was declined that way (#158). Bubble Tea gives keys no timestamp and cannot drain the tty, hence time. The model tests set `confirmArm = 0` in an `init`; `typeahead_test.go` turns it on with its own clock), `openAskpass` (the askpass popup over whatever shows; the bg landmine below), and `reportLog` (what the Driver reports that no Event carries, printed after the program) |
| `glue.go` | the TUI's settings for the shared glue (`internal/frontend`): the audit's seams (`aurRPCURL`, `auditDeadline`) and `currentAuditConfig` (`frontend.AuditConfig` over them, read on main's goroutine or the Driver's loop), `startPrefetch` (`frontend.Prefetcher(currentAuditConfig)`, the Driver's `Deps.Prefetch`), the downloads flag and seams (`downloadsFlag`, `downloadsNow`, `downloadsEntryHook`) and `downloadsOptions`, `downloadsRunning` (a `frontend.Deletions`) and `waitForDownloads` |
| `internal/downloads` | the resolver (`Resolve(Options)`), `Scan(dir, now, Options) Plan`, `Plan.Confirm()`, `Delete(ctx, Plan, Options) (Result, error)`; ctime, Lstat, `os.Root`, the mountinfo boundary and the per-node re-check; no UI, no logging, no env reads |
| `model.go` | screens, the `model` struct (what the Events left it showing, plus the TUI's own menu, log viewer and pane state), `initialModel`, `Init` (the menu is `actions.Menu`) |
| `update.go` | `Update` — the single source of state transitions: keys go to `engineKey` first, then `handleKey` (moving, toggling, typing, scrolling, the summary — enter/esc back to the menu, `q` quits — and the log viewer); `activate` (the TUI's own entries, otherwise `engine.Start`), `resizeOutput`, the log picker. No queue, sudo, askpass or abort logic: that is the engine's |
| `view.go` | `View` and every renderer; `popupLayer` is the one place that knows which screens are popups |
| `tasks.go` | `stepState` (a checklist row: the engine's `PlanStep` and its status), the knobs the Driver gets (`stepWaitDelay`, `sigkillGrace`), `actionEnv` (the `actions.Env` the TUI hands the Session: `frontend.Glue.Env` on the TUI's settings, with `renderAudit` drawing the report), `editConfig` |
| `output.go` | the running step's output pane: a capped tail for the viewport, follow-the-bottom (the full output is the engine's, which logs it) |
| `auditview.go` | the styled `renderAudit` (the menu audit's report, `frontend.Glue.Render`); the audit glue itself is `internal/frontend` |
| `log.go` | the TUI's seam to `internal/logstore`: one package-level store built from `logDir`/`dryRun` (`logs()`, cached under a mutex, rebuilt when either changes, with the run directory's stamp fixed for the process; the Driver gets it as `Deps.Log` and does every write), `readLog` (the store rotates the run history itself, see the state-dir landmine), and the "Log is empty." rendering. `logs()` is for main's and the model's goroutine; anything that logs off it captures the `*logstore.Store` where it is set up |
| `askpass.go` | askpass glue: `startAskpassServer`/`stopAskpassServer` (the `askpassWrapper` seam), `privileged()` (a `privilege.Sudo` with that wrapper: the Driver's `Deps.Priv`), the popup's `askpassRequester` and `askpassPromptText`. The Driver verifies each request against the running step's child (`Request.Verify`) and answers it (`Request.Answer`); the popup is `openAskpass` in `adapter.go` |
| `overlay.go` | ANSI-aware popup compositor |
| `status.go`, `theme.go` | the status line's tea glue (`refreshStatus`, `refreshStatusNow`, the `tickFn` seam; the reading itself is `internal/sysstat`: statfs, MemAvailable from /proc/meminfo, a cache-dir walk — no child processes); the Tokyo Night styles |

### `internal/` (UI-free: nothing here may import charmbracelet or gotk4; `check.sh` enforces it)

| Package | Owns |
|---|---|
| `internal/frontend` | the glue every front end hands the engine, once for the TUI and the GUI (P5): `AuditConfig(store, AuditOptions{RPCURL, Deadline, DryRun, Foreign})` (zero fields are the real thing; `ForeignPackages` is `pacman -Qqm`), `MenuAudit(ctx, cfg, now, render)` (`Env.AuditReport`'s Notice or Report; a cancelled audit is "stopped" and writes no snapshot), `AuditStep(cfg)` (the topgrade gate and snapshot commit, with its `Prefetch`), `StartAuditFetch`/`AuditFetch` (a `driver.Prefetch`) and `Prefetcher(cfg func)` (`driver.Deps.Prefetch`), `DownloadsOptions(flag, dry)`, `Deletions` (`Step(plan, store, opts)`: the delete step's Run with its abort record; `Wait`), `StepAborted(ctx)` (any cancel but `steps.ErrResultTaken`), and `Glue{AuditConfig, Render, Downloads, Store, Deletions}.Env(base)` filling in the four `actions.Env` funcs; `TopgradeConfigPath`, `EnsureTopgradeConfig` and `DefaultTopgradeConfig` (Edit topgrade config: the TUI before `$EDITOR`, the GUI before its launcher; never overwrites, `O_EXCL`). Neither charmbracelet nor gotk4, named in `check.sh` |
| `internal/ttyguard` | `Hold(f)` → `*Guard` (`Release`, `Pause`): the TUI's terminal in exclusive mode (`TIOCEXCL`) so no step child can open it (see the landmine); `IsTerminal`, `Exclusive` |
| `internal/sanitize` | `String(s, Mode)` with `KeepSGR`/`Plain`: the one filter for untrusted text (command output, logs); and the one-line text helpers `OneLine`, `Clip`, `ClipTail`, `Field`, `QuoteName`; `Hidden` (the format runes `String` drops, for `internal/vt`) |
| `internal/vt` | a step child's output as a terminal shows it (#129, `docs/design/run-pty.md`): `New() *Screen`, `Write`, `Flush(complete) (replace, lines)` (the tail's redraws), `Rewrite`/`Ready`/`Partial`/`Backlog` for `proc`'s coalescing. CR, LF, BS, TAB, EL/ED 0–1, ECH/DCH/ICH, relative cursor moves within the last `MaxRows` (64) lines, save/restore, SGR per cell as the child wrote it (conceal never); every other escape dropped, an unterminated string (OSC, DCS…) ending at a newline or after 4096 bytes. Caps: `MaxRows`, `MaxCols`, `MaxLineBytes`, `MaxStyles`, a 256-byte control sequence; fuzzed (`FuzzScreen`) |
| `internal/logstore` | the on-disk log: `New(Options{Dir, DryRun, Now})`, `Append` (plain text, 0700/0600 + chmod on write), `Read` (`ErrEmpty` when there is nothing; the TUI renders that text), `Prune(keep)`, `Root`, `RunDir`, `Path`, `Categories` (the four step categories and the four discovery ones, `Services`/`Repair`/`Downloads`/`AURAudit`, `Category.Discovery`; see "The discovery record"); `WriteLastRun`/`ReadLastRun` for `last-run.json` (`RunRecord`, see state files below) |
| `internal/pkgdb` | read-only reader of pacman's DBs for the Packages page (#232, contract in docs/design/packages-contract.md): `Open(Paths{Sync, Local, Conf}) (*DB, error)` (repo order = pacman.conf sections; gzip/zstd/plain tar by magic bytes), `Reload()` (mtime stamp), `Search(q, max)`, `Lookup`, `Installed(View)`, `Count()`, `Recent(n)`, `Warnings`, `SyncTime`/`SyncAge`, `Vercmp`, `ValidName`, `Deps`. Orphan/Foreign/Update follow `-Qdt`/`-Qm`/`-Qu` (a shim test ties them). `pkgdbtest` writes fixtures from Go values. No process, no network; all text sanitized and capped |
| `internal/aursearch` | AUR RPC v5 `New(Config{BaseURL, Timeout}) *Client` implementing `Searcher`: `Search` (name-desc, 2..64 byte term else `ErrBadTerm`, 40 hits, `ErrTooMany`) and `Info` (POST, chunks of 150); 8 s, 8 MiB cap, fields sanitized, illegal names dropped |
| `internal/aurbuild` | user-side AUR install for the Packages page (#235): `Resolve(ctx, names, Repo, Searcher)` (AUR closure of runtime deps, repo deps and makedepends for `--asdeps`, installed-provider skips, base build order; refuses cycles, AUR-only makedepends, unmet virtuals, a target found in a repo; `Resolution.Verify` checks the fetched `.SRCINFO` against the index), `Fetcher{Root}` (`Fetch` = git clone/fetch with a clean env, no config and no hooks into `~/.cache/archmaid/aur/<base>`; `Pin` = detached forced checkout of the reviewed commit; `Review` = the `steps.AURPlan` payload: files with capped sanitized text, diff against the last reviewed commit, hints), `Planner.Prepare` (resolve, fetch, review; the maintainer audit runs meanwhile), `Fetcher.Queue` (steps `aur-fetch:<b>` pin, `aur-build:<b>` = `env -C <dir> makepkg --clean --cleanbuild --noprogressbar --noconfirm`, `aur-collect` hashes and inserts the root `pkg-install-local`, `aur-remember`), `LocalHash` (tied to `helperclient.PkgLocalHash`; `helperclient.Argv` builds `pkg-install-local <H> <file>...`), `Reviewed` (`~/.local/state/archmaid/aur-reviewed.json`, fixed path via `logstore.DefaultDir`; `aurbuild.DefaultReviewed()`). Nothing sources a PKGBUILD before the sheet is accepted; the helper never imports it. Glue: `frontend.AURPlanner`, `actions.AURDiscover`, `Env.PlanAUR` |
| `internal/audit` | the AUR audit itself: `Run(ctx, Config, now) (Result, error)` with `Config{RPCURL, SnapshotPath, StatePath, Deadline, DryRun, Foreign, Log}`; `Result` (`Total`, `Findings`, `Baseline`; `Text()` plain report, `GateReason()`, `Commit(holdHigh, accepting)`, `LogSummary()`, `Note()`, `High()`/`Clean()`); `Prefetch(Config) *Fetch` with `Wait(ctx)`/`Cancel()`/`Done()`; the RPC (chunked POST, 8 MiB cap, unrequested names dropped), classification, the snapshot (`Entry`, `LoadSnapshot`, `WriteSnapshot`: atomic 0600 + flock, old-format compatible), `State`/`WriteState`/`ReadState` (audit-state.json), `ErrNoForeign`, `ErrSnapshotLocked`, `ShortErr`. No UI, no globals: everything comes in through `Config` |
| `internal/sysstat` | `Paths`/`DefaultPaths`, `Read(Paths) Status`, `Status.Line()`, the df/du/free-style size formatters; no UI imports |
| `internal/report` | the run report (#188, see "The run report"): `Collector` (`New(Config, action, started)`, `StepStarting`, `StepDone`, `StepResult`, `Finish(ctx, FinishInfo) Report`), `Report`/`Packages`/`Area`/`CacheHold`/`Attention`, `ReadLog`/`ParseLog`/`MarkLog` (pacman.log by byte offset), `DirSize`/`Freed` (user-side, bounded, clamped), `ParsePaccacheDry`/`CacheDirs`, `HumanBytes`, `DefaultConfig()`. UI-free; reads and runs only as the user. `internal/tui/summary.go` draws it; the Driver (`driver/runreport.go`) feeds it |

Shared packages under `internal/` are UI-free (the gate fails any that imports
charmbracelet, except `internal/tui`, and any that imports gotk4):

| Package | Owns |
|---|---|
| `internal/privilege` | getting root without prompting on the tty. `Privileged` (`Probe`, `Authenticate`, `Refresh`, `Command`) and its one implementation `Sudo{Wrapper, SudoDir}`: `sudo -n -v` probe and keep-alive, two-stage `Authenticate` (`CheckPassword` = `su -s /usr/bin/true <user>`, then `sudo -S -v`; lockout notices; then the ticket's reuse check `sudo -n -v`: without a reusable ticket `NoTicketNote` and no error when the sudo wrapper is wired, else `ErrNoTicket`), `Command` (sudo gets `-A` or `-n`; `Env` strips `SUDO_ASKPASS` and gives the wrapper only to privileged steps, with `SudoDir` first on their `PATH`). Askpass: `Listen(runtimeDir, exe, ServerOptions)` → `*Server` (`Requests`, `Wrapper`, `SudoDir`, `Close`; private 0700 dir, shell-quoted wrapper, per-client read deadline and `MaxPrompt` cap, `SO_PEERCRED` + start time per client; with `ServerOptions.SudoWrapper`, the TUI's, a `sudo` of its own in a 0700 `bin/` inside it: `sudowrap.go`, see "Nothing may prompt on the raw tty"), `*Request` (`Prompt`, `Peer`, `Verify(stepPid, stepStart)` → `Requester`, `Answer(pw)` — refused unless Verify passed, su-checked before anything is written —, `AnswerThen(pw, accepted)` (accepted runs between su's yes and the write: where the GUI's Driver keeps the password) and `Cancel`; only a Server makes one; other packages' tests use `privtest`), `RunClient(sock, prompt)` (the `--askpass` helper). `FaillockStatus(user, now)` → `Status{Failures, Locked, Unlock, Deny}` (`LastAttempt()`): pam_faillock's state, read before any password or polkit prompt (see the faillock landmine) |
| `internal/proc` | child processes: `Runner.Start` (a `Spec` run with `Options{DryRun, WaitDelay, KillGrace, Detach, Pty, Cols, Redraw}`; `Spec.ExtraEnv` is added after its environment, without any `SUDO_ASKPASS`, its combined output streamed as `Msg`s on one ordered channel through `internal/vt` (`lines.go`: complete lines at once, a redraw — `Msg.Replace` — at most every `Redraw`, 50 ms), the dry-run echo, `ErrWaitDelay` → success on a clean exit; `Pty` (with `Detach`, the GUI's) gives the child a pty as stdout and stderr, never its controlling terminal: `pty.go`, see the `Setsid` landmine), `Next` (blocks for one message, drains up to `BatchMax` more into a `Batch`, folding `Replace` in), `Child.Stop` (SIGINT, SIGKILL after the grace), `Query` (run a read-only command, return stdout, keep the `*exec.ExitError` and stderr), `Read` (`/proc/<pid>`: parent, group, session, start time, euid, exe) |
| `internal/steps` | what a unit of work is: `Step` (stable `ID`, the root helper's `Verb` where one exists, `Argv` or an in-process `Run(ctx, Env) Result`, `Env` (variables for its child alone: the Driver passes it as `proc.Spec.ExtraEnv`), `Confirm`/`Optional`/`OnAccept`, `Items`/`Cascade` (what a confirm lists, as data), `NeedsPriv`, `AlwaysLog`, log `Category`, `Prefetch`), `Result` (`Output`, `Err`, `ConfirmNext`, `Commit`, `InsertNext`; `Check` forbids gating and inserting at once), `ErrResultTaken` (the cause a front end releases an in-process step's ctx with once its result is taken; any other cancel is an abort), `Status` (`Pending` … `AuthRefused` — privilege not granted, one status whatever the reason, since pkexec exits 127 for every refusal — and `Aborted`) |
| `internal/actions` | the menu as data: `Menu []Action` (`ID`, `Label`, `Icon`, `Gate`, `Busy`, `Steps func(Env) []steps.Step` or `Discover func(ctx, Env) Discovery`, `Record` — the discovery's log category), `ByID`; the queues (Full Cleanup, Run topgrade), the Clear caches discovery and pick (`clearcaches.go`), the orphan probe, Repair's user scan and root recheck, the services discovery and `DisableUnits`, the downloads discovery. `Discovery` is a typed union: `Notice{Text, Err, Details, Log}` (ends the action: nothing found, or with `Err` the look failed), `Pending`, `NeedsSelection{Items, Rows, Steps}` (`Rows []PickRow{Label, Note, Disabled}`, optional: one per item, for a pick whose rows need a warning or can be disabled), `Report{Text, Accept}` (`Accept func() error` accepts the flagged findings, nil when there is nothing to accept), `NeedsRoot{Items, Busy, AuthFailed, Rescan}`; `Pending`, `NeedsSelection` and `NeedsRoot` carry `Checked{Summary, Details, Log}`, what the discovery looked at (see "The discovery record"). `Env` carries the run's flags and what still lives in the TUI, as func values: the AUR audit (`AuditStep`, `AuditReport`) and the downloads delete step (`Downloads`, `DeleteDownloads`). Those four are required (a nil one panics mid-action); `Env.Validate` names any left out, and the TUI's `main` calls it on `actionEnv()` at startup. `DryRun`, `LookPath` and `RepairWorkers` may stay zero |
| `internal/engine` | the run logic as a state machine with no IO, shared by the TUI and the GUI. **API frozen at E0: changes after this are additive only** (new types, fields, constants; nothing renamed, removed or given a new meaning). The package doc is the contract: event ordering within a step, prompt IDs (stale ones ignored), that only `OutputChunk` may be coalesced or dropped (by a Driver, never the engine) and that no Event carries a password. `Session` (`New(Options{DryRun, Now, Env, Auth})`, checked by `Options.Validate`; `Start`, `Confirm`, `SubmitPassword`, `CancelPrompt`, `Select`, `Abort`, `Handle(Input)`, each returning `Output{Events, Effects}`) runs, as of E1–E5, an action's discovery (a notice, a queue, a pick list or a report) and the whole queue core: the sudo probe and the proceed-gate, step confirms, steps' output and results, the logging policy, steps a result inserts (`InsertNext`) or gates (`ConfirmNext`, failing closed on a refused result), hyprpm kept last, the last-run record; auth (`auth.go`: the up-front password with three attempts and the give-up that refuses the queue's `NeedsPriv` steps, the faillock read before the prompt and before each polkit helper step — a locked account refuses the helper step without a dialog —, Repair's `NeedsRoot` probe/password/rescan, `WaitPolkit` until `PrivilegeGranted` or a `NotGranted` exit); askpass (`askpass.go`: only for the running sudo step, peer-checked by the Driver, on the run's attempt count). E5: `Abort` (`abort.go`: from a running step — `Stop`, then `Aborted` —, a prompt — `PromptClosed` — or a wait; pending steps get no event and count in `Finished.NotRun`; last-run.json as the TUI's abortRunning), the pick list (`select.go`: `NeedsSelection`/`Select`, matched by what was shown; nothing picked ends cancelled), reports (`report.go`: `OutputReport{Audit}`, accepted through `RunHook` then `ReportAccepted`), Repair's polkit recheck decided from the helper's check event (`repaircheck.go`, the `HelperCheck` input), the gate's plan preview and each confirm's `YesLabel`, and the discovery record (`discovery.go`: `DiscoveryResult`, the record's `WriteLog` and the `nothing-found`/`failed` last-run record; see "The discovery record"). Nothing panics (`queue.go` holds the run, `session.go` the API). Both front ends run on it through a `Driver` (`Events`, `Send(Command) bool` — never blocks — `Close`): the TUI since E7b and the GUI, each on `internal/driver`. Events mirror `internal/testtranscript`'s vocabulary so a `sessionTarget` maps them 1:1; privilege is one outcome (`steps.AuthRefused`) — except a Cancel in the GUI's own polkit dialog, which the Session sees and ends as the user's no (`askpass.go`, `cancelledByUser`; see "pkexec: one exit code" below) —, with `AuthWaiting`/`AuthResolved`/`FaillockState` for the polkit flow; passwords travel only as `Secret` (prints `<redacted>`) in Commands and Effects |
| `internal/driver` `refresh.go` | the widget's update count after a topgrade run: `engine.RefreshUpdates` is an Effect the Session emits once in `finish` (a real run whose topgrade step started: OK, failed or aborted while running; never a dry run, a declined gate or confirm, an abort before topgrade, or an action without topgrade). The engine stays pure (the Effect is data); the Driver, which both front ends run on, carries it out on a worker through `Deps.RefreshUpdates` (nil: nothing; the TUI's `driverDeps` (through its `refreshUpdates` seam) and `realdriver.New` set `driver.RefreshUpdates`): `systemctl --user start --no-block archmaid-updates-check.service` (5 s timeout, ended by Close), else `installed.UpdatesScript` (`/usr/lib/archmaid/archmaid-updates-check`) in the background, niced 19, not waited for, else (no `systemctl` or user manager or unit, and no installed check: the TUI over ssh, a build without the package; or a hung `systemctl`) one `WARN` line in the run's topgrade log (`refreshFailed`): never a `Report`, which the TUI prints on the terminal after it quits; only a failed log write is reported. Never fails the run. Tests: `engine/refresh_test.go`, `driver/refresh_test.go` (shim systemctl on PATH, their own script in `installed.UpdatesScript`). **No other test may reach it**: every harness on a real Driver fakes `RefreshUpdates` (the TUI's `TestMain` swaps `refreshUpdates`; the GUI's `realdriver` tests and harness set `Config.RefreshUpdates`), and `testenv` traps the installed script (see `internal/installed`) |
| `internal/version` | `Version` (set with `-ldflags -X github.com/Enzord758/archmaid/internal/version.Version=...` by `packaging/arch/PKGBUILD` and `install.sh`: `git describe --tags --dirty`, else `rN.sha`; both are allowed to set this one variable, `scripts/check.sh` refuses any other `-X` there), `String()` (it without a leading `v`, else `debug.ReadBuildInfo`'s `vcs.revision` short, `-dirty` when `vcs.modified`, else "unknown"). `archmaid --version` and the GUI's Settings "About" line read it; the helper does not (its argv is a root-side protocol). Imports nothing of archmaid's |
| `internal/sysbin` | the system programs archmaid runs for privilege, by absolute `/usr/bin` path, never from `PATH`: `Su`, `Sudo`, `Pkexec`, `Pkttyagent`, `Topgrade` (`All()`), as variables in a package that imports nothing. `internal/testenv`'s `init` sets each back to its bare name in every process of a test binary (re-exec'd children too), so the `PATH` traps and shims decide as before; the e2e build does the same with `-ldflags -X`. Read at the call, never copied at init. See the landmine "archmaid doesn't trust PATH for privilege" |
| `internal/installed` | the programs archmaid's package installs that archmaid runs by absolute path (`UpdatesScript`; `All()`), as variables in a package that imports nothing: an absolute path bypasses `testenv`'s `PATH` traps, so `testenv.Run` points each at a trap of its own, and `TestNoAbsoluteInstalledExec` keeps their literals in this package. (archmaid-helper isn't one: archmaid only names it to pkexec.) The hole it closes: on 62f0606 `TestTranscriptRealDriver`, whose `PATH` is its shim dir alone, ran the owner's real installed check 12 times per run (#108) |
| `internal/engine/enginetest` | test helpers shared by the engine's tests, the stub's and the transcript harness: `CheckContract(t, events)` holds an event sequence to the package doc's ordering rules |
| `internal/engine/stub` | a `Driver` that replays a scripted event sequence (`Script` of `Cue{After, Event}`) on a `Clock` (the wall clock, or `FakeClock`, which jumps instead of sleeping): prompts wait for their answer, stale IDs and early commands are ignored, `Abort` withdraws the prompt and ends the run like the engine would. `FullCleanup(start)` is a ready-made run shaped like the orphan-cascade transcript. Only the GUI's `--stub` demo runs on it; real runs use `internal/driver` (E6) |

### `gui/` (its own Go module: the GTK4/libadwaita GUI, on the real Driver's polkit path; `--stub` for demos)

`gui/go.mod` is `github.com/Enzord758/archmaid/gui`, with a `replace` onto the
root module, so the root's `go test ./...`, `check.sh` and the CI `core` job (run in the test VM: `~/archmaid-vmtest/run.sh ci <sha>`)
never compile gotk4. gotk4 `v0.4.1` and gotk4-adwaita
`v0.0.0-20260808200908-d4aecaa0ff32` are pinned exactly. No `go.work` is
committed (`.gitignore`d): it would pin a toolchain line and fold the GUI's
requirements into the root build; make one locally for gopls if you like.
Binary `archmaid-gui`, application id `io.github.enzord758.archmaid`.
Packaged as its own split package `archmaid-gui-git` (`packaging/arch/PKGBUILD`:
the binary, `packaging/io.github.enzord758.archmaid.{desktop,metainfo.xml}` and
the project icon under the application id), pinned to the `archmaid-git` of
the same build because it runs that build's root helper. `archmaid-git` stays
GTK-free; the DMS widget opens `archmaid-gui` when it is on PATH, else the TUI.

| Path | Owns |
|---|---|
| `gui/cmd/archmaid-gui` | `main`: `realdriver.Relay` first (the SUDO_ASKPASS relay, `--askpass <sock> [prompt]` → `privilege.RunClient`, as the TUI's `startup`), then `ui.Main` |
| `gui/internal/realdriver` | plain Go, no gotk4: the real Driver, wired. `Start(Config)` starts the askpass `Server` (best effort: without it topgrade's sudo runs `-n`); `Options()` is the base `engine.Options` (`AuthPolkit`, a complete `actions.Env`: `PlanOrphans` = `helperclient.PlanOrphans`, and the glue shared with the TUI, `internal/frontend` (`Glue.Env` with plain-text rendering, `AuditConfig`, `DownloadsOptions`, `Deletions`)); `New(engine.Options)` is the window's `NewDriver` (`driver.New` with `Polkit`, `Priv: privilege.Sudo{Wrapper}`, the Server's requests — the Driver verifies each — the AUR audit's prefetch as `Deps.Prefetch` — `frontend.Prefetcher` over the run's own store: started when the queue is planned, so it runs while the gate is up, awaited by the audit step, cancelled and reaped on a declined gate, an abort or `Close` —, and a fresh log store per Driver: the window makes one Driver per Start, so each real run gets its own `<stamp>` run directory, stamped at that Start (`Config.Now`), a dry run writes to the `dryrun` slot, a real run's first write rotates to `KeepRuns`, and `last-run.json` stays in the state dir's root); `Close` waits for a downloads deletion's abort record, then closes the Server. `Config` swaps in fakes (faillock, orphan plan, foreign packages, LookPath, state dir) for tests |
| `gui/internal/runview` | plain Go, no gotk4: `Model` (the Run page's state: checklist `Row`s keyed on step ID, the open confirm, phase/status, the output ring capped at `OutputCap` = 10k lines; `Apply(events) Change` says exactly what to redraw, including the output diff `OutTrim`/`OutRemove`/`OutAppend` so a batch bigger than the cap never inserts a line only to drop it, and a redraw (`OutputChunk.Replace`) takes the step's last lines back off the tail, never past its header, a dropped-lines note or what the ring let go), `Pump` (one goroutine reads `Driver.Events()`, merges adjacent `OutputChunk`s of a step (a redraw through `engine.Rewrite`), drops the oldest queued lines past `MaxPending` counting them in `Dropped`, and posts one flush at a time to the main thread; a flush applies at most `FlushLines` = 512 lines, so a flood is sliced into ~1–2 ms pieces; `PumpOptions.Ended` runs on the main thread after the last batch once Events has closed — the window takes it, for a Driver it did not close itself, as the engine having stopped: `Model.Stop` ends the action with `EngineStopped` in the status line and a toast, closes the dialogs and brings the controls back), `Flood`, `FloodColour` (the 50k-line stub scripts), `ParseSGR`/`StripSGR` (output SGR → text + styled spans; only SGR is interpreted, anything else is dropped), `Palette` (DMS's dank16 read from `$XDG_CACHE_HOME/DankMaterialShell/dms-colors.json`, by its `mode`, else the fixed `FallbackPalette`; 16–255 are the xterm cube and greys) |
| `gui/internal/pkgview` | plain Go, no gotk4: the Packages page (docs/design/packages-contract.md, task #234). `Search` (term cleaned, repo search after `RepoDebounce` 120 ms on a worker, AUR after `AURDebounce` 500 ms at 2+ runes with the request in flight cancelled by the next term, a stale result dropped by generation; `MergeAUR`: repo names win, installed state merged, caps 50/40; `Select`/`Detail` with one AUR `Info` call), `List` (installs xor removals, orphans and protected refused, 32 names; `Request` is `-Rs`, `-Rns` with `WithConfig`), `Sheet` (the confirm sheet state machine: planning, review, refused, updates pending, stale, running; prompt count, DB age, the AUR review and the boxes that enable Install), `Controller` (list, sheet, gate and `Backend` joined; the gate reads `overview.ReadUpdates`: a fresh cache with repo updates blocks Install, never a removal), `InstalledView`, `Catalog` (what the page reads of `*pkgdb.DB`), `Backend` (StartPackages/Answer/Abort/UpdateSystem) and the `Event` types the engine events map onto |
| `gui/internal/pkgview` live-check fixes (#244) | `Controller.Reload` (DB re-read off the main thread after any package run, after topgrade or a Full Cleanup (`Window.reloadPackages` from `redraw`'s Finished) and when the page is shown; coalesced; `Installed.Reload` + `Search.Refresh`, which keeps the selection), `ProtectedSet.UseHelperList` (the helper's list replaces the advisory one; `PackagesWiring.Protected`, bound to `protectedQuery` in `packages_wiring.go`), `Sheet.Dry`/`Kept` (the password note: kept = once, not kept = up to twice, dry = none), `RefusalTextFor`, `pkgdb.Satisfied` (a dependency is met by name or by a provide at a satisfying version). `ui`: `updateStop` (a Packages run has no Stop while its sheet or confirm is up or its root step runs), `polkitMessages` (the password dialog shows the policy's message, the id after it), `Window.dryOff` |
| `gui/internal/ui` Packages wiring (#237) | `packages_adapter.go` (the one adapter: `PackagesPreview`+`NeedsConfirm{ConfirmPackages}` -> `Preview`, `PackagesRefused/UpdatesPending/Stale`, `QueueStarted` -> `RunStarted`, `Finished` -> `Done`; these events and the sheet's confirm never reach the run model), `packages_run.go` (`Window.startPackages` on a fresh Driver, `busy`/`current` around the run, `pkgEngine` = the page's Backend: `engine.StartPackages`, `Confirm`, `Abort`), `packages_wiring.go` (`openRealPackages`: `pkgdb.Open` over `pacman-conf DBPath`, `aursearch.New`, `aurbuild.Planner` over the same DB with the audit (foreign packages from the DB, no log), `frontend.AURPlanner` as `realdriver.Config.PlanAUR`; `realOptions` sets `Options.Packages`), `packages_demo.go` (`--stub`'s catalog and AUR). A Packages run's report has `Action == actions.IDPackages`; the summary tells install from remove by the report |
| `gui/internal/ui` glass skin (#254) | `glass.go` + `glass.css`: an opt-in second stylesheet over `style.css` (provider at APPLICATION + 1, added to and removed from the display live; nothing of it loaded with glass off). `glassModeFor(settings, getenv)` gives off, blur or fallback; `applyGlass` sets `am-glass` (and `am-glass-fb` in the fallback) on the window and, in `amDialog.show` (`dialogOpened`), on every dialog, so the selectors are `.archmaid.am-glass`. The window background is `alpha(@window_bg_color, 0.60/0.76)` (named colours stay libadwaita's and DMS's; only the alpha is ours; light and dark by `prefers-color-scheme`), the sidebar pane its own alpha, text surfaces (`am-text-surface`: Run output, logs, PKGBUILD) 0.93, sheets 0.72/0.78; libadwaita's flat header bar and toolbar view paint nothing over the window (the test reads the screenshot's alpha to keep it so). The corner marks are eight background layers: glass.css replaces the image and resets size, position and repeat, or the sheen is drawn as one 10 by 1 strip (a test found it). Content blur: `am-under-dialog` on `am-content` (the split view) while any dialog is open (`w.openDialogs`), only in the blur skin on a non-cairo renderer (`contentBlurOK`, `rendererName`); GTK 4.24.1's `filter: blur()` draws (a GSK blur node), checked in the render node. The light state colours (`style_light.css`) and the dark glass tokens (`glass_dark.css`) are providers swapped by `syncScheme` on AdwStyleManager's `dark` flag, not `@media (prefers-color-scheme)`: GTK resolves a `@define-color` inside @media when the sheet is parsed and keeps it on a live switch (the KDE check, #255). Settings > Appearance (`settings.go` `appearanceCard`): the switch and the Auto/On/Off row apply live. Tests: `glass_test.go` (plain), `glass_harness_test.go` (`TestGUIGlass`, `TestGUIGlassShots`; their own part of `headless.sh`, built with `-tags shots`) |
| `gui/internal/overview` | plain Go: `UpdatesCard` (#252, the Overview's Updates card: `View(Updates, now)` gives title, detail, tag and tone for waiting, up to date, not checked, unknown (older than `UpdatesFresh`), checking and failed; `Begin`/`Poll`/`Fail`/`Clear`: a check is done when `ReadUpdatesStamp`'s Checked or mtime changes, failed after `CheckTimeout` = 2 min or when `Options.RefreshUpdates` errors. The card's widgets are `ui/updates_card.go`: Check runs `Options.RefreshUpdates` (= `driver.RefreshUpdates`, as the user; nil in the stub, so the button is off) on a goroutine and polls the file every second; it rereads after a run and when the Overview is shown; Check and Run topgrade are off while `busy`), `Entries` (`actions.Menu` as the GUI shows it: Adwaita symbolic icons, `Busy`; View log opens the Logs page; Exit and Edit config are the TUI's), `StatusLine`, `ReadLastRun`/`Summarize` (last-run.json through logstore, read only; the DMS widget's rule: outcome issue first, stale past `StaleDays` = 14) |
| `gui/internal/logview` | plain Go: `Runs` (the run directories under the state dir, newest first, the dry-run slot last, with the category logs each has) and `Run.Read` (through a `logstore.Store` whose clock reads the run's stamp). Read only: never creates, deletes or rotates |
| `gui/internal/settings` | plain Go: the Settings page's `Settings` (downloads folder, dry run), `Path`/`Load`/`Save` (`$XDG_CONFIG_HOME/archmaid/gui.json`, never the state dir; written atomically, 0600), `CheckDownloads` (`downloads.Resolve` with the Flag set: refuses what the TUI's `--downloads` refuses) and `Apply` (the settings onto the `engine.Options` for the next run) `Glass` and `Blur` (#254: the glass look; `BlurBehind(getenv)` is the Auto rule, translucent only when `HYPRLAND_INSTANCE_SIGNATURE` is set) |
| `gui/internal/ui` | the only gotk4 package: `Main` (the real Driver through `realdriver`, its base Options checked by `Validate` first; only `--stub` runs the stub instead, which touches nothing — no environment variable does, so a stray one never puts the real GUI on it (`parseLaunch`); a stub window always shows the banner "Demo — the stub, changes nothing", "(stub)" in its title and STUB in the status line, and runs as its own app id `StubAppID` (`AppID` + ".Stub"), so a demo and a real window never share an instance on the session bus. The stub plays its `Catalog`, a script per menu action with the declined paths; with `--stub`, `ARCHMAID_GUI_SCRIPT`: `flood`, `flood-colour` play a single script, `uncached`, `askpass` swap in the stub's password variants of Full Cleanup, and any other value is refused), `DefaultRenderer` (cairo unless `GSK_RENDERER` is set), `Run`, `Window` (a view stack: Overview — status line, last run, the menu, a row starts its action —, Run, Logs — runs as expanders, the log in a `ListView` —, Settings — dry run and the downloads folder, its error inline; they apply to the next Start; every Start makes a fresh Driver through `Options.NewDriver(engine.Options)` (closing the idle one), so a run never shares a Session or a log store with the one before; header with the view switcher and Stop; on the Run page a checklist `ListBox` of `AdwActionRow`s, output `ListView` over a `GtkStringList` whose rows get their SGR colour as Pango attributes at bind, status bar, toasts, close-mid-run confirm). An action that ends at its discovery (nothing found, or the look failed) is its own result: `engine.DiscoveryResult` (Nothing/Failed) makes the Run page this action's, a run of one "Discovery" step (`runview.DiscoveryStepID`) with the Summary as its line, the Details as its output, ok or failed, the kicker "Nothing found"/"Failed: ...", no "previous run" tag; a Found result changes nothing (its queue is the run). Its toast (`Model.EndToast`) and the status line say the Summary (cut at `runview.ShortMax`), until the next Start. An action that ends before its queue (a declined gate) leaves the page, its header and `Model.Finished` the last run's, tagged "previous run", but the status line says this action's end, its toast's words ("Run cancelled, nothing ran"), without the page's line count, until the next Start (`statusText`; Dev 5's VM round b6 read "Finished: completed · 14 lines" there). Its log is on the Logs page, and the Overview's last run cell reads `nothing-found` as NOTHING FOUND (ok tone). `dialogs.go`: one dialog per open prompt, an `amDialog` (`widedialog.go`: AdwAlertDialog's response API — labels, default and close response, `ConnectResponse` — over either a wide `AdwDialog` built by `newWideDialog`, heading in a top bar, the middle scrolling, the buttons in a bottom bar, its columns stacked under its own `max-width: 640sp` breakpoint, a bottom sheet in a narrow window; or an `AdwAlertDialog` through `alertDialog`, the password dialogs and the quit question) — confirm (wide: the gate 880 px with its steps beside "Before you start" and its notes, its steps tagged `asks` and by `runview.PrivTag` — `root` for a helper step, `sudo` for topgrade, which gets root through its own sudo, so the note's "6 steps run as root" counts the `root` tags —, the orphans 880 px with "Orphaned" beside "Removed with them" (430 px, ~48 monospace characters: one "name version" per line unbroken), a list 720 px, a question 640 px; Items, or the orphans' Direct and the cascade's extra, the gate's `Plan`; yes is `YesLabel` or "Yes", no is "Skip" for an Optional step else "Cancel"; no is the default for `Destructive` whatever `Default` says, else `Default`; yes red when destructive), password (an AdwAlertDialog with prefer-wide-layout, up to libadwaita's 600 px cap in a window over the narrow breakpoint; up front with `Gate` as heading; askpass naming `Requester` and sudo's prompt; `Error`, attempts left), selection (wide, 640 px; check boxes → `Select`), report (wide; audit findings, high ones flagged; "Accept" only with `CanAccept`). Every answer goes through `Window.answer`, which sends only when `Model.Answer` claims the ID, so a stale ID never reaches the engine. `secret.go` reads the password entry's text as C bytes (never a gotk4 Go string), wipes the entry, and zeroes the bytes after building the `Secret`. `harness_test.go` is the headless harness (see `TESTING.md` → GUI) |
| `gui/scripts/headless.sh` | runs a command on a private headless display (GTK's Broadway backend against `gui/scripts/nullbroadway`, one Unix socket in a 0700 temp dir, no compositor, no IP socket) and a private, activation-free D-Bus, so GUI tests never open a window on the desktop |
| `gui/scripts/nullbroadway` | a Broadway display server that shows nothing (broadwayd's no-browser behaviour, every roundtrip answered as a browser does, nodes and textures dropped); why not `gtk4-broadwayd`: `TESTING.md` → GUI |

## Decisions and landmines

Each of these was learned the hard way. The reasoning lives in the code
comments and `git log`; this is the "read before you touch it" list.

- **GTK 4.24 + ext-background-effect: no `set_blur_region` without a
  `backdrop-filter`, so no compositor blur (#258).** GTK binds
  `ext_background_effect_manager_v1` and calls `get_background_effect` for
  every toplevel, but sends a blur region only for a window whose CSS has a
  `backdrop-filter`. Hyprland 0.56 reads a client that holds the object with
  no region as "no blur" and skips its automatic blur, so a translucent
  window shows the desktop unblurred. `glass.css` therefore gives
  `window.archmaid.am-glass:not(.am-glass-fb)` `backdrop-filter: blur(20px)`:
  the radius is not sent (the protocol has none; the compositor uses its own
  `decoration:blur`), GTK blurs only an empty backdrop, which costs a node
  but draws nothing, and it must be absent in the fallback and the default
  look (a test reads the window's render node). Auto asks the registry
  (`compositor.go`, `gdk_wayland_display_query_registry`, declared by hand so
  the build needs no wayland headers; `registryHas` is the seam tests fake)
  and keeps `HYPRLAND_INSTANCE_SIGNATURE` as well, because an older Hyprland
  without the protocol blurs a translucent window by itself. GTK cannot know
  whether blur is *switched on* in the compositor, only whether it can blur.
- **Packages page: root never reads a user-writable path twice
  (#239, `pkglocal.go`).** `pkg-install-local H file...` is the most
  sensitive verb. Rules that must not be loosened: (1) each user file is
  opened once, through `openat(O_NOFOLLOW|O_DIRECTORY)` from the passwd home
  of `PKEXEC_UID` (never `$HOME`), every check on the open fd; copied into
  `/var/cache/archmaid/staging/run-*` (root 0700, on disk because `/run` is
  RAM and a package can be 2 GiB) while hashed; nothing reads the user's path
  again, and pacman is only ever given staged paths (a test fails on any
  path in the home). (2) The hash `H` is checked against the staged bytes
  *before* anything is parsed. (3) Parsing is `pacman -Qip`/`-Qlp`, not a
  tar reader of our own: the parser that checks is the parser that installs.
  (4) Dirs from the home down must be the user's and not group/other
  writable; a home or `~/.cache` mode 0775 (user-private-group umask 002)
  is refused with "writable by group or others": that is deliberate. (5)
  Staging is removed by `defer` (success, failure, panic); main catches and
  drops signals, so only SIGKILL leaves a `run-*` folder, which the next run
  removes under the helper lock. (6) The test seams `walkAnchor`,
  `fstatHook`, `afterOpen`, `afterStage` exist only in `Options` and are nil
  in production. (7) Known gap: a conflict with a name that an installed
  package only *provides* is not seen (as in `pkg-install`); `--noconfirm`
  answers pacman's conflict prompt with its default, No.
- **The GUI tests' display speaks GTK's private Broadway protocol
  (#162).** `gui/scripts/nullbroadway` implements the server side of
  gtk's `gdk/broadway/broadway-protocol.h` (request, reply and event
  layouts on a 64-bit ABI). A gtk4 update that changes that protocol ends
  the server with "unknown request type" or a short request, and every
  display test fails at once: compare the header of the new gtk with the
  constants in `server.go`. Don't swap it back for `gtk4-broadwayd`
  (1 fps without a browser, aborts with one) or weston (removed from the
  dev machine on purpose, 2026-10-16).

- **`internal/` APIs are safe by default.** A caller (the GUI) that forgets an
  option or a check gets the strict behaviour, never the permissive one; keep
  it that way, each guarantee has a regression test:
  - `downloads.Resolve` always guards the process's own `$HOME` and passwd
    home; `Options.Home`/`PasswdHome` only add homes to guard, and `--downloads`
    is refused when no home is known at all.
  - `audit.Result.Commit` is nil unless `audit.Run` completed: a failed,
    cancelled, abandoned (`Fetch.Cancel`, an aborted `Wait`) or hand-built
    Result can't rewrite the snapshot, and a save of no packages is refused.
  - `privilege.Request.Answer` refuses a request that `Request.Verify` hasn't
    passed (`ErrUnverified`). Only a `Server` builds a Request, and `Verify`
    takes only the step's pid and start time: the uid, binary and `/proc` it
    checks against aren't the caller's to choose. Other packages' tests use
    `privilege/privtest` (`NewRequest`, `FakeSystem`), which panics outside
    a test binary.
  - `proc.Runner.Start` (dry run included) and `proc.Query` refuse a sudo or
    sudo-rs whose first argument isn't `-n`/`--non-interactive`,
    `-A`/`--askpass` or a short-flag cluster with one of them
    (`ErrSudoMayPrompt`), also behind `env` or named in a shell's arguments;
    a sudo reached any other way (a script, a variable, `nice`…) isn't seen.
    Any child with a nil `Env` (sudo or not) runs without an inherited
    `SUDO_ASKPASS`, so a `sudo -A` behind `env`, a shell or topgrade never
    reaches a helper the user's shell exported.
  - `privilege.CheckPassword` (so `Authenticate` and `Answer`) refuses a
    password with `\r` or `\n`; su's lockout notice is sanitized and clipped.
  - `sanitize.Mode`'s zero value is `Plain`; colours need `KeepSGR`.
  - `logstore` writes only the four known categories (`Category.Valid`), and a
    store without an absolute state dir (unset/relative `$HOME`, relative
    `Dir`) refuses every read and write (`ErrNoStateDir`), never
    `./.local/state`.
  - The AUR's error text and `audit.ShortErr` go through
    `sanitize.OneLine`/`Clip` (cut by runes, never mid-character).
  - `proc.Read` reports an unparsable euid as -1, never 0 (root).

- **Steps run as plain children — never add `Setsid`.** sudo tickets are keyed to
  the tty *and* session. Per-child `Setsid` gave every internal sudo its own
  ticket (repeated popups during topgrade); a shared-pty attempt froze topgrade.
  Both are reverted in git. Plain children share the session, so the one primed
  ticket covers every step and everything topgrade/yay spawn. `TestNoSetsid`
  (`internal/proc`) holds the line: a started child shares our session and
  process group, and no non-test code under `cmd/` or `internal/` sets
  `SysProcAttr`'s `Setsid`, `Setpgid` or `Setctty` — except the root helper
  (`cmd/archmaid-helper`). That exception is deliberate: the helper runs as root
  through pkexec, runs no sudo, and *does* give every child its own session, so
  a Ctrl-C/Ctrl-Z/hangup at the terminal can't reach pacman mid-transaction.
  Don't "fix" one by copying the other. The GUI is the third case:
  `proc.Options.Detach` (`driver.Deps.Detach`, set only by
  `gui/internal/realdriver`) starts each step's child in a session of its
  own with no controlling terminal, so a GUI started from a shell can't
  have sudo prompt on that shell's terminal (sudo prefers a tty to
  `SUDO_ASKPASS`); Stop/Close then signal the child's whole process group
  and Close waits until it is gone. Its `Setsid` lives only in
  `internal/proc/detach.go`, which the guard exempts; `TestTUIDoesNotDetach`
  keeps the TUI off it. Under polkit topgrade is the only step that gets
  the askpass: the AUR-cache step runs `yay -Sc --aur` without sudo, and
  its `pacman -Sc` is a part of the helper's full-cleanup (see "Under
  polkit only topgrade asks"). Since the children are detached, a terminal's
  ctrl+c or hangup reaches only archmaid-gui, which treats SIGINT, SIGTERM
  and SIGHUP as Stop and Quit (`gui/internal/ui/signals.go`): the run is
  aborted and recorded, the groups stopped, and it exits with 128+signal. A
  second signal exits at once, once `driver.Driver.Aborted` says the record
  is written, after `driver.Driver.KillNow` has SIGKILLed every detached
  step's process group (no grace; a group already gone is never signalled),
  so nothing a run started outlives the GUI.
  **The GUI's pty (#129) is output only.** `proc.Options.Pty` (with Detach;
  `driver.Deps.Pty`, set only by `realdriver`) gives each step's child a pty
  slave as stdout and stderr, so tools colour and draw progress bars as in
  a terminal, as wide as the Run view (`ui.TermCols`). It is never the
  child's controlling terminal: Setsid stays and nothing sets `Setctty` or
  `TIOCSCTTY` (an inherited tty fd doesn't make one); stdin stays
  `/dev/null`. The rules that keep the step from taking the pty (review
  #135) are three: the slave is opened from the master (`TIOCGPTPEER`)
  **write-only** and `O_NOCTTY`, and its `/dev/pts/N` is **mode 0200**. The
  kernel makes a tty the controlling one only for a session leader that
  can read it (`TIOCSCTTY` on fd 1 is EPERM; an open by name without
  `O_NOCTTY` gives no terminal when it is write-only, and an open for
  reading is EACCES by the mode), and a read of fd 1 or 2 fails at once
  (EBADF), so a pager that reads its keys from fd 2 when `/dev/tty` fails
  (less; git or journalctl with stdout a terminal) gives up instead of
  hanging the step. Writes by name keep working: `>/dev/stderr`,
  `>/dev/stdout`, `>/proc/self/fd/1` (scripts, PKGBUILD builds under
  yay). Don't make the slave exclusive (`TIOCEXCL`): #129 did, and every
  one of those redirections failed with EBUSY. Root (sudo's pacman) passes
  the mode; nothing there opens a terminal by name as a session leader.
  So `/dev/tty` fails (ENXIO) and `tty_nr` is 0, which is where sudo looks
  for its tty: it still prompts only through `SUDO_ASKPASS`.
  `TestPtyIsOutputNotControllingTerminal`, `TestPtyOutputOnly`,
  `TestPtyReopenByNameNeverControls`, `TestPtyPagerReadFailsAtOnce`
  (proc), `TestRunPagePty` (realdriver) and the harness's sudo shim
  (`real-tty`: refuses `tty_nr != 0`, a tty stdin or an openable `/dev/tty`)
  hold it. **Residual (review #135, F3):** when sudo can't read
  `/proc/self/stat` it falls back to `ttyname()` of fd 0, 1 and 2, finds
  the pty on fd 1 and records it as the user's tty (in its log line and
  its time stamp). The prompt still goes through the askpass: `/dev/tty`
  can't be opened. **Residual (F5):** a step's child can rewrite its own
  last 64 lines (`vt.MaxRows`): what the Run page and the step's log keep
  is what it drew there last, so it can redraw an error line it printed
  away (never another step's output: one `vt.Screen` per child), and the
  dearest input (a line-wide `ESC[P`/`ESC[@`) costs about 0.16 µs per byte
  (`docs/design/run-pty.md`, "Cost").

  The pty is only in `internal/proc/pty.go` and only `Start` opens
  it (`TestChildrenNeverRunOnATerminal`): `Query`'s parsed commands keep
  pipes. The TUI never gets it (`TestTUIDoesNotDetach`). A tool that asks
  for a key once stdout is a terminal (topgrade's "Retry?" after a failed
  step) opens `/dev/tty`, fails and goes on with its default.
- **Auth is two-stage (`privilege.Sudo.Authenticate`).** One wrong `sudo -S -v` burns *two*
  `pam_faillock` failures (the wrong password plus sudo's retry hitting EOF), and
  `sudo -A` burns two as well — with Arch's `deny=3`, one typo from lockout. So
  the password is validated with `su -s /usr/bin/true <user>` first
  (`privilege.CheckPassword`: single-shot, su's stack has no faillock on Arch, and `-s` keeps
  the user's shell startup files out of it, which `su -c true` didn't); only a
  known-correct password reaches sudo. The mid-run askpass popup does the same
  (`privilege.Request.Answer`): a password su rejects is never written to sudo's
  connection, the popup stays up with the attempts left, and the third failure
  cancels the request and skips the queue's remaining sudo steps, like a
  failed prime. A password with a line break is refused before su (by the
  popup, and again by `Answer`): sudo reads one line, so it would get a
  different password than su checked. `Answer` is the only way a password
  reaches an askpass connection; don't add another.
- **Nothing may prompt on the raw tty: every sudo a step starts by name
  goes through archmaid's askpass popup (TUI, #153; the owner: "the TUI
  should always go through a sudo wrapper").** archmaid's own sudo steps
  run `sudo -A` by absolute path, and `SUDO_ASKPASS` routes any prompt into
  the TUI as a popup. topgrade, yay and makepkg start sudo **by name**, and
  in the TUI their sudo has the TUI's terminal: without a reusable ticket
  it would prompt there, under the full-screen UI (sudo is setuid root, so
  the terminal hold, `ttyguard`, can't stop it). So the TUI's askpass
  Server also writes a `sudo` of its own (`privilege/sudowrap.go`,
  `ServerOptions.SudoWrapper`): a 0700 file in a 0700 `bin/` inside the
  Server's private dir, and `Sudo.Env` puts that dir first on the `PATH` of
  every step that gets `SUDO_ASKPASS` (topgrade, and any `NeedsPriv`
  child). It `exec`s `sysbin.Sudo` by absolute path (fixed when written,
  never looked up on `PATH`) with `-A`, and sets `SUDO_ASKPASS` to the
  Server's askpass, so the real sudo's parent is still topgrade or yay
  (`Request.Verify` sees the same chain) and each password goes through
  `Request.Answer`'s su check first (faillock-safe). It adds nothing when
  the caller already said how sudo gets its password (`-A`/`--askpass`,
  `-S`/`--stdin`, `-n`/`--non-interactive`, also in a cluster such as
  `-nv`) or for a mode that never asks and takes no other option (`-K`,
  `-V`, `-h`); it reads sudo's own options only (to the command, `--` or
  an option it doesn't know, skipping an option's value: `-u -n` is the
  user "-n"), and passes every argument through as given. It is removed
  with the Server's dir when the TUI exits. Not written when `sysbin.Sudo`
  is a bare name (only under testenv: it would find itself on `PATH`).
  The GUI writes none: its children have no tty, and sudo uses the askpass
  by itself there (`TestGUIGetsNoSudoWrapper`).
  The ticket check stays: `Sudo.Authenticate` runs `sudo -n -v` right
  after `sudo -S -v` (never prompts, never counts toward faillock). On a
  ticket that can't be reused (`timestamp_timeout=0`,
  `timestamp_type=ppid`, ...) with the wrapper wired, the run goes on: it
  returns `privilege.NoTicketNote` with no error, the Session puts it on
  `AuthResolved{Granted: true, Detail}`, and the TUI shows it once above
  the running checklist ("sudo won't keep your password between prompts
  here, so archmaid asks for it in its own window each time sudo needs
  it."); each sudo then asks through its own popup. Without the wrapper
  (no askpass: no `XDG_RUNTIME_DIR`) it is `privilege.ErrNoTicket` as
  before: `AuthDone.NoTicket`, a Notice (`privilege.NoTicketMessage`), the
  run refused before any step. **Residuals:** (1) a program that runs
  sudo by **absolute path** bypasses the wrapper and can still prompt on
  the terminal. Checked by reading, never by running them: topgrade
  17.12.3 has no `/usr/bin/sudo` string in its binary; it looks its sudo
  up on `PATH` (the `which` crate: doas, please, sudo, ... in that order)
  and runs the path it found, so it finds the wrapper, unless
  `sudo_command` in `topgrade.toml` names a path (the owner's leaves it
  commented). yay 13.0.1 has no absolute sudo path either: it runs its
  `sudobin` (default `sudo`, Go's `exec.Command`, a `PATH` lookup) with
  its `sudoflags`; a `sudobin` set to a path in yay's config
  (`~/.config/yay/init.lua` on yay 13) would bypass it (the owner's sets
  no sudo option). makepkg (pacman 7.1) runs
  `PACMAN_AUTH` when set (`/etc/makepkg.conf`; commented here), else
  `sudo -k pacman …` by name when `type -p sudo` finds one (the wrapper:
  `-k` with a command ignores the ticket, so makepkg's sudo always asks,
  now in the popup), else `su root -c`; a `PACMAN_AUTH` naming a path,
  `doas` or `run0` bypasses it, as would topgrade choosing `doas` or
  `please` (installed before sudo on `PATH`): none is here. (2) The
  wrapper's dir is prepended, so it comes before every one of the user's
  own `PATH` dirs; a user's own `sudo` earlier on `PATH` (a wrapper of
  theirs) is no longer reached from a TUI step. (3) A sudo loop (yay's
  `sudoloop`, topgrade's `sudo_loop`; both off here) without a reusable
  ticket would raise a popup at every turn. (4) Repair's root recheck
  runs `sudo -n` (no askpass): without a reusable ticket it ends in its
  "Couldn't scan for broken packages" notice. (5) The wrapper is a file
  of the user's own, as `~/.local/bin` is: a process of the same uid
  could rewrite it mid-run (the #131 class, below). **Which steps get
  it (swept in #194, enforced by `TestEveryStepThatCanReachSudoGetsTheWrapper`
  and `TestQueuedStepsThatCanReachSudoAsk`):** every TUI step whose program
  can reach sudo has `Step.Asks()`: `NeedsPriv` (the `sudo …` steps,
  topgrade, and the **yay-cache step, `yay -Sc`, which runs
  `sudo pacman -Sc` itself**; it had neither flag until #194 and prompted
  on the tty without a reusable ticket; under polkit it is cleared, the
  step is `yay -Sc --aur` as the user) or `SudoByName` (hyprpm: elevates
  itself by name, is no root step: no up-front password, no root tag, may
  run last, not refused when the password was given up on; TUI only, the
  GUI's children have no tty). A step with a new program must be listed in
  the test's `sudoReaching` or `neverSudo`. The other steps (the audit,
  orphan probe, downloads, user cache's `bash -c rm`, Repair's
  `pacman -Qk`) run no sudo; the rest are `sudo …` steps by absolute path
  with `-A`. The password is
  passed straight through, never stored or logged, and never kept in a
  struct that outlives the call (`privilege.Request` holds no password).
  `sudo -n -v` is only the cache probe, the ticket check and the 60 s
  keep-alive (`Sudo.Probe`, `Sudo.Refresh`). Tests: `sudowrap_test.go`
  (the flag table, argv passed through exactly, exec with no shell
  between and a decoy later on `PATH` never run, modes, removal), the
  TUI's `TestTUISudoByNameAsksInThePopup` (the real TUI in a pty: a
  step's `sudo` by name reaches the popup, the answer reaches sudo after
  su, nothing reaches the terminal; with `timestamp_timeout=0` the run
  goes on with its note and one popup per sudo call), the
  `topgrade_ticket_unusable` transcript.
- **The TUI drops its sudo ticket on exit (owner's decision M1).** `main`
  calls `tui.DropSudoTicket` after `Finish` on every path: `sudo -k`
  (`privilege.DropTicket`: `sysbin.Sudo`, `SUDO_ASKPASS` dropped, stdin
  `/dev/null`, bounded at 3 s, its failure only reported), so your next
  manual `sudo` in that terminal asks again. Bubble Tea ends on q, SIGINT
  and SIGTERM; `tui.Run` makes SIGHUP quit it too instead of killing the
  process, so the drop runs then as well. A dry run primes nothing and
  drops nothing (so the e2e tapes, all dry runs, never see it).
  `TestTUIDropsTicketOnExit` holds every path, `TestDropTicket*` the call.
- **The TUI is not dumpable (`tui.DisableCoreDumps`).** The typed password
  lives in Go strings until the GC reuses them, so `main` calls
  `prctl(PR_SET_DUMPABLE, 0)`: no core file can hold it, and other
  processes of the user can't read the TUI's memory or ptrace-guarded
  `/proc` entries. Only after the `--askpass` relay dispatch: the askpass
  Server reads its client's `/proc/<pid>/exe` to verify it, so the relay
  must stay dumpable. Nothing else reads the TUI's own `/proc` (the Server
  reads its clients', sudo's and the step's; step children are dumpable
  again after exec). `TestDisableCoreDumps`, `TestAskpassSocketHonestNotDumpable`
  and the pty TUI test (`TestTTYChild` calls it as main does) hold it.
  The root helper is left dumpable on purpose: no password ever reaches
  it, and its `/proc` and any core of it are root's already.
- **No step child may open the TUI's terminal (`internal/ttyguard`).** A
  child's stdin is `/dev/null`, but it can still open `/dev/tty` itself:
  topgrade's `git pull` over HTTPS without credentials did (live VM run),
  and so do ssh (passphrase, host key) and pinentry-tty. It then read the
  same keys as Bubble Tea, they alternated between the two, and ctrl+c/y
  could become git's username. `tui.Run` therefore holds its stdin terminal
  in exclusive mode (`TIOCEXCL`) while it runs: the kernel refuses every
  new open of the terminal by a process without `CAP_SYS_ADMIN` (EBUSY), so
  such a child fails at once, as it would in the GUI with no terminal.
  Already-open fds (Bubble Tea's, the shell's) are untouched, and no
  process moves: same session, same process group, same controlling
  terminal, so sudo's ticket and `TestNoSetsid` are unaffected. Rejected:
  a background process group for steps (`Setpgid`) — a background read
  sends SIGTTIN to the reader's *whole group* (topgrade included), sudo
  with `use_pty` reads the user's tty itself, and a terminal hangup no
  longer reaches the steps; and environment tricks (`GIT_TERMINAL_PROMPT=0`,
  ssh `BatchMode`) — they cover one program each. Not covered: root
  (setuid sudo/su, and a command sudo runs without `use_pty`), which may
  still open the terminal. The editor (`editConfig`) is handed the terminal
  on purpose, so `handedTerminal` pauses the hold while it runs. Release
  happens when `Run` returns; a SIGKILLed TUI leaves the flag on until that
  terminal is closed (new opens of it fail). `TestStepCannotReadTheTerminal`
  runs the real TUI in a pty with a step that tries to read `/dev/tty`.
- **`SUDO_ASKPASS` goes only to steps that use sudo.** It is never set in
  archmaid's own environment: `privilege.Sudo.Command`/`Env` give it to `NeedsPriv` steps (sudo
  steps and topgrade) and strips it from every other child, including a value
  inherited from the user's shell. The socket and wrapper live in a private
  0700 directory under `XDG_RUNTIME_DIR`. Without that variable askpass stays
  off — no `/tmp` fallback — and sudo steps run `sudo -n` instead of `-A`:
  with `SUDO_ASKPASS` unset, `sudo -A` falls back to sudo.conf's
  `Path askpass` (on this machine seahorse's GUI ssh-askpass) or exits with
  "no askpass program specified", so `-n` (the primed ticket, or a clean
  failure) is the form that never prompts anywhere. This does **not** close
  the threat for topgrade: everything it starts — yay, makepkg, a PKGBUILD's
  `build()` — inherits `SUDO_ASKPASS` and can reach the socket. Topgrade stays
  archmaid's only update path, in the TUI and the GUI (there is no repo/AUR
  split), so the askpass peer checks below (F7b) are the control there. The
  GUI gives every step's child a pty as its output (#129, below); that
  doesn't change the sudo path. In the TUI the same steps (and only they)
  also get the sudo wrapper's dir first on their `PATH` ("Nothing may
  prompt on the raw tty"); a step without the askpass gets neither.
- **An askpass request is answered only from the running sudo step.** The
  server records each client's pid/uid (`SO_PEERCRED`) and start time; the
  Driver (`internal/driver`, `verify`) refuses a request while no step's
  child runs and checks it against that child, the Session hangs up unless
  the running step is a `NeedsPriv` child step, and the TUI only ever sees
  the `NeedsPassword` that follows. `Request.Verify` requires: our uid; the same start time as at accept (pid
  not reused); `/proc/<pid>/exe` = `os.Executable()` (the wrapper execs us);
  a parent that is sudo; and a parent chain that reaches the step's child,
  matched on pid *and* the start time `proc.Runner.Start` captured (`Child.Start`) before anything
  could reap it. A user can't read sudo's `/proc/<pid>/exe` (setuid root; it
  needs ptrace access), so "is sudo" is: exe = `/usr/bin/sudo` when readable,
  else euid 0 and comm `sudo`. A user process can't get euid 0, but comm comes
  from the executed file's name, so a symlink named `sudo` to another
  setuid-root binary would pass; none of Arch's starts a user program as its
  child without authenticating first.
  Refusals leave a line in the step's output. The popup names the step, the
  real binary and pid of sudo's caller, and sudo's prompt only quoted and
  sanitized (`sudo -p` sets it). Residual risk: code already running
  under topgrade can run `sudo -A` itself with its own askpass, connect from
  it, keep the socket in a forked child and exec archmaid in place — the peer
  then checks out. The popup's caller line is what gives that away: genuine
  requests come from topgrade or yay, that one from whatever ran sudo inside
  the build (typically `/usr/bin/bash`).
- **The GUI keeps the run's password (one prompt per run).** The owner's
  rule: a GUI run asks for the password once and keeps it in memory for
  that run only. The trust model:
  - *What it is:* the first password the user types in a GUI run, at
    topgrade's askpass prompt or at polkit's (below), once su has accepted
    it: kept right then, before it is written to sudo or the agent
    (`Driver.answerUser`, `Request.AnswerThen`), since polkit's verdict on
    it starts the next helper step at once, and that step's prompt must
    find it kept (kept after the write, it once went to the user instead:
    a second dialog). One kept that then can't be handed on (the agent
    went away) is dropped. It lives in `privilege.KeptPassword`: one mmap'd, mlocked,
    MADV_DONTDUMP page outside the Go heap, never written anywhere, never
    logged. Answers use a short-lived copy that is cleared at once. The
    `engine.Secret` string the user's answer arrived in is the existing
    accepted residual ("The GUI password's last copy"). archmaid-gui
    itself stays dumpable: `prctl(PR_SET_DUMPABLE, 0)` makes
    `/proc/<pid>/root` root's, xdg-desktop-portal can then no longer
    identify the GUI ("Portal operation not allowed: Unable to open
    /proc/<pid>/root"), and GTK loses its settings and header-bar layout
    (Dev 5's live test 7). Instead it sets RLIMIT_CORE to 1 byte, soft
    and hard (`ui.NoCoreFiles`, in main after the relay dispatch): with
    systemd-coredump's pipe core_pattern a limit of 0 would still stream
    the whole core to systemd-coredump, which only then drops it (systemd
    v262 coredump-submit.c: under a page, "Resource limits disable core
    dumping"), while 1 makes the kernel abort the dump before any helper
    runs (fs/coredump.c, coredump_pipe: "RLIMIT_CORE is set to 1, aborting
    core") and is below the minimum for a core file too. Go's own crashes
    write no core anyway unless GOTRACEBACK=crash; this covers that and a
    C-level abort in GTK. Children inherit the limit (a topgrade crashing
    under the GUI leaves no core). *Residual risk:* transient heap copies
    (the `engine.Secret` string, GTK's entry buffer, a kept answer's
    short-lived copy, su's stdin buffer) live in a dumpable process, so a
    same-uid process allowed to ptrace it could read them while they
    exist; `kernel.yama.ptrace_scope` decides that (2 on the owner's
    machine: admin-only attach). MADV_DONTDUMP covers the kept page only:
    the copies are in the Go and C heaps, which can't be marked page by
    page. The relay mode (`archmaid-gui --askpass`, and the TUI's)
    must be dumpable while the UI verifies it (it reads its `exe`), so
    `Request.Answer` runs a handshake: it says "verified", the relay makes
    itself non-dumpable (`hardenRelay`) and acknowledges, and only then is
    the password sent (`privilege.ReadAnswer`). No ack, no password.
  - *Who gets it, without asking:* (1) a sudo askpass request that
    `Request.Verify` passed against the running step's child (our uid, our
    helper, run by sudo, inside that child's tree), whose step is a
    `NeedsPriv` child, whose helper is in that child's session (Detach
    makes every step a session leader), and whose chain from sudo's caller
    up to the step's child is only `/usr/bin/topgrade` and `/usr/bin/yay`
    (`Requester.KeptMayAnswer`; pacman runs as root under sudo and never
    runs it). A sudo under makepkg, a PKGBUILD's `build()` shell or
    anything else gets the normal askpass dialog naming its caller, as
    before, and never the kept password. (2) polkit, through pkttyagent
    registered for archmaid-gui's own process (`gui/internal/polkitagent`),
    only while a helper step runs, only for an `org.archmaid.helper.*`
    action, and only for the current user's identity (an identity menu
    picks the user's entry). Anything else gets the normal dialog or is
    cancelled; never the kept password. Accepted (Dev 5's live finding,
    the lead and Dev 3): the allowlist trusts topgrade and yay as
    configured. Whatever topgrade runs as its own child with sudo
    (`[commands]`, `pre_commands` and the like in topgrade.toml) gets the
    kept password; anything run under makepkg or another program gets the
    dialog naming it. topgrade.toml is the owner's own config, writable
    only by the same uid. The dialogs say which: sudo's and polkit's are
    told apart by the prompt's data (`NeedsPassword.Polkit`, `.Action`),
    never by the Requester string; polkit's is titled "Authorise
    archmaid's Root Steps" with the action id shown verbatim.
  - *How long:* until the run ends, however it ends (Finished of any
    outcome, Stop, Close: Stop and Quit, a signal; `KillNow`), and at most
    60 min (`Deps.KeepFor`). Every Driver is one run; nothing carries over.
  - *Faillock:* every kept answer is su-checked first, as typed answers
    are, under the run's context (Close stops a su in flight). su on Arch
    has no pam_faillock, so a wrong kept password costs 0 faillock
    attempts; each su check leaves a few pam_unix session lines in the
    journal (`su: pam_unix(su:session): session opened/closed for user
    …`), a handful per run. A kept password su refuses, a sudo process that
    asks a second time (it refused what it got), or a polkit failure drops
    it for the rest of the run (`Fail`), and that request goes to the
    user. So a known-bad password is never sent twice.
  - *polkit:* `--notify-fd` makes the registration precede pkexec (whose
    subject is archmaid-gui, its caller), and polkitd prefers the
    process-scoped agent to polkit-gnome. Right after the registration
    the agent's pty is made exclusive (TIOCEXCL): nobody can open it any
    more; before a password is written, `/proc` is checked for any other
    process still holding it (opened before the lock), and if one does
    nothing is written (`ErrTTYShared`). The check (`procView.holders`,
    a seam for a fake `/proc`) tells the pty apart by its device numbers,
    never by the name its fd link shows: `statx` of each fd's link gives
    the devpts instance (`st_dev`) and the index (`st_rdev`), compared with
    the pty's own; another devpts instance's `/dev/pts/N` (a flatpak
    sandbox's) is not ours, our pty under another name (a bind mount in
    another mount namespace) is. Any `/dev/tty` node (5:0) counts only for
    a process in pkttyagent's session (its controlling terminal). The
    statx reads cached attributes only (`AT_STATX_DONT_SYNC`, empty mask),
    so a hung FUSE or network server can't hang it. A process or fd that is
    gone by the time it is read (ENOENT, ESRCH: reaped, closed) holds
    nothing; a link the kernel won't let us follow (EACCES/EPERM: the
    user's `systemd --user` has CAP_WAKE_ALARM, so its fds are listed but
    unreadable) is trusted like an fd table we may not list (root's,
    another uid's), as before (failing closed there refused every prompt);
    any other error counts as a holder. What `/proc` can't show isn't
    checked: a process of our uid that made itself non-dumpable, or a
    thread with an fd table of its own (or a process whose main thread
    has exited), that opened the pty in the registration window before
    the lock (open, Dev 3 #91, for the lead). The password is written only once
    the listener has turned echo off (it flushes earlier input). A request
    that isn't ours to answer (another action, another identity) and a
    second hidden prompt in one request are cancelled at the listener
    (an end of file, or an invalid identity choice). An invalid choice
    leaves pkttyagent up. An end of file spends it: the listener reads with
    stdio, whose EOF flag is sticky, so every later read would fail. That
    pkttyagent is stopped once the request has ended, and while the helper
    call still waits a new one is registered (at most 3 times, then the
    desktop's agent with the status-line notice), so a refused foreign
    request with archmaid-gui as its subject doesn't fail a request of ours
    that follows. Accepted residual (Dev 3, LOW): between stopping a spent
    agent and registering the next, a request goes to the desktop's dialog
    without the status-line notice; it fails safe.

    *Accepted residual (owner, 2026-10-12; Dev 3, from Dev 2's #77
    finding and polkit 127's source):* a request of ours that reaches the spent
    pkttyagent is refused without any dialog. **What:** polkitd allows one
    agent per subject and routes our pkexec's request to whichever agent is
    registered for archmaid-gui when it arrives. The text listener serves
    D-Bus on its own thread (`POLKIT_AGENT_REGISTER_FLAGS_RUN_IN_THREAD`,
    `pkttyagent.c`) and blocks that thread in `getc` while a password prompt
    is up (`on_request`), so our `BeginAuthentication` waits behind the
    foreign request. **When:** our request reaches polkitd while a foreign
    request for archmaid-gui's subject has our agent (its prompt is up for
    the few ms until our end of file), or in the ~10 ms after that request
    ended and before the spent pkttyagent has exited; it needs a foreign
    polkit request with archmaid-gui as its subject during one of our helper
    calls, which nothing on the owner's desktop is known to make. **Effect:**
    the spent listener prints our banner, starts a PAM session, and its
    sticky end of file cancels it at once: no dialog, pkexec exits 127, the
    step(s) read "Not authorised" and nothing runs as root (fails safe).
    It also costs one `pam_faillock` row the user never saw a dialog for
    (the session was cancelled after PAM began, as above). Once the agent
    has exited, a request polkitd still routes to it fails without PAM (no
    row); one that comes after the next agent registered gets our dialog.
    **Why not closed:** archmaid doesn't decide when pkexec's request
    reaches polkitd or when a foreign request comes; the agent exists only
    during our helper call, so there is no "next pkexec" to hold back until
    the spent agent is gone and the new one registered. A retry of a call
    refused without a dialog of ours would close the user-visible part, but
    it can't tell that case from a refusal at the desktop's dialog in the
    gap above (it would ask again right after the user cancelled there) and
    it bypasses the engine's faillock read; not small. **Decision:** the
    owner accepted the refusal and its hidden row on 2026-10-12. If the
    hidden row ever matters, cancel a foreign request
    at its password prompt by stopping pkttyagent instead of an end of file
    (its thread is still blocked in `getc`, so our queued request is never
    served by a spent listener: refused without PAM, no row), which also
    changes the foreign-request tests (`agenttest`, the GUI harness's
    foreign-first case). Without pkttyagent, or if
    it can't register, polkit asks through the desktop's agent as before,
    and the status line says so. Which desktop agent that is depends on
    who registered first: DMS (DankMaterialShell) has its own polkit
    agent and registers whenever polkit-gnome isn't registered when DMS
    (re)starts. On a normal login polkit-gnome wins (`dms run` starts
    first, but polkit-gnome registers first); a `dms restart` after
    polkit-gnome died makes DMS the agent, and its "Authentication
    Required" dialog (window class `com.danklinux.dms`) then asks for the
    fallback path (evidence: VM check b4c-noagent-B). With no agent at all, pkexec (started
    with `--disable-internal-agent`, `helperclient.DisableInternalAgent`)
    asks nobody and exits 127 with "No authentication agent found": the
    step is AuthRefused with `engine.ErrNoAuthAgent` as its text, so its
    row reads "Not authorised · no polkit agent is running, so the password
    can't be asked", not as a refused password. Without the flag pkexec
    would start its built-in text agent on the terminal the GUI was
    started from, which on polkit 126+ can't work: its socket-activated
    `polkit-agent-helper-1` replies as uid 0 (setuid pkexec), polkitd
    expects the user's uid, and a correct password ends in "No session for
    cookie" (`docs/polkit-keep-session-check.md`). pkttyagent dying mid-authentication drops
    the kept password (fail-safe). pkttyagent runs in a session of its own
    on our pty (`Setsid`, `Setctty`): it lives in the GUI module, outside
    TestNoSetsid's scan, and is never a step's child. The TUI keeps none
    of this.
- **archmaid doesn't trust PATH for privilege (#131).** Two halves:
  - *What archmaid starts itself* runs by absolute `/usr/bin` path,
    whatever `PATH` the session has (`internal/sysbin`): `su` (the
    password check), `sudo` (probe, `-S -v`, keep-alive, `-k`, every
    sudo step through `Sudo.Command`, Repair's root recheck), `pkexec`
    (`helperclient.Run`), `pkttyagent` (`polkitagent.Start`) and the
    update step's `topgrade` (`Sudo.Command` maps the step's name; the
    step keeps showing `topgrade -y`). Before, a `su` or `sudo` earlier on
    `PATH` (`~/.local/bin`, a relative entry) was handed the typed
    password, and a `pkttyagent` there read polkit's. The paths are
    variables so tests can still trap them: `testenv`'s `init` sets each
    back to its bare name in every process of a test binary (an init,
    because a re-exec'd child such as the GUI harness's window never calls
    `testenv.Run`, and a variable `Run` set would not cross the exec), so
    `PATH`'s traps and shims decide exactly as before; `Run` refuses to
    start with one absolute. The e2e harness builds the real binary with
    them bare (`-ldflags -X`; before any tape it reads the values the
    binary links with `scripts/sysbinvals`, from the ELF file, never
    running it, since `-trimpath` keeps `-ldflags` out of `go version
    -m`); `check.sh` fails a PKGBUILD, `install.sh` (the owner's
    `~/.local/bin` build) or cover build that names `sysbin`, a PKGBUILD
    or `install.sh` that sets any `-X`, and a main package that links
    `testenv` (a `go list` that fails fails the gate, here and in the
    Makefile's GUI check, rather than reading as "not linked"). Guards:
    `TestNoBarePrivilegedExec` (no bare privileged name as an exec call's
    program in product code), `TestSysbinDefaultsAbsolute`,
    `TestE2EBuildsBareSysbin`, and one test per call site that runs a shim
    at the sysbin path with a decoy first on `PATH` (each failed before).
    `TestNoBarePrivilegedExec` reads a call's first two arguments (and a
    composite literal's `Name`/`Path`) as the compiler folds them
    (`go/types`, `internal/testenv/barescan_test.go`): a literal, a named
    or typed constant from any archmaid package, a concatenation, a
    conversion to string; a string variable assigned a bare name anywhere
    in its package; and a shell (`sh`, `bash`…) given `-c` with a script
    that names one as a word. A value of a type of its own
    (`logstore.Category`'s `"topgrade"`) counts only once converted to a
    string. Its exemptions: `bareAllowed` per call site, and
    `steps.IDTopgrade` everywhere (`bareConstAllowed`: a step's ID); an
    entry that matches nothing fails the test. *Known limits:* a value
    reaching the call through a struct field, a function's result, a
    parameter or a range variable; a string built at run time (`Sprintf`,
    `Join`); a `-c` script in a step's `Argv` or a `proc.Spec`'s `Args`
    rather than in the call's own arguments; an absolute path is
    `TestNoAbsolutePrivilegedExec`'s.
  - *What a step's child starts by name* (topgrade's yay, yay's and
    makepkg's `sudo`) still goes through the step's `PATH`; archmaid
    can't change how those tools look sudo up. (In the TUI the first
    `sudo` on that `PATH` is archmaid's own wrapper, which execs
    `sysbin.Sudo`: "Nothing may prompt on the raw tty". The guard below
    takes it for what it is, sudo's boundary, but only that file at that
    path as the process wrote it: same file by device and inode, a 0700
    regular file, same contents; a file put in its place, a copy, a
    symlink to it or one left after its Server closed counts as a shadow
    (`TestPathShadowsOwnSudoWrapper`, `TestPathGuardOwnSudoWrapper`). The
    guard only runs under `KeepPassword`, the GUI's, which writes no
    wrapper, so today the two never meet.) The risk is the kept
    password: a script named `sudo` before `/usr/bin/sudo` that execs the
    real sudo with a command of its own (`exec /usr/bin/sudo -A sh -c
    '…'`) *is* sudo run by yay once it has exec'd, so its request passes
    `Request.Verify` and `KeptMayAnswer`, and the kept password would
    answer it with no dialog. So when a `NeedsPriv` child step starts
    under a Driver that keeps the password, the Driver scans the `PATH`
    the child will get (`privilege.ChildPATH`, `PathShadows`;
    `internal/driver/pathguard.go`, one call in `startStep`) for `sudo`,
    `sudo-rs`, `doas`, `run0`, `please`, `su` and `pkexec`. Each name has
    its own boundary (#136): the first directory that holds the system's
    own file of that name, the same file (not followed) as the one in
    `/usr/bin` (`/bin` and `/usr/sbin` count as `/usr/bin`; for sudo,
    `sysbin.Sudo` itself). Before it, a file of that name counts; after
    it, execvp finds the real one first. A name `/usr/bin` lacks (`doas`,
    `please`, `sudo-rs` here) is searched on the whole `PATH`: topgrade
    tries `doas` and `please` before `sudo`, wherever they are. A
    relative entry counts by itself while any name's boundary is still to
    come (yay builds in the package's own folder), and so does an empty
    `PATH` (`PATH=`: execvp searches the current folder). Conservative:
    any symlink with such a name counts (to `/usr/bin/sudo` too: its
    target can change mid-run; `/usr/bin/run0`, the system's own link to
    `systemd-run`, is the boundary), a regular file counts if it has an
    execute bit; a directory or a non-executable file doesn't. A
    root-owned `/usr/local/bin/sudo` counts too. If anything
    is found, the kept password is off for the rest of the run
    (`KeptPassword.Fail`): every prompt, sudo's and polkit's, gets the
    dialog naming its caller. One WARN line in the step's log and one
    line at the top of its output name what was found. The scan runs on
    the loop before the child starts, bounded at 2 s (a hung network or
    FUSE `PATH` entry): one that takes longer counts as found.
    `internal/driver/pathguard_test.go` holds it on a real child with the
    askpass (shadow before: off, WARN, dialog; a sudo after, not
    executable: no effect; symlink to the real sudo, other names, a
    relative entry, a `doas` after `/usr/bin` when it lacks one, a scan
    timeout: off; only a real `NeedsPriv` child under `KeepPassword`; the
    owner's `PATH` shape: on). Its stand-in `/usr/bin` holds every name,
    as Arch's holds `su`, `pkexec` and `run0`.
  - *Not covered* (open, Dev 3): a file planted on `PATH` after the scan,
    while the step runs. That needs a process of the user's own uid
    acting mid-run. Rejected for now: the *pin dir* (a private dir of
    symlinks to `/usr/bin/sudo` and the rest, first on the child's
    `PATH`). It covers files that appear later in the user's dirs, but
    the pin dir is the same uid's to rewrite, and the child's environment,
    which names it, is readable to that uid, so it moves the race rather
    than closing it. It also changes the `PATH` every program under
    topgrade sees, needs making and removing per run (a SIGKILLed GUI
    leaves it behind), and pins only the names on its list. Also rejected
    (review #134): re-running the scan right before each kept answer
    (`askpassRequest`, `agentRequest`). By the time a planted sudo's
    askpass request arrives it has exec'd the real sudo, and it can have
    removed itself first, so the second scan sees a clean `PATH` and the
    kept password answers all the same; it would only catch a planter
    that leaves its file in place, for a second call site in `keep.go`
    and a scan per answer. *Accepted residual, the same class* (review
    #134 N5): a directory before `/usr/bin` that is a user's symlink to it
    (`~/bin -> /usr/bin`) passes the same-file check as `/usr/bin` itself,
    and the user can repoint it mid-run. A hard link to sudo can't be made
    (`protected_hardlinks`). Since #136 a `/usr/bin/sudo` that is itself
    a symlink (sudo-rs installed as sudo) has its boundary at `/usr/bin`
    (the system's own link); before, no boundary was found and the kept
    password was always off. (The dialog's kept-password
    note once stayed "kept" after the guard tripped; fixed in #147:
    `NeedsPassword.Keeps/KeepOff` make the dialog say it is not keeping
    the password, and why.)
- **Repair scans as the user first, and asks for sudo only to recheck what it
  flagged.** Repair's discovery (`internal/actions`, `discoverRepair`) lists
  the installed packages (`pacman -Qq`) and runs `pacman -Qkq -- <chunk>` as
  the user, in up to `Env.RepairWorkers` (min(cores, 16)) parallel chunks,
  exec'd directly. Nothing flagged is the "No broken packages" notice with no
  probe and no prompt. Otherwise it returns `NeedsRoot`, which the engine
  (`internal/engine/auth.go`) authenticates like any
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
- **GUI: widgets only on the GTK main thread.** Everything from the Driver
  reaches the window through `runview.Pump`, which posts with glib's idle-add;
  nothing else may touch a widget from another goroutine. `Pump` never blocks
  (so `Driver.Close` can't deadlock against it), and closing the window hides
  it, closes the Driver off the main thread (a real Driver may wait out its
  kill grace), then destroys it; `Run` closes the Driver again after the loop
  in case `app.Quit` ended it. **An `AdwAlertDialog` closes before it
  responds:** a clicked button runs `adw_dialog_close` (its `closed` signal)
  and only then emits `response`; Escape or the window's close emit the
  close response from `closed`. So act on `response`, never on `closed`:
  the close-mid-run dialog's Stop and Quit calls `closeRequest` directly
  from its response (it once waited for `closed`, saw no answer yet, and
  the window stayed up with no record and pkexec still waiting), and the
  password dialog wipes its entry at `closed` only from an idle, after the
  response has read it (wiping at `closed` sent an empty password for a
  click on Authenticate). The harness clicks the buttons
  (`findButton`); `Emit("response")` then `ForceClose` is the reverse
  order and hid both bugs. That order is the AdwAlertDialog's, so it holds
  for the password dialogs and the quit question only. **A wide dialog
  (`widedialog.go`, an `AdwDialog`) has no response of its own:** its
  button sends the response, then closes it; Escape, the window's close
  (libadwaita closes the open dialog first, the window stays) and a
  `ForceClose` emit only `closed`, which `newWideDialog` turns into the
  close response. `amDialog.respond` answers once per dialog, so neither
  path can answer twice, and a dialog closed without a button still
  answers (else the engine waits for ever). `TestGUICovDialogClose` holds
  both (a mutant without the `closed` bridge sends nothing; one without the
  once-guard responds yes then no). While a helper step's pkexec waits for polkit's
  dialog (`WaitPolkit`), the X doesn't ask at all: the agent's window
  covers ours, so the question would open under it, and nothing of that
  step runs as root yet; it is Stop and Quit at once (pkexec killed, the
  run recorded aborted).
- **GUI: nothing may be wider than 360 px at its minimum.** Libadwaita
  lets the window go down to 360 px whatever its content asks; wider
  content is cut off on the right (the header title, chips, Stop, the
  toast) and logged as "AdwToastOverlay … exceeds AdwApplicationWindow
  width". A label that can be long wraps or ellipsizes (with the whole
  text as its tooltip); a title with tags is a `tagLine` (a box that
  `setNarrow` turns: above the narrow breakpoint the tags at the line's
  end, under it on their own line under the title, starting where the
  title does, on every row alike; an `AdwWrapBox` moved them only where
  the title didn't fit, so a narrow plan zig-zagged, the owner's r532
  screenshot). Rows the list makes later (the Run page's step headers)
  take `w.narrow` when built; anything new that changes with the width
  and isn't a breakpoint setter goes through `setNarrow`. A dialog's rows
  follow the dialog's own breakpoint instead (`amDialog.onNarrow`): the
  gate's rail is a `tagLine` per step, its tags under the title in the
  bottom sheet (Dev 5's VM round b6 had them at the line's end at 380 px). A wrapping
  label needs `wrapNatural`: by default its natural width is its longest
  word, so a box that gives it its natural width wraps it at every word.
  A label with the kicker's letter-spacing wraps its last word even at its
  natural width (and overlaps the line under it), so the Run page's kicker
  expands. A label that can hold one long word (a package name, a path, an
  error) also needs `WrapWordChar`: with the default `WrapWord` the word's
  length is the label's minimum width (a 90-letter word made the Run page
  849 px wide, a dialog's item list 1831 px; the VM's real orphan cascade,
  one "name version" per line, made the orphans dialog 392 px). A tag
  beside a title or a state line that wraps is valigned (the last-run
  chip: centre; a `tagLine`'s tags: the first line's baseline), or it
  stretches to their 2-3 lines (Dev 5's VM screenshot of a refused root
  row). A flow box keeps a line for a child whose content is hidden: hide
  its `FlowBoxChild` too (`setShown`). `TestGUIMinWidth` measures
  the real content at 360/480/960 px (the orphans dialog with the VM's
  real plan) and `TestGUIMinWidthStates` the
  long-text states (failed and refused rows, dialogs, discovery, Settings,
  Logs) at 360/480 px; both name the widest widget when something is too
  wide, and both check where the tags sit (`checkTags`).
- **GUI: how wide the dialogs get (task #95).** A wide dialog
  (`newWideDialog`) has breakpoints, so like an `AdwBreakpointBin` it has
  no minimum of its own: `SetSizeRequest(wideMinW, wideMinH)` (300x200)
  must fit the smallest window, and content wider than the dialog is
  clipped, not reported, so only the stacked layout under its
  `max-width: 640sp` breakpoint keeps it inside (`columns`; the min-width
  tests measure after it applied). Its height: libadwaita measures it
  once, at the content width, and keeps that, so a bottom sheet kept the
  wide layout's height (scrolling with room to spare) and the wide one got
  a gap; `fitHeight` sets the content height to the content's natural
  height at the width it got, on the map and each allocation change of
  the middle. It sums the heading's, the middle's and the button bar's
  heights each measured at that width: the toolbar view's own measure
  takes its bars at one line, so a wrapped heading (a long path) or a
  wrapped button label left the middle a negative height (a GTK warning
  the min-width tests fail on). `show` opens a wide dialog at the
  window's height first, for the same reason, and `fitHeight` shrinks it.
  **One scroller in the middle (task #101).** A scrolled window's minimum
  is a few rows, so a capped list (a `ScrolledWindow` with a max content
  height) inside the scrolling middle shrank in a low window instead of
  the middle scrolling: the gate's 20-step rail to ~3 steps, the
  orphans' lists to 110 px each at 620x500 (Dev 2's #96), with the middle
  never scrolling. So the gate's rail has no scroller of its own (it is
  whole; a long plan scrolls with the middle, so it gets the window's
  height), and every list in a middle is registered with `capList`:
  `fitHeight` caps them (orphans, downloads and the pick list at 460 px,
  the report at 360) while the dialog, capped, gets its whole height, and
  otherwise lets each take its whole height (vertical policy `Never`) so
  only the middle scrolls. A new window size is tried capped first. The
  test is the dialog's own height, not the middle's adjustment: with the
  lists shrinking, the middle never overflowed.
  Button labels wrap (`wrapNatural`): "Clear everything under that
  folder" in a homogeneous row was 566 px.
  A column's labels need a natural width under the column's (the notes'
  `SetMaxWidthChars`), or measure and allocation split the row
  differently. The password dialog stays an AdwAlertDialog, which
  libadwaita sizes as `clamp(content, 300, cap)` where, with
  prefer-wide-layout, cap is its button row's width (two buttons, 12 px
  between, 48 px padding) up to 600 px, and 372 px once the row is wider
  than 600: labels with `wrapNatural` alone left it at 328 px. So
  `.am-password`'s buttons get `min-width: 230px` over the window's
  narrow breakpoint (600 px measured at 1460x930); widen them past ~270 px
  and it drops back to 372. `TestGUIWideDialogs` (1460x930 and 380x720,
  on a 1600x1000 headless screen) holds the sizes.
- **The GUI password's last copy (accepted risk).** `engine.Secret` is a Go
  string (E0 is frozen), so the password typed into the GUI ends in one
  immutable string that can't be zeroed and stays in the heap until the GC
  reuses it. Everything before it is wiped: the entry is read as C bytes
  (`gui/internal/ui/secret.go`), never a gotk4 Go string, then cleared, and
  the bytes are zeroed once the `Secret` is built. The lead accepted the
  rest: a same-uid attacker who can read the process's memory can read the
  entry or the sudo child just as well.
- **`hyprpm` runs last.** It drops the sudo ticket on exit, silently breaking the
  keep-alive; Full Cleanup runs topgrade with `-y --no-retry --disable hyprpm` (--no-retry: a failed step ends the run instead of asking Retry? on a stdin nobody feeds) and appends its
  own final step. `TestNoPrivilegedStepAfterHyprpm` (`internal/actions`) fails
  any action whose queue has a root step, or any in-process step (it may insert
  one, as the orphan probe does), after hyprpm.
- **Full Cleanup's order:** AUR audit → topgrade → `paccache -r` (keeps 3;
  no "delete the entire cache" step since 2026-10-18, see "Delete entire
  package cache" below) → orphan probe → (orphan removal, inserted by the probe) →
  `yay -Sc` → tmpfiles → journal vacuum → `pacman -Qkq` → hyprpm. Under
  polkit (the GUI) it is AUR audit → topgrade → orphan probe → (orphan
  removal) → paccache → `pacman -Sc` → tmpfiles → journal →
  integrity → `yay -Sc --aur` → hyprpm, the root steps as one call (next
  entry). Building the
  queue runs nothing; the only work `activate` starts is the audit prefetch,
  off the UI thread. `BenchmarkActivateFullCleanup` (~5 µs) holds that in the
  opt-in perf guard, with a fixed 1 ms limit.
- **Clear caches is one menu action; the wipe is not a Full Cleanup step
  (owner's decisions 2026-10-18, tasks #186 and #199).** Step 04's
  `paccache -r` (keep 3) is the cache cleaning. `actions.IDClearCaches`
  ("Clear caches", `internal/actions/clearcaches.go`) replaced `IDUserCache`
  and `IDWipeCache`, which stay as constants only for `LegacyLabel` (old
  last-run.json and history records; the DMS `state.js` keeps both labels
  too). It is a Discover action, in the TUI menu and the GUI Overview, not
  in the DMS widget. The discovery measures both caches as the user, in
  parallel and read-only: `~/.cache` with `sysstat.EntriesUsage` (10 s
  bound; a timeout leaves the row pickable as "size unknown") and the
  package cache with `paccache --nocolor -dk0` (60 s bound), parsed by
  `report.ParsePaccacheDry` (strict: a hostile line is a failed
  measurement) and formatted by `report.HumanBytes`. It returns a
  `NeedsSelection` of two rows with `Items` `~/.cache` and `pkgcache` and
  `Rows`, nothing ticked; the package-cache row's `Note` is the warning and
  must stay visible. A 0-byte or failed row is `Disabled` with its reason
  (the engine ignores a pick of it); both disabled is a Notice (log category
  `pkgcache`).
  **The pick is the destructive confirm**: `ClearCachesSteps(picked)` builds
  the steps with no `Confirm`, `~/.cache` first (as the user), then
  `steps.IDPaccacheWipe` / `VerbPaccacheWipe` (`sudo paccache -rk0` in the
  TUI; the helper's standalone `paccache-wipe` verb under polkit, no
  arguments, `auth_admin`, so the policy and the `pkaction` count (9) are
  unchanged). **Password ordering:** nothing privileged happens before the
  pick (the Session only discovers). `Select` calls `plan` with the queue:
  a queue without the wipe has no root step and runs at once with no probe,
  faillock read or password; a queue with it goes the normal up-front way
  (Probe, then the faillock read, then the prompt, keep-alive only after
  auth), so #197's guarantees hold. `lateAuth` (a lone confirmed root step)
  no longer applies to this action, since its step has no confirm.
  **Both ticked (#204, `lateRoot`):** `~/.cache` runs first, with no probe,
  faillock read or password; `advance` settles root (Probe, then the
  faillock read, then the prompt, keep-alive in `lateGo`) when it reaches the
  first `NeedsPriv` step, the same machinery as `lateAuth`'s late path
  (`lateGoing`). `plan` sets `lateRoot` only for a queue built by `selected`
  (`pickQueue`) with no gate, at least two steps, a first step that needs no
  root and no root step with a confirm, `Run` or `Combine`: today only
  [`~/.cache`, wipe]. The wipe alone is still probed up front; Full Cleanup
  and every other queue are untouched. Cancelling the late prompt
  (`CancelPrompt` after a step ran) skips the rest and finishes
  `OutcomeAborted` (the record says aborted, `~/.cache` stays a done step);
  three wrong passwords refuse the wipe as in the up-front path. The TUI's
  password popup then sits over the running screen (`bg = screenRunning`) and
  returns to it when the check ends.
  An empty pick or a cancel ends cancelled with no record.
  `full-cleanup` no longer has `--wipe`: the helper rejects it as an unknown
  flag, and `helperclient` rejects a wipe part inside a combined call.
- **One polkit dialog per Full Cleanup run (owner's decision E).** The
  TUI asks one sudo password per run; the GUI must ask polkit once, not
  once per root step (the first live GUI run showed five dialogs). So
  under polkit Full Cleanup's root steps (orphan removal, paccache,
  `pacman -Sc`, tmpfiles, journal, integrity) are consecutive and carry
  `Step.Combine = full-cleanup` (`actions.polkitFullCleanup`), and the
  engine (`combine.go`) runs such a run as ONE helper call: it asks every
  confirm among them first (the orphan list, bound by its plan hash),
  reads faillock once, then one `StartStep` whose Step has
  `Parts`; `helperclient` builds `pkexec --disable-internal-agent
  archmaid-helper full-cleanup --keep 3 --journal 2weeks [--orphans H] [--uninstalled]` from the parts and
  maps the helper's per-part `progress`/`done` events back to
  `PartStarted`/`PartDone`, so every row still starts and ends on its own.
  A refused dialog marks exactly the steps it covered AuthRefused; the run
  goes on with `yay -Sc` and hyprpm. Nothing is weakened: `full-cleanup`
  is its own `auth_admin` action with its own argv1 (remove-orphans'
  strictness carries over), the helper still parses its flags strictly
  and recomputes the orphan plan as root, and the client accepts only the
  helper's part order. Keep: the probe right after topgrade (the removal
  it inserts must be the call's first part), and the part order in three places
  in step (helper `runFullCleanup`, `helperclient.fullCleanupOrder`,
  `actions.fullCleanupRoot`; `TestFullCleanupTiesToClient` ties them).
  Trade-off: once the dialog is granted, an Abort can't stop the parts
  still to come (the helper runs as root; the per-step design stopped
  before the next step's dialog). The polkit message is fixed text, so the
  in-app confirm is what says whether orphans are included.
  A GUI dry run takes the same route without asking: the engine combines
  under polkit in a dry run too (no faillock read, no polkit wait), and the
  Driver hands a dry-run helper step to `helperclient.DryRun`, which prints
  the exact `pkexec … archmaid-helper …` line `Argv` builds (a combined
  call's once, then one line per part on its own row) and starts nothing.
  Before this, a GUI dry run printed the TUI's `sudo …` commands
  (`TestFullCleanupPolkitDryRun`).
- **Under polkit only topgrade asks for a password (besides the dialog).**
  Without a terminal sudo's ticket is keyed on the parent process, so
  every tool that runs sudo asks through askpass again; no password is
  stored. yay -Sc did (its `sudo pacman -Sc`), so under polkit it is split:
  `pacman -Sc` is the helper's `clean-uninstalled` part
  (`steps.IDPkgCacheUninstalled`, Verb `full-cleanup`, no verb or polkit
  action of its own; after the orphan removal, so their packages go too),
  and the AUR cache step runs `yay -Sc --aur --noconfirm`. In yay (v13.0.1:
  `clean.go` `syncClean`, `parser.go` `NeedRoot`) `--aur` is the AUR mode,
  in which `-Sc` runs no pacman and needs no root (no sudo loop either): it
  cleans only yay's build dir, as the user. topgrade stays on askpass:
  its sudo calls come from yay and pacman inside it.
  `TestOnlyTopgradeAsksUnderPolkit`, the Driver's
  `TestFullCleanupPolkitOneDialog` (no sudo) and the GUI harness's tty
  case (one askpass dialog) hold it.
- **The helper's `pacman -Sc` part removes pacman's empty download
  leftovers first, under pacman's own lock (tasks #116, #122).** pacman 7
  downloads as its `DownloadUser` into a `download-XXXXXX` folder in the
  cache and removes it only after an actual download: an interrupted one
  leaves it, and so does every transaction that downloaded nothing
  (libalpm 7.1's `download_files` makes it before it knows; yay's
  `pacman -U` of a built AUR package, a `-Syu` with everything cached).
  `-Sc` then printed one red `error: could not open file
  …/download-XXXXXX` per folder, and Full Cleanup's own topgrade leaves
  fresh ones on most runs (Dev 5, VM rounds b8 and 8b).
  `clean-uninstalled` (`helper.tidyDownloadLeftovers`, `leftovers.go`)
  rmdirs only what it can prove abandoned: a direct child of a cache dir
  `pacman-conf CacheDir` names (never the caller's path), that dir opened
  `O_NOFOLLOW` and only root-writable, the exact mkdtemp name, and, while
  the helper holds `db.lck` (`holdPacmanLock`: created as libalpm does,
  `O_WRONLY|O_CREAT|O_EXCL|O_CLOEXEC`, mode 0000, empty; an existing lock
  means nothing is touched), `fstatat(AT_SYMLINK_NOFOLLOW)` a directory
  unchanged for 2 minutes by max(mtime, ctime) and `unlinkat(AT_REMOVEDIR)`
  (empty or nothing, never recursive). The floor stays because not every
  folder is made under the lock: `pacman -U <url>` fetches before its
  `trans_init`, and a libalpm user with another `DBPath` locks elsewhere;
  such a folder is empty for milliseconds with libalpm's built-in curl
  (with an XferCommand the file appears only when data arrives, so a
  lockless `pacman -U <url>` can keep an empty folder for over 2 minutes),
  and a lost race only fails that download. Keep the hold short and safe: pacman-conf, the opens and the
  readdir before it; only `fstatat`/`unlinkat` under it (no exec, no
  output: a pipe write can block); the release deferred (return, error,
  panic) and by inode; the lines after it. A stale `db.lck` blocks every
  pacman, so the hold relies on `catchSignals` (main.go) dropping every
  catchable signal: `TestCaughtSignalsCoverThePacmanLockHold` ties them.
  What is left is SIGKILL/SIGSTOP (root's, and the user's too: pkexec
  leaves the helper in archmaid-gui's user cgroup, so `cgroup.kill`,
  `cgroup.freeze`, `systemctl --user` stopping the scope, a logout or
  systemd-oomd reach it), a runtime fatal error or a power loss inside
  microseconds, pacman's own exposure (`pacman -Sc` right after has it for
  seconds). On an NFS or FUSE cache dir `fstatat`/`unlinkat` under the lock
  can block. One line per folder removed
  or left alone; a non-empty one (a `.part` inside) is left, `-Sc` still
  prints its line, and ours says how to delete it by hand once no pacman
  runs (with the `sudo rm -r` command only for a plain-ASCII path
  `[A-Za-z0-9/._+-]`: the line is sanitized, and any other path would be
  pasted as another one). The part's result stays `-Sc`'s. **The preview doesn't show the
  tidy**: the step shows `pacman -Sc --noconfirm` and a dry run names
  the part. The lock path (#125) is `pacman-conf DBPath` + `db.lck`
  (`dblock.go`, `session.lockPath`), read once per run before the first check
  that needs it and used for every `db.lck` check and the hold; an unreadable
  or invalid answer falls back to `/var/lib/pacman/db.lck` with one note (not
  a refusal: the lock is a guard, not a precondition). The hold needs the
  lock's folder root-owned and not group/other-writable (`checkLockDir`), else
  the tidy skips with a note, yet an existing lock still counts as busy. Keep it
  rmdir-only and fd-relative: no path walk, no `RemoveAll`, no path from
  the client; `openat2(RESOLVE_NO_SYMLINKS)` for the cache dir's parents
  is a documented note, not built (they are root's). The TUI is
  unchanged: its `yay -Sc` runs pacman through sudo, which can't do this
  without a new root command. Tests: `leftovers_test.go` (every negative
  named by the note it gets; the lock held under a probe, released on
  success, error and panic, an existing lock left as it was),
  `TestHelperColourReachesTheClient` (the line reaches the client plain).
- **Orphans are found after the update, by an in-process step.** The probe
  (`findOrphans`, `internal/actions`) runs `pacman -Qdtq` once topgrade is
  done — an upgrade orphans packages of its own, and the list `-Rns --noconfirm` acts on must be
  the one the confirm showed — and, if there are any, inserts the removal right
  behind itself (`steps.Result.InsertNext`), with exactly those names. No
  orphans: a line saying so, no confirm. A failed query fails the probe and
  inserts nothing. Don't move the query back into the queue's builder: it ran
  synchronously in `Update` (a 130–200 ms freeze) and went stale over
  topgrade.
- **The orphan confirm previews the `-Rns` cascade.** `-s` recursively removes a
  target's now-unneeded dependencies, so the real removal count can far exceed
  the `pacman -Qdtq` list (a real run confirmed 23 and removed 46). The probe
  previews it with `pacman -Rs --print` (`orphanRemovalPreview`; `-n` conflicts
  with `--print`; no root needed) and lists it in its output, under the
  confirm. Without the preview there's no removal: a failed `--print` fails the
  probe.
- **A Stop after the helper's start line can't stop it.** Once pkexec has
  become the root helper the user can't signal it, so `helperclient.Run`
  kills pkexec only before the start line (the dialog: NotGranted, "stopped
  during the authentication dialog"). After it, Run never kills and never
  closes the helper's stdout: it reads to EOF, waits, and keeps the real
  `Result`, with `Err` wrapping `ErrStoppedLate` ("stopped, but the root
  helper had already started and finished: …"). Don't go back to
  `exec.CommandContext` + `WaitDelay` there: the pipe was closed 5 s after the
  cancel, and a remove-orphans that went on to finish was logged as a
  protocol error with its result lost.
- **The helper's orphan removal has a millisecond TOCTOU left.**
  `remove-orphans` (and full-cleanup's orphan step) recomputes the plan as
  root right before `pacman -Rns`, between two checks that pacman's
  `db.lck` doesn't exist (exit 5 if it does; the check at the verb's start
  is minutes old by then in full-cleanup). The helper can't hold pacman's
  lock for `-Rns` itself, so what is left needs a concurrent **root**
  pacman started by someone else: one that runs a whole transaction inside
  the recompute, or starts between the second check and `-Rns` taking the
  lock. Even then `-Rns` refuses to remove a package that has become
  required. Don't move the recompute earlier or drop either check.
- **Logging policy.** Ordinary steps log only failures. `AlwaysLog` steps log
  success too, and a `SKIP … (declined)` entry when their confirm is declined:
  anything that removes or overwrites something, topgrade (the step that changes
  the most), and the AUR audit. That keeps what those steps did auditable from
  archmaid's own log (the engine decides, `WriteLog`; the Driver writes). An
  aborted in-process step's result is dropped by the engine, which never
  logs it; the downloads delete therefore
  writes its own `ABORT` entry (exactly what it deleted, and where it
  stopped) when it sees the abort, or from a `context.AfterFunc` if the abort
  lands after it returned — the result's `commit` disarms that when the
  result is taken, which is why the Driver releases the step's ctx with
  `steps.ErrResultTaken` only *after* the Output carrying `commit` is
  carried out (an abort cancels it with plain `context.Canceled`). Both
  writers run on a Driver worker, so they append to the store `actionEnv`
  captured at startup, never to `logs()` (which reads `logDir`/`dryRun`). `main` waits for such a run (`waitForDownloads`)
  before exiting. Logs are split into
  `topgrade`/`orphans`/`cleanup`/`audit` files per run directory, plus the
  discovery records (`services`/`repair`/`downloads`/`auraudit`, next entry);
  the newest 5 real run directories are kept, `dryrun/` is a single reused
  slot, and files in the state-dir root (the AUR snapshot, `last-run.json`)
  are ignored by the rotation. Only a
  log write (`Store.Append`) creates a run directory (viewing logs must not, or it evicts a real run), and
  logs are private: directories 0700, files 0600.
- **The discovery record (owner's decision).** An action that ends at its
  discovery (Disable unused services, Repair, Delete old downloads, Audit AUR
  packages: nothing found, or the look failed) is recorded like any run: it
  says what it checked (`actions.Notice{Text, Err, Details, Log}`: the
  summary line, the error alone, the full lists — each unit with its
  is-enabled state, each skipped one with its rule; the packages pacman
  flagged; each download with its age — and the commands with their raw
  output), the engine writes that to the action's discovery log
  (`Action.Record`: `services.log`, `repair.log`, `downloads.log`,
  `auraudit.log`) and `last-run.json` gets `nothing-found` (or `failed` with
  `steps_failed` 1; the DMS widget's `state.js` knows both). A discovery that
  finds something (`Checked` on `Pending`/`NeedsSelection`/`NeedsRoot`) writes
  its record into the first step's log when its queue starts, so a run that
  changes anything is a real run from its first entry, even if an Abort later
  leaves its step unlogged; a pick of nothing or an Abort before the queue
  writes nothing, as before. Repair's polkit recheck step logs to
  `repair.log` too: it is still the discovery. **History:** a run directory
  holding only discovery logs is discovery-only. It never counts towards the
  5 (`Prune` skips it), and an `Append` to a discovery log deletes every
  discovery-only directory whose records all have a newer copy (any run
  directory holding that action's log), so the newest record per action
  survives: at most 5 real runs + 4 discovery-only ones. A real entry in a
  discovery-only directory (the TUI keeps one directory per process) makes it
  real and rotates then, as a fresh one does. Anything the classifier doesn't
  know (a stray file, a subdirectory, an empty directory) counts as real, so
  a doubt never exposes a real run. A dry run writes `dryrun/<action>.log`
  and `dryrun/last-run.json` and prunes nothing. Accepted consequence: a
  nothing-found check overwrites `last-run.json`, so the widget's last run and
  its "No cleanup for N days" follow it, as the owner chose.
  **For front ends (Dev 4's Run page):** `engine.DiscoveryResult{Action,
  Outcome (DiscoveryFound | DiscoveryNothing | DiscoveryFailed), Summary,
  Details []string, Err, Category, LogPath, Started, Elapsed}` — show it as a
  run of one "Discovery" step: `Summary` its line, `Details` under it, `Err`
  for a failed look. Nothing/Failed are followed only by the record's
  `LogWritten`, `Notice` (same text, kept for old front ends) and
  `Finished{OutcomeNotice}`; Found comes before the prompt or queue it leads
  to, and Repair may send two (the later one includes the earlier details:
  show the latest). `LogPath` is filled by `internal/driver` once the record
  is on disk (`Store.Path(Category)` of that run's store; "" if the write
  failed, from a bare Session or the stub); for Found it is "" (the record
  lands in the queue's log). The Logs page lists the discovery logs by
  `logstore.Categories` ("Services check", …) with no change.
- **State files** (under `~/.local/state/archmaid/`, read by the DMS widget, see
  `docs/dms-widget.md`). `last-run.json` is the engine's `WriteLastRun` (the
  record built in `internal/engine/queue.go`, written by the Driver)
  exactly once per action, at its end: summary (`ok`/`failed` by `errs`; `incomplete` when
  nothing failed but a step was refused sudo, counted in `steps_refused`; `cancelled` if no
  step ran, e.g. sudo gave up), ctrl+c abort (`aborted`), a declined required step
  (`aborted`, or `cancelled` if nothing ran yet), a declined gate or cancelled password
  prompt (`cancelled`), a Cancel in the GUI's own polkit dialog (as a declined required
  step: `cancelled` if nothing ran, else `aborted`; `steps_refused` 0), an action that ended at its discovery (`nothing-found`, or
  `failed` with `steps_failed` 1 for a failed look; see "The discovery record"). The
  action is the menu `id` (the two static actions have ids `fullcleanup` and
  `topgrade`; Clear caches is `clearcaches`, and records from before it carry `usercache` or `wipecache`). A dry run writes `dryrun/last-run.json` instead and never touches
  the real one. `audit-state.json`: see its own entry below.
- **The run report (#188) is the Driver's, not the Session's.** The engine stays
  pure. A Driver given `Deps.RunReport` (a `*report.Config`; nil, the default,
  means none) makes a `report.Collector` at each real `QueueStarted` (never in
  a dry run), tells it each `StartStep` effect (the sizes of that step's
  targets are measured *before* the child starts) and each `StepDone`/`Aborted`
  event (measured again), and `apply` splits the Output that carries
  `Finished`: the events before it go out, the report is made (bounded), the
  `WriteLastRun` effect is written with the summary object, then
  `engine.RunReport` and `Finished` go out. The TUI's `driverDeps` sets
  `RunReport: report.DefaultConfig()` through the `runReportConfig` seam (its
  `TestMain` swaps in nil: the real config reads the machine's pacman log and
  runs `paccache`/`pacman`, which testenv traps). **The GUI sets it too (#191):**
  `realdriver.Config.RunReport` (nil, the default, means none, which every
  test config leaves) is handed to `driver.Deps.RunReport`, and the binary's
  config (`ui.realConfig`, tested by `TestRealConfigHasRunReport`) sets
  `report.DefaultConfig()`; the GUI part is below the sources. Sources, as built:
  - *Packages*: the `[ALPM] upgraded|installed|removed|downgraded` lines of
    `/var/log/pacman.log` written after the **byte offset and inode** the log
    had at queue start (not timestamps), folded per package to its net change
    (installed-then-removed cancels; upgraded twice is one upgrade). A log with
    another inode (rotated) or smaller than the mark (truncated) is read from
    its start and marked `Partial` (a note says so). Any other pacman run while
    archmaid runs writes to the same log and counts too (a second terminal, a
    timer): the report says what changed on the machine, not what topgrade did.
    AUR = `pacman -Qqm` once, at the end; if it fails the AUR counts are
    unknown (`AURKnown` false, a note), exit 1 with no output is "none".
  - *Orphans removed*: the orphan step's own `Items`/`Cascade` (a combined
    helper call's Parts included), only when its step ended OK.
  - *Freed per area*: `DirSize` (allocated blocks, symlinks not followed,
    hard links once, other filesystems not entered) of each step's targets as the user,
    before and after, `Freed` clamps a negative delta to 0, a target that
    can't be read (ENOTDIR/EACCES on the top folder) or a walk past its time
    is **unknown**, never 0. Budget: 4 s per walk, 20 s of walking per run (`Config`;
    only time spent walking counts, never topgrade, password or confirm waits),
    after which every walk is unknown at once; a `Close` cancels them
    (`Driver.live`). Areas by step ID (`report.Area*`; a step that isn't in
    the queue has no row, a skipped/refused step none either): `paccache`,
    `paccache-wipe`, `pkg-cache-uninstalled`, `yay-cache`, `journal`,
    `tmpfiles`, `user-cache`. Under sudo `yay -Sc` does `pacman -Sc` and yay's
    own cleaning, so that one step has two rows (`pkg-cache-uninstalled` and
    `yay-cache`); under polkit (`--aur` in its argv) only `yay-cache`.
    A combined call's parts are all measured before the call, and each part's
    end moves the next part's start on the same folders to where it left off,
    so `paccache` and `pacman -Sc` don't count each other's work (a step ends
    a moment before its event is handled, so a second's work can land in the
    next row; the total is right). Folders: `pacman-conf CacheDir` (fallback
    `/var/cache/pacman/pkg`), yay's `buildDir` from `~/.config/yay/config.json`
    else `$XDG_CACHE_HOME/yay`, `/tmp` and `/var/tmp`, `/var/log/journal` and
    `/run/log/journal` (files are only stat-ed, so a 0640 journal is
    measurable), `~/.cache`. Temp files covers only those two folders: tmpfiles.d
    rules clean other paths too, which are not counted.
  - *Cache still holds*: `paccache -dk0 --nocolor` as the user (dry run, nothing
    removed), parsed from its `finished dry run: N candidates (disk space
    saved: S)` line (or `no candidate packages found for pruning`);
    if it is missing, fails or prints something else, the cache folders are
    summed (`Source` "folder"). The TUI's line names the menu entry "Delete
    entire package cache" (`cacheMenuLabel`: keep it equal to the entry's label).
  - *Attention*: **restart** when the running kernel's module folder
    (`/usr/lib/modules/<uname -r>`) is gone — every kernel package leaves
    `<release>/pkgbase` there, so a replaced kernel shows as a missing folder,
    and the installed release is the folder of an upgraded kernel package — or
    when `systemd`, `mesa` or an `nvidia*` driver package (32-bit builds too;
    not `nvidia-settings`/`-prime`) was upgraded; **.pacnew/.pacsave**
    from the log's `warning: … installed as …pacnew` / `saved as …pacsave`
    lines; **audit** only for high findings (the audit step's `Result.Audit`;
    the TUI's own Audit line shows any finding).
  - *The run*: `Elapsed` and `Steps{Done, Skipped, Failed}` (a step an abort
    left pending counts skipped).
  - **Left out on purpose: AUR updates held back by the 72 h yay cooldown.**
    `updates-cache.json`'s `aur_held` is a count from the last timer run, and
    the refresh after a run is only queued (`systemctl --no-block`), so at report
    time it still describes the system *before* the run; it has no names or
    install time; the cooldown hook lives in the owner's dotfiles and logs
    nothing archmaid knows. A stale "held back" is worse than none. To add it,
    the check would have to finish before the report (a synchronous run of
    `archmaid-updates-check`, ~10 s of yay) and the script would have to write
    the held packages and their ages.
  - `last-run.json` gets `summary{upgraded, aur, freed_bytes, attention[]}`
    (`logstore.RunSummary`) only when the report has data; the schema stays
    `v:1` (added within v1, like `steps_refused`). `state.js` reads it
    (`parseLastRun().summary`, null for old or damaged); the popout shows it
    under LAST RUN (#191: `popout().lastRun.summaryLine` "47 upgraded · 3.1 GiB
    freed", `warnLine` "⚠ Restart recommended · 1 new .pacnew" only with
    attention items; `docs/dms-widget.md`).
  - **The GUI's part (#191, the owner's variant C of the #187 mockup).** The
    model keeps the event (`runview.Model.Report`, `Change.Report`; cleared by a
    new queue, a discovery result or a report page). `runview.Summary()` (pure,
    `runview/summary.go`) turns it into the words and numbers, or nil (a run
    still going, a dry run, no report, a report without `HasData`): then the
    Run page is what it always was. With a summary, `ui/summary.go` draws the
    finished page: the hero card (corner marks; kicker "NEEDS YOUR ATTENTION" /
    "ALL QUIET", headline = the first attention item's short text or
    "Everything is up to date", the sub line "47 packages upgraded · 3.1 GiB
    freed · 14 m 52 s", the attention rows), the Packages and Space freed
    columns (a grid: side by side, stacked under the narrow breakpoint, where
    the space bars are left out; a GtkBox of wrapping labels made GTK warn
    about its minimum height), the audit line only with findings, and "Steps
    and output" folded below: the timeline is the same widget (its scroll is
    shown and hidden, never moved), with a minimum height of 420 px, inside one
    scroll for the whole page so a low window reaches everything. A run that
    ended unclean with nothing else to say is "CHECK THE STEPS" instead of
    "ALL QUIET" (the brief's two kickers would call a failed run quiet).
    The only button is "Copy pacdiff command" on a .pacnew/.pacsave row; it
    copies `sudo pacdiff` (`runview.PacdiffCommand`; the docs prefer no other
    spelling, and /etc needs root), never runs it. No reminder button. The
    cache line is the TUI's wording, `runview.CacheMenuLabel`, held to the menu
    entry by `TestSummaryCacheLabelIsTheMenu`. AUR held back is not shown (not
    in the data).
  - Tests never see the machine's files: `testenv.Run` points
    `ARCHMAID_PACMAN_LOG` and `ARCHMAID_MODULES_DIR` (`report.Env*`, which
    `DefaultConfig` honours) at nothing, and traps `pacman-conf` with the other
    tools. Fixtures: `internal/report/*_test.go`, `driver/runreport_test.go`,
    `tui/summary_test.go` (goldens `TestSummaryReportGolden`).
- **Untrusted text goes through `sanitize.String`.** Command output (topgrade, yay, AUR
  build scripts) can carry escapes that retitle the terminal, write the
  clipboard (OSC 52) or redraw what the user reads. Streamed output keeps only
  SGR colours (`sanitize.KeepSGR`); logs are plain text, both when written and
  when an older log is read back (`sanitize.Plain`); AUR names and maintainers
  go through it too (`sanitize.Field`: plain, one line, capped). It also drops
  Unicode format characters (bidi overrides, zero-width). New paths that show foreign
  text should use it rather than a second filter.
- **Colour in the run output is asked for per step (`internal/actions/colour.go`).**
  A step's child writes into a pipe, so the tools turn colour off; the
  steps that run as the user ask for it on their own child only, never in
  archmaid's environment: topgrade gets `CLICOLOR_FORCE=1` in its
  `Step.Env` (its console crate has no flag; the variable reaches
  everything topgrade starts, and tools that honour it colour too), yay
  `--color always` in its argv (yay passes it to the pacman it runs).
  `NO_COLOR` turns both off. The front ends draw it (the GUI with Pango
  attributes, the TUI's pane as is); logs and `last-run.json` stay plain
  (`TestTopgradeColourShownLogsPlain`). Not covered: yay and pacman *under
  topgrade* (topgrade passes yay only `[linux] yay_arguments` from the
  owner's `topgrade.toml`; `yay_arguments = "--color always"` there would
  colour them) and the TUI's sudo steps (sudo resets the environment, and
  paccache colours only on a terminal). hyprpm always colours.
  **The root helper colours its pacman, per call (task #111, security
  review).** Its argv are constants in `cmd/archmaid-helper` (`helper.go`):
  a pacman whose output it only relays gets `--color always` (`-Rns`,
  `-S`, `-Sc`, full-cleanup's `-Qk` part), one whose output it parses
  gets `--color never` (every Query: `-Qq`, `-Qdtq`, `-Rs --print`, `-Q`,
  `-Si`, `-Qk -- pkg`, `-Sp`; and the check-files verb's `-Qk`, whose
  summary lines are its check event), so a parsed line never holds an
  escape. Its `line` events keep well-formed SGR only
  (`helper.SanitizeLine`); every other field stays plain. The client
  filters again (`sanitize.KeepSGR`, no conceal) and drops the colour
  under `NO_COLOR` (`helperclient.lineMode`: nothing of the caller's
  reaches the helper, NO_COLOR included). Keep: **`TERM=dumb` and no
  `SYSTEMD_COLORS` in `helper.ChildEnv`**. No child's colour needs TERM
  (pacman reads none; paccache checks for a tty), and `disable-unit`
  parses `systemctl show`/`cat`: with colour on, `systemctl cat` wraps
  `[Install]` and every directive in SGR (systemd's `cat_file`), so
  `parseInstall` would never see the section and the Alias=/WantedBy=/
  Also= denylist checks would pass by default.
  **Systemd's colour, per call (task #133, the owner said yes).**
  `helper.CallEnv(argv)` (runner.go; `command` uses it for Query and
  Stream) is `ChildEnv` plus `SYSTEMD_COLORS=1` for an allowlist of exact
  argv shapes whose output is only relayed: `systemd-tmpfiles --clean`,
  `journalctl --vacuum-*`, `systemctl disable --now -- ...`. Everything
  else (`systemctl show/cat/list-unit-files`, all pacman, paccache,
  pacman-conf) keeps plain `ChildEnv`; TERM stays dumb. The line events keep
  the SGR as before; the client still filters it and drops it under
  `NO_COLOR`; logs stay plain. paccache can't be forced (a tty check), and
  `pacman -Qk` already has `--color always`. A new relayed systemd call
  must be added to `relayedSystemd` by hand: forgetting it only costs
  colour, the other way round would fail a vetting open. Tests:
  `TestSystemdColoursOnlyForRelayedCalls`, `TestSystemdColoursReachTheRunner`
  (helper), `TestHelperColourReachesTheClient` (SGR through to the client,
  other escapes gone, `NO_COLOR` strips it). Other tests: `TestColourPerCall`, `TestEveryQueryIsColourless`,
  `TestChildEnvStaysDumb`, `TestLineKeepsOnlySGR`, `TestSanitizeLine`
  (helper), `TestLineColour` (client), `TestRunPageHelperColour` (the GUI's Run
  page coloured, every log plain).
  **The user side follows the same rule (task #114).** Every user-side
  pacman whose output archmaid parses gets `--color never` after its
  operation (`-Qdtq`, `-Rs --print`, `-Qq`, `-Qkq`, `-Qqm`; shims that read
  `$1` keep working, those that skip fixed argument counts must skip the
  two more). And the names parsed out of `-Qdtq` and `-Qkq` are checked
  with `actions.PackageNameOK` (the helper's pattern, tied to the client's
  by `TestActionsPackageNameIsTheClients`) before any `sudo pacman -Rns`/
  `-S`/`-Qkq` argv is built; a bad name fails the probe or scan with the
  name quoted (`TestOrphanNamesCheckedBeforeSudo`,
  `TestBrokenNamesCheckedBeforeSudo`). Keep both when adding a parsed call.
- **The AUR audit diffs against a snapshot** because the RPC only exposes the
  *current* maintainer and co-maintainers, never their history. Consequence:
  the first audit only records a baseline. Network: archmaid's own code talks to `aur.archlinux.org` in three places, always as the user, never as root and never from the helper: the AUR audit (RPC `info`), the Packages page's AUR search and info (RPC `search`/`info`, only when the user types a term of at least 2 characters, debounced, cancellable), and the AUR install (a `git clone`/`fetch` of the package's repo into `~/.cache/archmaid/aur`, and the sources `makepkg` downloads from the PKGBUILD's own URLs, only after the user reviewed and confirmed). The programs archmaid starts (pacman, topgrade, yay) and the optional `archmaid-updates-check` user timer (`checkupdates` and `yay -Qua` as the user under `NoNewPrivileges`) use the network as they always do. The root helper makes no network call of its own; pacman, run by it, downloads packages as it always does.
  A dry run reads the snapshot but never advances it.
- **Co-maintainers gate like maintainers.** A co-maintainer can push to the
  package's AUR git, so an *added* one is high (named in the gate: "foo:
  co-maintainer bob added"), a removed one a glance. The RPC omits
  `CoMaintainers` when there are none, so absent means none. The snapshot entry
  is `{"maintainer": …, "comaintainers": [sorted]}`; files from before this
  hold a bare `"maintainer"` string per package, which still loads (`audit.Entry`
  with `HasCo` false). No co-maintainer record = a baseline for that field
  (no alarm, like a first audit), recorded by the next commit. Don't make it
  alarm instead: the upgrade would gate every package with a co-maintainer.
  Any other entry shape (null, a number, no `maintainer`) is corrupt.
- **Saving the snapshot is the acknowledgement, so it happens only on accept.**
  `audit.Run` never writes; callers run `Result.Commit`. The audit step hands it to
  topgrade's gate as `Step.OnAccept`: "yes" saves (logged `ACCEPT`), "no" or an
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
- **DMS widget (P7)** lives in `packaging/dms/archmaid/`; the full description, install and
  enable steps are in `docs/dms-widget.md` ("Implementation as built"). It only reads
  `last-run.json`, `audit-state.json` and `updates-cache.json` (the last one written by the
  `archmaid-updates-check` user timer, `packaging/systemd/`). **The JSON field names of
  `logstore.RunRecord` and `audit.State` are now a contract with `state.js`**:
  `packaging/dms/dms_test.go` generates its fixtures from the real writers, so renaming a
  field fails that test; add new fields freely, bump `v` for removals. The widget
  understands `outcome: "incomplete"` + `steps_refused`, and `RunRecord` carries both now
  (96911ea), but `withRefused` in `packaging/dms/dms_test.go` still sets them on the JSON by
  hand: switch it to the real writer.
- **`audit-state.json` (state-dir root, beside the snapshot) lists the
  unacknowledged high findings** for the DMS widget
  (`audit.State{V, Checked, Packages, High[{Pkg, Reason}], SnapshotPresent,
  SnapshotReadable}`; atomic 0600 write, `audit.WriteState`/`ReadState`). Path:
  `Config.StatePath`, built in `currentAuditConfig` like the rest. `Run` writes
  it when an audit completes (highs = still held back; "nothing to audit" too,
  which clears stale ones); `Commit` rewrites it after the snapshot save:
  highs cleared when accepting (gate "yes", `a`), kept on a held commit
  (`holdHigh`); a declined gate never runs the commit, so they stay. A failed
  or cancelled audit (AUR unreachable) leaves the file exactly as it was,
  `Checked` included: it means "last completed audit", and bumping it would
  make old highs look freshly verified. **A dry run never reads or writes it**
  (it skips the real file rather than using a `dryrun/` slot, because this file
  is "current truth" for the widget, not a per-run log). A state write failure
  is a logged `WARN`, never an audit failure.
- **Every audit setting is read on the model's goroutine.** An audit runs off
  it (the menu action's Cmd, the step's run, the prefetch), so
  `internal/audit` reads nothing global: `currentAuditConfig` builds its
  `audit.Config` — endpoint, snapshot path, deadline, dry run, the log store
  (`Config.Log` appends to the captured `*logstore.Store`) — in
  `actionEnv` (for the menu audit), `auditStep` (when the queue is built) and
  `startAuditFetch`. The `Result` keeps that `Config`, so `Commit` writes the
  snapshot the audit read and logs beside it even if `logDir` moved since.
  Don't read `logDir`, `aurRPCURL`, `auditDeadline` or `dryRun` inside the
  Cmd. `pacman -Qqm` is `Config.Foreign` (`foreignPackages` in the glue), so
  switching it to another runner touches only the glue.
- **Full Cleanup and Run topgrade open with the audit, directly in front of
  topgrade** (one `auditStep`, gated identically). It runs
  as an in-process step (`Step.Run`, run on a Driver worker — not a shell
  command, so there's nothing to signal: an abort cancels the step's `ctx`,
  which ends the pacman query and the RPC at once, and the engine drops the
  late result). The
  whole audit has one deadline, `auditDeadline` (20 s for the query and every
  RPC chunk together), not a timeout per request. The lookup itself starts
  the moment the action is chosen: the Session asks for `StartPrefetch`, and
  the Driver starts a `frontend.AuditFetch` through `Deps.Prefetch`
  (`frontend.Prefetcher`, the TUI's `startPrefetch` in `glue.go`;
  `audit.Prefetch`: a goroutine publishing its result by
  closing `Done()`) so it runs while the password prompt or the gate is up,
  and the step awaits it instead of fetching again (one RPC per run). It only
  reads: the snapshot commit still comes from the step's result, so the gate
  semantics below are unchanged. Esc on the password prompt, a declined gate,
  an abort and the end of the run cancel it (`CancelPrefetch`); three wrong
  passwords don't, because the queue still runs and the audit step (no sudo)
  still reports from it. A
  high-severity finding (adoption, maintainer change, added co-maintainer) uses
  `Result.ConfirmNext` to put a named confirm in front of topgrade and make it
  optional (a result may not also set `InsertNext`: `Result.Check` fails the
  step rather than let the confirm land on an inserted step), so "no" skips only the update and the cleanup carries on. An
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
  `downloads.Resolve` walks flag → `ARCHMAID_DOWNLOADS` → XDG → `~/Downloads`
  → `~/Download`, resolves symlinks, and refuses `/`, `$HOME`, the passwd
  home (`user.Current`, passed in as `Options.PasswdHome`) and ancestors of either for every
  source; all but the flag must be strictly inside the home folder, which is
  `$HOME` unless that is unset, relative or `/` — then the passwd home.
  Don't add a candidate that skips `checkDownloadsDir` (in `internal/downloads`). `--downloads` refuses
  a value starting with `-` (exit 2): `archmaid --downloads --dryrun` would
  otherwise take `--dryrun` as the folder and silently run for real.
- **Old downloads are judged by ctime, in Go — never `find -mtime … rm -rf`.**
  The old `find "$dir" -maxdepth 1` matched `$dir` itself (once its mtime aged,
  the whole folder went), and mtime lies: wget, `curl -R` and unzip back-date
  it. `oldDownloads` lists only the folder's entries; a directory counts only
  if nothing under it has a new ctime; nothing is followed (Lstat, a symlink is
  removed as the link). The delete is an in-process step that checks the
  folder is still the scanned inode, re-checks each entry right before
  removing it, and removes through an `os.Root` (hence `go 1.25`). An abort
  cancels its ctx, which stops it between entries (nothing after that is
  touched), and the run is still logged: an `ABORT` entry naming exactly what
  it deleted (see Logging policy). Tests can't set ctimes: they move the
  clock (`Options.Now`, `downloadsNow` in cmd) past a mark instead.
- **The downloads cleaner never crosses a mount point — never use
  `os.Root.RemoveAll` there.** RemoveAll descends into a filesystem mounted
  below the folder (a USB disk, sshfs, a bind mount of `~/Documents`) and
  empties it; those files carry their own old ctimes. `boundary` is the
  folder's `st_dev` plus every mount point inside it from
  `/proc/self/mountinfo` (a bind mount keeps the device, so `st_dev` alone
  misses it; an unreadable mount table fails closed). An entry that is or
  holds anything outside it is never old, and `remover.removeTree` deletes
  bottom-up by hand: each node is Lstat'd right before it is touched and
  must still be inside the boundary and old (a directory is checked before
  it is emptied, since emptying bumps its ctime; other names of a hard link
  it unlinked are exempt), and each directory is opened as a nested
  `os.Root` that must be the inode it Lstat'd. It stops at the first node
  that fails and reports the entry as partly deleted. Left open: a node
  swapped between its Lstat and its unlink.
  `TestDownloadsMountPoints` (in `internal/downloads`) mounts tmpfs and bind mounts for real inside
  `unshare -rm` (skipped where that's unavailable); package vars `statDev` and
  `mountinfoPath` are the seams for the rest.
- **Streamed output is batched, and the pane shows only a tail.** A step's
  child writes combined stdout+stderr to one ordered channel: every line, then
  exactly one done (`proc.Runner.Start`). The Driver's pump (over
  `proc.Next`) blocks for the first message,
  then takes whatever else is already buffered — up to `proc.BatchMax` (256)
  lines, stopping at the done — and the outbox coalesces what piles up into
  one `OutputChunk`; the model applies the whole chunk with one `SetContent`
  (a stale step's lines never reach it: the engine drops them). A slow
  producer gets one-line chunks; a flood gets full ones, so the UI catches up
  by itself. The full output is the engine's (what it logs, so `AlwaysLog`
  steps keep everything); the viewport gets only the newest
  `maxVisibleLines` (2000) from `m.tail`. One update per line with the whole
  buffer re-split each time was quadratic: 5k lines cost ~1 s of CPU and
  ~1.8 GB of allocations, 20k lines ~5 s (`BenchmarkStreamView*`, now ~5 ms
  and ~21 ms; `ARCHMAID_PERF_GUARD=1 scripts/check.sh` fails if they
  regress past 3×, see `TESTING.md` → Perf guard). The pane follows new output only while it's at the bottom; a
  user who scrolled up stays put, and when the tail drops old lines their
  offset moves with the text. A step is done when its child exits: the pipe
  is read for at most `stepWaitDelay` (2 s, `proc.Options.WaitDelay` → `exec.Cmd.WaitDelay`) after that,
  so a background process the child left holding stdout can't keep it
  "running" — no process-group change needed (see the `Setsid` landmine).
- **The engine's Session is held to the TUI's transcripts.** `sessionTarget`
  (`internal/tui/transcript_session_test.go`) replays the transcript scenarios
  through an `engine.Session`, playing its Effects against the same shims, and
  compares with the goldens the TUI records (through `tuiEngineTarget`, the
  TUI's model on a `syncDriver`), byte for byte; `driverTarget` does the same
  on the `syncDriver` alone. A behaviour change goes into the engine, or a
  golden changes deliberately; never add a session-only golden. What the Session can't run yet is skipped by name with
  the milestone that brings it (`sessionSkips`): remove the skip in the
  commit that makes it pass. Where the TUI was fixed to fail closed (a
  refused result keeps its gate) the Session mirrors it, and it adds two
  guards of its own: hyprpm is moved last in any plan, and a result that
  would insert a root step after hyprpm ran is refused like a malformed one.
- **`m.bg` is one field, not a stack.** A popup that overwrites it with its own
  screen value makes `renderScreen` recurse forever; see `openAskpass` in
  `adapter.go` (an askpass popup over the password, a confirm or a notice
  leaves `m.bg` as it is).
- **The TUI is an engine front end (E7b).** It runs no queue, sudo, askpass
  or abort logic of its own: a menu entry is `engine.Start`, a key on a
  prompt is a Command (`engineKey`), and the screen, checklist, prompt and
  output pane come from the Events (`applyEvents`). A behaviour change goes
  into `internal/engine` (or the Driver), never back into `update.go`. The
  binary runs on `internal/driver` (`newDriver`); the tests run the same
  model on `syncDriver` (pull mode) and the goldens are recorded through it
  (`tuiEngineTarget`).

- **`auth_admin_keep` is kept for the scope of the agent that asked.**
  Answered by the owner on their machine (`docs/polkit-keep-session-check.md`
  Part A, r518.59f90b3, 2026-10-11/12): in the graphical session, with
  polkit-gnome as the agent, a new process (G3, `sh -c`) was authorised
  without a prompt (rc 0), and `pkcheck --list-temp` (G5) showed the kept
  authorization for a `unix-session:` subject. polkitd stores a kept
  authorization for the scope of the agent that authenticated it
  (`authentication_agent_get_scope (agent)`, `check_authorization_challenge_cb`
  in `polkitbackendinteractiveauthority.c`), which is what the earlier
  no-session run (process scope, `docs/polkit-live-test.md` F2) and this one
  both show. For archmaid-gui that means:
  - *Normal path:* its own pkttyagent is registered for archmaid-gui's
    process (`--process`), so the authorization is process-scoped: only
    archmaid-gui (and the sibling case below) reuses it.
  - *Fallback path* (no pkttyagent, or it can't register, so the desktop's
    agent asks): the authorization is session-scoped. Any process of the
    owner's session can then use archmaid's 4 `auth_admin_keep` actions for
    about 5 minutes without a prompt.
  - That session scope is the one the docs already assumed as the safe
    case; nothing changes. A tty login (Part B) wasn't run; it is the same
    rule (the agent's scope).

  *Accepted residual (owner, 2026-10-11; Dev 3, from polkit 127's source,
  `polkitbackendinteractiveauthority.c`):* a kept authorization is stored
  for the subject process, but `subject_equal_for_authz` also lets a
  *different* process use it when all of these hold: both processes have
  safe pidfds, the same uid, the same parent that is still running (ppid >
  1), the same cgroup as each other and as that parent, and the same
  controlling `/dev/pts/N`, created before both processes started. archmaid's
  4 `auth_admin_keep` actions are kept for archmaid-gui (pkexec's subject).
  Started from a terminal shell, any other child of that same shell in the
  same cgroup on the same terminal can reuse them for about 5 minutes
  without a prompt. Started from the desktop (its parent has no
  controlling terminal) or on a VT (the code only builds a `/dev/pts`
  path), there is no sibling reuse. And because archmaid-gui's pkttyagent is
  registered for its own process, these kept authorizations are
  process-scoped: `pkcheck --list-temp` doesn't show them and
  `pkcheck --revoke-temp` doesn't remove them (both only cover the caller's
  session scope); they only expire. The owner accepted this on 2026-10-11, since
  reuse needs the same user in the same shell on the same terminal, and
  the helper's verbs are fixed and validate their own input. No sibling
  check (G6) was added to the keep-check guide.

## Live-testing safely

Unit tests can't cover real PAM, real pacman or a real terminal. When you need
to:

- Drive the real binary with VHS or tmux under a **scratch `HOME`** and never
  point it at the real `~/.local/state/archmaid` (see `cmd/archmaid/e2e/run.sh`).
- Wrong-password flows: stay under faillock's `deny=3` — check
  `faillock --user $USER` first, pace attempts ~10 s apart so nothing is typed
  while auth is in flight, and expect the two-stage auth to burn nothing.
- A real Full Cleanup needs a human at the keyboard for the sudo password and the
  destructive confirms; don't automate it. `--dryrun` is the safe substitute.
- Check the result in `~/.local/state/archmaid/<run>/` (or View log), and use
  `/var/log/pacman.log` as the independent record of what pacman actually did.

- **pkexec: one exit code for every refusal, and a cancel costs a failure.**
  Live-tested (`docs/polkit-live-test.md`): pkexec 127 exits 127 for a wrong
  password, a dismissed dialog and a locked account alike, so the engine
  can't tell them apart — "the helper exited 127 before its `start` line"
  means *privilege not granted*. One wrong polkit password costs one
  `pam_faillock` failure (deny=3), and so does cancelling the dialog; a
  success resets the count. Never add a policy action without `exec.argv1`
  for the helper's path: it can capture calls meant for the verb actions.

  *The one cancel archmaid can tell apart* is a Cancel in archmaid-gui's
  own dialog ("Authorise archmaid's Root Steps", polkit's prompt through
  its pkttyagent): the Session saw the `CancelPrompt`, so the 127 that
  follows ends the step(s) that dialog covered `Skipped` ("authorisation
  cancelled" in the log), skips the rest as behind a declined required
  step, and ends the action cancelled when nothing ran (else aborted),
  `steps_refused` 0: every front end says cancelled, as for a declined gate
  (`internal/engine/polkitcancel_test.go`, end to end on the real Driver in
  `gui/internal/realdriver/cancel_test.go`). Everything else stays "not
  authorised": the desktop agent's Cancel or wrong password (archmaid sees
  only the 127), no agent, three wrong passwords at our dialog (su refused
  each; the third gives up on the request).

  *That cancel still costs one faillock row, and no cancel can avoid it*
  (polkit 127's source): our dialog only opens once PAM is already asking,
  since pkttyagent passes on the prompt from `on_request`
  (`polkitagenttextlistener.c`), which runs when `polkit-agent-helper-1`'s
  conversation sends `PAM_PROMPT_ECHO_OFF` from inside `pam_authenticate`
  (`polkitagenthelper-pam.c`). Every way to end it from there closes the
  helper's socket: an end of file (`on_request` → `polkit_agent_session_cancel`),
  stopping pkttyagent (its socket closes with it), or killing pkexec
  (polkitd's name-owner-changed handler cancels the caller's sessions →
  `CancelAuthentication` → `on_cancelled` → the same `session_cancel`).
  `kill_helper` (`polkitagentsession.c`) closes the socket of the
  socket-activated helper (Arch's `polkit-agent-helper.socket`; it can't
  signal it: the helper runs as root under systemd), so the helper's
  `conversation_function` gets `fgets` = NULL, returns `PAM_CONV_ERR`,
  `pam_unix` fails, and Arch's `system-auth` runs `pam_faillock authfail`.
  The only cancel with no row would come before pkexec starts: archmaid
  asking for the password itself before the helper step (su-checked, then
  kept and handed to the agent). Not built: it would ask even when polkit
  wouldn't (a kept `auth_admin_keep` authorization, a rule that allows the
  action), and it changes when the dialog appears. *Decided by the owner
  on 2026-10-12: keep the row as a documented cost* (Dev 3's
  recommendation): the last-attempt warning already says a cancelled
  dialog counts.
- **Read the faillock state before every password or polkit prompt.**
  `privilege.FaillockStatus(user, now)` returns `Status{Failures, Locked,
  Unlock, Deny}` as pam_faillock would judge it, read-only and without root:
  it parses the user's own tally `/run/faillock/<user>` (`<user>:root 0660`)
  and honours `deny`, `unlock_time` (`never` = 0), `fail_interval` and `dir`
  from `/etc/security/faillock.conf` (defaults 3 / 600 s / 900 s /
  `/var/run/faillock`). The GUI calls it right before it shows the sudo
  password prompt or starts pkexec, and:
  - `Locked`: says so with `Unlock` ("try again at 14:32"; zero = an admin
    must `faillock --reset`) instead of prompting — even the right password
    fails, and pkexec's 127 can't say why;
  - `LastAttempt()` (`Failures == Deny-1`, deny=3: at two): warns that the
    next wrong password **or a cancelled polkit dialog** (both cost a
    failure, `docs/polkit-live-test.md`) locks the account.
  The tally format and the rules (64-byte host-order records `source[52]`,
  `reserved u16`, `status u16` (0x1 valid), `time u64`; locked when the valid
  records within `fail_interval` of the newest reach `deny`, until newest +
  `unlock_time`; a lapsed lock resets the count) are Linux-PAM's
  `pam_faillock` (`faillock.h`, `check_tally`/`write_tally` in
  `pam_faillock.c`, `faillock_config.c`); see the comment in
  `internal/privilege/faillock.go` before changing it. The admin rules
  (`root_unlock_time`, `even_deny_root`, `admin_group`) are not applied:
  archmaid only asks for the desktop user's own password. Never run the
  `faillock` binary from archmaid or its tests (`testenv` traps it).
- **Nothing deletes run history at startup.** On 2026-10-04 a real run folder
  (`~/.local/state/archmaid/20260923-2159`) vanished: `main()` pruned to
  `KeepRuns` (5) right after `parseArgs`, on every start, and `parseArgs`
  ignored unknown flags, so an agent's `archmaid --help` against the real
  HOME started the TUI and evicted the oldest of six runs. Now the rotation
  lives in the store: `logstore.Options.KeepRuns` makes the `Append` that
  creates a real run's directory (or makes a discovery-only one real) prune,
  and a discovery record's `Append` drop superseded discovery-only runs
  (dry-run stores never do either, and the run's own directory is never
  pruned; see "The discovery record"). `startup` (askpass relay, flags,
  `--help`/`-h` exit 0, anything unknown exit 2) touches nothing on disk;
  `TestStartupDeletesNothing` pins it. A front-end check that fails before
  the first log entry (e.g. an `Env.Validate`) is safe by construction — keep
  it that way: never call `Prune` from a startup path. `scripts/check.sh` and
  `make e2e` snapshot the real state dir (names, sizes, mtimes) before and
  after and fail on any difference (`scripts/state-guard.sh`).

## Open items

- **The GUI on the real Driver has been run live** (the owner's run and the
  VM runs: one polkit dialog per Full Cleanup, topgrade's askpass). The
  root helper and policy come only from the package (`packaging/arch`);
  `install.sh` installs the TUI alone, which needs neither. The owner's
  step-by-step first live run: `docs/first-live-run.md` (its C4 predates
  the one-dialog call and the kept password). One password per run is
  merged (`internal/privilege/kept.go`, `gui/internal/polkitagent`); the
  Proceed sheet previews its PATH guard (`driver.PreviewKeepOff`) and says
  when the password will not be kept, while the guard at step start stays
  the authority.
- **Both packages are installed on the owner's machine** (`archmaid-git`
  and `archmaid-gui-git`; r608 as of 2026-10-17, the lead's word). Keep
  installing them together, from the same build (`packaging/arch/README.md`).
- **One launcher entry.** The TUI has no desktop entry at all (not even a
  hidden one); only the GUI's `io.github.enzord758.archmaid.desktop` exists.
  The `archmaid` command still starts the TUI. `scripts/check.sh` fails if
  `packaging/archmaid.desktop` returns or the PKGBUILD or `install.sh` makes
  one; `install.sh` removes a stale one of its own. The icon name `archmaid`
  is unused too (the GUI uses its app-id icon, the widget a Material one):
  neither the PKGBUILD nor `install.sh` installs `apps/archmaid.svg`, and
  `install.sh` removes an older copy only if it is a regular file identical
  to `packaging/archmaid.svg`.
- **The GUI's speed budgets are measured by hand.** First frame, idle RSS,
  throughput and zero drops are enforced only with `ARCHMAID_GUI_PERF=1`
  (`cd gui && ARCHMAID_GUI_PERF=1 scripts/headless.sh`, or
  `ARCHMAID_GUI_PERF=1 make gui-test`), run by the owner on the desktop
  machine when it is quiet. CI's Broadway display is too slow to measure them,
  so CI checks correctness only (`docs/TESTING.md` → GUI measurements).
- **GUI RAM on NVIDIA.** With GTK's default GPU renderer (Vulkan) the idle
  RSS is ~280–390 MB, far over the 120 MB budget; with `GSK_RENDERER=cairo`
  it is ~110–120 MB and the first frame ~85 ms instead of ~375–450 ms. The
  difference is NVIDIA's userspace driver mapped into the process, so
  archmaid-gui defaults to cairo (`ui.DefaultRenderer`, only when
  `GSK_RENDERER` is unset; `TESTING.md` → GUI measurements).

- **Decided: colour for the helper's systemd tools (task #111 asked,
  #133 built, the owner said yes).** See "Colour in the run output".
  Checked in the VM (rounds 10 and #173): `systemd-tmpfiles --clean` and
  `journalctl --vacuum-*` print no escape codes on success, even with
  `SYSTEMD_COLORS=1`, so a clean run shows nothing to colour; colour only
  shows on systemd warnings or errors.
- Test gaps (e2e breadth, hand-only checks) are tracked in
  `TESTING.md` → Known gaps.

- **The Run page's keyboard target is the step's title row (#207).** The
  timeline is one `GtkListView` with a header per step (`ListHeader`); the
  headers are not in the view's focus chain, and the view is one Tab stop.
  So `timeline.go` gives each header's clickable line (`stepHeader.row`)
  `focusable` + the `am-step-row` class (hover and `:focus-visible` highlight
  in `style.css`), its own key controller (Enter/Space toggles, Up/Down/
  Home/End call `focusStep`, which scrolls an undrawn header into view and
  focuses it), and a focus controller on the view that hands a Tab arriving
  on the view itself to the row the keyboard was last on (`tl.cursor`; not
  within 200 ms of a row losing focus, or Shift+Tab out of the list would be
  caught again). The list items: never activatable or selectable; the summary
  item of a folded step is not focusable either (`ConnectBind` sets it per
  item, a recycled row is rebound); output lines stay focusable so the keys
  still scroll. Don't make the summary item activatable to "fix" the
  highlight: the owner wants the title row highlighted.
- **Clear caches with both caches empty never takes the Run page (#207).**
  The engine still ends it as a discovery (`DiscoveryResult{Nothing}` with
  `actions.NoCacheText` as its Notice, last-run `nothing-found`, the CHECK
  log line); `ui/nothing.go` drops that one `DiscoveryResult` for
  `actions.IDClearCaches` from the batch before the model sees it (so the
  Run page keeps the last run, as for a declined gate), and on the action's
  `Finished{Notice}` goes back to the page `Start` left (`Window.pageBefore`)
  and shows an AdwAlertDialog (`Window.nothingDialog`) headed
  `actions.NoCacheHeading`. A failed look is `DiscoveryFailed`, not
  `Nothing`, so it still gets the Run page. Clear caches (only it: `Window.stayUntilQueue`, set in `start` for
  `actions.IDClearCaches`) stays on the page it was started from while it
  measures (the status line and the cell say "Measuring the caches…") and
  while its pick is open or cancelled; `redraw` switches to Run at `c.Reset`,
  the queue start of a non-empty pick (or a failed look's Discovery page).
  Every other action still switches at Start: their discovery results (Repair
  "No broken packages found", services, downloads, the audit's report) are
  pages of their own that the user came for, and Repair's and the audit's
  look can be the whole action; a generic "stay until the first step" would
  hide those and was not asked for.
