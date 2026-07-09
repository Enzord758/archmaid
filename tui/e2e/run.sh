#!/usr/bin/env bash
# End-to-end check: drive the real binary through VHS under --dryrun and assert
# on the dry-run log it produces. Safe — the dry run stubs every mutating step.
set -euo pipefail

cd "$(dirname "$0")/.."   # tui module root

# Build with the real HOME so the Go build cache is reused.
go build -o tui .

# Run the app under a throwaway HOME so we assert on a clean log and never touch
# the user's real ~/.maintenance_dryrun.log.
SCRATCH="$(mktemp -d)"
trap 'rm -rf "$SCRATCH"' EXIT

HOME="$SCRATCH" vhs e2e/run.tape

LOG="$SCRATCH/.maintenance_dryrun.log"
if [[ ! -f "$LOG" ]]; then
	echo "FAIL: no dry-run log was written at $LOG"
	exit 1
fi
if ! grep -q "DRYRUN: User cache cleared" "$LOG"; then
	echo "FAIL: expected 'DRYRUN: User cache cleared' entry missing"
	echo "--- log ---"; cat "$LOG"
	exit 1
fi

echo "PASS: e2e dry-run flow logged the expected DRYRUN entry"
echo "--- $LOG ---"
cat "$LOG"
