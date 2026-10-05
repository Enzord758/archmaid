#!/usr/bin/env bash
# Re-measures the plan's performance baselines (archmaid-plan.md §4; the
# figures TestPerfGuard, bench-compare.sh's caps and the GUI budgets are
# set from) on this machine, but only on a quiet one: a busy machine
# measures the load, not the code. Also `make perf-baseline`.
#
# Usage: scripts/perf-baseline.sh
# Env:   PERF_COUNT     samples per benchmark, and GUI display runs (default 6:
#                       the baselines are medians of 6)
#        PERF_OUT       directory for the raw output and summary.txt (default: a temp dir)
#        PERF_MAX_LOAD  highest 1-minute load average that counts as quiet
#                       (default: an eighth of the CPUs)
#        PERF_GUI=0     leave out the GUI module (no gtk4/weston here)
#        PERF_FORCE=1   measure anyway on a machine that isn't quiet; the
#                       summary then says NOT A BASELINE (for a smoke test)
#
# Quiet means: the load average is at most PERF_MAX_LOAD, no VM
# (qemu-system) runs, and no other gate or test run does (check.sh,
# headless.sh, e2e/run.sh, bench-compare.sh, go test, a *.test binary).
# It is checked before and again after the measurement; otherwise the
# script exits 3 before measuring anything.
#
# What it measures, all through tests and builds (nothing here starts
# archmaid or a system tool):
#   - the TUI benchmarks the perf guard and the bench job use (StreamView5k/20k,
#     SystemStatus, ActivateFullCleanup, ViewRunning), the sysstat and sanitize
#     benchmarks, with -benchmem;
#   - TestPerfGuard against its current baselines;
#   - the GUI model's flush benchmarks, and the GUI's first frame, idle RSS and
#     50k-line throughput (TestGUIFirstFrameAndIdleRAM, TestGUIThroughput) in a
#     headless weston with ARCHMAID_GUI_PERF=1;
#   - the stripped sizes of archmaid, archmaid-helper and archmaid-gui (built, never run).
# The plan's figures that need the real system (the Repair scan's
# pacman -Qk, the services discovery, the installed GUI's idle CPU) are not
# measured here: the owner measures those by hand.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
ROOT="$PWD"

COUNT="${PERF_COUNT:-6}"
CPUS="$(nproc)"
MAX_LOAD="${PERF_MAX_LOAD:-$(awk -v c="$CPUS" 'BEGIN { printf "%.2f", c / 8 }')}"
GUI="${PERF_GUI:-1}"
FORCE="${PERF_FORCE:-0}"
BENCH='StreamView|SystemStatus|ActivateFullCleanup|ViewRunning'
out="${PERF_OUT:-$(mktemp -d)}"
mkdir -p "$out"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# busy prints one line per reason the machine isn't quiet.
busy() {
	local load
	load="$(cut -d' ' -f1 /proc/loadavg)"
	if awk -v l="$load" -v m="$MAX_LOAD" 'BEGIN { exit !(l + 0 > m + 0) }'; then
		echo "load average $load is over $MAX_LOAD"
	fi
	local vm
	if vm="$(pgrep -a -f '(^|/)qemu-system' | head -1)"; then
		echo "a VM is running: ${vm:0:100}"
	fi
	local self=$$ pid cmd
	# Other gates and test runs; this script's own children aren't running yet.
	while read -r pid cmd; do
		[[ $pid == "$self" ]] && continue
		echo "another run is going: $pid ${cmd:0:100}"
	done < <(pgrep -a -f '(^|/)(check\.sh|headless\.sh|e2e/run\.sh|bench-compare\.sh)( |$)|go test|\.test -test\.' |
		grep -v -e "perf-baseline" -e 'pgrep' || true)
}

quiet_check() { # quiet_check WHEN
	local reasons
	reasons="$(busy)"
	[[ -z "$reasons" ]] && return 0
	echo "machine not quiet ($1):"
	sed 's/^/  - /' <<<"$reasons"
	if [[ $FORCE != 1 ]]; then
		echo "refusing to measure: the numbers would be the load's. Wait, or PERF_FORCE=1 for a smoke test."
		exit 3
	fi
	echo "PERF_FORCE=1: measuring anyway; the results are NOT A BASELINE"
	tainted=1
}

tainted=0
echo "==> quiet check (load ≤ $MAX_LOAD, no VM, no other gate)"
quiet_check before
echo "ok: $(cut -d' ' -f1-3 /proc/loadavg) load; $CPUS CPUs; $COUNT samples; results in $out"

# Compile first, so no measurement pays for a build.
echo "==> build"
go test -c -o "$work/tui.test" ./internal/tui
go test -c -o "$work/sysstat.test" ./internal/sysstat
go test -c -o "$work/sanitize.test" ./internal/sanitize
for b in archmaid archmaid-helper; do
	go build -trimpath -ldflags='-s -w' -o "$work/$b" "./cmd/$b"
