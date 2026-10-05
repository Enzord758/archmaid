#!/usr/bin/env bash
# browser-retry.sh CMD [ARG...]: runs CMD (run.sh's `env ... vhs TAPE`) and,
# only when it fails because VHS's headless browser never started, runs it
# once more. Any other failure, and a second browser failure, is CMD's exit
# status at once.
#
# The browser failure is the runner's, not the tape's: on 2783217 CI's e2e
# job failed with "could not start browser: browser exited unexpectedly
# before its debugging endpoint was ready" before the tape typed anything,
# so nothing ran and the retry starts from the same untouched HOME.
#
# CMD's stdout and stderr both go to stdout, as they arrive.
set -uo pipefail

BROWSER_FAILED='could not start browser'

out="$(mktemp)"
trap 'rm -f "$out"' EXIT

for attempt in 1 2; do
	"$@" 2>&1 | tee "$out"
	rc=${PIPESTATUS[0]}
	if ((rc == 0)) || ((attempt == 2)) || ! grep -qF -- "$BROWSER_FAILED" "$out"; then
		exit "$rc"
	fi
	echo "browser-retry: VHS's browser didn't start; running ${*: -1} once more"
done
