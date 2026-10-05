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
go build -trimpath -o archmaid .

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
		ALL_PROXY=http://127.0.0.1:9 "$@" \
		vhs "e2e/$name.tape"
	for c in "${MUTATING[@]}"; do
		# sudo is allowed only in its two read-only forms: the ticket probe and
		# Repair's root recheck (the shim only logs them and prints canned output).
		if [[ $c == sudo ]]; then
			if grep '^sudo ' "$SHIM_LOG" | grep -qvE '^sudo -n (-v|pacman -Qkq -- )'; then
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
	refuse "$CLEANUP_LOG" "fake-alpha" "services: the untoggled unit leaked into the log"
	if grep '^systemctl ' "$SHIM_LOG" | grep -qvE '^systemctl (list-|is-enabled )'; then
		cat "$SHIM_LOG"; fail "services: systemctl was used for more than listing and is-enabled"
	fi
	grep -q '^systemctl list-' "$SHIM_LOG" || fail "services: the shim was never asked to list"
	echo "PASS: services disabled exactly the toggled unit, dry-run only"
fi

if want orphans; then
	run_tape orphans
	need "$TXT" "Remove 2 orphaned package\(s\) .* 3 total" "orphans: confirm did not show '2 ... 3 total'"
	ORPH="$HOME_DIR/.local/state/archmaid/dryrun/orphans.log"
	[[ -f "$ORPH" ]] || fail "orphans: no dry-run orphans.log"
	need "$ORPH" "DRYRUN: Orphaned packages removed" "orphans: DRYRUN entry missing"
	need "$ORPH" "pacman -Rns --noconfirm -- orphan-one orphan-two$" "orphans: wrong package list in the log"
	need "$SHIM_LOG" "^pacman -Rs --print -- orphan-one orphan-two$" "orphans: cascade probe not run"
	refuse "$SHIM_LOG" "^pacman -Rns" "orphans: pacman removal actually ran"
	echo "PASS: orphan confirm showed 2/3 total and logged the dry-run removal"
fi

if want abort; then
	run_tape abort SHIM_BLOCK_QQM=6
	need "$SHIM_LOG" "^pacman -Qqm$" "abort: the blocking step never started"
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
	need "$TXT" "No broken packages found" "repair-none: the notice was not shown"
	need "$SHIM_LOG" "^pacman -Qkq -- " "repair-none: the user scan never ran"
	refuse "$SHIM_LOG" "^sudo " "repair-none: nothing flagged, yet sudo was used"
	refuse "$CLEANUP_LOG" "." "repair-none: a notice-only run wrote to the cleanup log"
	echo "PASS: repair found nothing, showed the notice and never touched sudo"
fi

if want repair-found; then
	run_tape repair-found SHIM_BROKEN=broken-pkg
	need "$SHIM_LOG" "^pacman -Qkq -- .*broken-pkg" "repair-found: the user scan never ran"
	need "$SHIM_LOG" "^sudo -n pacman -Qkq -- .*broken-pkg$" "repair-found: the root recheck did not go through sudo"
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
	need "$TXT" "Nothing older than 30 days" "downloads: the nothing-old notice was not shown"
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
	rc=0; out="$(env HOME="$h" XDG_STATE_HOME="$h/.local/state" ./archmaid --help 2>&1)" || rc=$?
	[[ $rc -eq 0 ]] || fail "help: --help exited $rc"
	[[ "$out" == *"Usage: archmaid"* && "$out" == *"--dryrun"* && "$out" == *"--downloads"* ]] ||
		fail "help: usage text incomplete: $out"
	rc=0; out="$(env HOME="$h" XDG_STATE_HOME="$h/.local/state" ./archmaid --bogus 2>&1)" || rc=$?
	[[ $rc -eq 2 ]] || fail "help: --bogus exited $rc, want 2"
	[[ "$out" == *"unknown option"* && "$out" == *"--help"* ]] || fail "help: bad-flag message wrong: $out"
	[[ -z "$(ls -A "$h")" ]] || fail "help: --help/--bogus wrote under HOME: $(ls -A "$h")"
	echo "PASS: --help prints usage and exits 0, --bogus exits 2, neither writes anything"
fi

if want fullcleanup-gate-declined; then
	run_tape fullcleanup-gate-declined
	need "$TXT" "Proceed with full cleanup" "gate: the gate was never shown"
	[[ "$(awk '/Proceed with full cleanup/{a=NR} /Full Cleanup/{m=NR} END{print (m>a)}' "$TXT")" == 1 ]] ||
		fail "gate: the menu did not reappear after declining"
	# Only the audit prefetch (pacman -Qqm, read-only) starts at the gate.
	if grep -qvE "^pacman -Qqm$" "$SHIM_LOG"; then cat "$SHIM_LOG"; fail "gate: a declined gate ran commands"; fi
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
