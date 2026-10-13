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
# The binary's build info records the flags: refuse to drive one without them.
go version -m archmaid | grep -qF -- "$SYSBIN.Sudo=sudo" ||
	{ echo "FAIL: ./archmaid wasn't built with the bare sysbin names; it would run the real /usr/bin/sudo"; exit 1; }

# The privileged binaries match internal/testenv's traps (su, sudo, pkexec,
# runuser, faillock): none may run for real, and calling any is a failure.
SHIMMED=(sudo pacman systemctl topgrade yay paccache journalctl hyprpm systemd-tmpfiles su pkexec runuser faillock)
MUTATING=(sudo topgrade yay paccache journalctl hyprpm systemd-tmpfiles su pkexec runuser faillock)

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
		PATH="$shims:$PATH" SHIM_LOG="$SHIM_LOG" \
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

if want clear-cache; then
	run_tape clear-cache
	[[ -f "$CLEANUP_LOG" ]] || fail "clear-cache: no dry-run log at $CLEANUP_LOG"
	need "$CLEANUP_LOG" "DRYRUN: User cache cleared" "clear-cache: DRYRUN entry missing"
	echo "PASS: clear-cache logged the DRYRUN entry"
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
	# By design (logstore.OutcomeCancelled: "nothing ran: declined gate") a declined
	# gate leaves a "cancelled" last-run record, in the dry-run slot only.
	LR="$STATE_DIR/dryrun/last-run.json"
	[[ -f "$LR" ]] || fail "gate: no cancelled last-run record in the dry-run slot"
	need "$LR" '"outcome": *"cancelled"' "gate: the last-run record is not 'cancelled'"
	refuse "$LR" '"steps_failed": *[1-9]' "gate: the cancelled record counts failed steps"
	[[ ! -e "$STATE_DIR/last-run.json" ]] || fail "gate: a dry run wrote the real last-run slot"
	refuse "$CLEANUP_LOG" "." "gate: a declined gate wrote to the cleanup log"
	echo "PASS: declined gate returned to the menu; nothing ran, only a cancelled dry-run record"
fi