done
if [[ $GUI == 1 ]]; then
	(cd gui && go test -c -o "$work/runview.test" ./internal/runview &&
		go test -c -o "$work/ui.test" ./internal/ui &&
		go build -trimpath -ldflags='-s -w' -o "$work/archmaid-gui" ./cmd/archmaid-gui) 2>&1 |
		grep -v -e 'cgo-generated-wrappers' -e "^# github.com/diamondburned" || true
	[[ -x "$work/ui.test" ]] || { echo "FAIL: the GUI didn't build (PERF_GUI=0 leaves it out)"; exit 1; }
fi

bench() { # bench NAME DIR BINARY PATTERN
	echo "==> benchmarks: $1"
	(cd "$2" && "$3" -test.run '^$' -test.bench "$4" -test.benchmem -test.count="$COUNT") | tee "$out/bench-$1.txt"
}
bench tui "$ROOT/internal/tui" "$work/tui.test" "$BENCH"
bench sysstat "$ROOT/internal/sysstat" "$work/sysstat.test" .
bench sanitize "$ROOT/internal/sanitize" "$work/sanitize.test" .
if [[ $GUI == 1 ]]; then
	bench runview "$ROOT/gui/internal/runview" "$work/runview.test" .
fi

echo "==> perf guard"
(cd internal/tui && ARCHMAID_PERF_GUARD=1 "$work/tui.test" -test.count=1 -test.run '^TestPerfGuard$' -test.v) |
	tee "$out/perfguard.txt" || echo "perf guard failed (see above)" | tee -a "$out/perfguard.txt"

if [[ $GUI == 1 ]]; then
	echo "==> GUI first frame, idle RSS, throughput ($COUNT runs, headless)"
	: >"$out/gui.txt"
	for ((i = 1; i <= COUNT; i++)); do
		# headless.sh bounds weston and the run to 60 s each.
		ARCHMAID_GUI_PERF=1 gui/scripts/headless.sh bash -c 'cd internal/ui && exec "$0" "$@"' "$work/ui.test" -test.count=1 -test.v \
			-test.run '^TestGUI(FirstFrameAndIdleRAM|Throughput)$' >"$work/gui-run.txt" 2>&1 || true
		grep -E 'first frame:|lines/s|budget|^(--- |ok|FAIL|PASS)' "$work/gui-run.txt" | tee -a "$out/gui.txt"
	done
fi

echo "==> binary sizes (stripped, -trimpath)"
for b in archmaid archmaid-helper archmaid-gui; do
	if [[ -f "$work/$b" ]]; then
		printf '%-16s %s bytes\n' "$b" "$(stat -c %s "$work/$b")"
	fi
done | tee "$out/sizes.txt"

quiet_check after

# The summary: each benchmark's median ns/op, B/op and allocs/op.
{
	echo "perf baseline $(git log -1 --format='%h %s') on $(date -Iseconds)"
	echo "$CPUS CPUs, $(go env GOVERSION), $COUNT samples, load at the end: $(cut -d' ' -f1-3 /proc/loadavg)"
	if ((tainted)); then
		echo "NOT A BASELINE: the machine wasn't quiet (PERF_FORCE=1)"
	fi
	echo
	printf '%-40s %14s %12s %12s\n' benchmark 'median ns/op' 'B/op' 'allocs/op'
	awk '
		# name, N, then value-unit pairs: ns/op, MB/s (with SetBytes),
		# B/op, allocs/op. The name gets its file'"'"'s package: tui and
		# sysstat both have a SystemStatus.
		$1 ~ /^Benchmark/ {
			pkg = FILENAME; sub(/.*bench-/, "", pkg); sub(/\.txt$/, "", pkg)
			n = $1; sub(/-[0-9]+$/, "", n); sub(/^Benchmark/, "", n); n = pkg "/" n
			if (!(n in cnt)) order[++k] = n
			for (f = 4; f <= NF; f++) {
				if ($f == "ns/op") v[n, ++cnt[n]] = $(f - 1)
				if ($f == "B/op") b[n] = $(f - 1)
				if ($f == "allocs/op") a[n] = $(f - 1)
			}
		}
		END {
			for (j = 1; j <= k; j++) {
				n = order[j]; c = cnt[n]
				for (x = 1; x <= c; x++) s[x] = v[n, x] + 0
				for (x = 2; x <= c; x++) for (y = x; y > 1 && s[y-1] > s[y]; y--) { t = s[y]; s[y] = s[y-1]; s[y-1] = t }
				med = (c % 2) ? s[(c + 1) / 2] : (s[c / 2] + s[c / 2 + 1]) / 2
				printf "%-40s %14.0f %12s %12s\n", n, med, b[n], a[n]
			}
		}' "$out"/bench-*.txt
	echo
	grep -E '^\s+--- (PASS|FAIL)|^--- FAIL' "$out/perfguard.txt" || true
	if [[ -f "$out/gui.txt" ]]; then
		echo
		grep -E 'first frame:|lines/s' "$out/gui.txt" | sed 's/^ *//' || true
	fi
	echo
	cat "$out/sizes.txt"
} >"$out/summary.txt"
echo "==> summary ($out/summary.txt)"
cat "$out/summary.txt"
