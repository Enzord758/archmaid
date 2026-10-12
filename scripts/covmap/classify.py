#!/usr/bin/env python3
"""Sorts the uncovered blocks of a merged run into docs/coverage-map.md's
categories: A unit, B e2e, C vm, D owner, E unreachable (and "pending" for
packages another change is reworking).

usage: classify.py BLOCKS.json OUT.json
BLOCKS.json is `go run ./scripts/covblocks -json PROFILE` of the merged
profile (scripts/cover-merge.sh). Rules: first match wins; a rule names a
file (or a dir ending in /), a function regex and optionally a line range,
all at the map's base commit: refresh the line ranges when the code moves.
Read-only; prints the totals and any block no rule matched ("?").
"""
import json, re, sys

M = "github.com/Enzord758/archmaid/"

PENDING = ()
# internal/engine/stub and enginetest are classified (Dev 3's change is the engine package).
NOT_PENDING = ("internal/engine/stub/", "internal/engine/enginetest/")

R = []
def rule(f, fn, cat, why, home="", lo=0, hi=10**9, rec=""):
    R.append((f, re.compile(fn), lo, hi, cat, why, home, rec))

U = "unreachable"
# ---- cmd/archmaid
rule("cmd/archmaid/main.go", r"^main$", "E", "prctl(PR_SET_DUMPABLE) on our own process doesn't fail", "", lo=32, hi=34, rec="keep")
rule("cmd/archmaid/main.go", r"^main$", "E", "tui.ActionEnv always fills every required field (Env.Validate is the guard for a future change)", "", lo=38, hi=42, rec="keep: fail-closed startup check")
rule("cmd/archmaid/main.go", r"^main$", "B", "tui.Run's error exit: a new e2e run of the binary without a terminal (Bubble Tea can't open one), under the usual scratch HOME", "cmd/archmaid/e2e/run.sh")
# ---- audit
rule("internal/audit/audit.go", r"Config.deadline", "A", "Config with Deadline 0 gets DefaultDeadline", "internal/audit/audit_test.go")
rule("internal/audit/audit.go", r"^fetch$", "A", "RPCURL with a control character fails http.NewRequest", "internal/audit/audit_test.go")
rule("internal/audit/result.go", r"Result.save", "A", "a corrupt snapshot's high \"maintainer snapshot\" finding has no previous entry: commit it with holdHigh", "internal/audit/snapshot_test.go")
rule("internal/audit/result.go", r"packageNames", "E", "only called with the gating (high) names, never none", "", rec="keep: // unreachable: callers pass ≥1 name")
rule("internal/audit/result.go", r"Result.Note", "A", "corrupt snapshot in a dry run", "internal/audit/snapshot_test.go")
rule("internal/audit/snapshot.go", r"Entry.UnmarshalJSON", "E", "b starts with a quote and the outer decoder already validated the JSON, so the string always unmarshals", "", rec="keep: // unreachable: validated JSON string")
rule("internal/audit/snapshot.go", r"WriteSnapshot", "E", "json.MarshalIndent of map[string]Entry can't fail", "", lo=176, hi=178, rec="keep: // unreachable: plain map marshals")
rule("internal/audit/snapshot.go", r"WriteSnapshot", "A", "MkdirAll under a regular file fails", "internal/audit/snapshot_test.go", lo=180, hi=182)
rule("internal/audit/snapshot.go", r"WriteSnapshot", "A", "a pre-created lock file in a 0500 dir: the lock opens, the corrupt file's rename aside / CreateTemp fail", "internal/audit/snapshot_test.go", lo=190, hi=199)
rule("internal/audit/snapshot.go", r"WriteSnapshot", "E", "write/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam", "", lo=200, hi=211, rec="keep: idiomatic error return, no seam")
rule("internal/audit/snapshot.go", r"WriteSnapshot", "A", "the snapshot path is a non-empty directory: rename over it fails", "internal/audit/snapshot_test.go", lo=212, hi=214)
rule("internal/audit/snapshot.go", r"WriteSnapshot", "E", "opening the directory it just wrote into can't fail", "", lo=215, hi=218, rec="keep")
rule("internal/audit/snapshot.go", r"lockSnapshot", "A", "lock file can't be created in a 0500 dir", "internal/audit/snapshot_test.go", lo=228, hi=230)
rule("internal/audit/snapshot.go", r"lockSnapshot", "E", "flock(LOCK_EX|LOCK_NB) on an open regular file fails only with EWOULDBLOCK (handled) or EINTR", "", lo=231, hi=240, rec="keep")
rule("internal/audit/state.go", r"WriteState", "E", "json.MarshalIndent of State can't fail", "", lo=62, hi=64, rec="keep: // unreachable")
rule("internal/audit/state.go", r"WriteState", "A", "MkdirAll under a file / CreateTemp in a 0500 dir / rename onto a directory", "internal/audit/state_test.go", lo=66, hi=72)
rule("internal/audit/state.go", r"WriteState", "E", "write/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam", "", lo=73, hi=84, rec="keep")
rule("internal/audit/state.go", r"WriteState", "A", "StatePath is a non-empty directory: the rename fails", "internal/audit/state_test.go", lo=85, hi=87)
rule("internal/audit/state.go", r"WriteState", "E", "opening the directory it just wrote into can't fail", "", lo=88, hi=91, rec="keep")
rule("internal/audit/state.go", r"Result.writeState", "A", "StatePath under a regular file: the WARN is logged", "internal/audit/state_test.go")
# ---- downloads
rule("internal/downloads/downloads.go", r"PasswdHome", "E", "user.Current fails only when the uid has no passwd entry", "", rec="keep")
rule("internal/downloads/downloads.go", r"Options.now", "A", "Delete with Options.Now nil uses the real clock", "internal/downloads/downloads_test.go")
rule("internal/downloads/downloads.go", r"homeDirs", "A", "Options.Home an absolute path that doesn't exist (EvalSymlinks fails)", "internal/downloads/downloads_test.go")
rule("internal/downloads/downloads.go", r"checkDownloadsDir", "E", "Stat after a successful EvalSymlinks fails only if the folder vanishes in between (race)", "", rec="keep: TOCTOU guard")
rule("internal/downloads/downloads.go", r"xdgDownloadDir", "A", "user-dirs.dirs with a relative XDG_CONFIG_HOME and no home, $HOME with no home, a relative value", "internal/downloads/downloads_test.go")
rule("internal/downloads/downloads.go", r"^Scan$", "A", "a missing/0000 folder (OpenRoot), the mountinfoPath seam on a missing file, a 0300 folder (ReadDir)", "internal/downloads/downloads_test.go", lo=289, hi=291)
rule("internal/downloads/downloads.go", r"^Scan$", "E", "Stat(\".\") of a root it just opened can't fail", "", lo=294, hi=296, rec="keep")
rule("internal/downloads/downloads.go", r"^Scan$", "A", "the mountinfoPath seam on a missing file; a 0300 folder (ReadDir fails)", "internal/downloads/downloads_test.go", lo=298, hi=304)
rule("internal/downloads/downloads.go", r"oldDownloads", "A", "a folder that opens but can't be listed (0300)", "internal/downloads/downloads_test.go")
rule("internal/downloads/downloads.go", r"entryIsOld", "A", "an old directory holding an unreadable (0000) subdirectory counts as new", "internal/downloads/downloads_test.go", lo=357, hi=360)
rule("internal/downloads/downloads.go", r"entryIsOld", "E", "d.Info fails only if the node vanishes mid-walk (race)", "", lo=364, hi=367, rec="keep: TOCTOU guard")
rule("internal/downloads/downloads.go", r"changedBefore|dirIdentity|<package>|inode", "E", "on Linux every os FileInfo carries a *syscall.Stat_t and Stat of an open root can't fail", "", rec="keep: // unreachable on Linux")
rule("internal/downloads/downloads.go", r"newBoundary", "A", "mountinfoPath seam pointing at a missing file", "internal/downloads/downloads_test.go")
rule("internal/downloads/downloads.go", r"^Delete$", "A", "the folder removed after the scan (OpenRoot fails); mountinfoPath seam on a missing file", "internal/downloads/downloads_test.go")
rule("internal/downloads/downloads.go", r"remover.removeTree", "E", "Lstat of a node just listed fails only if it vanishes (race)", "", lo=574, hi=577, rec="keep: TOCTOU guard")
rule("internal/downloads/downloads.go", r"remover.removeTree", "A", "a file in an old 0500 subdirectory (clock moved past the chmod with Options.Now): Remove fails EACCES", "internal/downloads/downloads_test.go", lo=590, hi=592)
rule("internal/downloads/downloads.go", r"remover.removeContents", "E", "OpenRoot/Lstat/Open/Readdirnames of a directory entryIsOld just walked fail, or it is swapped, only in a race; no seam between the checks", "", rec="keep: TOCTOU guard")
rule("internal/downloads/downloads.go", r"PathErrCause", "A", "Scan of a missing folder gives a *fs.PathError", "internal/downloads/downloads_test.go")
# ---- engine test helpers
rule("internal/engine/enginetest/contract.go", r"", "A", "the checker's own failure paths: add the missing hand-built breaches", "internal/engine/enginetest/contract_test.go")
rule("internal/engine/stub/fullcleanup.go", r"builder.index", "E", "the hand-built script only names its own steps", "", rec="keep: programming-error panic")
rule("internal/engine/stub/stub.go", r"", "A", "Close (or Abort) while the replay waits on a cue or a prompt", "internal/engine/stub/stub_test.go")
# ---- frontend
rule("internal/frontend/topgrade.go", r"EnsureTopgradeConfig", "E", "O_EXCL finds the file already there only if it appears between the Lstat and the open (race); the result is the same as the Lstat's", "", lo=55, hi=56, rec="keep-exempt")
rule("internal/frontend/topgrade.go", r"", "A", "Lstat error other than not-exist (0000 parent), MkdirAll under a file, the file appearing between Lstat and OpenFile (O_EXCL), a 0500 dir", "internal/frontend/topgrade_test.go")
# ---- helperclient
rule("internal/helperclient/plan.go", r"^PlanOrphans$", "C", "runs the installed /usr/lib/archmaid/archmaid-helper plan-orphans, which runs the real pacman -Qdtq/-Rs --print", "VM")
rule("internal/helperclient/helpertest/helpertest.go", r"", "E", "test-helper guards: panic outside a test binary or on an empty path", "", rec="keep")
rule("internal/helperclient/run.go", r"exitText", "A", "a fake helper killed by a signal", "internal/helperclient/run_test.go")
rule("internal/helperclient/run.go", r"tail.Write", "A", "a fake helper writing >4 KiB to stderr", "internal/helperclient/run_test.go")
rule("internal/helperclient/", r"", "A", "a fake pkexec/helper script (helpertest) emitting the malformed or failing JSON lines, or missing from PATH", "internal/helperclient/*_test.go")
# ---- privilege
rule("internal/privilege/privtest/privtest.go", r"", "E", "panics outside a test binary", "", rec="keep")
rule("internal/privilege/askpass.go", r"^Listen$", "E", "WriteFile of the wrapper into the 0700 dir just created can't fail", "", rec="keep")
rule("internal/privilege/askpass.go", r"Server.peerOf", "E", "SyscallConn/Control/SO_PEERCRED on an accepted unix conn don't fail", "", rec="keep: // unreachable")
rule("internal/privilege/askpass.go", r"verifyPeer", "E", "os.Executable fails only without /proc", "", rec="keep")
rule("internal/privilege/askpass.go", r"", "A", "net.Pipe relays that hang up or answer the wrong line; a harden func that fails", "internal/privilege/askpass_test.go")
rule("internal/privilege/faillock.go", r"", "A", "a tally path that is a directory, an unreadable faillock.conf, a scanner error (an over-long line)", "internal/privilege/faillock_test.go")
rule("internal/privilege/kept.go", r"KeptPassword.Keep", "A", "unix.Mmap or Mlock failing: a re-executed test child with setrlimit(RLIMIT_AS / RLIMIT_MEMLOCK) lowered (needs Dev 3: it is the kept-password code)", "internal/privilege/kept_test.go")
rule("internal/privilege/kept.go", r"wipeLocked", "E", "the wipe check can't see non-zero bytes after the wipe", "", rec="keep-exempt")
rule("internal/privilege/kept.go", r"", "A", "a password with a line break; su's check failing (shim)", "internal/privilege/kept_test.go")
rule("internal/privilege/privilege.go", r"CheckPassword", "A", "su shim missing/failing to start", "internal/privilege/privilege_test.go")
# ---- proc
rule("internal/proc/detach.go", r"groupAlive", "E", "Getpgid errors other than ESRCH (EPERM across namespaces) don't happen for our own children", "", rec="keep")
rule("internal/proc/", r"", "A", "sudo argv without -n/-A, env -S/--split-string/-- forms, a nil Child, a child that left its group, readAt on fake malformed/short stat files", "internal/proc/*_test.go")
# ---- sanitize, sysstat
rule("internal/sanitize/sanitize.go", r"", "A", "CSI with intermediates, a CSI cut off before its final byte, OSC ended by ESC \\", "internal/sanitize/sanitize_test.go")
rule("internal/sysstat/sysstat.go", r"", "A", "a meminfo fixture without the key", "internal/sysstat/sysstat_test.go")
# ---- testenv
rule("internal/testenv/orphans.go", r"", "A", "runs in every package, but after m.Run, when the testing package has already written its counters (measurement blind spot); TestRunReapsOrphans runs it in a child; a direct call inside a test counts", "internal/testenv/orphan_test.go")
rule("internal/testenv/testenv.go", r"^Run$", "A", "the post-m.Run checks (reap, trap report, guard compare): run every time, counted never (after the counters are written); a test calling Run on a fake M counts them", "internal/testenv/trap_test.go", lo=160, hi=200)
rule("internal/testenv/watchdog.go", r"startWatchdog", "E", "os.Pipe and exec of /bin/sh fail only on fd exhaustion or a missing /bin/sh", "", rec="keep: fail closed")
rule("internal/testenv/watchdog.go", r"watchdog.stop", "A", "post-m.Run in Run (counted never, after the counters are written); a direct call in a test counts", "internal/testenv/trap_test.go")
rule("internal/testenv/testenv.go", r"guardCompare", "A", "post-m.Run: counted only from a direct test", "internal/testenv/trap_test.go")
rule("internal/testenv/testenv.go", r"guardScript|guardSnapshot", "A", "Run from a dir with no scripts/state-guard.sh above it; a guard script that fails", "internal/testenv/trap_test.go")
rule("internal/testenv/testenv.go", r"", "E", "setup failures (MkdirTemp, crypto/rand, PR_SET_CHILD_SUBREAPER, writing traps into a fresh temp dir)", "", rec="keep: fail closed")
# ---- testtranscript, ttyguard
rule("internal/testtranscript/recorder.go", r"", "A", "fake targets failing Start/Next/Reply and an odd replace list", "internal/testtranscript/recorder_test.go")
rule("internal/ttyguard/ttyguard.go", r"", "E", "TIOCGEXCL/TIOCEXCL on an fd IsTerminal just accepted fail only if it is closed concurrently", "", rec="keep")
# ---- helper (root helper)
H = "cmd/archmaid-helper/"
rule(H + "dev_off.go", r"", "E", "devLockPath is only called when devBuild, which is false in this build", "", rec="keep: build-tag stub")
rule(H + "main.go", r"", "B", "main only runs in the binary: a cover helper started as the user with a root verb is refused before it runs anything (verified under a scratch HOME); the root path is the VM's", "e2e (new helper check)")
rule(H + "harden.go", r"", "B", "runs in the harden test's child, but hardenProcess clears GOCOVERDIR before the test binary writes counters; the cover helper binary counts it (any run)", "e2e (new helper check)")
rule(H + "internal/helper/checks.go", r"LstatFile", "E", "os.FileInfo.Sys() is always a *syscall.Stat_t on Linux", "", lo=37, hi=39, rec="keep: fail-closed type check")
rule(H + "internal/helper/checks.go", r"acquireLock", "E", "flock(LOCK_EX|LOCK_NB) on a regular file just opened fails only with EWOULDBLOCK (busy, covered); ENOLCK/EBADF/EINTR are not producible", "", lo=135, hi=135, rec="keep: fail-closed")
rule(H + "internal/helper/checks.go", r"LstatFile", "A", "a plain os.Lstat wrapper: call it on a temp file, a missing path", "cmd/archmaid-helper/internal/helper/checks_test.go")
rule(H + "internal/helper/helper.go", r"DefaultOptions", "B", "only main uses it: covered by any cover-helper run", "e2e (new helper check)")
rule(H + "internal/helper/", r"", "A", "fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \\r", "cmd/archmaid-helper/internal/helper/*_test.go")

