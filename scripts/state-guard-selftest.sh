#!/usr/bin/env bash
# Self-test of scripts/state-guard.sh, run by check.sh before it trusts the
# guard: a guard that always says "ok" would otherwise pass silently. Every
# case points XDG_STATE_HOME at a scratch dir from mktemp -d, so the real state
# dir is never read or touched here.
#
# Cases: unchanged -> ok; file grown, nested entry deleted, entry added,
# mtime-only change -> fail; the owner's timer rewriting updates-cache.json and
# health.json (temp file + rename) and its temp names -> ok; either file gone,
# created, a symlink, a directory or chmod-ed, a look-alike name, a stray
# last-run.json / health-ignore.json / run folder write, and a stray write
# beside a timer rewrite -> fail; no diff on PATH -> exit 2 and the message;
# absent -> still absent -> ok; absent -> created -> fail. Exit 0 if the guard
# got every case right, 1 with the case names
# otherwise.
set -euo pipefail

guard="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/state-guard.sh"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
failed=()
ncases=1 # the no-diff case below

# seed DIR: a populated state dir like the real one, with old mtimes so a
# rewrite in the same second still shows.
seed() {
	mkdir -p "$1/archmaid/20260101-0000"
	printf 'scratch\n' >"$1/archmaid/20260101-0000/cleanup.log"
	printf '{}\n' >"$1/archmaid/last-run.json"
	printf '{}\n' >"$1/archmaid/updates-cache.json"
	printf '{}\n' >"$1/archmaid/health.json"
	chmod 600 "$1/archmaid/health.json" "$1/archmaid/updates-cache.json"
	touch -d "2026-01-01 00:00" "$1/archmaid/updates-cache.json" "$1/archmaid/health.json" "$1/archmaid/20260101-0000/cleanup.log" \
		"$1/archmaid/last-run.json" "$1/archmaid/20260101-0000" "$1/archmaid"
}

# check NAME WANT CHANGE: snapshot a fresh scratch state home, run CHANGE (a
# shell snippet, with $S the state home) and compare; WANT is ok or fail.
check() {
	ncases=$((ncases + 1))
	local name="$1" want="$2" change="$3" S="$scratch/$1" got=ok
	mkdir -p "$S"
	[[ "$name" == absent* ]] || seed "$S"
	XDG_STATE_HOME="$S" "$guard" snapshot >"$S.before"
	S="$S" guard="$guard" bash -c "$change"
	XDG_STATE_HOME="$S" "$guard" compare "$S.before" >/dev/null || got=fail
	[[ "$got" == "$want" ]] || failed+=("$name: got $got, want $want")
}

check unchanged ok ':'
check grown fail 'printf more >>"$S/archmaid/20260101-0000/cleanup.log"'
check deleted fail 'rm "$S/archmaid/20260101-0000/cleanup.log"'
check added fail 'touch -d "2026-01-01 00:00" "$S/archmaid/new.log"'
check touched fail 'touch "$S/archmaid/last-run.json"'
# The owner's updates-check timer (#312): it replaces updates-cache.json and
# health.json through a temp file and a rename, in the same mode, and
# creates updates-yay.err.* briefly. All of that passes. What doesn't: the
# two files gone, or made where none were, a symlink swap, a mode change, a
# look-alike name, any other file (last-run.json, health-ignore.json, a run
# folder), and a stray write next to a timer rewrite.
timer_rewrite='for f in updates-cache.json health.json; do printf "{\"v\":2,\"more\":1}\n" >"$S/archmaid/$f.tmp.Ab12" && chmod 600 "$S/archmaid/$f.tmp.Ab12" && mv "$S/archmaid/$f.tmp.Ab12" "$S/archmaid/$f"; done'
check timer-rewrites-both ok "$timer_rewrite"
check timer-cache-rewritten ok 'printf "{\"v\":2}\n" >"$S/archmaid/updates-cache.json"'
check timer-health-rewritten ok 'printf "{\"v\":2}\n" >>"$S/archmaid/health.json"'
check timer-temp-files ok 'touch "$S/archmaid/updates-yay.err.AbC123" "$S/archmaid/updates-cache.json.tmp.xYz789" "$S/archmaid/health.json.tmpQ1w2"'
check timer-cache-removed fail 'rm -f "$S/archmaid/updates-cache.json"'
check timer-health-removed fail 'rm -f "$S/archmaid/health.json"'
check health-created fail 'rm -f "$S/archmaid/health.json" && XDG_STATE_HOME="$S" "$guard" snapshot >"$S.before" && printf "{}\n" >"$S/archmaid/health.json" && chmod 600 "$S/archmaid/health.json"'
check health-symlink fail 'rm "$S/archmaid/health.json" && ln -s /etc/hostname "$S/archmaid/health.json"'
check cache-symlink fail 'rm "$S/archmaid/updates-cache.json" && ln -s /etc/hostname "$S/archmaid/updates-cache.json"'
check health-chmod fail 'chmod 644 "$S/archmaid/health.json"'
check cache-chmod fail 'chmod 666 "$S/archmaid/updates-cache.json"'
check health-is-dir fail 'rm "$S/archmaid/health.json" && mkdir "$S/archmaid/health.json"'
check stray-last-run fail 'printf x >>"$S/archmaid/last-run.json"'
check stray-ignore-file fail 'printf "{}\n" >"$S/archmaid/health-ignore.json"'
check stray-run-folder fail 'mkdir "$S/archmaid/20260202-0000" && touch "$S/archmaid/20260202-0000/cleanup.log"'
check stray-dryrun fail 'mkdir "$S/archmaid/dryrun"'
check lookalike-added fail 'touch "$S/archmaid/updates-cache.json.bak" "$S/archmaid/health.json.bak"'
check nested-lookalike fail 'touch "$S/archmaid/20260101-0000/updates-cache.json" "$S/archmaid/20260101-0000/health.json"'
check other-with-timer fail "$timer_rewrite"' && printf more >>"$S/archmaid/last-run.json"'
check absent-unchanged ok ':'
check absent-created fail 'mkdir "$S/archmaid"'

# A PATH without diff: exit 2 and the message, not a confusing diff later.
# The PATH is a dir of symlinks to the other tools the guard needs.
nodiff="$scratch/nodiff-bin"
mkdir -p "$nodiff" "$scratch/nodiff"
for t in bash find sort mkdir cat dirname; do
	ln -s "$(command -v "$t")" "$nodiff/$t"
done
: >"$scratch/nodiff.before"
rc=0
msg="$(PATH="$nodiff" XDG_STATE_HOME="$scratch/nodiff" "$nodiff/bash" "$guard" compare "$scratch/nodiff.before" 2>&1)" || rc=$?
[[ "$rc" == 2 && "$msg" == *"state-guard: diff not found; install diffutils"* ]] ||
	failed+=("no-diff: got exit $rc and '$msg', want exit 2 and the diffutils message")

if ((${#failed[@]})); then
	echo "FAIL: scripts/state-guard.sh got these self-test cases wrong:"
	printf '  %s\n' "${failed[@]}"
	exit 1
fi
echo "ok: state-guard self-test ($ncases cases)"
