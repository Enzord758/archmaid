# Coverage exemptions (category E)

Statements no test or run can reach, as measured at `5cd3d64` (178 statements in 127 blocks). Machine-readable copy: [`coverage-exempt.tsv`](coverage-exempt.tsv), which `scripts/covblocks -reachable -exempt` reads (`make e2e-cover` prints both figures per scope; `scripts/check.sh` prints the core figure). Why the list exists: Go has no pragma to leave a branch out of the count, so raw coverage reaches 100 % only if these branches go. The goal is **100 % of reachable statements**: covered / (total - exempt). Every exemption is one line here, in review; an entry whose lines moved, or that a test now covers, is reported as stale and takes nothing out of the denominator, so a drifting list can only lower the reachable figure, never inflate it.

Refreshing: after a code change that moves a listed block, re-run the measurement (docs/coverage-map.md, "How it was measured") and `scripts/covmap/report.py`; do not edit the tsv by hand.

## Recommendations

| Recommendation | Means | Blocks | Statements | of which security code |
|---|---|---|---|---|
| `keep-exempt` | the branch is right as written (defensive, fail-closed, or a platform fallback); it stays in the exempt list | 103 | 144 | 41 |
| `make-testable` | a small seam turns it into a unit test (A): the shared atomic-write helper, or a hook between a TOCTOU check and its use | 23 | 33 | 8 |
| `remove` | dead code: delete the branch (a reviewer confirms it cannot be reached) | 1 | 1 | 0 |