# ---- internal/actions
rule("internal/actions/downloads.go", r"", "A", "the downloads discovery's edge outputs: a Scan error notice, more than the listed items, an age-unknown or recently-changed or other-filesystem entry, nothing older than 30 days", "internal/actions/downloads_test.go")
rule("internal/actions/services.go", r"", "A", "a unit listing with a blank line, systemctl failing on is-enabled, more than three skip rules", "internal/actions/services_test.go")
# ---- internal/engine
rule("internal/engine/events.go", r"DiscoveryOutcome.String", "A", "String of an out-of-range outcome", "internal/engine/events_test.go")
rule("internal/engine/combine.go", r"Session.combineNext", "E", "the guard skips a slot that is not Pending or already accepted; every caller advances r.cur past a slot it has settled or accepted, so the loop never meets one", "", lo=85, hi=85, rec="keep-exempt")
rule("internal/engine/", r"", "A","a combined queue whose parts are all declined, finish out of order, or exit without having run", "internal/engine/combine_test.go")
# ---- internal/logstore
rule("internal/logstore/lastrun.go", r"Store.WriteLastRun", "E", "json.Marshal of a RunRecord can't fail", "", lo=66, hi=67, rec="keep-exempt")
rule("internal/logstore/lastrun.go", r"Store.WriteLastRun", "A", "MkdirAll under a regular file / CreateTemp in a 0500 dir", "internal/logstore/lastrun_test.go", lo=71, hi=79)
rule("internal/logstore/lastrun.go", r"Store.WriteLastRun", "E", "write/chmod/fsync/close of a fresh temp file fail only on ENOSPC/EIO; no seam", "", lo=80, hi=96, rec="make-testable: the shared atomic-write helper with an injectable file")
rule("internal/logstore/logstore.go", r"", "A", "a store with an error, a run dir under a regular file, a log path that is a directory", "internal/logstore/logstore_test.go")
# ---- internal/driver
rule("internal/driver/driver.go", r"currentFaillock", "E", "user.Current fails only when the uid has no passwd entry; no seam", "", rec="keep-exempt")
rule("internal/driver/driver.go", r"Driver.command.func", "E", "the switch over the closed Command set is exhaustive; the fallthrough return is the compiler's", "", rec="keep-exempt")
rule("internal/driver/driver.go", r"Driver.handle|Driver.loop", "A", "a Session that panics on the local or inputs path: the Driver tears down", "internal/driver/driver_test.go")
rule("internal/driver/", r"", "A", "a hung-up agent, a closed Askpass/Agent channel, a Driver stopped with children, prefetches and asks open, a failing last-run write, an agent whose kept password fails or can't be handed on, a requester with no exe", "internal/driver/*_test.go")
# ---- internal/tui
rule("internal/tui/askpass.go", r"startAskpassServer", "E", "os.Executable fails only without /proc", "", rec="keep-exempt")
rule("internal/tui/tui.go", r"^Run", "E", "ttyguard.Hold reports an error only when TIOCGEXCL/TIOCEXCL fails on a terminal that just answered TCGETS; a closed or non-terminal stdin is a Guard that does nothing, no error", "", lo=60, hi=66, rec="keep-exempt")
rule("internal/tui/tui.go", r"^Run", "E", "Release of the guard fails only if the fd was closed concurrently", "", rec="keep-exempt")
rule("internal/tui/tui.go", r"DisableCoreDumps", "E", "prctl(PR_SET_DUMPABLE) on our own process doesn't fail", "", rec="keep-exempt")
rule("internal/tui/tui.go", r"DropSudoTicket", "A", "privilege.DropTicket failing (sudo shim)", "internal/tui/tui_test.go")
rule("internal/tui/view.go", r"model.viewOf", "E", "safe fallback: renderScreen/baseScreen name every screen", "", rec="keep-exempt")
rule("internal/tui/tasks.go", r"^<package>$", "B", "the default execProcess hands the terminal to the editor: a pty tape with EDITOR set to a script (the unit tests swap the seam)", "cmd/archmaid/e2e/run.sh")
rule("internal/tui/tasks.go", r"handedTerminal", "B", "the terminal handover to a real editor: a pty tape with EDITOR set to a script", "cmd/archmaid/e2e/run.sh")
rule("internal/tui/", r"", "A", "key handling per screen (menu quit, log picker, log, audit, notice, discovering, services, confirm), the editor name from VISUAL/EDITOR, overlay padding, a PromptClosed for the askpass, a Finished with an open askpass or a cancelled outcome", "internal/tui/*_test.go")
rule("internal/frontend/", r"", "A","an audit helper edge", "internal/frontend/audit_test.go")

