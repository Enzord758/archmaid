#!/usr/bin/env bash
# The gate every commit must pass: race-enabled tests (run three times to shake
# out flakes), go vet, gofmt, and coverage floors. Run from anywhere; it works
# from the repo root: one Go module covers ./cmd/archmaid and
# ./cmd/archmaid-helper. Leaves no files behind.
#
# Usage:
#   scripts/check.sh
#   ARCHMAID_PERF_GUARD=1 scripts/check.sh   # also run the perf guard (opt-in:
#                                            # its thresholds are machine-specific)
set -euo pipefail

# Coverage floors, in percent of statements. Raise them when coverage rises;
# never lower one to make a change pass.
MIN_TOTAL=89.1   # the TUI package (cmd/archmaid), measured after the askpass tests + view goldens
MIN_ASKPASS=90   # internal/privilege/askpass.go: it carries the sudo password (95.0% after the move)

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# The owner's real state dir must come out of the gate exactly as it went in
# (scripts/state-guard.sh: read-only, names + sizes + mtimes). The compare
# runs on every exit, so a failing step can't hide a change; a change fails
# the gate even when everything else passed.
GUARD_SNAP="$(mktemp)"
PROFILE=""
scripts/state-guard.sh snapshot >"$GUARD_SNAP"
guard_done=0
guard_compare() {
	guard_done=1
	scripts/state-guard.sh compare "$GUARD_SNAP"
}
finish() {
	local rc=$?
	[[ -n "$PROFILE" ]] && rm -f "$PROFILE"
	if ((!guard_done)) && ! guard_compare; then
		rc=1
	fi
	rm -f "$GUARD_SNAP"
	exit "$rc"
}
trap finish EXIT

TUI=./cmd/archmaid

# Coverage is measured over the TUI and the packages it is being split into
# (internal/, from P2 on), so moving code out of cmd/archmaid moves its
# statements without changing the total. ./internal/... is only named once it
# has packages: an empty pattern fails `go test`.
COVERPKG="$TUI"
COVERTESTS=("$TUI")
if compgen -G 'internal/*/*.go' >/dev/null; then
	COVERPKG="$TUI,./internal/..."
	COVERTESTS+=(./internal/...)
fi

echo "==> go test -race -count=3 (every package)"
go test -race -count=3 ./...

echo "==> go vet"
go vet ./...

echo "==> gofmt"
unformatted="$(gofmt -l .)"
if [[ -n "$unformatted" ]]; then
	echo "FAIL: not gofmt-clean:"
	echo "$unformatted"
	exit 1
fi

echo "==> layering"
# The shared packages must stay UI-free so a GUI can use them: nothing under
# internal/ except internal/tui may pull in Bubble Tea, lipgloss or bubbles.
if compgen -G 'internal/*/*.go' >/dev/null; then
	for pkg in $(go list ./internal/... | grep -v '/internal/tui'); do
		if go list -deps "$pkg" | grep -q '^github.com/charmbracelet/'; then
			echo "FAIL: $pkg imports github.com/charmbracelet/* (only internal/tui and cmd/archmaid may)"
			exit 1
		fi
	done
fi

echo "==> coverage"
PROFILE="$(mktemp)" # removed by finish
if ! out="$(go test -count=1 -coverpkg="$COVERPKG" -coverprofile="$PROFILE" "${COVERTESTS[@]}" 2>&1)"; then
	echo "$out"
	exit 1
fi

# coverage FILE prints the statement coverage of FILE (a path suffix such as
# "/internal/privilege/askpass.go"), or of everything when FILE is empty.
# Profile lines are `path/file.go:a.b,c.d numStmts count`; a block is
# covered if any line for it has a non-zero count.
coverage() {
	awk -v file="$1" '
		NR == 1 { next } # mode: line
		{
			split($1, loc, ":")
			if (file != "" && substr(loc[1], length(loc[1]) - length(file) + 1) != file) next
			stmts[$1] = $2
			if ($3 > 0) hit[$1] = 1
		}
		END {
			for (b in stmts) { total += stmts[b]; if (b in hit) covered += stmts[b] }
			if (total == 0) { print "none"; exit }
			printf "%.1f\n", int(1000 * covered / total) / 10
		}' "$PROFILE"
}

failed=0
# floor NAME FILE MIN checks one coverage figure against its floor.
floor() {
	local got
	got="$(coverage "$2")"
	if [[ "$got" == none ]]; then
		echo "FAIL: $1: no statements found in the coverage profile"
		failed=1
	elif awk -v got="$got" -v min="$3" 'BEGIN { exit !(got + 0 < min + 0) }'; then
		echo "FAIL: $1 coverage $got% < floor $3%"
		failed=1
	else
		echo "ok: $1 coverage $got% (floor $3%)"
	fi
}
floor total "" "$MIN_TOTAL"
floor internal/privilege/askpass.go /internal/privilege/askpass.go "$MIN_ASKPASS"

if ((failed)); then
	exit 1
fi

# Opt-in: the timings behind TestPerfGuard's thresholds are the dev machine's,
# so the default gate stays fast and machine-independent. It runs without
# -race or coverage (the test skips itself under either).
if [[ "${ARCHMAID_PERF_GUARD:-}" == 1 ]]; then
	echo "==> perf guard"
	go test -count=1 -run '^TestPerfGuard$' -v ./...
fi

echo "==> real state dir"
guard_compare
echo "PASS: gate green"
