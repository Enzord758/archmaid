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

# Coverage floors, in percent of statements. Raised with every coverage batch
# (docs/coverage-map.md, "Work packages") to the figure the gate then
# measures, rounded down to one decimal; never lowered to make a change pass.
# The goal is 100 % of reachable statements (docs/coverage-exempt.md): the
# gate also prints the reachable figure, so the floor and the goal stay in view.
MIN_TOTAL=96.8   # 97.06 measured at #231 (6737/6941), floor keeps >= 0.2 margin; before: core = cmd/archmaid + internal/... WITHOUT the test-only packages (testenv, testtranscript, enginetest, privtest, helpertest: their own "testonly" line, not floored); 96.80 measured at #153 (6173/6377), so the floor keeps 0.20 points (12 statements) of margin against flakes (>= 0.2 required)
MIN_ASKPASS=93   # internal/privilege/askpass.go: it carries the sudo password (93.36 % = 183/196 at WP3; 0.36 points of margin, >= 0.2)

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# The owner's real state dir must come out of the gate exactly as it went in
# (scripts/state-guard.sh: read-only, names + sizes + mtimes). The compare
# runs on every exit, so a failing step can't hide a change; a change fails
# the gate even when everything else passed.
GUARD_SNAP="$(mktemp)"
PROFILE=""
# Every testenv.Run appends the scratch dirs it makes to this file; the gate
# fails if any of them is still there at the end (scripts/testenv-leftovers.sh).
CREATED="$(mktemp)"
export ARCHMAID_TESTENV_CREATED="$CREATED"
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
	scripts/testenv-leftovers.sh "$CREATED" || rc=1
	rm -f "$CREATED"
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
	# The lists are captured before they are looped or grepped: a go list
	# that fails (or lists nothing) fails the gate, where a for loop over
	# $(go list) or a pipe into grep reads it as "nothing to check".
	ipkgs="$(go list ./internal/...)" || {
		echo "FAIL: go list ./internal/... failed; can't check the layering"
		exit 1
	}
	if [[ -z "$ipkgs" ]]; then
		echo "FAIL: go list ./internal/... found no package; can't check the layering"
		exit 1
	fi
	for pkg in $ipkgs; do
		deps="$(go list -deps -test "$pkg")" || {
			echo "FAIL: go list -deps -test $pkg failed; can't check the layering"
			exit 1
		}
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
cpkgs="$(go list ./cmd/...)" || {
	echo "FAIL: go list ./cmd/... failed; can't check the commands' imports"
	exit 1
}
if [[ -z "$cpkgs" ]]; then
	echo "FAIL: go list ./cmd/... found no package; can't check the commands' imports"
	exit 1
fi
for pkg in $cpkgs; do
	imports="$(go list -f '{{join .Imports "\n"}}{{"\n"}}{{join .TestImports "\n"}}{{"\n"}}{{join .XTestImports "\n"}}' "$pkg")" || {
		echo "FAIL: go list of $pkg's imports failed; can't check them"
		exit 1
	}
	if grep -q '^github.com/charmbracelet/' <<<"$imports"; then
		echo "FAIL: $pkg imports github.com/charmbracelet/* directly (only internal/tui may)"
		exit 1
	fi
done
# Test-only code stays out of the binaries: no main package may link the
# testing package (privilege/privtest, helperclient/helpertest and testenv are
# for _test.go files). Each go list is captured before it is grepped, and
# its failure fails the gate: piped into grep (or a for loop's word list),
# a go list that fails reads as "not linked" and the check passes.
mains="$(go list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./cmd/...)" || {
	echo "FAIL: go list of the main packages failed; can't check what they link"
	exit 1
}
if [[ -z "$mains" ]]; then
	echo "FAIL: go list found no main package under ./cmd; can't check what they link"
	exit 1
fi
for pkg in $mains; do
	deps="$(go list -deps "$pkg")" || {
		echo "FAIL: go list -deps $pkg failed; can't check what it links"
		exit 1
	}
	if grep -qx 'testing' <<<"$deps"; then
		echo "FAIL: $pkg links the testing package; test helpers belong in _test.go files"
		exit 1
	fi
	# testenv's init points internal/sysbin's absolute paths (su, sudo,
	# pkexec, pkttyagent, topgrade) back at bare names that PATH decides.
	if grep -qx 'github.com/Enzord758/archmaid/internal/testenv' <<<"$deps"; then
		echo "FAIL: $pkg links internal/testenv, whose init makes archmaid look su and sudo up on PATH again"
		exit 1
	fi
