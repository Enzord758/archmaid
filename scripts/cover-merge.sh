#!/usr/bin/env bash
# Merges raw coverage data (covdata dirs) from any mix of runs into one
# number: the unit tests (scripts/cover-unit.sh), the GUI's tests
# (gui/scripts/headless-cover.sh), the e2e tapes (cmd/archmaid/e2e/run.sh
# with E2E_COVER_DIR), the test VM's and the owner's runs of the cover
# binaries (scripts/cover-build.sh). Read-only on its inputs.
#
#   scripts/cover-merge.sh [-o OUT] DIR...
#
# OUT (default: a new temp dir, printed) gets
#   covdata/      the merged covdata (merge it again with later runs)
#   profile.txt   the merged profile, `go test -coverprofile` format
#   summary.txt   per-package statements and percent, and the totals below
#   uncovered.txt every uncovered block: file:lines, function, statements
#   coverage.html `go tool cover -html` of the profile (needs the GUI module
#                 to resolve gui/ files; skipped with a note if it can't)
# and prints the totals:
#   core    cmd/archmaid + internal/... (check.sh's MIN_TOTAL set)
#   helper  cmd/archmaid-helper/...
#   gui     gui/...
#   all     everything in the data
# A block counts as covered if any run counted it (check.sh's rule). A
# package only shows up once some run linked it: a merge of e2e data alone
# has no internal/testenv, so its "core" isn't check.sh's denominator.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
out=""
if [[ "${1:-}" == -o ]]; then
	out="${2:?-o needs a dir}"
	shift 2
fi
(($#)) || { echo "usage: cover-merge.sh [-o OUT] DIR..." >&2; exit 2; }
ins=()
for d in "$@"; do
	[[ -d "$d" ]] || { echo "cover-merge: $d: not a directory" >&2; exit 2; }
	if ! compgen -G "$d/covmeta.*" >/dev/null; then
		echo "cover-merge: $d holds no covmeta file: not a GOCOVERDIR (did the binary exit normally?)" >&2
		exit 2
	fi
	ins+=("$(realpath "$d")")
done
[[ -n "$out" ]] || out="$(mktemp -d "${TMPDIR:-/tmp}/archmaid-cover.XXXXXX")"
mkdir -p "$out/covdata"
out="$(realpath "$out")"
joined="$(IFS=,; echo "${ins[*]}")"

go tool covdata merge -i="$joined" -o="$out/covdata"
go tool covdata textfmt -i="$out/covdata" -o="$out/profile.txt"

cd "$root"
go run ./scripts/covblocks -summary "$out/profile.txt" >"$out/summary.txt"
go run ./scripts/covblocks "$out/profile.txt" >"$out/uncovered.txt"

# total PREFIX-REGEX prints "covered/stmts pct" over the profile's blocks
# whose file matches, with check.sh's rule (a block is covered if any line
# for it has a count; truncated to one decimal).
total() {
	awk -v re="$1" '
		NR == 1 { next }
		{
			split($1, loc, ":")
			if (loc[1] !~ re) next
			stmts[$1] = $2
			if ($3 > 0) hit[$1] = 1
		}
		END {
			for (b in stmts) { t += stmts[b]; if (b in hit) c += stmts[b] }
			if (t == 0) { print "none"; exit }
			printf "%.1f%% (%d/%d)\n", int(1000 * c / t) / 10, c, t
		}' "$out/profile.txt"
}
m=github.com/Enzord758/archmaid
{
	echo
	echo "core    $(total "^$m/(cmd/archmaid/|internal/)")"
	echo "helper  $(total "^$m/cmd/archmaid-helper/")"
	echo "gui     $(total "^$m/gui/")"
	echo "all     $(total ".")"
} | tee -a "$out/summary.txt"

# The HTML report: go tool cover finds each file through its package, so it
# runs in the GUI module, whose replace sees the root module's packages too.
if (cd gui && go tool cover -html="$out/profile.txt" -o "$out/coverage.html") 2>"$out/html.err"; then
	rm -f "$out/html.err"
else
	echo "note: no coverage.html ($(head -1 "$out/html.err"))"
fi
echo "merged: $out"
