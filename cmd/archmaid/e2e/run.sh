#!/usr/bin/env bash
# End-to-end checks: drive the real binary through VHS under --dryrun, one tape
# at a time, and assert on the dry-run log, the tape's text output and the shim
# log. Optional args name the tapes to run (default: all).
#
# Safety: every run gets a throwaway HOME and a shim directory FIRST on PATH in
# which every external command archmaid can run (sudo, pacman, systemctl, ...)
# is a symlink to shim.sh, which only logs its argv and prints canned output.
# --dryrun stubs every mutating step on top of that. Before each tape we check
# the shims really shadow the real binaries, and afterwards that no mutating
# shim was invoked.
set -euo pipefail

cd "$(dirname "$0")/.."   # tui module root
E2E="$PWD/e2e"

# Build with the real HOME so the Go build cache is reused.
go build -o archmaid .

SHIMMED=(sudo pacman systemctl topgrade yay paccache journalctl hyprpm systemd-tmpfiles su)
MUTATING=(sudo topgrade yay paccache journalctl hyprpm systemd-tmpfiles su)

fail() { echo "FAIL: $*"; exit 1; }

SCRATCH_ROOT="$(mktemp -d)"
trap 'rm -rf "$SCRATCH_ROOT"' EXIT

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
	env HOME="$HOME_DIR" PATH="$shims:$PATH" SHIM_LOG="$SHIM_LOG" \
		HTTPS_PROXY=http://127.0.0.1:9 https_proxy=http://127.0.0.1:9 \
		ALL_PROXY=http://127.0.0.1:9 "$@" \
		vhs "e2e/$name.tape"
	for c in "${MUTATING[@]}"; do
		if grep -q "^$c " "$SHIM_LOG"; then
			echo "--- shim log ---"; cat "$SHIM_LOG"
			fail "$name: mutating command $c was invoked"
		fi
	done
	CLEANUP_LOG="$HOME_DIR/.local/state/archmaid/dryrun/cleanup.log"
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
