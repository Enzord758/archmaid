#!/usr/bin/env bash
# go test -exec wrapper for scripts/cover-unit.sh: runs one test binary with
# its coverage counters going to $ARCHMAID_COVER_UNIT_DIR, both its own
# (-test.gocoverdir) and those of every child it re-executes (GOCOVERDIR,
# which children inherit). Plain `go test -cover` points GOCOVERDIR at a
# temp dir of its own and drops what children write there, so the askpass
# relay, the TTY child, the GUI's display child and the helper's harden
# children would never count.
#
#   go test -exec scripts/cover-exec.sh -cover ... ./pkg
set -euo pipefail
dir="${ARCHMAID_COVER_UNIT_DIR:?ARCHMAID_COVER_UNIT_DIR unset (run scripts/cover-unit.sh)}"
bin="$1"
shift
# go test passes a -test.gocoverdir of its own; drop it so ours is the one.
args=()
for a in "$@"; do
	[[ "$a" == -test.gocoverdir=* ]] || args+=("$a")
done
GOCOVERDIR="$dir" exec "$bin" -test.gocoverdir="$dir" "${args[@]}"
