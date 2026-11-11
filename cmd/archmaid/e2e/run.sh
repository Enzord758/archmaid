#!/usr/bin/env bash
# End-to-end checks: drive the real binary through VHS under --dryrun, one tape
# at a time, and assert on the dry-run log, the tape's text output and the shim
# log. Optional args name the tapes to run (default: all).
#
# Safety: every run gets a throwaway HOME (and XDG_STATE_HOME) and a shim directory FIRST on PATH in
# which every external command archmaid can run (sudo, pacman, systemctl, ...)
# is a symlink to shim.sh, which only logs its argv and prints canned output.
# --dryrun stubs every mutating step on top of that. Before each tape we check
# the shims really shadow the real binaries, and afterwards that no mutating
# shim was invoked. The real state dir is snapshotted first and must be
# unchanged at the end (scripts/state-guard.sh).
set -euo pipefail

cd "$(dirname "$0")/.."   # cmd/archmaid: the tapes run from here (they use ./archmaid, e2e/...)
E2E="$PWD/e2e"

# Build with the real HOME so the Go build cache is reused.
#
# E2E_COVER_DIR=DIR (make e2e-cover): build the binary with coverage instead
# (as scripts/cover-build.sh does) and collect every run's covdata into DIR.
# Each run writes its counters to a GOCOVERDIR inside its own scratch dir,
# copied to DIR afterwards; merge DIR with scripts/cover-merge.sh. The tapes
# and their assertions are the same either way.
#
# The product runs su, sudo, pkexec, pkttyagent and topgrade by absolute
# path (internal/sysbin), which the shim dir on PATH can't shadow. This
# build sets each back to its bare name, so the tapes' sudo probe and
# Repair's root recheck reach the shims and never the real /usr/bin/sudo.
# Only this test build does: scripts/check.sh fails a PKGBUILD or cover
# build that names internal/sysbin, and testenv's TestE2EBuildsBareSysbin
# keeps this list complete.
SYSBIN=github.com/Enzord758/archmaid/internal/sysbin
SYSBIN_LDFLAGS="-X $SYSBIN.Su=su -X $SYSBIN.Sudo=sudo -X $SYSBIN.Pkexec=pkexec -X $SYSBIN.Pkttyagent=pkttyagent -X $SYSBIN.Topgrade=topgrade"
COVER_DIR="${E2E_COVER_DIR:-}"
if [[ -n "$COVER_DIR" ]]; then
	mkdir -p "$COVER_DIR"
	COVER_DIR="$(realpath "$COVER_DIR")"
	go build -trimpath -cover -covermode=atomic -ldflags "$SYSBIN_LDFLAGS" \
		-coverpkg=github.com/Enzord758/archmaid/cmd/...,github.com/Enzord758/archmaid/internal/... \
		-o archmaid .
else
	go build -trimpath -ldflags "$SYSBIN_LDFLAGS" -o archmaid .
fi
# Refuse to drive a binary whose sysbin paths aren't the bare names: read
# what it links (scripts/sysbinvals reads the ELF file, it never runs it;
# -trimpath keeps -ldflags out of `go version -m`). The TUI links Su, Sudo,
# Pkexec and Topgrade (Pkttyagent is the GUI's).
SYSBIN_VALS="$(go run ../../scripts/sysbinvals archmaid)" ||
	{ echo "FAIL: can't read ./archmaid's sysbin paths"; exit 1; }
grep -qx 'Sudo=sudo' <<<"$SYSBIN_VALS" ||
	{ echo "FAIL: ./archmaid has no bare sysbin.Sudo; it would run the real /usr/bin/sudo"; exit 1; }
if grep -v -E '^([A-Za-z]+)=[a-z]+$' <<<"$SYSBIN_VALS"; then
	echo "FAIL: ./archmaid links an absolute sysbin path (above); it would run the real program, not the shim"
	exit 1
fi

# The privileged binaries match internal/testenv's traps (su, sudo, pkexec,
# runuser, faillock): none may run for real, and calling any is a failure.
SHIMMED=(sudo pacman systemctl topgrade yay paru paccache journalctl hyprpm systemd-tmpfiles su pkexec runuser faillock)
MUTATING=(sudo topgrade yay paru paccache journalctl hyprpm systemd-tmpfiles su pkexec runuser faillock)

