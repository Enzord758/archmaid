#!/usr/bin/env bash
# Fails when a scratch dir testenv.Run made is still there. Every Run appends
# the dirs it creates (archmaid-test-home*, archmaid-test-trap*) to the file
# named by ARCHMAID_TESTENV_CREATED; callers (scripts/check.sh,
# gui/scripts/headless.sh) point that at a scratch file, run their tests, and
# call this with the file. It names each leftover, removes it (only if named
# archmaid-test-*) and exits 1. An unset or missing file means nothing ran.
#
#   scripts/testenv-leftovers.sh FILE
set -euo pipefail
file="${1:?usage: testenv-leftovers.sh FILE}"
[[ -f "$file" ]] || exit 0
bad=0
while IFS= read -r dir; do
	[[ -n "$dir" ]] || continue
	if [[ -e "$dir" ]]; then
		echo "FAIL: testenv left $dir behind"
		[[ "${dir##*/}" == archmaid-test-?* ]] && rm -rf -- "$dir"
		bad=1
	fi
done <"$file"
exit "$bad"
