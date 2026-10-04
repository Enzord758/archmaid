#!/usr/bin/env bash
# The CI bench job's script, also `make bench`: benchmark a base commit and
# HEAD on this machine, back to back and interleaved, compare them with
# benchstat and fail on a real regression. Absolute timings differ wildly
# between machines (the perf guard's baselines are a 16-core desktop's), so
# nothing here compares to a stored number except two generous backstops;
# the verdict is relative: base and HEAD ran on the same runner, so runner
# speed cancels out, and benchstat's significance test (p < 0.05) keeps noise
# from failing it.
#
# Usage: scripts/bench-compare.sh [BASE_REF]    (default: merge-base with origin/main)
# Env:   BENCH_COUNT   samples per side (default 6)
#        BENCH_OUT     directory for old.txt, new.txt, benchstat.txt (default: a temp dir)
#
# Fails when, for HEAD (the working tree) against base:
#   - a benchmark's sec/op is significantly slower by more than MAX_SLOWDOWN_PCT;
#   - SystemStatus allocs/op (the status line allocates on every refresh) rose at all;
#   - ActivateFullCleanup is over 1 ms or ViewRunning over 600 µs in absolute terms
#     (best sample; the same caps as TestPerfGuard's fixed limits).
set -euo pipefail

BENCHSTAT=golang.org/x/perf/cmd/benchstat@v0.0.0-20260929162123-406019bb8b68
# benchstat itself needs a newer Go than the module (go.mod 1.25, which CI pins
# with GOTOOLCHAIN=local). Only the tool gets a newer toolchain; the benchmarks
# above still build and run with the module's Go.
BENCHSTAT_TOOLCHAIN=auto  # local Go, or the newer one benchstat requires
BENCH='StreamView|SystemStatus|ActivateFullCleanup|ViewRunning'
MAX_SLOWDOWN_PCT=25
COUNT="${BENCH_COUNT:-6}"
PKG=./cmd/archmaid

cd "$(dirname "${BASH_SOURCE[0]}")/.."
ROOT="$PWD"

base="${1:-$(git merge-base HEAD origin/main)}"
base="$(git rev-parse --verify "$base^{commit}")"
out="${BENCH_OUT:-$(mktemp -d)}"
mkdir -p "$out"
work="$(mktemp -d)"
cleanup() {
	git -C "$ROOT" worktree remove --force "$work/base" >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

echo "==> base $(git log -1 --format='%h %s' "$base")"
echo "==> head $(git log -1 --format='%h %s' HEAD)"
if [[ "$base" == "$(git rev-parse HEAD)" && -z "$(git status --porcelain)" ]]; then
	echo "base is HEAD: nothing to compare"
	exit 0
fi

git worktree add --detach "$work/base" "$base" >/dev/null
# Compile both test binaries first (so a build error shows before any timing,
# and neither side pays for compilation while measured), then alternate the
# runs: a slow stretch on a shared runner hits both sides alike.
(cd "$work/base" && go test -c -o "$work/old.test" "$PKG")
go test -c -o "$work/new.test" "$PKG"

: >"$out/old.txt"
: >"$out/new.txt"
echo "==> $COUNT interleaved rounds of -bench '$BENCH'"
for ((i = 1; i <= COUNT; i++)); do
	for side in old new; do
		dir="$work/base"
		[[ $side == new ]] && dir="$ROOT"
		(cd "$dir/cmd/archmaid" && "$work/$side.test" -test.run '^$' -test.bench "$BENCH" -test.benchmem -test.benchtime=300ms) >>"$out/$side.txt"
	done
done

echo "==> benchstat"
GOTOOLCHAIN="$BENCHSTAT_TOOLCHAIN" go run "$BENCHSTAT" "$out/old.txt" "$out/new.txt" 2>&1 | tee "$out/benchstat.txt"
GOTOOLCHAIN="$BENCHSTAT_TOOLCHAIN" go run "$BENCHSTAT" -format csv "$out/old.txt" "$out/new.txt" >"$out/benchstat.csv" 2>/dev/null

failed=0
fail() {
	echo "FAIL: $*"
	failed=1
}

# benchstat's csv: a section per unit (sec/op, B/op, allocs/op), rows of
# name,old,ci,new,ci,delta,p. A delta of "~" means no significant change.
while IFS='|' read -r unit name delta; do
	pct="${delta//[+%]/}"
	case "$unit" in
	sec/op)
		if awk -v p="$pct" -v max="$MAX_SLOWDOWN_PCT" 'BEGIN { exit !(p + 0 > max + 0) }'; then
			fail "$name sec/op is $delta vs base (significant, limit +$MAX_SLOWDOWN_PCT%)"
		fi
		;;
	allocs/op)
		if [[ "$name" == SystemStatus* && "$delta" == +* ]]; then
			fail "$name allocs/op is $delta vs base (any increase fails)"
		fi
		;;
	esac
done < <(awk -F, '
	$2 == "sec/op" || $2 == "B/op" || $2 == "allocs/op" { unit = $2; next }
	unit != "" && NF >= 6 && $1 != "geomean" && $6 != "~" && $6 != "" { print unit "|" $1 "|" $6 }
' "$out/benchstat.csv")

# Backstops: the best HEAD sample against a fixed cap, so a regression that
# base already had still trips, and so does a slowdown spread over many small
# commits that never shows as one significant step.
check_cap() { # NAME LIMIT_NS
	local best
	best="$(awk -v n="Benchmark$1" '
		index($1, n "-") == 1 { if (min == "" || $3 + 0 < min + 0) min = $3 }
		END { print min }' "$out/new.txt")"
	if [[ -z "$best" ]]; then
		fail "$1 did not run in HEAD"
	elif awk -v b="$best" -v l="$2" 'BEGIN { exit !(b + 0 > l + 0) }'; then
		fail "$1 best sample ${best} ns/op is over the fixed cap of $2 ns"
	else
		echo "ok: $1 best ${best} ns/op (cap $2 ns)"
	fi
}
check_cap ActivateFullCleanup 1000000
check_cap ViewRunning 600000

echo "results: $out"
if ((failed)); then
	exit 1
fi
echo "PASS: no benchmark regression against ${base:0:9}"
