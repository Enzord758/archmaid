# HANDOVER

The living orientation doc: where things are, the decisions you shouldn't undo
without reading why, how to live-test safely, and what's still open. It is
deliberately **not** a changelog — history is in `git log` (commit messages
carry the root-causing and verification), tests are in `TESTING.md`, features
are in `cmd/archmaid/README.md`. Keep this file current-state only; rewrite entries rather
than appending dated ones.

## Status

Feature-complete: all nine menu actions work, with `--dryrun` for a safe end to
end. The gate is `scripts/check.sh`: race tests, vet, gofmt and coverage
floors (see `TESTING.md`). Verified live against a real Arch system: Full
Cleanup runs, the sudo/faillock flows, and the AUR audit against the real AUR.

## Code map (`cmd/archmaid/`, `package main`)

| File | Owns |
|---|---|
| `main.go` | package doc + `main()`: `--askpass` dispatch to `privilege.RunClient`, the askpass server's requests fed to the program as `askpassMsg`s, `startup` (the askpass relay and flag parsing: `parseArgs`: `--dryrun`, `--downloads`, `--help`/`-h`; anything else exits 2 — it writes and deletes nothing), `tea.NewProgram` |
| `downloads.go` | "Delete old downloads" glue: `downloadsOptions` (process state -> `downloads.Options`), `deleteOldDownloads` (the in-process step's `Run`, abort logging), `waitForDownloads`; the discovery is `internal/actions` |
| `internal/downloads` | the resolver (`Resolve(Options)`), `Scan(dir, now, Options) Plan`, `Plan.Confirm()`, `Delete(ctx, Plan, Options) (Result, error)`; ctime, Lstat, `os.Root`, the mountinfo boundary and the per-node re-check; no UI, no logging, no env reads |
| `model.go` | screens, the `model` struct, `initialModel`, `Init` (the menu is `actions.Menu`) |
| `update.go` | `Update`/`handleKey` — the single source of state transitions — plus `activate` (menu → queue or discovery), `handleDiscovery` (an `actions.Discovery` → its screen), queue, confirm and abort orchestration |
| `view.go` | `View` and every renderer; `popupLayer` is the one place that knows which screens are popups |
| `tasks.go` | `runStep`/`stepSpec` (a `steps.Step`'s command via `privilege.Sudo.Command`, started through `internal/proc`), `waitForStream` (`proc.Next` as a `tea.Cmd`: pumps the output to the UI in batches), the sudo probe/auth/keep-alive glue, `discoveryMsg` and `discover` (an action's `Discover` as a `tea.Cmd`), `actionEnv` (the `actions.Env` the TUI hands the actions), `editConfig` |
| `output.go` | the running step's output pane: full buffer for the log, capped tail for the viewport, follow-the-bottom |
| `audit.go` | the AUR audit's tea glue: `currentAuditConfig` (the package vars -> `audit.Config`, read on the model's goroutine; `foreignPackages` runs `pacman -Qqm` behind `Config.Foreign`), the menu action (`menuAudit`, handed to `internal/actions` as `Env.AuditReport` by `actionEnv`; `auditOutcome` → `actions.Notice`/`Report`), `auditStep` + `auditStepResult` (the topgrade gate and the snapshot commit), the model's prefetch (`auditFetch`, wrapping `audit.Fetch`), and the styled `renderAudit` |
| `log.go` | the TUI's seam to `internal/logstore`: one package-level store built from `logDir`/`dryRun` (`logs()`, cached under a mutex, rebuilt when either changes, with the run directory's stamp fixed for the process), thin `appendLog`/`readLog` wrappers (the store rotates the run history itself, see the state-dir landmine), and the "Log is empty." rendering. `logs()`/`appendLog` are for the model's goroutine; anything that logs off it captures the `*logstore.Store` where it is set up |
| `askpass.go` | askpass tea glue: `startAskpassServer`/`stopAskpassServer` (the `askpassWrapper` seam), `privileged()` (a `privilege.Sudo` with that wrapper), `askpassMsg`/`askpassResultMsg`, `answerAskpass` (`Request.Answer` off the UI thread), `verifyAskpassPeer` (`Request.Verify` for the running step), the popup's `askpassRequester`; the popup itself is `handleAskpass` in `update.go` |
| `overlay.go` | ANSI-aware popup compositor |
| `status.go`, `theme.go` | the status line's tea glue (`refreshStatus`, `refreshStatusNow`, the `tickFn` seam; the reading itself is `internal/sysstat`: statfs, MemAvailable from /proc/meminfo, a cache-dir walk — no child processes); the Tokyo Night styles |