**Security code** (`yes` in the tsv): `cmd/archmaid-helper`, `internal/privilege`, `internal/helperclient`, `internal/ttyguard`, `gui/internal/polkitagent`, `gui/internal/realdriver`, `internal/downloads` (it deletes the owner's files), `internal/driver/keep.go`, askpass in `engine` and `tui`. Dev 3 reviews any removal, seam or test there before it lands; the others need only the usual review.

## `make-testable` (33 statements)

The atomic-write pattern (CreateTemp, write, chmod, fsync, close, rename, fsync the directory) is copied into `audit.WriteSnapshot`, `audit.WriteState`, `logstore.WriteLastRun` and `gui/internal/settings.Save`. One shared helper taking an injectable file turns those branches into unit tests and removes three copies. The downloads TOCTOU guards need a hook between the check and the use (like `Options.EntryHook`); that is security code, so Dev 3 decides.

| Where | Function | Stmts | Security | Why it can't run |
|---|---|---|---|---|
| `gui/internal/settings/settings.go:96-98` | `Save` | 2 |  | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO |
| `gui/internal/settings/settings.go:100-102` | `Save` | 2 |  | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO |
| `gui/internal/settings/settings.go:104-106` | `Save` | 2 |  | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO |
| `internal/audit/snapshot.go:202-204` | `WriteSnapshot` | 2 |  | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/audit/snapshot.go:206-208` | `WriteSnapshot` | 2 |  | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/audit/snapshot.go:210-211` | `WriteSnapshot` | 1 |  | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/audit/snapshot.go:217-218` | `WriteSnapshot` | 1 |  | opening the directory it just wrote into can't fail |
| `internal/audit/state.go:75-77` | `WriteState` | 2 |  | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/audit/state.go:79-81` | `WriteState` | 2 |  | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/audit/state.go:83-84` | `WriteState` | 1 |  | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/audit/state.go:90-91` | `WriteState` | 1 |  | opening the directory it just wrote into can't fail |
| `internal/downloads/downloads.go:180-181` | `checkDownloadsDir` | 1 | yes | Stat after a successful EvalSymlinks fails only if the folder vanishes in between (race) |
| `internal/downloads/downloads.go:366-367` | `entryIsOld.func` | 1 | yes | d.Info fails only if the node vanishes mid-walk (race) |
| `internal/downloads/downloads.go:576-577` | `remover.removeTree` | 1 | yes | Lstat of a node just listed fails only if it vanishes (race) |
| `internal/downloads/downloads.go:608-609` | `remover.removeContents` | 1 | yes | OpenRoot/Lstat/Open/Readdirnames of a directory entryIsOld just walked fail, or it is swapped, only in a race; no seam between the checks |
| `internal/downloads/downloads.go:612-613` | `remover.removeContents` | 1 | yes | OpenRoot/Lstat/Open/Readdirnames of a directory entryIsOld just walked fail, or it is swapped, only in a race; no seam between the checks |
| `internal/downloads/downloads.go:614-615` | `remover.removeContents` | 1 | yes | OpenRoot/Lstat/Open/Readdirnames of a directory entryIsOld just walked fail, or it is swapped, only in a race; no seam between the checks |
| `internal/downloads/downloads.go:618-619` | `remover.removeContents` | 1 | yes | OpenRoot/Lstat/Open/Readdirnames of a directory entryIsOld just walked fail, or it is swapped, only in a race; no seam between the checks |
| `internal/downloads/downloads.go:623-624` | `remover.removeContents` | 1 | yes | OpenRoot/Lstat/Open/Readdirnames of a directory entryIsOld just walked fail, or it is swapped, only in a race; no seam between the checks |
| `internal/logstore/lastrun.go:82-84` | `Store.WriteLastRun` | 2 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/logstore/lastrun.go:86-88` | `Store.WriteLastRun` | 2 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/logstore/lastrun.go:90-92` | `Store.WriteLastRun` | 2 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/logstore/lastrun.go:94-95` | `Store.WriteLastRun` | 1 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |

## `remove` (1 statements)

| Where | Function | Stmts | Security | Why it can't run |
|---|---|---|---|---|
| `internal/audit/result.go:186-186` | `packageNames` | 1 |  | only called with the gating (high) names, never none |

## `keep-exempt` (144 statements)

| Where | Function | Stmts | Security | Why it can't run |
|---|---|---|---|---|
| `cmd/archmaid-helper/dev_off.go:9-9` | `devLockPath` | 1 | yes | devLockPath is only called when devBuild, which is false in this build |
| `cmd/archmaid-helper/internal/helper/checks.go:38-39` | `LstatFile` | 1 | yes | os.FileInfo.Sys() is always a *syscall.Stat_t on Linux |
| `cmd/archmaid-helper/internal/helper/checks.go:135-135` | `acquireLock` | 1 | yes | flock(LOCK_EX|LOCK_NB) on a regular file just opened fails only with EWOULDBLOCK (busy, covered); ENOLCK/EBADF/EINTR are not producible |
| `cmd/archmaid/main.go:33-34` | `main` | 1 |  | prctl(PR_SET_DUMPABLE) on our own process doesn't fail |
| `cmd/archmaid/main.go:40-42` | `main` | 2 |  | tui.ActionEnv always fills every required field (Env.Validate is the guard for a future change) |
| `gui/internal/polkitagent/polkitagent.go:298-299` | `Start` | 1 | yes | openPTY failing |
| `gui/internal/polkitagent/polkitagent.go:370-373` | `Start` | 3 | yes | TIOCEXCL on the pty just opened can't fail |
| `gui/internal/polkitagent/polkitagent.go:718-719` | `Agent.waitNoEcho` | 1 | yes | the termios read fails only on a closed pty |
| `gui/internal/polkitagent/polkitagent.go:737-738` | `ioctl` | 1 | yes | posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail |
| `gui/internal/polkitagent/polkitagent.go:743-744` | `ioctl` | 1 | yes | posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail |
| `gui/internal/polkitagent/polkitagent.go:746-747` | `ioctl` | 1 | yes | posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail |
| `gui/internal/polkitagent/polkitagent.go:755-756` | `openPTY` | 1 | yes | posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail |
| `gui/internal/polkitagent/polkitagent.go:759-761` | `openPTY` | 2 | yes | posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail |
| `gui/internal/polkitagent/polkitagent.go:764-766` | `openPTY` | 2 | yes | posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail |
| `gui/internal/polkitagent/polkitagent.go:769-771` | `openPTY` | 2 | yes | posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail |
| `gui/internal/runview/stepline.go:51-52` | `Model.seqLine` | 1 |  | StepLine derives seq from viewLo and the run lengths, so it is always inside the view and the ring; the guard is for a future change |
| `gui/internal/ui/dump.go:36-37` | `NoCoreFiles` | 1 |  | setrlimit/prctl on our own process don't fail |
| `gui/internal/ui/dump.go:39-40` | `NoCoreFiles` | 1 |  | setrlimit/prctl on our own process don't fail |
| `gui/internal/ui/dump.go:48-49` | `coreRlimit` | 1 |  | setrlimit/prctl on our own process don't fail |
| `gui/internal/ui/logs.go:189-190` | `Window.newLogs.func` | 1 |  | GTK binds only positions the model holds, so i is always below len(p.rows) |
| `gui/internal/ui/secret.go:30-31` | `editableBytes` | 1 |  | the password entry is always a GtkEditable with a buffer |
| `gui/internal/ui/style.go:39-40` | `loadStyle.func` | 1 |  | the embedded CSS parses; only a future GTK could report errors |
| `gui/internal/ui/style.go:43-46` | `loadStyle.func` | 3 |  | the embedded CSS parses; only a future GTK could report errors |
| `internal/audit/snapshot.go:76-77` | `Entry.UnmarshalJSON` | 1 |  | b starts with a quote and the outer decoder already validated the JSON, so the string always unmarshals |
| `internal/audit/snapshot.go:177-178` | `WriteSnapshot` | 1 |  | json.MarshalIndent of map[string]Entry can't fail |
| `internal/audit/snapshot.go:236-236` | `lockSnapshot` | 1 |  | flock(LOCK_EX|LOCK_NB) on an open regular file fails only with EWOULDBLOCK (handled) or EINTR |
| `internal/audit/state.go:63-64` | `WriteState` | 1 |  | json.MarshalIndent of State can't fail |
| `internal/downloads/downloads.go:48-49` | `PasswdHome` | 1 | yes | user.Current fails only when the uid has no passwd entry |
| `internal/downloads/downloads.go:295-296` | `Scan` | 1 | yes | Stat(".") of a root it just opened can't fail |
| `internal/downloads/downloads.go:381-382` | `changedBefore` | 1 | yes | on Linux every os FileInfo carries a *syscall.Stat_t and Stat of an open root can't fail |
| `internal/downloads/downloads.go:395-396` | `dirIdentity` | 1 | yes | on Linux every os FileInfo carries a *syscall.Stat_t and Stat of an open root can't fail |
| `internal/downloads/downloads.go:399-400` | `dirIdentity` | 1 | yes | on Linux every os FileInfo carries a *syscall.Stat_t and Stat of an open root can't fail |
| `internal/downloads/downloads.go:409-410` | `<package>` | 1 | yes | on Linux every os FileInfo carries a *syscall.Stat_t and Stat of an open root can't fail |
| `internal/downloads/downloads.go:638-638` | `inode` | 1 | yes | on Linux every os FileInfo carries a *syscall.Stat_t and Stat of an open root can't fail |
| `internal/driver/driver.go:309-310` | `currentFaillock` | 1 |  | user.Current fails only when the uid has no passwd entry; no seam |
| `internal/driver/driver.go:519-519` | `Driver.command.func` | 1 |  | the switch over the closed Command set is exhaustive; the fallthrough return is the compiler's |
| `internal/engine/combine.go:85-85` | `Session.combineNext` | 1 |  | the guard skips a slot that is not Pending or already accepted; every caller advances r.cur past a slot it has settled or accepted, so the loop never meets one |
| `internal/engine/stub/fullcleanup.go:93-93` | `builder.index` | 1 |  | the hand-built script only names its own steps |
| `internal/frontend/topgrade.go:55-56` | `EnsureTopgradeConfig` | 1 |  | O_EXCL finds the file already there only if it appears between the Lstat and the open (race); the result is the same as the Lstat's |
| `internal/helperclient/helpertest/helpertest.go:16-16` | `testOnly` | 1 | yes | test-helper guards: panic outside a test binary or on an empty path |
| `internal/helperclient/helpertest/helpertest.go:29-29` | `UseHelperFile` | 1 | yes | test-helper guards: panic outside a test binary or on an empty path |
| `internal/logstore/lastrun.go:66-67` | `Store.WriteLastRun` | 1 |  | json.Marshal of a RunRecord can't fail |
| `internal/privilege/askpass.go:119-122` | `Listen` | 3 | yes | WriteFile of the wrapper into the 0700 dir just created can't fail |
| `internal/privilege/askpass.go:206-207` | `Server.peerOf` | 1 | yes | SyscallConn/Control/SO_PEERCRED on an accepted unix conn don't fail |
| `internal/privilege/askpass.go:213-214` | `Server.peerOf` | 1 | yes | SyscallConn/Control/SO_PEERCRED on an accepted unix conn don't fail |
| `internal/privilege/askpass.go:216-217` | `Server.peerOf` | 1 | yes | SyscallConn/Control/SO_PEERCRED on an accepted unix conn don't fail |
| `internal/privilege/askpass.go:515-516` | `verifyPeer` | 1 | yes | os.Executable fails only without /proc |
| `internal/privilege/kept.go:150-151` | `KeptPassword.wipeLocked` | 1 | yes | the wipe check can't see non-zero bytes after the wipe |
| `internal/privilege/privtest/privtest.go:18-18` | `testOnly` | 1 | yes | panics outside a test binary |
| `internal/proc/detach.go:57-58` | `groupAlive` | 1 |  | Getpgid errors other than ESRCH (EPERM across namespaces) don't happen for our own children |
| `internal/testenv/cover.go:24-25` | `coverChildren` | 1 |  | the early return is for runs without coverage, which count nothing |
| `internal/testenv/testenv.go:105-105` | `Run` | 3 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:106-106` | `Run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:107-110` | `Run` | 3 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:112-112` | `Run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:126-127` | `mkScratch` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:141-143` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:147-149` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:155-157` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:162-164` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:167-169` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:179-181` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:186-188` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:192-194` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:198-200` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:223-225` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:233-236` | `run` | 6 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:237-237` | `run` | 6 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:238-238` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:239-240` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:241-242` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:245-246` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:247-249` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:250-250` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:251-252` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:253-254` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:255-255` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:257-257` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:258-260` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:261-262` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:263-263` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:265-265` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:266-268` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:269-269` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:270-271` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:272-272` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:324-325` | `InstallTraps` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:327-328` | `InstallTraps` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:336-337` | `InstallTraps` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:367-369` | `TrapInstalled` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:393-394` | `newMarker` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:403-404` | `setChildSubreaper` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/watchdog.go:60-61` | `startWatchdog` | 1 |  | os.Pipe and exec of /bin/sh fail only on fd exhaustion or a missing /bin/sh |
| `internal/testenv/watchdog.go:67-69` | `startWatchdog` | 2 |  | os.Pipe and exec of /bin/sh fail only on fd exhaustion or a missing /bin/sh |
| `internal/ttyguard/ttyguard.go:63-64` | `Hold` | 1 | yes | TIOCGEXCL/TIOCEXCL on an fd IsTerminal just accepted fail only if it is closed concurrently |
| `internal/ttyguard/ttyguard.go:122-123` | `Exclusive` | 1 | yes | TIOCGEXCL/TIOCEXCL on an fd IsTerminal just accepted fail only if it is closed concurrently |
| `internal/ttyguard/ttyguard.go:130-131` | `ioctl` | 1 | yes | TIOCGEXCL/TIOCEXCL on an fd IsTerminal just accepted fail only if it is closed concurrently |
| `internal/ttyguard/ttyguard.go:136-137` | `ioctl` | 1 | yes | TIOCGEXCL/TIOCEXCL on an fd IsTerminal just accepted fail only if it is closed concurrently |
| `internal/tui/askpass.go:36-37` | `startAskpassServer` | 1 | yes | os.Executable fails only without /proc |
| `internal/tui/tui.go:63-64` | `Run` | 1 |  | ttyguard.Hold reports an error only when TIOCGEXCL/TIOCEXCL fails on a terminal that just answered TCGETS; a closed or non-terminal stdin is a Guard that does nothing, no error |
| `internal/tui/tui.go:69-70` | `Run.func` | 1 |  | Release of the guard fails only if the fd was closed concurrently |
| `internal/tui/tui.go:128-129` | `DisableCoreDumps` | 1 |  | prctl(PR_SET_DUMPABLE) on our own process doesn't fail |
| `internal/tui/view.go:90-90` | `model.viewOf` | 1 |  | safe fallback: renderScreen/baseScreen name every screen |