fail() { echo "FAIL: $*"; exit 1; }

# The owner's real state dir must come out of the run unchanged (read-only
# snapshot of names + sizes + mtimes; see scripts/state-guard.sh). Taken
# before any HOME is swapped, compared on every exit.
GUARD="$E2E/../../../scripts/state-guard.sh"
SCRATCH_ROOT="$(mktemp -d)"
"$GUARD" snapshot >"$SCRATCH_ROOT/state.before"
finish() {
	local rc=$?
	"$GUARD" compare "$SCRATCH_ROOT/state.before" || rc=1
	rm -rf "$SCRATCH_ROOT"
	exit "$rc"
}
trap finish EXIT

# The browser retry must retry VHS's browser failure once and nothing else:
# prove it on a stub vhs first (well under a second).
"$E2E/browser-retry-selftest.sh"

# cover_env DIR prints the GOCOVERDIR=DIR assignment for one run of the
# binary (nothing without E2E_COVER_DIR); cover_collect DIR copies what that
# run wrote into E2E_COVER_DIR. A run killed before it exits (SIGKILL, a tape
# that never quits) writes no counters: every tape ends with q.
cover_env() {
	[[ -n "$COVER_DIR" ]] || return 0
	mkdir -p "$1"
	echo "GOCOVERDIR=$1"
}
cover_collect() {
	[[ -n "$COVER_DIR" ]] || return 0
	compgen -G "$1/covcounters.*" >/dev/null || fail "coverage: no counters in $1 (did the binary exit normally?)"
	cp "$1"/cov* "$COVER_DIR"/
}

# run_tape NAME [VAR=VALUE...]: runs e2e/NAME.tape with a fresh HOME + shim dir,
# leaving HOME_DIR, SHIM_LOG, TXT and CLEANUP_LOG for the caller's assertions.
run_tape() {
	local name="$1"; shift
	local dir="$SCRATCH_ROOT/$name"
	local shims="$dir/shims"
	HOME_DIR="$dir/home"; SHIM_LOG="$dir/shim.log"; TXT="$E2E/$name.txt"
	mkdir -p "$HOME_DIR" "$shims"; : >"$SHIM_LOG"
	# SEED_USER_CACHE=1: a ~/.cache with something in it (a row to tick).
	if [[ -n "${SEED_USER_CACHE:-}" ]]; then
		mkdir -p "$HOME_DIR/.cache/app"
		head -c 1048576 /dev/urandom >"$HOME_DIR/.cache/app/blob"
	fi
	for c in "${SHIMMED[@]}"; do ln -s "$E2E/shim.sh" "$shims/$c"; done
	for c in "${SHIMMED[@]}"; do
		[[ "$(PATH="$shims:$PATH" command -v "$c")" == "$shims/$c" ]] ||
			fail "shim for $c does not shadow the real binary"
	done
	# The audit is the only step that could reach the network under --dryrun
	# (and only if `pacman -Qqm` listed packages, which the shim never does);
	# a dead proxy makes a stray lookup fail rather than succeed.
	# Only HOME and XDG_STATE_HOME are swapped: VHS's headless browser runs
	# under this env too and fails to start with a scratch XDG_CONFIG_HOME or
	# XDG_CACHE_HOME.
	# browser-retry.sh runs it once more only if VHS's headless browser
	# never started (a runner flake, before the tape typed anything).
	"$E2E/browser-retry.sh" env HOME="$HOME_DIR" XDG_STATE_HOME="$HOME_DIR/.local/state" \
		PATH="$shims:$PATH" SHIM_LOG="$SHIM_LOG" ARCHMAID_AUR_HELPER=yay \
		HTTPS_PROXY=http://127.0.0.1:9 https_proxy=http://127.0.0.1:9 \
		ALL_PROXY=http://127.0.0.1:9 $(cover_env "$dir/cover") "$@" \
		vhs "e2e/$name.tape"
	cover_collect "$dir/cover"
	for c in "${MUTATING[@]}"; do
		# sudo is allowed only in its two read-only forms: the ticket probe and
		# Repair's root recheck (the shim only logs them and prints canned output).
		if [[ $c == sudo ]]; then
			if grep '^sudo ' "$SHIM_LOG" | grep -qvE '^sudo -n (-v|pacman -Qkq --color never -- )'; then
				echo "--- shim log ---"; cat "$SHIM_LOG"
				fail "$name: sudo was invoked for more than the ticket probe and the Qkq recheck"
			fi
			continue
		fi
		# paccache is allowed only as Clear caches' read-only dry run.
		if [[ $c == paccache ]]; then
			if grep '^paccache ' "$SHIM_LOG" | grep -qvE '^paccache --nocolor -dk0$'; then
				echo "--- shim log ---"; cat "$SHIM_LOG"
				fail "$name: paccache was invoked for more than the dry run"
			fi
			continue
		fi
		if grep -q "^$c " "$SHIM_LOG"; then
			echo "--- shim log ---"; cat "$SHIM_LOG"
			fail "$name: mutating command $c was invoked"
		fi
	done
	# Every tape is a dry run, which never refreshes the widget's update
	# count. The real refresh starts the user unit and, if that fails, runs
	# the installed check by absolute path, which no shim here can catch.
	refuse "$SHIM_LOG" '^systemctl --user ' "$name: the widget's update check was started"
	CLEANUP_LOG="$HOME_DIR/.local/state/archmaid/dryrun/cleanup.log"
	STATE_DIR="$HOME_DIR/.local/state/archmaid"
}

