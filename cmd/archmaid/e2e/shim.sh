#!/usr/bin/env bash
# Stand-in for every external command archmaid can run. run.sh symlinks each
# command name (sudo, pacman, systemctl, ...) to this one script and puts that
# directory first on PATH. It never executes anything real: it appends its argv
# to $SHIM_LOG and prints canned output. Behaviour is chosen by the invoked
# name ($0) and, where a tape needs it, by SHIM_* variables run.sh exports.
set -u
name="$(basename "$0")"
# One write per line: archmaid runs several shims at once (the async Driver),
# and under O_APPEND a single short write is atomic, while three separate
# appends interleave ("pacmanpacman -Qkq ...") and break the log assertions.
line="$name"
for a in "$@"; do line+=" $a"; done
printf '%s\n' "$line" >>"${SHIM_LOG:?SHIM_LOG unset}"

case "$name" in
systemctl)
	case "$1" in
	list-unit-files) printf 'fake-alpha.service enabled disabled\nfake-beta.service  enabled disabled\n' ;;
	list-units) printf 'fake-alpha.service loaded inactive dead Fake Alpha\nfake-beta.service  loaded inactive dead Fake Beta\n' ;;
	is-enabled) shift; [[ "${1:-}" == -- ]] && shift; for _ in "$@"; do echo enabled; done ;;
	esac
	;;
sudo)
	# Only the root recheck of Repair's candidates is canned (run.sh allows
	# exactly `sudo -n -v` and `sudo -n pacman -Qkq`; any other sudo fails the tape).
	case "$*" in
	"-n pacman -Qkq -- "*) [[ -n "${SHIM_BROKEN:-}" ]] && printf '%s /usr/share/fake/missing-file\n' "$SHIM_BROKEN" ;;
	esac
	;;
pacman)
	case "$*" in
	"-Qq") printf 'fine-pkg\n%s\n' "${SHIM_BROKEN:-other-pkg}" ;;
	"-Qkq -- "*) [[ -n "${SHIM_BROKEN:-}" ]] && printf '%s /usr/share/fake/missing-file\n' "$SHIM_BROKEN" ;;
	"-Qdtq") printf 'orphan-one\norphan-two\n' ;;
	"-Rs --print -- "*) printf 'orphan-one-1.0\norphan-two-1.0\norphan-dep-1.0\n' ;;
	"-Qqm")
		# The audit step is the one in-process step that really touches a
		# command under --dryrun; SHIM_BLOCK_QQM holds it open for the abort tape.
		[[ -n "${SHIM_BLOCK_QQM:-}" ]] && exec sleep "$SHIM_BLOCK_QQM"
		;; # empty: no foreign packages, so the audit never reaches the network
	esac
	;;
esac
exit 0