### `internal/` (UI-free: nothing here may import charmbracelet; `check.sh` enforces it)

| Package | Owns |
|---|---|
| `internal/sanitize` | `String(s, Mode)` with `KeepSGR`/`Plain`: the one filter for untrusted text (command output, logs); and the one-line text helpers `OneLine`, `Clip`, `ClipTail`, `Field`, `QuoteName` |
| `internal/logstore` | the on-disk log: `New(Options{Dir, DryRun, Now})`, `Append` (plain text, 0700/0600 + chmod on write), `Read` (`ErrEmpty` when there is nothing; the TUI renders that text), `Prune(keep)`, `Root`, `RunDir`, `Path`, `Categories`; `WriteLastRun`/`ReadLastRun` for `last-run.json` (`RunRecord`, see state files below) |
| `internal/audit` | the AUR audit itself: `Run(ctx, Config, now) (Result, error)` with `Config{RPCURL, SnapshotPath, StatePath, Deadline, DryRun, Foreign, Log}`; `Result` (`Total`, `Findings`, `Baseline`; `Text()` plain report, `GateReason()`, `Commit(holdHigh, accepting)`, `LogSummary()`, `Note()`, `High()`/`Clean()`); `Prefetch(Config) *Fetch` with `Wait(ctx)`/`Cancel()`/`Done()`; the RPC (chunked POST, 8 MiB cap, unrequested names dropped), classification, the snapshot (`Entry`, `LoadSnapshot`, `WriteSnapshot`: atomic 0600 + flock, old-format compatible), `State`/`WriteState`/`ReadState` (audit-state.json), `ErrNoForeign`, `ErrSnapshotLocked`, `ShortErr`. No UI, no globals: everything comes in through `Config` |
| `internal/sysstat` | `Paths`/`DefaultPaths`, `Read(Paths) Status`, `Status.Line()`, the df/du/free-style size formatters; no UI imports |

Shared packages under `internal/` are UI-free (the gate fails any that imports
charmbracelet, except `internal/tui`):

