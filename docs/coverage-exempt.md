# Coverage exemptions (category E)

Statements no test or run can reach, as measured at `3f8f0d0` (195 statements in 147 blocks). Machine-readable copy: [`coverage-exempt.tsv`](coverage-exempt.tsv), which `scripts/covblocks -reachable -exempt` reads (`make e2e-cover` prints both figures per scope; `scripts/check.sh` prints the core figure). Why the list exists: Go has no pragma to leave a branch out of the count, so raw coverage reaches 100 % only if these branches go. The goal is **100 % of reachable statements**: covered / (total - exempt). Every exemption is one line here, in review; an entry whose lines moved, or that a test now covers, is reported as stale and takes nothing out of the denominator, so a drifting list can only lower the reachable figure, never inflate it.

Refreshing: after a code change that moves a listed block, re-run the measurement (docs/coverage-map.md, "How it was measured") and `scripts/covmap/report.py`; do not edit the tsv by hand.

## Recommendations

| Recommendation | Means | Blocks | Statements | of which security code |
|---|---|---|---|---|
| `keep-exempt` | the branch is right as written (defensive, fail-closed, or a platform fallback); it stays in the exempt list | 109 | 144 | 50 |
| `make-testable` | a small seam turns it into a unit test (A): the shared atomic-write helper, or a hook between a TOCTOU check and its use | 37 | 50 | 8 |
| `remove` | dead code: delete the branch (a reviewer confirms it cannot be reached) | 1 | 1 | 0 |

