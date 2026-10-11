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

# Fail up front, not later with an empty or confusing diff.
declare -A pkg=([find]=findutils [sort]=coreutils [diff]=diffutils [awk]=gawk)
for tool in find sort diff awk; do
	command -v "$tool" >/dev/null 2>&1 || {
		echo "state-guard: $tool not found; install ${pkg[$tool]}" >&2
		exit 2
	}
done

dir="${XDG_STATE_HOME:-$HOME/.local/state}/archmaid"

snapshot() {
	echo "# $dir"
	if [[ ! -e "$dir" && ! -L "$dir" ]]; then
		echo "<absent>"
		return
	fi
	# -H: a symlinked state dir is listed, not just the link. /%P path below
	# dir ("/" for dir itself), %y type, %s size, %T@ mtime (ns).
	#
	# Not guarded: the owner's updates-check user timer
	# (packaging/systemd/archmaid-updates-check, every ~30 min) rewrites
	# updates-cache.json at the top of the dir via updates-cache.json.tmp.*
	# and briefly creates updates-yay.err.*. A gate run never writes those
	# (the tests of that script use a scratch XDG_STATE_HOME), so exactly
	# these names are skipped, and nothing else. The renames also move the
	# dir's own size and mtime, so those two are blanked on the "/" line;
	# any other file added, removed or changed still shows by its own line.
	find -H "$dir" -printf '/%P\t%y\t%s\t%T@\n' 2>/dev/null |
		awk -F'\t' -v OFS='\t' '
			$1 == "/updates-cache.json" || $1 ~ /^\/updates-cache\.json\.tmp\.[^\/]*$/ || $1 ~ /^\/updates-yay\.err\.[^\/]*$/ { next }
			$1 == "/" { $3 = "-"; $4 = "-" }
			{ print }' |
		LC_ALL=C sort || true
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
