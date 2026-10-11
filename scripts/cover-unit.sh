#!/usr/bin/env bash
# The root module's unit tests, writing raw coverage data (covdata) into DIR
# instead of a profile, so they merge with the e2e tapes', the test VM's and
# the owner's own runs of the cover binaries (scripts/cover-build.sh) into one
# number (scripts/cover-merge.sh).
#
#   scripts/cover-unit.sh DIR [PKG...]      # default PKG: ./...
#
# Differences from check.sh's coverage run, both on purpose:
#   - every test binary runs through scripts/cover-exec.sh, so a child it
#     re-executes (the askpass relay, the TTY child running the real tui.Run,
#     the helper's harden and signal children) counts too; plain `go test
#     -cover` drops what children write;
#   - coverage is over ./cmd/... and ./internal/... (the helper included),
#     atomic mode, like every cover run, so all of them merge.
# What runs is exactly `go test ./...`: every package under testenv.Run, with
# its traps and the real-state-dir guard. No -race: the gate runs that.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
dir="${1:?usage: cover-unit.sh DIR [PKG...]}"
shift
pkgs=("$@")
((${#pkgs[@]})) || pkgs=(./...)
mkdir -p "$dir"
ARCHMAID_COVER_UNIT_DIR="$(realpath "$dir")" go test -count=1 \
	-exec "$PWD/scripts/cover-exec.sh" -cover -covermode=atomic \
	-coverpkg=./cmd/...,./internal/... "${pkgs[@]}"