# ---- cover tooling itself
rule("cmd/archmaid-helper/internal/coverdir/coverdir.go", r"init", "B", "only a cover helper running as root sets it: verified under unshare -r with a scratch HOME (coverage-map.md, helper check); in the VM every pkexec run does", "e2e (new helper check)")
rule("internal/testenv/cover.go", r"", "E", "the early return is for runs without coverage, which count nothing", "", rec="keep")
# ---- gui
G = "gui/internal/"
rule(G + "logview/", r"", "A", "a missing state dir, an unreadable one, a stray name and a DST-gap stamp, an empty run", "gui/internal/logview/logview_test.go")
rule(G + "overview/", r"", "A", "a dry-run last-run record", "gui/internal/overview/overview_test.go")
rule(G + "polkitagent/polkitagent.go", r"openPTY|ioctl", "E", "posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail", "", rec="keep")
rule(G + "polkitagent/polkitagent.go", r"^Start$", "E", "TIOCEXCL on the pty just opened can't fail", "", lo=365, hi=368, rec="keep")
rule(G + "polkitagent/polkitagent.go", r"^Start$", "E", "openPTY failing", "", lo=292, hi=295, rec="keep")
rule(G + "polkitagent/polkitagent.go", r"waitNoEcho", "E", "the termios read fails only on a closed pty", "", lo=713, hi=715, rec="keep")
rule(G + "polkitagent/", r"", "A", "a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject", "gui/internal/polkitagent/polkitagent_test.go")
rule(G + "realdriver/realdriver.go", r"Wiring.env", "A", "Options() with no fake PlanOrphans picks helperclient.PlanOrphans (assignment only; running it is C)", "gui/internal/realdriver/realdriver_test.go")
rule(G + "realdriver/", r"", "A", "a fake polkit agent that keeps ending or fails to start; no XDG_RUNTIME_DIR in Config", "gui/internal/realdriver/agent_test.go")
rule(G + "runview/palette.go", r"Dank16Path", "A", "XDG_CACHE_HOME empty: the ~/.cache fallback, and HOME unset too (t.Setenv)", "gui/internal/runview/palette_test.go")
rule(G + "runview/stepline.go", r"seqLine", "E", "StepLine derives seq from viewLo and the run lengths, so it is always inside the view and the ring; the guard is for a future change", "", lo=50, hi=52, rec="keep: fail-closed")
rule(G + "runview/", r"", "A", "SGR edge cases (bad 38/48 forms, an empty last span), a StepInserted for a known row, the run ring wrapping, a FloodSteps of 0 chunks", "gui/internal/runview/*_test.go")
rule(G + "settings/settings.go", r"Save", "E", "write/fsync/close of a fresh temp file fail only on ENOSPC/EIO", "", lo=94, hi=106, rec="keep")
rule(G + "settings/", r"", "A", "no XDG_CONFIG_HOME nor HOME, an unreadable gui.json, a read-only config dir, a rename onto a directory, a bad downloads folder", "gui/internal/settings/settings_test.go")
rule(G + "ui/launcher.go", r"", "C", "GtkFileLauncher/GtkFileDialog need a desktop session with xdg-desktop-portal and a file manager; headless.sh runs without one on purpose (the VM's desktop has one)", "VM desktop")
rule(G + "ui/window.go", r"newWindow.func", "C", "the launcher's report callback: only a real launch failure (VM desktop)", "VM desktop", lo=133, hi=133)
rule(G + "ui/settings.go", r"topgradeCard.func|logFolderCard.func|chooseDownloads", "C", "Open folder / Choose folder through the real launcher (VM desktop); a fake launcher in the harness would make these A", "VM desktop")
rule(G + "ui/app.go", r"^Main$|mainReal", "B", "the binary's entry: start the cover archmaid-gui under headless.sh with a scratch HOME (a GUI e2e; the harness calls Run, not Main)", "gui/scripts/headless.sh + cover archmaid-gui (new)")
rule(G + "ui/app.go", r"stubDriver", "B", "archmaid-gui --stub with each ARCHMAID_GUI_SCRIPT, headless (the harness builds its stub drivers itself)", "gui/scripts/headless.sh + cover archmaid-gui (new)")
rule(G + "ui/dump.go", r"", "E", "setrlimit/prctl on our own process don't fail", "", rec="keep")
rule(G + "ui/style.go", r"", "E", "the embedded CSS parses; only a future GTK could report errors", "", rec="keep: it guards GTK upgrades")
rule(G + "ui/secret.go", r"", "E", "the password entry is always a GtkEditable with a buffer", "", rec="keep")
rule(G + "ui/shot.go", r"", "A", "screenshot to an unwritable path (build tag shots)", "gui/internal/ui/harness_test.go")
rule(G + "ui/logs.go", r"newLogs.func", "E", "GTK binds only positions the model holds, so i is always below len(p.rows)", "", lo=187, hi=190, rec="keep: fail-closed")
rule(G + "ui/overview.go", r"refreshLastRun", "E", "the last-run cell is built before the first refresh, so lastRunExtras never returns nil", "", lo=376, hi=378, rec="keep: fail-closed")
rule(G + "ui/timeline.go", r"newRunPage.func", "E", "ConnectBind always follows ConnectSetup, which stores the header in tl.headers", "", lo=133, hi=135, rec="keep: fail-closed")
rule(G + "ui/", r"", "A", "a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt", "gui/internal/ui/harness_test.go")


