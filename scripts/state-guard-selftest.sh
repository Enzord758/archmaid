#!/usr/bin/env bash
# Self-test of scripts/state-guard.sh, run by check.sh before it trusts the
# guard: a guard that always says "ok" would otherwise pass silently. Every
# case points XDG_STATE_HOME at a scratch dir from mktemp -d, so the real state
# dir is never read or touched here.
#
# Cases: unchanged -> ok; file grown, nested entry deleted, entry added,
# mtime-only change -> fail; absent -> still absent -> ok; absent -> created
# -> fail. Exit 0 if the guard got every case right, 1 with the case names
# otherwise.
set -euo pipefail

guard="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/state-guard.sh"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
failed=()

# seed DIR: a populated state dir like the real one, with old mtimes so a
# rewrite in the same second still shows.
seed() {
	mkdir -p "$1/archmaid/20260101-0000"
	printf 'scratch\n' >"$1/archmaid/20260101-0000/cleanup.log"
	printf '{}\n' >"$1/archmaid/last-run.json"
	touch -d '2026-01-01 00:00' "$1/archmaid/20260101-0000/cleanup.log" \
		"$1/archmaid/last-run.json" "$1/archmaid/20260101-0000" "$1/archmaid"
}

# check NAME WANT CHANGE: snapshot a fresh scratch state home, run CHANGE (a
# shell snippet, with $S the state home) and compare; WANT is ok or fail.
check() {
	local name="$1" want="$2" change="$3" S="$scratch/$1" got=ok
	mkdir -p "$S"
	[[ "$name" == absent* ]] || seed "$S"
	XDG_STATE_HOME="$S" "$guard" snapshot >"$S.before"
	S="$S" bash -c "$change"
	XDG_STATE_HOME="$S" "$guard" compare "$S.before" >/dev/null || got=fail
	[[ "$got" == "$want" ]] || failed+=("$name: got $got, want $want")
}

check unchanged ok ':'
check grown fail 'printf more >>"$S/archmaid/20260101-0000/cleanup.log"'
check deleted fail 'rm "$S/archmaid/20260101-0000/cleanup.log"'
check added fail 'touch -d "2026-01-01 00:00" "$S/archmaid/new.log"'
check touched fail 'touch "$S/archmaid/last-run.json"'
check absent-unchanged ok ':'
check absent-created fail 'mkdir "$S/archmaid"'

if ((${#failed[@]})); then
	echo "FAIL: scripts/state-guard.sh got these self-test cases wrong:"
	printf '  %s\n' "${failed[@]}"
	exit 1
fi
echo "ok: state-guard self-test (7 cases)"