**Security code** (`yes` in the tsv): `cmd/archmaid-helper`, `internal/privilege`, `internal/helperclient`, `internal/ttyguard`, `gui/internal/polkitagent`, `gui/internal/realdriver`, `internal/downloads` (it deletes the owner's files), `internal/driver/keep.go`, askpass in `engine` and `tui`. Dev 3 reviews any removal, seam or test there before it lands; the others need only the usual review.

## `make-testable` (50 statements)

The atomic-write pattern (CreateTemp, write, chmod, fsync, close, rename, fsync the directory) is copied into `audit.WriteSnapshot`, `audit.WriteState`, `logstore.WriteLastRun` and `gui/internal/settings.Save`. One shared helper taking an injectable file turns those branches into unit tests and removes three copies. The downloads TOCTOU guards need a hook between the check and the use (like `Options.EntryHook`); that is security code, so Dev 3 decides.

| Where | Function | Stmts | Security | Why it can't run |
|---|---|---|---|---|
| `internal/audit/snapshot.go:202-204` | `WriteSnapshot` | 2 |  | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/audit/snapshot.go:206-208` | `WriteSnapshot` | 2 |  | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/audit/snapshot.go:210-211` | `WriteSnapshot` | 1 |  | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/audit/snapshot.go:217-218` | `WriteSnapshot` | 1 |  | opening the directory it just wrote into can't fail |
| `internal/audit/state.go:75-77` | `WriteState` | 2 |  | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/audit/state.go:79-81` | `WriteState` | 2 |  | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/audit/state.go:83-84` | `WriteState` | 1 |  | write/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/audit/state.go:90-91` | `WriteState` | 1 |  | opening the directory it just wrote into can't fail |
| `internal/aurbuild/reviewed.go:91-92` | `Reviewed.Mark` | 1 |  | json.MarshalIndent of the reviewed map can't fail; MkdirAll/CreateTemp/write/close of a fresh temp file fail only on ENOSPC/EIO |
| `internal/aurbuild/reviewed.go:98-99` | `Reviewed.Mark` | 1 |  | json.MarshalIndent of the reviewed map can't fail; MkdirAll/CreateTemp/write/close of a fresh temp file fail only on ENOSPC/EIO |
| `internal/aurbuild/reviewed.go:102-104` | `Reviewed.Mark` | 2 |  | json.MarshalIndent of the reviewed map can't fail; MkdirAll/CreateTemp/write/close of a fresh temp file fail only on ENOSPC/EIO |
| `internal/aurbuild/reviewed.go:106-107` | `Reviewed.Mark` | 1 |  | json.MarshalIndent of the reviewed map can't fail; MkdirAll/CreateTemp/write/close of a fresh temp file fail only on ENOSPC/EIO |
| `internal/downloads/downloads.go:180-181` | `checkDownloadsDir` | 1 | yes | Stat after a successful EvalSymlinks fails only if the folder vanishes in between (race) |
| `internal/downloads/downloads.go:366-367` | `entryIsOld.func` | 1 | yes | d.Info fails only if the node vanishes mid-walk (race) |
| `internal/downloads/downloads.go:576-577` | `remover.removeTree` | 1 | yes | Lstat of a node just listed fails only if it vanishes (race) |
| `internal/downloads/downloads.go:608-609` | `remover.removeContents` | 1 | yes | OpenRoot/Lstat/Open/Readdirnames of a directory entryIsOld just walked fail, or it is swapped, only in a race; no seam between the checks |
| `internal/downloads/downloads.go:612-613` | `remover.removeContents` | 1 | yes | OpenRoot/Lstat/Open/Readdirnames of a directory entryIsOld just walked fail, or it is swapped, only in a race; no seam between the checks |
| `internal/downloads/downloads.go:614-615` | `remover.removeContents` | 1 | yes | OpenRoot/Lstat/Open/Readdirnames of a directory entryIsOld just walked fail, or it is swapped, only in a race; no seam between the checks |
| `internal/downloads/downloads.go:618-619` | `remover.removeContents` | 1 | yes | OpenRoot/Lstat/Open/Readdirnames of a directory entryIsOld just walked fail, or it is swapped, only in a race; no seam between the checks |
| `internal/downloads/downloads.go:623-624` | `remover.removeContents` | 1 | yes | OpenRoot/Lstat/Open/Readdirnames of a directory entryIsOld just walked fail, or it is swapped, only in a race; no seam between the checks |
| `internal/health/ignore.go:230-231` | `IgnoreStore.write` | 1 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/health/ignore.go:235-236` | `IgnoreStore.write` | 1 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/health/ignore.go:239-241` | `IgnoreStore.write` | 2 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/health/ignore.go:243-245` | `IgnoreStore.write` | 2 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/health/ignore.go:247-249` | `IgnoreStore.write` | 2 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/health/ignore.go:251-252` | `IgnoreStore.write` | 1 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/health/news.go:251-252` | `writeNewsCache` | 1 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/health/news.go:254-255` | `writeNewsCache` | 1 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/health/news.go:258-259` | `writeNewsCache` | 1 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/health/news.go:264-265` | `writeNewsCache.func` | 1 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/health/news.go:268-270` | `writeNewsCache` | 2 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/health/news.go:272-274` | `writeNewsCache` | 2 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/health/news.go:276-277` | `writeNewsCache` | 1 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/logstore/lastrun.go:121-123` | `Store.WriteLastRun` | 2 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/logstore/lastrun.go:125-127` | `Store.WriteLastRun` | 2 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/logstore/lastrun.go:129-131` | `Store.WriteLastRun` | 2 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |
| `internal/logstore/lastrun.go:133-134` | `Store.WriteLastRun` | 1 |  | write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam |

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
| `gui/internal/ui/style.go:45-46` | `loadStyle.func` | 1 |  | the embedded CSS parses; only a future GTK could report errors |
| `gui/internal/ui/style.go:49-52` | `loadStyle.func` | 3 |  | the embedded CSS parses; only a future GTK could report errors |
| `gui/internal/ui/style.go:83-84` | `setProvider` | 1 |  | the embedded CSS parses; only a future GTK could report errors |
| `gui/internal/ui/style.go:97-100` | `setProvider.func` | 3 |  | the embedded CSS parses; only a future GTK could report errors |
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
| `internal/driver/driver.go:335-336` | `currentFaillock` | 1 |  | user.Current fails only when the uid has no passwd entry; no seam |
| `internal/driver/driver.go:550-550` | `Driver.command.func` | 1 |  | the switch over the closed Command set is exhaustive; the fallthrough return is the compiler's |
| `internal/engine/combine.go:85-85` | `Session.combineNext` | 1 |  | the guard skips a slot that is not Pending or already accepted; every caller advances r.cur past a slot it has settled or accepted, so the loop never meets one |
| `internal/engine/stub/fullcleanup.go:93-93` | `builder.index` | 1 |  | the hand-built script only names its own steps |
| `internal/helperclient/helpertest/helpertest.go:16-16` | `testOnly` | 1 | yes | test-helper guards: panic outside a test binary or on an empty path |
| `internal/helperclient/helpertest/helpertest.go:29-29` | `UseHelperFile` | 1 | yes | test-helper guards: panic outside a test binary or on an empty path |
| `internal/logstore/lastrun.go:105-106` | `Store.WriteLastRun` | 1 |  | json.Marshal of a RunRecord can't fail |
| `internal/privilege/askpass.go:127-130` | `Listen` | 3 | yes | WriteFile of the wrapper into the 0700 dir just created can't fail |
| `internal/privilege/askpass.go:133-136` | `Listen` | 3 | yes | WriteFile of the wrapper into the 0700 dir just created can't fail |
| `internal/privilege/askpass.go:229-230` | `Server.peerOf` | 1 | yes | SyscallConn/Control/SO_PEERCRED on an accepted unix conn don't fail |
| `internal/privilege/askpass.go:236-237` | `Server.peerOf` | 1 | yes | SyscallConn/Control/SO_PEERCRED on an accepted unix conn don't fail |
| `internal/privilege/askpass.go:239-240` | `Server.peerOf` | 1 | yes | SyscallConn/Control/SO_PEERCRED on an accepted unix conn don't fail |
| `internal/privilege/askpass.go:547-548` | `verifyPeer` | 1 | yes | os.Executable fails only without /proc |
| `internal/privilege/kept.go:150-151` | `KeptPassword.wipeLocked` | 1 | yes | the wipe check can't see non-zero bytes after the wipe |
| `internal/privilege/privtest/privtest.go:18-18` | `testOnly` | 1 | yes | panics outside a test binary |
| `internal/privilege/sudowrap.go:122-123` | `writeSudoWrapper` | 1 | yes | write/close of the wrapper into the 0700 dir just created can't fail short of ENOSPC/EIO |
| `internal/privilege/sudowrap.go:125-127` | `writeSudoWrapper` | 2 | yes | write/close of the wrapper into the 0700 dir just created can't fail short of ENOSPC/EIO |
| `internal/privilege/sudowrap.go:129-130` | `writeSudoWrapper` | 1 | yes | write/close of the wrapper into the 0700 dir just created can't fail short of ENOSPC/EIO |
| `internal/privilege/sudowrap.go:133-134` | `writeSudoWrapper` | 1 | yes | write/close of the wrapper into the 0700 dir just created can't fail short of ENOSPC/EIO |
| `internal/privilege/sudowrap.go:157-158` | `ctimeNs` | 1 | yes | Sys() of a Lstat on Linux is always a *syscall.Stat_t |
| `internal/proc/detach.go:57-58` | `groupAlive` | 1 |  | Getpgid errors other than ESRCH (EPERM across namespaces) don't happen for our own children |
| `internal/testenv/cover.go:24-25` | `coverChildren` | 1 |  | the early return is for runs without coverage, which count nothing |
| `internal/testenv/sysbin.go:54-55` | `UseSysbin` | 1 |  | MkdirAll and WriteFile into the test's own fresh t.TempDir fail only on ENOSPC/EIO |
| `internal/testenv/sysbin.go:57-58` | `UseSysbin` | 1 |  | MkdirAll and WriteFile into the test's own fresh t.TempDir fail only on ENOSPC/EIO |
| `internal/testenv/sysbin.go:75-76` | `Decoys` | 1 |  | MkdirAll and WriteFile into the test's own fresh t.TempDir fail only on ENOSPC/EIO |
| `internal/testenv/sysbin.go:80-81` | `Decoys` | 1 |  | MkdirAll and WriteFile into the test's own fresh t.TempDir fail only on ENOSPC/EIO |
| `internal/testenv/testenv.go:108-111` | `Run` | 3 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:127-128` | `mkScratch` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:142-144` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:148-150` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:156-158` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:163-165` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:168-170` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:173-175` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:187-189` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:193-195` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:199-201` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:232-234` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:242-245` | `run` | 3 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:246-246` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:247-247` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:248-249` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:250-251` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:254-255` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:256-258` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:259-259` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:260-261` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:262-263` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:264-264` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:266-266` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:267-269` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:270-271` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:272-272` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:274-274` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:275-277` | `run` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:278-278` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:279-280` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:281-281` | `run` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:333-334` | `InstallTraps` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:336-337` | `InstallTraps` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:345-346` | `InstallTraps` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:376-378` | `TrapInstalled` | 2 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:402-403` | `newMarker` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/testenv.go:412-413` | `setChildSubreaper` | 1 |  | setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir) |
| `internal/testenv/watchdog.go:60-61` | `startWatchdog` | 1 |  | os.Pipe and exec of /bin/sh fail only on fd exhaustion or a missing /bin/sh |
| `internal/testenv/watchdog.go:67-69` | `startWatchdog` | 2 |  | os.Pipe and exec of /bin/sh fail only on fd exhaustion or a missing /bin/sh |
| `internal/ttyguard/ttyguard.go:63-64` | `Hold` | 1 | yes | TIOCGEXCL/TIOCEXCL on an fd IsTerminal just accepted fail only if it is closed concurrently |
| `internal/ttyguard/ttyguard.go:122-123` | `Exclusive` | 1 | yes | TIOCGEXCL/TIOCEXCL on an fd IsTerminal just accepted fail only if it is closed concurrently |
| `internal/ttyguard/ttyguard.go:130-131` | `ioctl` | 1 | yes | TIOCGEXCL/TIOCEXCL on an fd IsTerminal just accepted fail only if it is closed concurrently |
| `internal/ttyguard/ttyguard.go:136-137` | `ioctl` | 1 | yes | TIOCGEXCL/TIOCEXCL on an fd IsTerminal just accepted fail only if it is closed concurrently |
| `internal/tui/askpass.go:45-46` | `startAskpassServer` | 1 | yes | os.Executable fails only without /proc |
| `internal/tui/tui.go:77-78` | `Run` | 1 |  | Release of the guard fails only if the fd was closed concurrently |
| `internal/tui/tui.go:83-84` | `Run.func` | 1 |  | Release of the guard fails only if the fd was closed concurrently |
| `internal/tui/tui.go:142-143` | `DisableCoreDumps` | 1 |  | prctl(PR_SET_DUMPABLE) on our own process doesn't fail |
| `internal/tui/view.go:90-90` | `model.viewOf` | 1 |  | safe fallback: renderScreen/baseScreen name every screen |
