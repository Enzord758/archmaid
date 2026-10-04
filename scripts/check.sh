#!/usr/bin/env bash
# The gate every commit must pass: race-enabled tests (run three times to shake
# out flakes), go vet, gofmt, and coverage floors. Run from anywhere; it works
# in the tui/ module. Leaves no files behind.
#
# Usage:
#   scripts/check.sh
set -euo pipefail

# Coverage floors, in percent of statements. Raise them when coverage rises;
# never lower one to make a change pass.
MIN_TOTAL=89.1   # whole package, measured after the askpass tests + view goldens
MIN_ASKPASS=80   # askpass.go: it carries the sudo password

cd "$(dirname "${BASH_SOURCE[0]}")/../tui"

echo "==> go test -race -count=3"
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

echo "==> coverage"
PROFILE="$(mktemp)"
trap 'rm -f "$PROFILE"' EXIT
if ! out="$(go test -count=1 -coverprofile="$PROFILE" ./... 2>&1)"; then
	echo "$out"
	exit 1
fi

# coverage FILE prints the statement coverage of FILE (a path suffix such as
# "/askpass.go"), or of everything when FILE is empty. Profile lines are
# `path/file.go:a.b,c.d numStmts count`; a block is covered if any line for it
# has a non-zero count.
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
floor askpass.go /askpass.go "$MIN_ASKPASS"

if ((failed)); then
	exit 1
fi
echo "PASS: gate green"
