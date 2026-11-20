#!/usr/bin/env bash
# The GUI's performance budgets, enforced: runs TestGUIFirstFrameAndIdleRAM and
# TestGUIThroughput headless (Broadway on nullbroadway, no window on any
# desktop) with ARCHMAID_GUI_PERF=1, prints one line per budget (measured vs
# budget) and exits non-zero if any budget is breached or the tests fail.
# Budgets and their reasoning: docs/TESTING.md, "GUI performance budgets".
#
#   scripts/gui-perf.sh                          # the owner's desktop: budgets as written
#   ARCHMAID_GUI_PERF_SCALE=2 scripts/gui-perf.sh  # a slower machine: every budget x2
#   HEADLESS_TIMEOUT=300 scripts/gui-perf.sh     # per-run cap, seconds (default 240)
#
# Exit: 0 all budgets met, 1 a budget breached or a test failed, 2 usage or no
# measurements. Needs what `make gui-test` needs (gtk4, libadwaita, gcc, dbus).
# Run it on a quiet machine: it measures the machine. Everything it creates is
# under one mktemp dir that is removed on exit.
set -euo pipefail
root="$(cd "$(dirname "$(realpath "${BASH_SOURCE[0]}")")/.." && pwd)"

if [[ $# -ne 0 ]]; then
	echo "usage: scripts/gui-perf.sh (no arguments; see the header for the environment)" >&2
	exit 2
fi
if ! awk -v s="${ARCHMAID_GUI_PERF_SCALE:-1}" 'BEGIN { exit !(s ~ /^[0-9]+(\.[0-9]+)?$/ && s >= 1) }'; then
	echo "ARCHMAID_GUI_PERF_SCALE must be a number >= 1, not ${ARCHMAID_GUI_PERF_SCALE}" >&2
	exit 2
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
export ARCHMAID_GUI_PERF=1
export ARCHMAID_GUI_PERF_REPORT="$tmp/report.tsv"
export HEADLESS_TIMEOUT="${HEADLESS_TIMEOUT:-240}"

rc=0
(
	cd "$root/gui"
	go test -c -o "$tmp/ui.test" ./internal/ui
) || {
	echo "gui-perf: the test binary did not build" >&2
	exit 1
}
"$root/gui/scripts/headless.sh" bash -c 'cd internal/ui && exec "$@"' _ "$tmp/ui.test" \
	-test.count=1 -test.v -test.run '^TestGUI(FirstFrameAndIdleRAM|Throughput)$' >"$tmp/test.log" 2>&1 || rc=$?

if [[ ! -s "$tmp/report.tsv" ]]; then
	echo "gui-perf: no budget was measured (tests skipped or crashed); log:" >&2
	cat "$tmp/test.log" >&2
	exit 2
fi

echo "GUI performance budgets (scale x${ARCHMAID_GUI_PERF_SCALE:-1}):"
bad=0
while IFS=$'\t' read -r name measured unit limit ok; do
	if [[ "$ok" == true ]]; then verdict=ok; else
		verdict=BREACH
		bad=1
	fi
	printf '  %-7s %-22s measured %-9s %-8s budget %s\n' "$verdict" "$name" "$measured" "$unit" "$limit"
done <"$tmp/report.tsv"

if [[ "$rc" -ne 0 && "$bad" -eq 0 ]]; then
	# A test failed for a reason that is not a budget (correctness, a leftover process).
	echo "gui-perf: FAIL: the tests failed without a budget breach (exit $rc); log:" >&2
	cat "$tmp/test.log" >&2
	exit 1
fi
if [[ "$bad" -ne 0 ]]; then
	echo "gui-perf: FAIL: a budget is breached" >&2
	exit 1
fi
echo "gui-perf: all budgets met"
