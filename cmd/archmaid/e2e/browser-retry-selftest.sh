#!/usr/bin/env bash
# Self-test of browser-retry.sh, run by run.sh before the tapes: a stub vhs
# first on a scratch PATH plays each case, and the case checks the exit
# status and how often vhs ran. Nothing real runs: the stub only counts its
# calls and prints. Exit 0 if every case is right, 1 with the case names
# otherwise.
set -euo pipefail

retry="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/browser-retry.sh"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
failed=()

BROWSER='could not start browser: browser exited unexpectedly before its debugging endpoint was ready'

# check NAME PLAN WANT_RC WANT_CALLS: the stub's Nth call does the PLAN's
# Nth word (ok, browser: the CI failure, tape: an ordinary failure, the
# message on stdout as VHS prints it); browser-retry must exit WANT_RC
# after WANT_CALLS calls.
check() {
	local name="$1" plan="$2" want_rc="$3" want_calls="$4" dir="$scratch/$1"
	mkdir -p "$dir/bin"
	cat >"$dir/bin/vhs" <<STUB
#!/usr/bin/env bash
echo x >>"$dir/calls"
n=\$(wc -l <"$dir/calls")
plan=($plan)
case "\${plan[n-1]:-ok}" in
ok) echo "File: \$1"; exit 0 ;;
browser) echo "recording failed"; echo "$BROWSER"; exit 1 ;;
tape) echo "recording failed"; echo "Error: invalid command on line 3" >&2; exit 1 ;;
esac
STUB
	chmod +x "$dir/bin/vhs"
	local rc=0
	PATH="$dir/bin:$PATH" "$retry" vhs "e2e/$name.tape" >"$dir/out" 2>&1 || rc=$?
	local calls
	calls=$(wc -l <"$dir/calls")
	if [[ $rc != "$want_rc" || $calls != "$want_calls" ]]; then
		echo "browser-retry self-test: $name: exit $rc after $calls vhs run(s), want $want_rc after $want_calls"
		sed 's/^/  | /' "$dir/out"
		failed+=("$name")
	fi
}

check ok "ok" 0 1                        # a passing tape runs once
check browser-then-ok "browser ok" 0 2   # the browser failure: retried once, then passes
check browser-twice "browser browser" 1 2 # ... and only once
check tape-fails "tape ok" 1 1           # an ordinary failure fails at once
check browser-then-tape "browser tape" 1 2

# The retry says why it ran again, and both runs' output is shown.
grep -q "browser-retry: VHS's browser didn't start; running e2e/browser-then-ok.tape once more" "$scratch/browser-then-ok/out" ||
	failed+=("retry-notice")
[[ $(grep -c 'recording failed' "$scratch/browser-twice/out") == 2 ]] || failed+=("both-outputs")
# An ordinary failure's stderr still reaches the log.
grep -q 'invalid command on line 3' "$scratch/tape-fails/out" || failed+=("stderr-shown")

if ((${#failed[@]})); then
	echo "FAIL: browser-retry self-test: ${failed[*]}"
	exit 1
fi
echo "ok: browser-retry self-test (retries VHS's browser failure once, nothing else)"