done
# Only the e2e harness's test build sets internal/sysbin back to bare names
# (-ldflags -X): the package, the owner's own build (install.sh, into
# ~/.local/bin) and the VM's cover builds keep /usr/bin. The package and
# install.sh set no variable but internal/version.Version (the cover build sets coverdir's).
# A missing or renamed file would read as clean to grep: each must exist.
for f in packaging/arch/PKGBUILD install.sh scripts/cover-build.sh; do
	test -f "$f" || {
		echo "FAIL: $f is missing; the build-flag checks below read it"
		exit 1
	}
done
if grep -nE -- 'sysbin' packaging/arch/PKGBUILD install.sh scripts/cover-build.sh; then
	echo "FAIL: a product or cover build names internal/sysbin; its /usr/bin paths must stay as built (only cmd/archmaid/e2e/run.sh sets them bare)"
	exit 1
fi
# The one allowed flag is cut out of each line first, so a second -X on the
# same line (another variable, another package) still trips the guard.
if grep -nE -- '-X' packaging/arch/PKGBUILD install.sh |
	sed -E 's/-X github\.com\/Enzord758\/archmaid\/internal\/version\.Version=//' | grep -E -- '-X'; then
	echo "FAIL: packaging/arch/PKGBUILD or install.sh sets a variable at link time (-X) other than internal/version.Version; a product build sets no other (internal/sysbin's /usr/bin paths stay as built)"
	exit 1
fi

# The TUI has no desktop entry at all, not even a hidden one (only the GUI's
# io.github.enzord758.archmaid.desktop exists). Nothing may make one again:
# no packaging/archmaid.desktop, and neither the PKGBUILD nor install.sh
# installs or writes a file named archmaid.desktop (install.sh may only
# remove an older one).
# Nor does anything install the icon under the plain name apps/archmaid.svg
# (the GUI's copy is apps/<app-id>.svg; install.sh may only remove an older
# copy of its own).
if grep -nE -- 'apps/archmaid\.svg' packaging/arch/PKGBUILD; then
	echo "FAIL: packaging/arch/PKGBUILD installs apps/archmaid.svg; nothing uses the icon name archmaid"
	exit 1
fi
if test -e packaging/archmaid.desktop; then
	echo "FAIL: packaging/archmaid.desktop exists; the TUI has no desktop entry"
	exit 1
fi
if grep -nE -- '(^|[^.[:alnum:]_-])archmaid\.desktop' packaging/arch/PKGBUILD; then
	echo "FAIL: packaging/arch/PKGBUILD names archmaid.desktop; the TUI has no desktop entry"
	exit 1
fi
# install.sh's own run, in a scratch prefix with a fake go: a fresh install
# leaves no applications/archmaid.desktop, and a stale entry of archmaid's own
# is removed while a foreign file of the same name is kept.
(
	set -e
	t="$(mktemp -d)"
	trap 'rm -rf "$t"' EXIT
	mkdir -p "$t/bin" "$t/home/share/applications"
	printf '#!/bin/sh\nwhile [ "$1" != -o ]; do shift; done\nprintf "#!/bin/sh\\n" >"$2"\n' >"$t/bin/go"
	chmod +x "$t/bin/go"
	run() { HOME="$t/home" PREFIX="$t/home" PATH="$t/bin:$PATH" bash ./install.sh "$@" >/dev/null; }
	app="$t/home/share/applications/archmaid.desktop"
	printf '[Desktop Entry]\nName=archmaid\nExec=kitty /h/.local/bin/archmaid\n' >"$app"
	run
	test ! -e "$app" || { echo "FAIL: install.sh left archmaid's own stale archmaid.desktop"; exit 1; }
	printf '[Desktop Entry]\nName=other\nExec=other\n' >"$app"
	run
	run --uninstall
	test -f "$app" || { echo "FAIL: install.sh removed a foreign archmaid.desktop"; exit 1; }
	rm -f "$app"
	run
	test ! -e "$app" || { echo "FAIL: install.sh wrote archmaid.desktop"; exit 1; }
	# The same for the icon: a fresh install leaves no apps/archmaid.svg, an
	# own stale copy (byte-identical to packaging/archmaid.svg) goes, a
	# different file or a symlink of that name stays.
	ico="$t/home/share/icons/hicolor/scalable/apps/archmaid.svg"
	mkdir -p "${ico%/*}"
	run
	test ! -e "$ico" || { echo "FAIL: install.sh wrote apps/archmaid.svg"; exit 1; }
	cp packaging/archmaid.svg "$ico"
	run
	test ! -e "$ico" || { echo "FAIL: install.sh left archmaid's own stale apps/archmaid.svg"; exit 1; }
	cp packaging/archmaid.svg "$ico"
	run --uninstall
	test ! -e "$ico" || { echo "FAIL: --uninstall left archmaid's own stale apps/archmaid.svg"; exit 1; }
	printf '<svg>other</svg>\n' >"$ico"
	run
	run --uninstall
	test -f "$ico" || { echo "FAIL: install.sh removed a foreign apps/archmaid.svg"; exit 1; }
	rm -f "$ico"
	ln -s packaging-target "$ico"
	run
	test -L "$ico" || { echo "FAIL: install.sh removed a symlink apps/archmaid.svg"; exit 1; }
) || exit 1

