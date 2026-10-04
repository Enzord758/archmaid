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
MIN_TOTAL=89.1   # cmd/archmaid + internal/... (the TUI is internal/tui since P5), measured after the askpass tests + view goldens
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

# A guard that can't see a change would pass every run: prove it on scratch
# dirs first (scripts/state-guard-selftest.sh, well under a second).
echo "==> state-guard self-test"
scripts/state-guard-selftest.sh

TUI=./cmd/archmaid # the binary's main; the TUI itself is internal/tui (P5)

# Coverage is measured over the binary and the packages it was split into
# (internal/, from P2 on; internal/tui from P5), so moving code out of
# cmd/archmaid moves its statements without changing the total. ./internal/... is only named once it
# has packages: an empty pattern fails `go test`.
COVERPKG="$TUI"
COVERTESTS=("$TUI")
if compgen -G 'internal/*/*.go' >/dev/null; then
	COVERPKG="$TUI,./internal/..."
	COVERTESTS+=(./internal/...)
fi

echo "==> go test -race -count=3 (every package)"
# The full output is kept in a file so a red run always names its package.
gatelog="${TMPDIR:-/tmp}/archmaid-gate-$$.log"
if go test -race -count=3 ./... 2>&1 | tee "$gatelog"; then
	rm -f "$gatelog"
else
	echo "FAIL: go test -race; full output in $gatelog"
	grep -E -- '^(--- FAIL|FAIL|panic:)|DATA RACE' "$gatelog" || true
	exit 1
fi

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
# The shared packages must stay UI-free so both front ends can use them:
# nothing under internal/ except internal/tui may pull in Bubble Tea, lipgloss
# or bubbles, and nothing under internal/ at all may pull in gotk4 (the GUI's
# toolkit lives in the gui/ module). internal/frontend, the glue the TUI and
# the GUI share, is held to both by name, so the rule can't lapse for it.
if compgen -G 'internal/*/*.go' >/dev/null; then
	go list ./internal/frontend >/dev/null || {
		echo "FAIL: internal/frontend is missing; the layering rule below names it"
		exit 1
	}
	for pkg in $(go list ./internal/...); do
		deps="$(go list -deps -test "$pkg")"
		if [[ "$pkg" != */internal/tui ]] && grep -q '^github.com/charmbracelet/' <<<"$deps"; then
			echo "FAIL: $pkg imports github.com/charmbracelet/* (only internal/tui may)"
			exit 1
		fi
		if grep -q '^github.com/diamondburned/' <<<"$deps"; then
			echo "FAIL: $pkg imports github.com/diamondburned/* (gotk4: only the gui/ module may)"
			exit 1
		fi
	done
fi
# The commands are thin mains: cmd/archmaid reaches Bubble Tea only through
# internal/tui (tui.Run), so no package under cmd/ may import charmbracelet
# itself, in its code or its tests. (Its deps include it through
# internal/tui, so this checks direct imports.)
for pkg in $(go list ./cmd/...); do
	if go list -f '{{join .Imports "\n"}}{{"\n"}}{{join .TestImports "\n"}}{{"\n"}}{{join .XTestImports "\n"}}' "$pkg" |
		grep -q '^github.com/charmbracelet/'; then
		echo "FAIL: $pkg imports github.com/charmbracelet/* directly (only internal/tui may)"
		exit 1
	fi
done
# Test-only code stays out of the binaries: no main package may link the
# testing package (privilege/privtest and testenv are for _test.go files).
for pkg in $(go list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./cmd/...); do
	if go list -deps "$pkg" | grep -qx 'testing'; then
		echo "FAIL: $pkg links the testing package; test helpers belong in _test.go files"
		exit 1
	fi
done

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
