#!/usr/bin/env bash
# Guards the owner's real archmaid state dir — run logs, last-run.json, the
# AUR snapshot — against the gate itself. scripts/check.sh and
# cmd/archmaid/e2e/run.sh take a snapshot before they start and compare at the
# end; any change fails them with a diff. (A real run folder was deleted on
# 2026-10-04 by a binary started against the real HOME; see docs/HANDOVER.md.)
#
# Read-only: it only lists the dir. The snapshot is the recursive list of
# paths with type, size and mtime, so a deleted, added, grown or rewritten
# file all show. A missing dir snapshots as "<absent>", so the check is then
# that it still doesn't exist (CI).
#
# Usage:
#   scripts/state-guard.sh snapshot > FILE    # before
#   scripts/state-guard.sh compare FILE       # after: exit 1 and a diff on change
#
# The dir is ${XDG_STATE_HOME:-$HOME/.local/state}/archmaid, from the caller's
# environment: call it before anything points HOME at a scratch dir. Don't run
# archmaid for real while the gate runs, or the guard will (rightly) see it.
set -euo pipefail

dir="${XDG_STATE_HOME:-$HOME/.local/state}/archmaid"

snapshot() {
	echo "# $dir"
	if [[ ! -e "$dir" && ! -L "$dir" ]]; then
		echo "<absent>"
		return
	fi
	# -H: a symlinked state dir is listed, not just the link. /%P path below
	# dir ("/" for dir itself), %y type, %s size, %T@ mtime (ns).
	find -H "$dir" -printf '/%P\t%y\t%s\t%T@\n' 2>/dev/null | LC_ALL=C sort || true
}

case "${1:-}" in
snapshot)
	snapshot
	;;
compare)
	[[ -r "${2:-}" ]] || { echo "state-guard: compare needs a snapshot file" >&2; exit 2; }
	if ! out="$(diff -u --label before --label after "$2" <(snapshot))"; then
		echo "FAIL: the real state dir $dir changed during this run"
		echo "(path<TAB>type<TAB>size<TAB>mtime; - before, + after):"
		echo "$out"
		exit 1
	fi
	echo "ok: real state dir unchanged ($dir)"
	;;
*)
	echo "usage: $0 snapshot | compare FILE" >&2
	exit 2
	;;
esac
