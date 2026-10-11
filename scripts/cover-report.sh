#!/usr/bin/env bash
# `make e2e-cover`: the unit tests and the e2e tapes, each writing covdata,
# merged into one number (scripts/cover-merge.sh), plus the GUI's tests with
# COVER_GUI=1 (headless weston, ~3 minutes). Further runs (the test VM's, the
# owner's) merge in afterwards:
#   scripts/cover-merge.sh -o NEW "$COVER_OUT/merged/covdata" VMDIR OWNERDIR
#
#   scripts/cover-report.sh                 # COVER_OUT: a new temp dir
#   COVER_OUT=/tmp/cov COVER_GUI=1 scripts/cover-report.sh
#
# COVER_OUT gets unit/, e2e/, gui/ (each a GOCOVERDIR) and merged/ (see
# cover-merge.sh). Both suites keep every guard of theirs: the e2e harness
# its shims and the real-state-dir check, every test package testenv.Run.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
out="${COVER_OUT:-$(mktemp -d "${TMPDIR:-/tmp}/archmaid-cover.XXXXXX")}"
mkdir -p "$out"
out="$(realpath "$out")"
echo "==> unit tests, covdata into $out/unit"
scripts/cover-unit.sh "$out/unit"
echo "==> e2e tapes on the cover binary, covdata into $out/e2e"
E2E_COVER_DIR="$out/e2e" bash cmd/archmaid/e2e/run.sh
dirs=("$out/unit" "$out/e2e")
if [[ "${COVER_GUI:-0}" == 1 ]]; then
	echo "==> GUI tests (headless), covdata into $out/gui"
	gui/scripts/headless-cover.sh "$out/gui"
	dirs+=("$out/gui")
fi
echo "==> merged"
scripts/cover-merge.sh -o "$out/merged" "${dirs[@]}"