| Package | Owns |
|---|---|
| `internal/privilege` | getting root without prompting on the tty. `Privileged` (`Probe`, `Authenticate`, `Refresh`, `Command`) and its one implementation `Sudo{Wrapper}`: `sudo -n -v` probe and keep-alive, two-stage `Authenticate` (`CheckPassword` = `su -s /usr/bin/true <user>`, then `sudo -S -v`; lockout notices), `Command` (sudo gets `-A` or `-n`; `Env` strips `SUDO_ASKPASS` and gives the wrapper only to privileged steps). Askpass: `Listen(runtimeDir, exe, ServerOptions)` → `*Server` (`Requests`, `Wrapper`, `Close`; private 0700 dir, shell-quoted wrapper, per-client read deadline and `MaxPrompt` cap, `SO_PEERCRED` + start time per client), `*Request` (`Prompt`, `Peer`, `Verify(stepPid, stepStart)` → `Requester`, `Answer(pw)` — refused unless Verify passed, su-checked before anything is written — and `Cancel`; only a Server makes one; other packages' tests use `privtest`), `RunClient(sock, prompt)` (the `--askpass` helper). `FaillockStatus(user, now)` → `Status{Failures, Locked, Unlock, Deny}` (`LastAttempt()`): pam_faillock's state, read before any password or polkit prompt (see the faillock landmine) |
| `internal/proc` | child processes: `Runner.Start` (a `Spec` run with `Options{DryRun, WaitDelay, KillGrace}`, its combined output streamed as `Msg`s on one ordered channel, the dry-run echo, `ErrWaitDelay` → success on a clean exit), `Next` (blocks for one message, drains up to `BatchMax` more into a `Batch`), `Child.Stop` (SIGINT, SIGKILL after the grace), `Query` (run a read-only command, return stdout, keep the `*exec.ExitError` and stderr), `Read` (`/proc/<pid>`: parent, group, session, start time, euid, exe) |
| `internal/steps` | what a unit of work is: `Step` (stable `ID`, the root helper's `Verb` where one exists, `Argv` or an in-process `Run(ctx, Env) Result`, `Confirm`/`Optional`/`OnAccept`, `Items`/`Cascade` (what a confirm lists, as data), `NeedsPriv`, `AlwaysLog`, log `Category`, `Prefetch`), `Result` (`Output`, `Err`, `ConfirmNext`, `Commit`, `InsertNext`; `Check` forbids gating and inserting at once), `ErrResultTaken` (the cause a front end releases an in-process step's ctx with once its result is taken; any other cancel is an abort), `Status` (`Pending` … `AuthRefused` — privilege not granted, one status whatever the reason, since pkexec exits 127 for every refusal — and `Aborted`) |
| `internal/actions` | the menu as data: `Menu []Action` (`ID`, `Label`, `Icon`, `Gate`, `Busy`, `Steps func(Env) []steps.Step` or `Discover func(ctx, Env) Discovery`), `ByID`; the queues (Full Cleanup, Run topgrade, Clear user cache), the orphan probe, Repair's user scan and root recheck, the services discovery and `DisableUnits`, the downloads discovery. `Discovery` is a typed union: `Notice`, `Pending`, `NeedsSelection{Items, Steps}`, `Report{Text, Accept}` (`Accept func() error` accepts the flagged findings, nil when there is nothing to accept), `NeedsRoot{Items, Busy, AuthFailed, Rescan}`. `Env` carries the run's flags and what still lives in the TUI, as func values: the AUR audit (`AuditStep`, `AuditReport`) and the downloads delete step (`Downloads`, `DeleteDownloads`). Those four are required (a nil one panics mid-action); `Env.Validate` names any left out, and the TUI's `main` calls it on `actionEnv()` at startup. `DryRun`, `LookPath` and `RepairWorkers` may stay zero |
| `internal/engine` | the run logic as a state machine with no IO, shared by the TUI and the GUI. **API frozen at E0: changes after this are additive only** (new types, fields, constants; nothing renamed, removed or given a new meaning). The package doc is the contract: event ordering within a step, prompt IDs (stale ones ignored), that only `OutputChunk` may be coalesced or dropped (by a Driver, never the engine) and that no Event carries a password. `Session` (`New(Options{DryRun, Now, Env, Auth})`, checked by `Options.Validate`; `Start`, `Confirm`, `SubmitPassword`, `CancelPrompt`, `Select`, `Abort`, `Handle(Input)`, each returning `Output{Events, Effects}`) runs, as of E1–E4, an action's discovery (a notice or a queue) and the whole queue core: the sudo probe and the proceed-gate, step confirms, steps' output and results, the logging policy, steps a result inserts (`InsertNext`) or gates (`ConfirmNext`, failing closed on a refused result), hyprpm kept last, the last-run record; auth (`auth.go`: the up-front password with three attempts and the give-up that refuses the queue's `NeedsPriv` steps, the faillock read before the prompt and before each polkit helper step — a locked account refuses the helper step without a dialog —, Repair's `NeedsRoot` probe/password/rescan, `WaitPolkit` until `PrivilegeGranted` or a `NotGranted` exit); askpass (`askpass.go`: only for the running sudo step, peer-checked by the Driver, on the run's attempt count). Abort, selection and reports (E5) still panic (`queue.go` holds the run, `session.go` the API). The TUI doesn't use it yet (E7); `Driver` (`Events`, `Send(Command) bool` — never blocks — `Close`) is what the GUI talks to. Events mirror `internal/testtranscript`'s vocabulary so a `sessionTarget` maps them 1:1; privilege is one outcome (`steps.AuthRefused`), with `AuthWaiting`/`AuthResolved`/`FaillockState` for the polkit flow; passwords travel only as `Secret` (prints `<redacted>`) in Commands and Effects |
| `internal/engine/enginetest` | test helpers shared by the engine's tests, the stub's and the transcript harness: `CheckContract(t, events)` holds an event sequence to the package doc's ordering rules |
| `internal/engine/stub` | a `Driver` that replays a scripted event sequence (`Script` of `Cue{After, Event}`) on a `Clock` (the wall clock, or `FakeClock`, which jumps instead of sleeping): prompts wait for their answer, stale IDs and early commands are ignored, `Abort` withdraws the prompt and ends the run like the engine would. `FullCleanup(start)` is a ready-made run shaped like the orphan-cascade transcript. The GUI builds against it until the real Driver (E6) |

### `gui/` (its own Go module: the GTK4/libadwaita GUI, a walking skeleton on the stub Driver)

`gui/go.mod` is `github.com/Enzord758/archmaid/gui`, with a `replace` onto the
root module, so the root's `go test ./...`, `check.sh` and CI's `core` job
never compile gotk4. gotk4 `v0.4.1` and gotk4-adwaita
`v0.0.0-20260808200908-d4aecaa0ff32` are pinned exactly. No `go.work` is
committed (`.gitignore`d): it would pin a toolchain line and fold the GUI's
requirements into the root build; make one locally for gopls if you like.
Binary `archmaid-gui`, application id `io.github.enzord758.archmaid`.

| Path | Owns |
|---|---|
| `gui/cmd/archmaid-gui` | `main` → `ui.Main` |
| `gui/internal/runview` | plain Go, no gotk4: `Model` (the Run page's state: checklist `Row`s keyed on step ID, the open confirm, phase/status, the output ring capped at `OutputCap` = 10k lines; `Apply(events) Change` says exactly what to redraw, including the output diff `OutRemove`/`OutAppend` so a batch bigger than the cap never inserts a line only to drop it), `Pump` (one goroutine reads `Driver.Events()`, merges adjacent `OutputChunk`s of a step, drops the oldest queued lines past `MaxPending` counting them in `Dropped`, and posts one flush at a time to the main thread; a flush applies at most `FlushLines` = 512 lines, so a flood is sliced into ~1–2 ms pieces), `Flood` (the 50k-line stub script) |
| `gui/internal/ui` | the only gotk4 package: `Main` (picks the stub script, `ARCHMAID_GUI_SCRIPT=flood` for the 50k one), `Run`, `Window` (header with Run/Stop, checklist `ListBox` of `AdwActionRow`s, output `ListView` over a `GtkStringList`, status bar, toasts, `AdwAlertDialog` per `NeedsConfirm` with "No" as default and close response and a red "Yes" for `Destructive`, close-mid-run confirm). `harness_test.go` is the headless harness (see `TESTING.md` → GUI) |
| `gui/scripts/headless.sh` | runs a command against a private `weston --backend=headless` and a private, activation-free D-Bus, so GUI tests never open a window on the desktop |

## Decisions and landmines

Each of these was learned the hard way. The reasoning lives in the code
comments and `git log`; this is the "read before you touch it" list.

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
  Don't "fix" one by copying the other.
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
- **Nothing may prompt on the raw tty.** Sudo steps run `sudo -A`, and
  `SUDO_ASKPASS` routes any re-prompt back into the TUI as a popup. The password
  is passed straight through, never stored or logged, and never kept in a
  struct that outlives the call (`privilege.Request` holds no password).
  `sudo -n -v` is only the cache probe and the 60 s keep-alive
  (`Sudo.Probe`, `Sudo.Refresh`).
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
  split), so the askpass peer checks below (F7b) are the control there. If the
  GUI later gives topgrade its own pty, that doesn't change the sudo path.
- **An askpass request is answered only from the running sudo step.** The
  server records each client's pid/uid (`SO_PEERCRED`) and start time;
  `handleAskpass` hangs up unless a `NeedsPriv` step is running, and
  `Request.Verify` requires: our uid; the same start time as at accept (pid
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
- **Repair scans as the user first, and asks for sudo only to recheck what it
  flagged.** Repair's discovery (`internal/actions`, `discoverRepair`) lists
  the installed packages (`pacman -Qq`) and runs `pacman -Qkq -- <chunk>` as
  the user, in up to `Env.RepairWorkers` (min(cores, 16)) parallel chunks,
  exec'd directly. Nothing flagged is the "No broken packages" notice with no
  probe and no prompt. Otherwise it returns `NeedsRoot`, which the TUI holds
  as `model.needsRoot` and authenticates like any
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
  in case `app.Quit` ended it. While an `AdwDialog` is up, libadwaita turns the
  window's close into closing the dialog, so the close-mid-run dialog closes
  the window from its `closed` signal, not from `response`.
- **`hyprpm` runs last.** It drops the sudo ticket on exit, silently breaking the
  keep-alive; Full Cleanup runs topgrade with `--disable hyprpm` and appends its
  own final step. `TestNoPrivilegedStepAfterHyprpm` (`internal/actions`) fails
  any action whose queue has a root step, or any in-process step (it may insert
  one, as the orphan probe does), after hyprpm.
- **Full Cleanup's order:** AUR audit → topgrade → `paccache -r` → optional
  `paccache -rk0` → orphan probe → (orphan removal, inserted by the probe) →
  `yay -Sc` → tmpfiles → journal vacuum → `pacman -Qkq` → hyprpm. Building the
  queue runs nothing; the only work `activate` starts is the audit prefetch,
  off the UI thread. `BenchmarkActivateFullCleanup` (~5 µs) holds that in the
  opt-in perf guard, with a fixed 1 ms limit.
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
- **Logging policy.** Ordinary steps log only failures. `AlwaysLog` steps log
  success too, and a `SKIP … (declined)` entry when their confirm is declined:
  anything that removes or overwrites something, topgrade (the step that changes
  the most), and the AUR audit. That keeps what those steps did auditable from
  archmaid's own log. An aborted in-process step's result is dropped by
  `stepSeq`, so `finishStep` never logs it; the downloads delete therefore
  writes its own `ABORT` entry (exactly what it deleted, and where it
  stopped) when it sees the abort, or from a `context.AfterFunc` if the abort
  lands after it returned — the result's `commit` disarms that when the
  result is taken, which is why `handleStepResult` releases the step's ctx
  only *after* `commit`. Both writers run off the model's goroutine, so they
  append to the store `actionEnv` captured on it, never to `logs()`
  (which reads `logDir`/`dryRun`). `main` waits for such a run (`waitForDownloads`)
  before exiting. Logs are split into
  `topgrade`/`orphans`/`cleanup`/`audit` files per run directory; the newest 5
  run directories are kept, `dryrun/` is a single reused slot, and files in the
  state-dir root (the AUR snapshot, `last-run.json`) are ignored by the rotation. Only `appendLog`
  creates a run directory (viewing logs must not, or it evicts a real run), and
  logs are private: directories 0700, files 0600.
- **State files** (under `~/.local/state/archmaid/`, for the planned DMS widget, see
  `docs/dms-widget.md`). `last-run.json` is written by `model.recordRun` (cmd/archmaid/log.go)
  exactly once per queue, at its end: summary (`ok`/`failed` by `errs`; `incomplete` when
  nothing failed but a step was refused sudo, counted in `steps_refused`; `cancelled` if no
  step ran, e.g. sudo gave up), ctrl+c abort (`aborted`), a declined required step
  (`aborted`, or `cancelled` if nothing ran yet), a declined gate or cancelled password
  prompt (`cancelled`). `runDone` makes it idempotent; `activate`/`resetRun` re-arm it. The
  action is the menu `id` (the three static actions have ids `fullcleanup`, `usercache`,
  `topgrade` for this). A dry run writes `dryrun/last-run.json` instead and never touches
  the real one. `audit-state.json` is planned, not written yet.
- **Untrusted text goes through `sanitize.String`.** Command output (topgrade, yay, AUR
  build scripts) can carry escapes that retitle the terminal, write the
  clipboard (OSC 52) or redraw what the user reads. Streamed output keeps only
  SGR colours (`sanitize.KeepSGR`); logs are plain text, both when written and
  when an older log is read back (`sanitize.Plain`); AUR names and maintainers
  go through it too (`sanitize.Field`: plain, one line, capped). It also drops
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
  field fails that test; add new fields freely, bump `v` for removals. The widget already
  understands `outcome: "incomplete"` + `steps_refused`; the Go writer test only simulates
  them until `RunRecord` carries them (then use the real writer in `withRefused`).
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
  as an in-process step (`Step.Run`, run as a `tea.Cmd` — not a shell command, so
  there's nothing to signal: an abort cancels the step's `ctx`, which ends the
  pacman query and the RPC at once, and `stepSeq` drops the late result). The
  whole audit has one deadline, `auditDeadline` (20 s for the query and every
  RPC chunk together), not a timeout per request. The lookup itself starts
  the moment the action is chosen: `activate` starts an `auditFetch`
  (`audit.Prefetch`: a goroutine publishing its result by closing `Done()`,
  owned by the model as `m.auditFetch`) so it runs while the password prompt
  or the gate is up, and
  the step awaits it instead of fetching again (one RPC per run). It only
  reads: the snapshot commit still comes from the step's result, so the gate
  semantics below are unchanged. Esc on the password prompt, a declined gate,
  an abort and the end of the run cancel it (`dropAuditFetch`); three wrong
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
  exactly one done (`proc.Runner.Start`). The pump (`waitForStream`, over
  `proc.Next`) blocks for the first message,
  then takes whatever else is already buffered — up to `proc.BatchMax` (256)
  lines, stopping at the done — and delivers it as one `streamBatchMsg`
  stamped with the launch's `stepSeq`; `handleStream` applies the whole batch
  with one `SetContent`, or drops it if the seq is stale. A slow producer gets
  one-line batches; a flood gets full ones, so the UI catches up by itself. The
  full output still accumulates in `m.buf` (what `finishStep` logs, so
  `AlwaysLog` steps keep everything); the viewport gets only the newest
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
  (`cmd/archmaid/transcript_session_test.go`) replays the transcript scenarios
  through an `engine.Session`, playing its Effects against the same shims, and
  compares with the goldens the TUI records, byte for byte. A behaviour change
  goes into both front ends, or a golden changes deliberately; never add a
  session-only golden. What the Session can't run yet is skipped by name with
  the milestone that brings it (`sessionSkips`): remove the skip in the
  commit that makes it pass. Where the TUI was fixed to fail closed (a
  refused result keeps its gate) the Session mirrors it, and it adds two
  guards of its own: hyprpm is moved last in any plan, and a result that
  would insert a root step after hyprpm ran is refused like a malformed one.
- **`m.bg` is one field, not a stack.** A popup that overwrites it with its own
  screen value makes `renderScreen` recurse forever; see `handleAskpass` in
  `update.go`.

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
  creates a real run's directory prune (dry-run stores never do, and the
  run's own directory is never pruned). `startup` (askpass relay, flags,
  `--help`/`-h` exit 0, anything unknown exit 2) touches nothing on disk;
  `TestStartupDeletesNothing` pins it. A front-end check that fails before
  the first log entry (e.g. an `Env.Validate`) is safe by construction — keep
  it that way: never call `Prune` from a startup path. `scripts/check.sh` and
  `make e2e` snapshot the real state dir (names, sizes, mtimes) before and
  after and fail on any difference (`scripts/state-guard.sh`).

## Open items

- **Remove the engine.New guard when E5 lands.** Until the engine Session
  stops panicking (errNotImplemented) on SubmitPassword, Abort and some
  discoveries, `internal/engine/guard_test.go`
  (`TestNoProductionCallerBeforeE5`) fails any production package other than
  engine, engine/stub and engine/enginetest that calls `engine.New`. Delete
  that file when E5 lands and a real caller is wired in.

- **`auth_admin_keep` inside a real session.** The live test (see
  `docs/polkit-live-test.md`) showed keep scoped to the requesting process when
  there is no login session; re-check on tty3 or from the GUI, where polkit may
  scope it to the session. The docs assume the wider (session) scope, which is
  the safe assumption. The owner's step-by-step check, with a throwaway
  action and a results table: `docs/polkit-keep-session-check.md`.

- **GUI skeleton TODOs, waiting for the real Driver (E6):** the polkit
  dialog → helper progress in the checklist (`AuthWaiting{WaitPolkit}`,
  `AuthResolved`, `FaillockState` are recorded in `runview.Model.Auth` but not
  shown) and the askpass dialog for the AUR step (`NeedsPassword` →
  `SubmitPassword`); today a password, selection or report prompt is
  cancelled with a toast. ANSI colour is stripped, not rendered (P6b step 2).
- **GUI RAM on NVIDIA.** With GTK's default GPU renderer (Vulkan) the idle
  RSS is ~280–390 MB, far over the 120 MB budget; with `GSK_RENDERER=cairo`
  it is ~110–120 MB and the first frame ~85 ms instead of ~375–450 ms. The
  difference is NVIDIA's userspace driver mapped into the process. Whether
  archmaid-gui should default to cairo is the owner's call (`TESTING.md` →
  GUI measurements).

- Test gaps (e2e breadth, hand-only checks) are tracked in
  `TESTING.md` → Known gaps.
