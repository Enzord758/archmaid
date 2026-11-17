#!/usr/bin/env bash
# The GUI's display tests (gui/scripts/parts.sh's parts, the same ones
# headless.sh runs, each in its own private headless display under its
# timeout) plus the plain-Go GUI tests, with coverage: every test binary and
# every child it re-executes (the display tests run the window in one) writes
# covdata into DIR, through scripts/cover-exec.sh. Merge DIR with
# scripts/cover-merge.sh.
#
#   gui/scripts/headless-cover.sh DIR
#
# Like headless.sh it runs HEADLESS_JOBS parts at a time (default 4, 1..8).
# That is safe for the data: every test process writes its own uniquely named
# covcounters file (pid and time in the name) and identical covmeta files, so
# concurrent parts only add files to DIR. A part's output is printed whole
# when all have ended, then a table; every part runs even when one fails, so
# one red part doesn't lose the others' covdata, and the script fails at the
# end.
#
# Coverage is over github.com/Enzord758/archmaid/... (the root module's
# packages the GUI links, and the GUI's own), in atomic mode like every other
# cover run (scripts/cover-unit.sh, scripts/cover-build.sh), so the data
# merges. No window opens on the desktop: headless.sh's rules apply.
set -euo pipefail
self="$(realpath "${BASH_SOURCE[0]}")"
gui="$(dirname "$self")/.."
root="$gui/.."
dir="${1:?usage: headless-cover.sh DIR}"
mkdir -p "$dir"
dir="$(realpath "$dir")"
export ARCHMAID_COVER_UNIT_DIR="$dir"
cover=(-exec "$(realpath "$root/scripts/cover-exec.sh")" -cover -covermode=atomic
	-coverpkg=github.com/Enzord758/archmaid/...)
# The parts are headless.sh's own (one definition, parts.sh).
source "$(dirname "$self")/parts.sh"
cd "$gui"

jobs="${HEADLESS_JOBS:-4}"
[[ "$jobs" =~ ^[1-8]$ ]] || {
	echo "HEADLESS_JOBS must be 1..8, not $jobs" >&2
	exit 2
}
export HEADLESS_TIMEOUT="${HEADLESS_TIMEOUT:-180}"

# The plain-Go tests (no display; the display tests skip without it).
env -u ARCHMAID_GUI_DISPLAY_TESTS go test -count=1 "${cover[@]}" ./...

# Build once outside the timeouts: the instrumented ui test binary is cached
# for the runs below.
go test -count=1 "${cover[@]}" -run '^$' ./internal/ui >/dev/null
go test -count=1 "${cover[@]}" -tags shots -run '^$' ./internal/ui >/dev/null

skip="$(parts_skip)"
parts+=("rest|1280|800|t|")
out="$(mktemp -d)"
pids=()
cleanup() {
	local pid
	for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || true; done
	rm -rf "$out"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

run_part() { # run_part name width height tags regex
	local name="$1" wd="$2" ht="$3" tg="$4" re="$5" rc=0 start=$SECONDS tags=() sel
	[[ "$tg" == s ]] && tags=(-tags shots)
	if [[ -n "$re" ]]; then
		sel=(-run "$re")
	else
		sel=(-skip "$skip")
	fi
	HEADLESS_WIDTH="$wd" HEADLESS_HEIGHT="$ht" scripts/headless.sh go test "${cover[@]}" "${tags[@]}" -count=1 -v "${sel[@]}" ./internal/ui >"$out/$name.log" 2>&1 || rc=$?
	echo "$rc" >"$out/$name.rc"
	echo "$((SECONDS - start))" >"$out/$name.secs"
	echo "part $name: $((SECONDS - start)) s, $([[ $rc -eq 0 ]] && echo ok || echo "FAIL ($rc)")" >&2
}

started=$SECONDS
running=0
order=()
for p in "${parts[@]}"; do
	IFS='|' read -r name wd ht tg re <<<"$p"
	order+=("$name")
	while ((running >= jobs)); do
		wait -n || true
		running=$((running - 1))
	done
	run_part "$name" "$wd" "$ht" "$tg" "$re" &
	pids+=($!)
	running=$((running + 1))
done
wait || true
rc=0
for name in "${order[@]}"; do
	echo "=== part $name"
	cat "$out/$name.log"
done
echo
printf '%-10s %6s  %s\n' part secs result
for name in "${order[@]}"; do
	prc="$(cat "$out/$name.rc" 2>/dev/null || echo 1)"
	[[ "$prc" -eq 0 ]] || rc=1
	printf '%-10s %6s  %s\n' "$name" "$(cat "$out/$name.secs" 2>/dev/null || echo ?)" "$([[ "$prc" -eq 0 ]] && echo ok || echo "FAIL ($prc)")"
done
echo "all parts: $((SECONDS - started)) s wall, $jobs at a time"
exit "$rc"