# A cover build (scripts/cover-build.sh) is never what gets installed: a
# normal build of the helper doesn't link the cover-only coverdir package
# (tag archmaidcover), and the PKGBUILD builds no cover binary.
deps="$(go list -deps ./cmd/archmaid-helper)" || {
	echo "FAIL: go list -deps ./cmd/archmaid-helper failed; can't check what it links"
	exit 1
}
if grep -q '/cmd/archmaid-helper/internal/coverdir$' <<<"$deps"; then
	echo "FAIL: a normal build of cmd/archmaid-helper links internal/coverdir (cover builds only, tag archmaidcover)"
	exit 1
fi
if grep -nE -- '-cover|archmaidcover|GOCOVERDIR' packaging/arch/PKGBUILD; then
	echo "FAIL: packaging/arch/PKGBUILD names a coverage build; the package ships plain binaries (scripts/cover-build.sh is for test runs)"
	exit 1
fi

echo "==> coverage"
PROFILE="$(mktemp)" # removed by finish
if ! out="$(go test -count=1 -coverpkg="$COVERPKG" -coverprofile="$PROFILE" "${COVERTESTS[@]}" 2>&1)"; then
	echo "$out"
	exit 1
fi

# Test-only packages (the same list as scripts/covblocks TestOnlyDirs) are no
# product code: the total floor and the core figures leave them out, and they
# are reported on their own line.
TESTONLY='/internal/(testenv|testtranscript|engine/enginetest|privilege/privtest|helperclient/helpertest)/'

# coverage FILE prints the statement coverage of FILE (a path suffix such as
# "/internal/privilege/askpass.go"), or of everything but the test-only
# packages when FILE is empty (just those when FILE is "testonly"). Profile
# lines are `path/file.go:a.b,c.d numStmts count`; a block is covered if any
# line for it has a non-zero count. With "counts" as second argument the
# (covered/total) statements follow the figure.
coverage() {
	awk -v file="$1" -v testonly="$TESTONLY" -v counts="${2:-}" '
		NR == 1 { next } # mode: line
		{
			split($1, loc, ":")
			if (file == "testonly") { if (loc[1] !~ testonly) next }
			else if (file != "") { if (substr(loc[1], length(loc[1]) - length(file) + 1) != file) next }
			else if (loc[1] ~ testonly) next
			stmts[$1] = $2
			if ($3 > 0) hit[$1] = 1
		}
		END {
			for (b in stmts) { total += stmts[b]; if (b in hit) covered += stmts[b] }
			if (total == 0) { print "none"; exit }
			if (counts == "counts") printf "%.1f%% (%d/%d)\n", int(1000 * covered / total) / 10, covered, total
			else printf "%.1f\n", int(1000 * covered / total) / 10
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
# The same profile against the exempt list (docs/coverage-exempt.md): raw and
# reachable side by side. Informational; the floor above is the raw figure.
# A stale entry (code moved) is named on stderr: refresh the list.
echo "info: $(go run ./scripts/covblocks -reachable -exempt docs/coverage-exempt.tsv "$PROFILE" | grep -E '^(core|testonly)')"
echo "info: core $(coverage "" counts); test-only packages $(coverage testonly counts) (not in the floor)"
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
