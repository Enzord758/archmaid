# Coverage exemptions (category E)

Statements no test or run can reach, as measured at `ae00269` (124 statements in 94 blocks). Machine-readable copy: [`coverage-exempt.tsv`](coverage-exempt.tsv), which `scripts/covblocks -reachable -exempt` reads (`make e2e-cover` prints both figures per scope; `scripts/check.sh` prints the core figure). Why the list exists: Go has no pragma to leave a branch out of the count, so raw coverage reaches 100 % only if these branches go. The goal is **100 % of reachable statements**: covered / (total - exempt). Every exemption is one line here, in review; an entry whose lines moved, or that a test now covers, is reported as stale and takes nothing out of the denominator, so a drifting list can only lower the reachable figure, never inflate it.

Refreshing: after a code change that moves a listed block, re-run the measurement (docs/coverage-map.md, "How it was measured") and `scripts/covmap/report.py`; do not edit the tsv by hand.

## Recommendations

| Recommendation | Means | Blocks | Statements | of which security code |
|---|---|---|---|---|
| `keep-exempt` | the branch is right as written (defensive, fail-closed, or a platform fallback); it stays in the exempt list | 70 | 90 | 42 |
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

## `keep-exempt` (90 statements)

| Where | Function | Stmts | Security | Why it can't run |
|---|---|---|---|---|
| `cmd/archmaid-helper/dev_off.go:9-9` | `devLockPath` | 1 | yes | devLockPath is only called when devBuild, which is false in this build |
| `cmd/archmaid/main.go:33-34` | `main` | 1 |  | prctl(PR_SET_DUMPABLE) on our own process doesn't fail |
| `cmd/archmaid/main.go:40-42` | `main` | 2 |  | tui.ActionEnv always fills every required field (Env.Validate is the guard for a future change) |
| `gui/internal/polkitagent/polkitagent.go:181-182` | `Start` | 1 | yes | openPTY failing |
| `gui/internal/polkitagent/polkitagent.go:185-188` | `Start` | 3 | yes | openPTY failing |
| `gui/internal/polkitagent/polkitagent.go:247-250` | `Start` | 3 | yes | TIOCEXCL on the pty just opened can't fail |
| `gui/internal/polkitagent/polkitagent.go:595-596` | `Agent.waitNoEcho` | 1 | yes | the termios read fails only on a closed pty |
| `gui/internal/polkitagent/polkitagent.go:614-615` | `ioctl` | 1 | yes | posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail |
| `gui/internal/polkitagent/polkitagent.go:620-621` | `ioctl` | 1 | yes | posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail |
| `gui/internal/polkitagent/polkitagent.go:623-624` | `ioctl` | 1 | yes | posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail |
| `gui/internal/polkitagent/polkitagent.go:632-633` | `openPTY` | 1 | yes | posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail |
| `gui/internal/polkitagent/polkitagent.go:636-638` | `openPTY` | 2 | yes | posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail |
| `gui/internal/polkitagent/polkitagent.go:641-643` | `openPTY` | 2 | yes | posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail |
| `gui/internal/polkitagent/polkitagent.go:646-648` | `openPTY` | 2 | yes | posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail |
| `gui/internal/ui/dump.go:36-37` | `NoCoreFiles` | 1 |  | setrlimit/prctl on our own process don't fail |
| `gui/internal/ui/dump.go:39-40` | `NoCoreFiles` | 1 |  | setrlimit/prctl on our own process don't fail |
| `gui/internal/ui/dump.go:48-49` | `coreRlimit` | 1 |  | setrlimit/prctl on our own process don't fail |
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
| `internal/driver/driver.go:301-302` | `currentFaillock` | 1 |  | user.Current fails only when the uid has no passwd entry; no seam |
| `internal/driver/driver.go:511-511` | `Driver.command.func` | 1 |  | the switch over the closed Command set is exhaustive; the fallthrough return is the compiler's |
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
| `internal/privilege/kept.go:136-137` | `KeptPassword.wipeLocked` | 1 | yes | the wipe check can't see non-zero bytes after the wipe |
| `internal/privilege/privtest/privtest.go:18-18` | `testOnly` | 1 | yes | panics outside a test binary |
| `internal/proc/detach.go:57-58` | `groupAlive` | 1 |  | Getpgid errors other than ESRCH (EPERM across namespaces) don't happen for our own children |
| `internal/testenv/cover.go:24-25` | `coverChildren` | 1 |  | the early return is for runs without coverage, which count nothing |
| `internal/testenv/testenv.go:94-96` | `Run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:100-102` | `Run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:108-110` | `Run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:115-117` | `Run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:120-122` | `Run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:125-127` | `Run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:131-133` | `Run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:137-139` | `Run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:252-253` | `InstallTraps` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:255-256` | `InstallTraps` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:264-265` | `InstallTraps` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:288-289` | `newMarker` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:298-299` | `setChildSubreaper` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/ttyguard/ttyguard.go:63-64` | `Hold` | 1 | yes | TIOCGEXCL/TIOCEXCL on an fd IsTerminal just accepted fail only if it is closed concurrently |
| `internal/ttyguard/ttyguard.go:122-123` | `Exclusive` | 1 | yes | TIOCGEXCL/TIOCEXCL on an fd IsTerminal just accepted fail only if it is closed concurrently |
| `internal/ttyguard/ttyguard.go:130-131` | `ioctl` | 1 | yes | TIOCGEXCL/TIOCEXCL on an fd IsTerminal just accepted fail only if it is closed concurrently |
| `internal/ttyguard/ttyguard.go:136-137` | `ioctl` | 1 | yes | TIOCGEXCL/TIOCEXCL on an fd IsTerminal just accepted fail only if it is closed concurrently |
| `internal/tui/askpass.go:36-37` | `startAskpassServer` | 1 | yes | os.Executable fails only without /proc |
| `internal/tui/tui.go:63-64` | `Run` | 1 |  | ttyguard.Hold reports an error only when TIOCGEXCL/TIOCEXCL fails on a terminal that just answered TCGETS; a closed or non-terminal stdin is a Guard that does nothing, no error |
| `internal/tui/tui.go:69-70` | `Run.func` | 1 |  | Release of the guard fails only if the fd was closed concurrently |
| `internal/tui/tui.go:128-129` | `DisableCoreDumps` | 1 |  | prctl(PR_SET_DUMPABLE) on our own process doesn't fail |
| `internal/tui/view.go:90-90` | `model.viewOf` | 1 |  | safe fallback: renderScreen/baseScreen name every screen |
