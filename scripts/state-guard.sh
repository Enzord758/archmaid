#!/usr/bin/env bash
# Guards the owner's real archmaid state dir — run logs, last-run.json, the
# AUR snapshot — against the gate itself. scripts/check.sh and
# cmd/archmaid/e2e/run.sh take a snapshot before they start and compare at the
# end; any change fails them with a diff. (A real run folder was deleted on
# 2026-10-04 by a binary started against the real HOME; see docs/HANDOVER.md.)
#
# Read-only: it only lists the dir. The snapshot is the recursive list of
# paths with type, size, mtime, mode and owner, so a deleted, added, grown,
# rewritten, chmod-ed or swapped-for-a-symlink file all show. A missing dir
# snapshots as "<absent>", so the check is then that it still doesn't exist
# (CI).
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
declare -A pkg=([find]=findutils [sort]=coreutils [diff]=diffutils [awk]=gawk [mktemp]=coreutils)
for tool in find sort diff awk mktemp; do
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
	# dir ("/" for dir itself), %y type, %s size, %T@ mtime (ns), %m mode,
	# %u owner.
	#
	# The owner's installed archmaid writes two files here on its own: the
	# updates-check user timer (packaging/systemd/archmaid-updates-check,
	# every ~30 min) rewrites updates-cache.json and, through `archmaid
	# --health-report`, health.json, each via a temp file and a rename
	# (updates-cache.json.tmp.*, health.json.tmp*, and briefly
	# updates-yay.err.*). A gate run never writes them (the tests use a
	# scratch XDG_STATE_HOME), so the guard must not call that a leak (#312).
	# Those two names are guarded for their form only: still present (or
	# still absent), still a regular file (a symlink swap shows as type l),
	# the same mode and owner. Their size and mtime are blanked, the only
	# way a timer rewrite differs. The temp names are skipped. Anything else
	# added, removed or changed shows by its own line (last-run.json,
	# health-ignore.json, a run folder...). The renames also move the dir's
	# own size and mtime, so those are blanked on the "/" line.
	find -H "$dir" -printf '/%P\t%y\t%s\t%T@\t%m\t%u\n' 2>/dev/null |
		awk -F'\t' -v OFS='\t' '
			$1 ~ /^\/updates-cache\.json\.tmp\.[^\/]*$/ || $1 ~ /^\/health\.json\.tmp[^\/]*$/ || $1 ~ /^\/updates-yay\.err\.[^\/]*$/ { next }
			$1 == "/updates-cache.json" || $1 == "/health.json" || $1 == "/" { $3 = "-"; $4 = "-" }
			{ print }' |
		LC_ALL=C sort || true
}

# explain BEFORE AFTER: one line per differing path, with both mtimes read
# as dates, so a real hit is easy to see (the raw diff follows it).
explain() {
	awk -F'\t' '
		function t(x) { return (x == "-" || x == "") ? "-" : strftime("%F %T", int(x)) }
		FNR == NR { if (!/^#/ && !/^<absent>/) { b[$1] = $2 " " $3 " " $5 " " $6; bm[$1] = $4 } next }
		/^#/ || /^<absent>/ { next }
		{ a[$1] = $2 " " $3 " " $5 " " $6; am[$1] = $4 }
		END {
			for (p in b) if (!(p in a)) print "  deleted: " p "  (was " b[p] ", mtime " t(bm[p]) ")"
			for (p in a) if (!(p in b)) print "  added:   " p "  (" a[p] ", mtime " t(am[p]) ")"
			for (p in a) if ((p in b) && (a[p] != b[p] || am[p] != bm[p]))
				print "  changed: " p "  (" b[p] ", mtime " t(bm[p]) " -> " a[p] ", mtime " t(am[p]) ")"
		}' "$1" "$2" | LC_ALL=C sort
}

case "${1:-}" in
snapshot)
	snapshot
	;;
compare)
	[[ -r "${2:-}" ]] || { echo "state-guard: compare needs a snapshot file" >&2; exit 2; }
	now="$(mktemp)"
	trap 'rm -f "$now"' EXIT
	snapshot >"$now"
	if ! out="$(diff -u --label before --label after "$2" "$now")"; then
		echo "FAIL: the real state dir $dir changed during this run"
		echo "what changed (the timer's own rewrite of updates-cache.json / health.json is not counted):"
		explain "$2" "$now"
		echo "(path<TAB>type<TAB>size<TAB>mtime<TAB>mode<TAB>owner; - before, + after):"
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
