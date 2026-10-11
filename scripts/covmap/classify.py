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

PENDING = ("internal/actions/", "internal/engine/", "internal/logstore/", "internal/driver/",
           "internal/tui/", "internal/frontend/audit.go", "packaging/dms/")
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
rule("internal/privilege/kept.go", r"KeptPassword.Keep|wipeLocked", "E", "mmap/mlock fail only under a tiny RLIMIT_MEMLOCK; the wipe check can't see non-zero bytes after the wipe", "", rec="keep: defensive")
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
rule(H + "internal/helper/checks.go", r"LstatFile", "A", "a plain os.Lstat wrapper: call it on a temp file, a missing path", "cmd/archmaid-helper/internal/helper/checks_test.go")
rule(H + "internal/helper/helper.go", r"DefaultOptions", "B", "only main uses it: covered by any cover-helper run", "e2e (new helper check)")
rule(H + "internal/helper/", r"", "A", "fake Runner/Stat/Exe returning the error, a LockPath that is empty, unwritable or held, a PacmanDBLock under a 0000 dir, a > maxCapture output, a trailing \\r", "cmd/archmaid-helper/internal/helper/*_test.go")

# ---- cover tooling itself
rule("cmd/archmaid-helper/internal/coverdir/coverdir.go", r"init", "B", "only a cover helper running as root sets it: verified under unshare -r with a scratch HOME (coverage-map.md, helper check); in the VM every pkexec run does", "e2e (new helper check)")
rule("internal/testenv/cover.go", r"", "E", "the early return is for runs without coverage, which count nothing", "", rec="keep")
# ---- gui
G = "gui/internal/"
rule(G + "logview/", r"", "A", "a missing state dir, an unreadable one, a stray name and a DST-gap stamp, an empty run", "gui/internal/logview/logview_test.go")
rule(G + "overview/", r"", "A", "a dry-run last-run record", "gui/internal/overview/overview_test.go")
rule(G + "polkitagent/polkitagent.go", r"openPTY|ioctl", "E", "posix_openpt/grantpt/unlockpt/TIOCGPTPEER and termios ioctls on a fresh pty don't fail", "", rec="keep")
rule(G + "polkitagent/polkitagent.go", r"^Start$", "E", "TIOCEXCL on the pty just opened can't fail", "", lo=246, hi=250, rec="keep")
rule(G + "polkitagent/polkitagent.go", r"^Start$", "E", "openPTY failing", "", lo=180, hi=188, rec="keep")
rule(G + "polkitagent/polkitagent.go", r"waitNoEcho", "E", "the termios read fails only on a closed pty", "", lo=594, hi=596, rec="keep")
rule(G + "polkitagent/", r"", "A", "a fake pkttyagent (agenttest) that is missing, never registers, never turns echo off, ignores SIGTERM, or prints refusals; no subject", "gui/internal/polkitagent/polkitagent_test.go")
rule(G + "realdriver/realdriver.go", r"Wiring.env", "A", "Options() with no fake PlanOrphans picks helperclient.PlanOrphans (assignment only; running it is C)", "gui/internal/realdriver/realdriver_test.go")
rule(G + "realdriver/", r"", "A", "a fake polkit agent that keeps ending or fails to start; no XDG_RUNTIME_DIR in Config", "gui/internal/realdriver/agent_test.go")
rule(G + "runview/palette.go", r"Dank16Path", "E", "os.UserHomeDir fails only with HOME unset, and XDG_CACHE_HOME empty too", "", rec="keep")
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
rule(G + "ui/", r"", "A", "a new headless scenario: the key, scroll and click it needs, a log page state (no logs, unreadable, empty), a settings error, a signal mid-run, a stale prompt", "gui/internal/ui/harness_test.go")


def classify(b):
    f = b["file"][len(M):] if b["file"].startswith(M) else b["file"]
    if f.startswith(PENDING) and not f.startswith(NOT_PENDING):
        return f, "pending", "pending Dev 3's discovery change", "", ""
    for (rf, rfn, lo, hi, cat, why, home, rec) in R:
        if (f == rf or (rf.endswith("/") and f.startswith(rf))) and rfn.search(b["func"]) and lo <= b["start"] and b["end"] <= hi:
            return f, cat, why, home, rec
    return f, "?", "", "", ""

blocks = json.load(open(sys.argv[1]))
out = []
for b in blocks:
    f, cat, why, home, rec = classify(b)
    d = {"file": f, "lines": f"{b['start']}-{b['end']}", "func": b["func"], "stmts": b["stmts"], "cat": cat, "reason": why}
    if home:
        d["home"] = home
    if rec:
        d["recommend"] = rec
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