def classify(b):
    f = b["file"][len(M):] if b["file"].startswith(M) else b["file"]
    if f.startswith(PENDING) and not f.startswith(NOT_PENDING):
        return f, "pending", "pending Dev 3's discovery change", "", ""
    for (rf, rfn, lo, hi, cat, why, home, rec) in R:
        if (f == rf or (rf.endswith("/") and f.startswith(rf))) and rfn.search(b["func"]) and lo <= b["start"] and b["end"] <= hi:
            return f, cat, why, home, rec
    return f, "?", "", "", ""

# Security code: a change to these (a test, a seam, a removal) is Dev 3's to review.
SEC_PREFIX = ("cmd/archmaid-helper/", "internal/privilege/", "internal/helperclient/", "internal/ttyguard/",
              "gui/internal/polkitagent/", "gui/internal/realdriver/", "internal/downloads/",
              "internal/driver/keep.go", "internal/engine/askpass.go", "internal/tui/askpass.go")

def is_sec(f):
    return f.startswith(SEC_PREFIX)

# The three recommendations of docs/coverage-exempt.md.
def norm_rec(f, fn, why, rec):
    if "make-testable" in rec:
        return "make-testable"
    if "write/fsync/close" in why or "write/chmod/fsync/close" in why or "opening the directory it just wrote into" in why:
        return "make-testable"
    if f == "internal/downloads/downloads.go" and "TOCTOU" in rec and re.search(r"removeContents|removeTree|entryIsOld|checkDownloadsDir", fn):
        return "make-testable"
    if "callers pass" in rec:
        return "remove"
    return "keep-exempt"

blocks = json.load(open(sys.argv[1]))
out = []
for b in blocks:
    f, cat, why, home, rec = classify(b)
    d = {"file": f, "lines": f"{b['start']}-{b['end']}", "func": b["func"], "stmts": b["stmts"], "cat": cat, "reason": why}
    if home:
        d["home"] = home
    if rec:
        d["recommend"] = norm_rec(f, b["func"], why, rec)
        d["note"] = rec
    d["security"] = is_sec(f)
    out.append(d)
json.dump(out, open(sys.argv[2], "w"), indent=1, ensure_ascii=False)
from collections import Counter
c = Counter()
for d in out:
    c[d["cat"]] += d["stmts"]
print(dict(c))
for d in out:
    if d["cat"] == "?":
        print("UNCLASSIFIED", d["file"], d["lines"], d["func"], d["stmts"])
