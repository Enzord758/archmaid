#!/usr/bin/env bash
# The GUI's display tests (gui/scripts/headless.sh's default groups, each in
# its own private headless weston under its timeout) plus the plain-Go GUI
# tests, with coverage: every test binary and every child it re-executes (the
# display tests run the window in one) writes covdata into DIR, through
# scripts/cover-exec.sh. Merge DIR with scripts/cover-merge.sh.
#
#   gui/scripts/headless-cover.sh DIR
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
cd "$gui"

# The plain-Go tests (no display; the display tests skip without it).
env -u ARCHMAID_GUI_DISPLAY_TESTS go test -count=1 "${cover[@]}" ./...

# The groups are headless.sh's own; keep them in step with it.
long='^TestGUI(Prompts|FullCleanup)$'
real='^TestGUI(RealDriver|StopAndQuit|OnePassword|RealServices)$'
stops='^TestGUI(EngineStops|StubOwnInstance|Shots|DMSColoursWin|SmallWindow|ToastPlain|NarrowNav)$'
starts='^TestGUI(DryFirst|ConfirmFlag|LastRunTones|RepairNothingFound|OverviewStarts|LogsPage|SettingsCards|ClearRerun)$'
cov='^TestGUICov'
# Build once outside the timeouts: the instrumented ui test binary is cached
# for the runs below.
go test -count=1 "${cover[@]}" -run '^$' ./internal/ui >/dev/null
go test -count=1 "${cover[@]}" -tags shots -run '^$' ./internal/ui >/dev/null
# Every group runs even when one fails, so one red group doesn't lose the
# others' covdata; the script fails at the end.
rc=0
scripts/headless.sh go test "${cover[@]}" -count=1 -run "$long" ./internal/ui || rc=1
scripts/headless.sh go test "${cover[@]}" -count=1 -run "$real" ./internal/ui || rc=1
scripts/headless.sh go test "${cover[@]}" -tags shots -count=1 -run "$stops" ./internal/ui || rc=1
scripts/headless.sh go test "${cover[@]}" -count=1 -run "$starts" ./internal/ui || rc=1
scripts/headless.sh go test "${cover[@]}" -count=1 -run "$cov" ./internal/ui || rc=1
scripts/headless.sh go test "${cover[@]}" -count=1 -skip "$long|$real|$stops|$starts|$cov" ./internal/ui || rc=1
exit "$rc"
