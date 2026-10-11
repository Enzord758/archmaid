#!/usr/bin/env bash
# Builds coverage-instrumented archmaid, archmaid-helper and archmaid-gui into
# OUTDIR. Each writes raw coverage data (covdata) into $GOCOVERDIR when it
# exits normally (main returns or os.Exit; a SIGKILL loses that process's
# counters); scripts/cover-merge.sh merges it with the unit tests'
# (scripts/cover-unit.sh) and everyone else's.
#
#   scripts/cover-build.sh OUTDIR                 # all three
#   COVER_GUI=0 scripts/cover-build.sh OUTDIR     # without the GUI (gotk4: minutes on a cold cache)
#   COVER_HELPER_DIR=/var/lib/archmaid-cover scripts/cover-build.sh OUTDIR
#
# A cover build is for the e2e harness, the test VM and the owner's own
# measured runs only. It is never packaged: packaging/arch/PKGBUILD builds
# plain binaries and check.sh fails if it ever names -cover or the
# archmaidcover tag. Run a cover binary like any archmaid binary: under a
# scratch HOME and XDG_* dirs off the VM (CLAUDE.md), with GOCOVERDIR set to a
# scratch dir, never one under ~/.local/state/archmaid. Without GOCOVERDIR it
# prints "warning: GOCOVERDIR not set, no coverage data emitted" on stderr and
# otherwise runs as usual.
#
# COVER_HELPER_DIR bakes a directory into archmaid-helper for its runs under
# pkexec, which drops every variable but a fixed few, GOCOVERDIR included.
# The helper (tag archmaidcover, cmd/archmaid-helper/internal/coverdir) then
# writes there when it runs as root and GOCOVERDIR is unset, but only if the
# directory exists, is no symlink, is owned by root and isn't group- or
# other-writable: root never writes into a directory a user controls. Create
# it as root first: install -d -m 0755 -o root -g root /var/lib/archmaid-cover
# Runs as the user (plan-orphans) inherit GOCOVERDIR like the other binaries.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
out="${1:?usage: cover-build.sh OUTDIR}"
mkdir -p "$out"
out="$(realpath "$out")"

# Atomic mode in every cover run (cover-unit.sh, the GUI's headless-cover.sh):
# covdata of different modes doesn't merge. -trimpath like the e2e and the
# package builds.
flags=(-trimpath -cover -covermode=atomic)
# The same statements the unit run counts (./cmd/... and ./internal/...);
# each binary carries only the packages it links.
rootpkg=./cmd/...,./internal/...

helper=()
if [[ -n "${COVER_HELPER_DIR:-}" ]]; then
	[[ "$COVER_HELPER_DIR" == /* ]] || { echo "COVER_HELPER_DIR must be absolute" >&2; exit 2; }
	helper=(-tags archmaidcover
		-ldflags "-X github.com/Enzord758/archmaid/cmd/archmaid-helper/internal/coverdir.Dir=$COVER_HELPER_DIR")
fi

cd "$root"
go build "${flags[@]}" -coverpkg="$rootpkg" -o "$out/archmaid" ./cmd/archmaid
# The helper links none of ./internal/... (it would only warn).
go build "${flags[@]}" -coverpkg=./cmd/archmaid-helper/... "${helper[@]}" -o "$out/archmaid-helper" ./cmd/archmaid-helper
if [[ "${COVER_GUI:-1}" != 0 ]]; then
	# From the GUI module: the root module's packages it links and its own.
	(cd gui && go build "${flags[@]}" -coverpkg=github.com/Enzord758/archmaid/... \
		-o "$out/archmaid-gui" ./cmd/archmaid-gui)
fi
ls -l "$out"
