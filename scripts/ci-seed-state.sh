#!/usr/bin/env bash
# CI only: fills the runner's real state dir
# (${XDG_STATE_HOME:-$HOME/.local/state}/archmaid) with a fake but realistic
# history (run folders with logs, last-run.json, the AUR snapshot) before the
# gate or the e2e run, so scripts/state-guard.sh guards real content in CI
# instead of "<absent>". With --verify it checks the seed is still all there.
#
# NEVER run this on a developer machine. It refuses unless GITHUB_ACTIONS is
# "true", and it refuses to seed a state dir that already exists, so it can
# only ever create a fresh one, never add to (or overwrite) a real history.
#
# Usage (in .github/workflows/ci.yml):
#   scripts/ci-seed-state.sh            # before make
#   scripts/ci-seed-state.sh --verify   # after make
set -euo pipefail

dir="${XDG_STATE_HOME:-$HOME/.local/state}/archmaid"
runs=(20260101-0900 20260102-0900 20260103-0900 20260104-0900 20260105-0900 20260106-0900)
files=(last-run.json aur-maintainers.json)
for r in "${runs[@]}"; do files+=("$r/topgrade.log" "$r/cleanup.log"); done

if [[ "${GITHUB_ACTIONS:-}" != true ]]; then
	echo "ci-seed-state: refusing: CI only (GITHUB_ACTIONS is not true)" >&2
	exit 2
fi

if [[ "${1:-}" == --verify ]]; then
	for f in "${files[@]}"; do
		[[ -s "$dir/$f" ]] || { echo "FAIL: seeded $dir/$f is gone or empty" >&2; exit 1; }
	done
	echo "ok: seeded state dir intact ($dir, ${#files[@]} files)"
	exit 0
fi

if [[ -e "$dir" || -L "$dir" ]]; then
	echo "ci-seed-state: refusing: $dir already exists" >&2
	exit 2
fi

# One more run folder than logstore.KeepRuns (5), as on a real machine that
# has been used for a while: a stray prune would show as a deletion.
for r in "${runs[@]}"; do
	mkdir -p "$dir/$r"
	printf '[09:00:00] seeded topgrade log for %s\n' "$r" >"$dir/$r/topgrade.log"
	printf '[09:00:01] seeded cleanup log for %s\n' "$r" >"$dir/$r/cleanup.log"
done
printf '{"seeded":true}\n' >"$dir/last-run.json"
printf '{"seeded":true}\n' >"$dir/aur-maintainers.json"
chmod -R go-rwx "$dir"
echo "seeded $dir:"
find "$dir" | LC_ALL=C sort