need() { # need FILE PATTERN WHAT
	grep -Eq -- "$2" "$1" || { echo "--- $1 ---"; cat "$1" 2>/dev/null; fail "$3"; }
}
refuse() { # refuse FILE PATTERN WHAT
	if grep -Eq -- "$2" "$1" 2>/dev/null; then echo "--- $1 ---"; cat "$1"; fail "$3"; fi
}

only=("$@")
want() { [[ ${#only[@]} -eq 0 ]] || [[ " ${only[*]} " == *" $1 "* ]]; }

if want clearcaches-user; then
	SEED_USER_CACHE=1 run_tape clearcaches-user
	[[ -f "$CLEANUP_LOG" ]] || fail "clearcaches-user: no dry-run log at $CLEANUP_LOG"
	# The pick shows both rows, with the package cache's warning, before anything ran.
	need "$TXT" "Clear caches" "clearcaches-user: the pick was not shown"
	need "$TXT" "~/.cache — 1 MiB" "clearcaches-user: the pick did not show ~/.cache's size"
	need "$TXT" "412 MiB \\(2 files\\)" "clearcaches-user: the pick did not show the package cache's size and count"
	need "$TXT" "without internet" "clearcaches-user: the pick lacks the package cache's warning"
	need "$CLEANUP_LOG" "DRYRUN: User cache cleared" "clearcaches-user: DRYRUN entry missing"
	refuse "$CLEANUP_LOG" "Entire package cache" "clearcaches-user: the package cache step ran although it was not ticked"
	# ~/.cache alone needs no root: no sudo at all, not even the ticket probe.
	refuse "$SHIM_LOG" "^sudo " "clearcaches-user: sudo was called for ~/.cache alone"
	need "$SHIM_LOG" "^paccache --nocolor -dk0$" "clearcaches-user: the dry run never ran"
	echo "PASS: Clear caches with ~/.cache ticked logged the DRYRUN entry and asked for no root"
	echo "--- $CLEANUP_LOG ---"; cat "$CLEANUP_LOG"
fi

if want services; then
	run_tape services
	[[ -f "$CLEANUP_LOG" ]] || fail "services: no dry-run log at $CLEANUP_LOG"
	need "$CLEANUP_LOG" "DRYRUN: Services disabled" "services: DRYRUN entry missing"
	need "$CLEANUP_LOG" "would run: sudo systemctl disable --now -- fake-beta.service$" \
		"services: the log must name exactly the toggled unit"
	refuse "$CLEANUP_LOG" "would run: .*fake-alpha" "services: the untoggled unit was disabled"
	# What the discovery found heads the run's log (both units, with their state).
	need "$CLEANUP_LOG" "CHECK: Checked 2 stopped services: 2 are enabled" "services: the discovery record is missing"
	need "$CLEANUP_LOG" "^fake-alpha.service: enabled$" "services: the record lacks a unit's state"
	if grep '^systemctl ' "$SHIM_LOG" | grep -qvE '^systemctl (list-|is-enabled )'; then
		cat "$SHIM_LOG"; fail "services: systemctl was used for more than listing and is-enabled"
	fi
	grep -q '^systemctl list-' "$SHIM_LOG" || fail "services: the shim was never asked to list"
	echo "PASS: services disabled exactly the toggled unit, dry-run only"
fi

if want services-none; then
	run_tape services-none SHIM_UNIT_STATE=disabled
	need "$TXT" "Checked 2 stopped services: none are enabled" "services-none: the notice did not say what was checked"
	need "$TXT" "Details: View log → Services check" "services-none: the notice did not say where the record is"
	SVC_LOG="$STATE_DIR/dryrun/services.log"
	[[ -f "$SVC_LOG" ]] || fail "services-none: no record at $SVC_LOG"
	need "$SVC_LOG" "CHECK: Checked 2 stopped services: none are enabled" "services-none: the record's entry is missing"
	need "$SVC_LOG" "^fake-beta.service: disabled$" "services-none: the record lacks a unit's state"
	need "$SVC_LOG" '^\$ systemctl list-units --type=service' "services-none: the record lacks the command"
	need "$SVC_LOG" "Fake Alpha" "services-none: the record lacks the raw listing"
	LR="$STATE_DIR/dryrun/last-run.json"
	[[ -f "$LR" ]] || fail "services-none: no last-run record in the dry-run slot"
	need "$LR" '"outcome": *"nothing-found"' "services-none: the last-run record is not 'nothing-found'"
	need "$LR" '"action": *"services"' "services-none: the last-run record names another action"
	[[ ! -e "$STATE_DIR/last-run.json" ]] || fail "services-none: a dry run wrote the real last-run slot"
	refuse "$CLEANUP_LOG" "." "services-none: nothing found, yet the cleanup log was written"
	if ls "$STATE_DIR" | grep -qE '^[0-9]{8}-[0-9]{4}$'; then
		ls -la "$STATE_DIR"; fail "services-none: a dry run made a real run directory"
	fi
	if grep '^systemctl ' "$SHIM_LOG" | grep -qvE '^systemctl (list-units|is-enabled) '; then
		cat "$SHIM_LOG"; fail "services-none: systemctl was used for more than listing and is-enabled"
	fi
	echo "PASS: services found nothing, said what it checked, and recorded it in the dry-run slot"
	echo "--- $SVC_LOG ---"; cat "$SVC_LOG"
fi

if want orphans; then
	run_tape orphans
	need "$TXT" "Remove 2 orphaned package\(s\) .* 3 total" "orphans: confirm did not show '2 ... 3 total'"
	ORPH="$HOME_DIR/.local/state/archmaid/dryrun/orphans.log"
	[[ -f "$ORPH" ]] || fail "orphans: no dry-run orphans.log"
	need "$ORPH" "DRYRUN: Orphaned packages removed" "orphans: DRYRUN entry missing"
	need "$ORPH" "pacman -Rns --noconfirm -- orphan-one orphan-two$" "orphans: wrong package list in the log"
	need "$SHIM_LOG" "^pacman -Rs --print --color never -- orphan-one orphan-two$" "orphans: cascade probe not run"
	refuse "$SHIM_LOG" "^pacman -Rns" "orphans: pacman removal actually ran"
	echo "PASS: orphan confirm showed 2/3 total and logged the dry-run removal"
fi

if want clearcaches-none; then
	SEED_USER_CACHE=1 run_tape clearcaches-none
	need "$TXT" "without internet" "clearcaches-none: the pick lacks the warning"
	need "$TXT" "\\[ \\] ~/.cache" "clearcaches-none: ~/.cache is not shown unticked"
	need "$SHIM_LOG" "^paccache --nocolor -dk0$" "clearcaches-none: the dry run never ran"
	refuse "$SHIM_LOG" "-rk0" "clearcaches-none: the wipe ran with nothing ticked"
	refuse "$SHIM_LOG" "^sudo " "clearcaches-none: sudo was called with nothing ticked"
	refuse "$CLEANUP_LOG" "." "clearcaches-none: an empty pick wrote to the cleanup log"
	echo "PASS: an empty pick ran nothing and asked for no password"
fi

if want clearcaches-pkg; then
	SEED_USER_CACHE=1 run_tape clearcaches-pkg
	need "$TXT" "412 MiB \\(2 files\\)" "clearcaches-pkg: the pick did not show what it frees"
	need "$TXT" "without internet" "clearcaches-pkg: the pick lacks the warning"
	need "$SHIM_LOG" "^paccache --nocolor -dk0$" "clearcaches-pkg: the dry run never ran"
	refuse "$SHIM_LOG" "-rk0" "clearcaches-pkg: a dry run executed the wipe"
	need "$CLEANUP_LOG" "DRYRUN: Entire package cache removed" "clearcaches-pkg: the DRYRUN entry is missing"
	need "$CLEANUP_LOG" "would run: sudo paccache -rk0$" "clearcaches-pkg: the log does not name the wipe command"
	refuse "$CLEANUP_LOG" "User cache cleared" "clearcaches-pkg: ~/.cache was cleared although it was not ticked"
	echo "PASS: the package cache alone, under --dryrun, logged what it would run and ran nothing"
fi

if want clearcaches-empty; then
	run_tape clearcaches-empty SHIM_CACHE_EMPTY=1
	need "$TXT" "No cache to clear" "clearcaches-empty: the notice was not shown"
	need "$STATE_DIR/dryrun/pkgcache.log" "CHECK: No cache to clear" "clearcaches-empty: no discovery record"
	refuse "$CLEANUP_LOG" "." "clearcaches-empty: a notice-only run wrote to the cleanup log"
	echo "PASS: two empty caches are a notice, nothing asked, nothing run"
fi

if want abort; then
	run_tape abort SHIM_BLOCK_QQM=6
	need "$SHIM_LOG" "^pacman -Qqm --color never$" "abort: the blocking step never started"
	need "$TXT" "Abort" "abort: the abort confirm was never shown"
	# After the abort the app is back on the menu and still responding.
	[[ "$(awk '/Abort/{a=NR} /Full Cleanup/{m=NR} END{print (m>a)}' "$TXT")" == 1 ]] ||
		fail "abort: the menu did not reappear after the abort"
	# An aborted run records nothing: no summary, no log entries for it.
	refuse "$TXT" "Summary|DRYRUN" "abort: an aborted run produced a summary or log entry"
	refuse "$CLEANUP_LOG" "." "abort: an aborted run wrote to the cleanup log"
	echo "PASS: abort returned to the menu and recorded nothing"
fi

if want repair-none; then
	run_tape repair-none
	need "$TXT" "No broken packages: checked 2 installed packages" "repair-none: the notice was not shown"
	need "$SHIM_LOG" "^pacman -Qkq --color never -- " "repair-none: the user scan never ran"
	refuse "$SHIM_LOG" "^sudo " "repair-none: nothing flagged, yet sudo was used"
	refuse "$CLEANUP_LOG" "." "repair-none: a notice-only run wrote to the cleanup log"
	need "$STATE_DIR/dryrun/repair.log" "CHECK: No broken packages" "repair-none: no discovery record"
	need "$STATE_DIR/dryrun/last-run.json" '"outcome": *"nothing-found"' "repair-none: last-run is not 'nothing-found'"
	echo "PASS: repair found nothing, showed the notice and never touched sudo"
fi

if want repair-found; then
	run_tape repair-found SHIM_BROKEN=broken-pkg
	need "$SHIM_LOG" "^pacman -Qkq --color never -- .*broken-pkg" "repair-found: the user scan never ran"
	need "$SHIM_LOG" "^sudo -n pacman -Qkq --color never -- .*broken-pkg$" "repair-found: the root recheck did not go through sudo"
	need "$TXT" "Reinstall 1 broken package" "repair-found: the reinstall confirm was not shown"
	refuse "$SHIM_LOG" "pacman -S" "repair-found: a reinstall ran although it was declined"
	# The step is Optional + AlwaysLog: a declined confirm is on record, and no
	# reinstall is.
	need "$CLEANUP_LOG" "[Dd]eclined|[Ss]kipped" "repair-found: the declined reinstall was not logged"
	refuse "$CLEANUP_LOG" "DRYRUN: Package repair completed" "repair-found: a declined reinstall was logged as done"
	echo "PASS: repair confirmed the broken package as root, declined, reinstalled nothing"
	echo "--- $CLEANUP_LOG ---"; cat "$CLEANUP_LOG"
fi

if want downloads; then
	# Age is ctime, which touch -d cannot set: files with a year-old mtime must
	# NOT count as old. Listing old files in the plan would need a clock seam.
	DL_DIR="$SCRATCH_ROOT/downloads-dir"
	mkdir -p "$DL_DIR"
	for f in old-mtime-a.iso old-mtime-b.zip; do
		: >"$DL_DIR/$f"; touch -d '400 days ago' "$DL_DIR/$f"
	done
	: >"$DL_DIR/new.txt"
	run_tape downloads DL_DIR="$DL_DIR"
	need "$TXT" "Checked 3 items in" "downloads: the nothing-old notice was not shown"
	need "$STATE_DIR/dryrun/downloads.log" "CHECK: Checked 3 items in .*: none older than 30 days" "downloads: no discovery record"
	need "$STATE_DIR/dryrun/downloads.log" "^new.txt: " "downloads: the record does not list the folder's items"
	for f in old-mtime-a.iso old-mtime-b.zip new.txt; do
		[[ -e "$DL_DIR/$f" ]] || fail "downloads: $f was deleted"
	done
	refuse "$TXT" "old-mtime|new.txt|Delete [0-9]+" "downloads: a plan was shown for files that are not old"
	refuse "$CLEANUP_LOG" "." "downloads: a notice-only run wrote to the cleanup log"
	echo "PASS: --downloads honoured; old mtime is not old (ctime), nothing deleted"
fi

if want help-and-bad-flag; then
	# No VHS: the binary runs under a scratch HOME like everywhere else here.
	h="$SCRATCH_ROOT/help-home"; mkdir -p "$h"
	hc="$SCRATCH_ROOT/help-cover" # outside $h: the check below wants $h empty
	rc=0; out="$(env HOME="$h" XDG_STATE_HOME="$h/.local/state" $(cover_env "$hc") ./archmaid --help 2>&1)" || rc=$?
	[[ $rc -eq 0 ]] || fail "help: --help exited $rc"
	[[ "$out" == *"Usage: archmaid"* && "$out" == *"--dryrun"* && "$out" == *"--downloads"* ]] ||
		fail "help: usage text incomplete: $out"
	rc=0; out="$(env HOME="$h" XDG_STATE_HOME="$h/.local/state" $(cover_env "$hc") ./archmaid --bogus 2>&1)" || rc=$?
	[[ $rc -eq 2 ]] || fail "help: --bogus exited $rc, want 2"
	[[ "$out" == *"unknown option"* && "$out" == *"--help"* ]] || fail "help: bad-flag message wrong: $out"
	[[ -z "$(ls -A "$h")" ]] || fail "help: --help/--bogus wrote under HOME: $(ls -A "$h")"
	cover_collect "$hc"
	echo "PASS: --help prints usage and exits 0, --bogus exits 2, neither writes anything"
fi

if want fullcleanup-gate-declined; then
	run_tape fullcleanup-gate-declined
	need "$TXT" "Proceed with full cleanup" "gate: the gate was never shown"
	[[ "$(awk '/Proceed with full cleanup/{a=NR} /Full Cleanup/{m=NR} END{print (m>a)}' "$TXT")" == 1 ]] ||
		fail "gate: the menu did not reappear after declining"
	# Only the audit prefetch (pacman -Qqm, read-only) starts at the gate.
	if grep -qvE "^pacman -Qqm --color never$" "$SHIM_LOG"; then cat "$SHIM_LOG"; fail "gate: a declined gate ran commands"; fi
	refuse "$TXT" "Summary|DRYRUN" "gate: a declined gate produced a summary"
	# A declined gate is a run in which nothing ran (logstore.OutcomeCancelled):
	# since #276 it leaves no last-run record in either slot, so the previous
	# real run stays the Overview's and the widget's Last run.
	[[ ! -e "$STATE_DIR/dryrun/last-run.json" ]] || fail "gate: a declined gate wrote a dry-run last-run record"
	[[ ! -e "$STATE_DIR/last-run.json" ]] || fail "gate: a dry run wrote the real last-run slot"
	refuse "$CLEANUP_LOG" "." "gate: a declined gate wrote to the cleanup log"
	echo "PASS: declined gate returned to the menu; nothing ran and no last-run record was written"
fi
