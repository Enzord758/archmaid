// Package agenttest is a pkttyagent stand-in for tests (Shim): the real
// pkttyagent registers with polkitd and is trapped by testenv, so tests
// never run it.
package agenttest

// Shim is a pkttyagent stand-in that speaks polkit 127's text listener on
// its controlling terminal (polkitagenttextlistener.c), byte for byte
// where it matters: the coloured banner, the message, "Authenticating as:"
// or the identity menu, the PAM prompt with echo turned off, and the
// verdict. It registers by closing fd 3 (--notify-fd), then serves each
// request written to $AGENT_DIR/request (the action id), as polkitd would
// call it when pkexec asks. It records its argv (agent.args), what it was
// answered (agent.got) and polkit's verdict (result: ok or fail; right
// when the answer is $AGENT_WANT). AGENT_MODE=noreg fails to register as
// pkttyagent does; AGENT_MODE=snoop has another process (its pid in
// snoop.pid) open the terminal by name and hold it before registering, as
// a same-uid snooper could; AGENT_IDENTS=multi offers root and the user,
// other authenticates as someone else; AGENT_MODE=twoprompts asks a
// second hidden question after the password; AGENT_MODE=slowstop ends
// only 0.6 s after SIGTERM, as a stopped agent may on a loaded machine.
// An end of file at a prompt, or an invalid identity choice, cancels the
// request (result: cancel), as the listener does; and, as there, an end
// of file is sticky: every read after it fails at once (rd). Like the
// real text listener
// (polkitagenttextlistener.c, initable_init) it opens its terminal once,
// before it registers (fd 4), and only uses that fd from then on.
const Shim = `#!/bin/sh
d=${AGENT_DIR:?}
printf '%s\n' "$*" >> "$d/agent.args"
if [ "${AGENT_MODE:-}" = noreg ]; then
	echo "Error registering authentication agent: GDBus.Error:org.freedesktop.PolicyKit1.Error.Failed: nope (polkit-error-quark, 0)" >&2
	exit 126
fi
exec 4<>/dev/tty
if [ "${AGENT_MODE:-}" = snoop ]; then
	setsid sleep 30 <"$(tty)" >/dev/null 2>&1 3>&- 4>&- &
	echo $! > "$d/snoop.pid"
fi
exec 3>&-
echo $$ > "$d/agent.pid"
# rd reads a line from the terminal as the listener's getc does on its one
# stdio stream: an end of file is sticky (glibc's EOF flag, never cleared),
# so every read after one fails too.
# SIGTERM (Agent.Stop) ends it as it ends pkttyagent, but only once the
# loop's sleep has exited, so no sleep outlives it; and it ends by itself
# once the process that started it is gone. On a loaded CPU bash 5.3
# sometimes takes a SIGTERM that comes right after an end-of-file cancel
# (no longer pending, caught) without ever running the trap, and only
# Stop's SIGKILL a second later ends it: nothing may depend on how soon a
# stopped agent is gone.
p=$PPID
if [ "${AGENT_MODE:-}" = slowstop ]; then
	trap 'sleep 0.6; exit 0' TERM
else
	trap 'exit 0' TERM
fi
eof=
rd() {
	[ -z "$eof" ] || return 1
	IFS= read -r "$1" <&4 || { eof=1; return 1; }
}
while :; do
	if [ -f "$d/request" ]; then
		action=$(cat "$d/request"); rm -f "$d/request"
		printf '\033[1;31m==== AUTHENTICATING FOR %s ====\n\033[0m' "$action" >&4
		printf 'Authentication is needed to run archmaid'"'"'s root helper.\n' >&4
		case "${AGENT_IDENTS:-}" in
		multi)
			printf 'Multiple identities can be used for authentication:\n 1.  root\n 2.  Me Myself (%s)\n' "$AGENT_USER" >&4
			printf 'Choose identity to authenticate as (1-2): ' >&4
			if ! rd n; then
				printf '\033[1;31m==== AUTHENTICATION CANCELED ====\n\033[0m' >&4
				echo cancel > "$d/result"
				continue
			fi
			printf '%s\n' "$n" > "$d/chosen"
			case "$n" in 1|2) ;; *)
				printf 'Invalid response ` + "`" + `%s'"'"'.\n' "$n" >&4
				printf '\033[1;31m==== AUTHENTICATION CANCELED ====\n\033[0m' >&4
				echo cancel > "$d/result"
				continue ;;
			esac
			;;
		other) printf 'Authenticating as: Someone Else (other)\n' >&4 ;;
		*) printf 'Authenticating as: Me Myself (%s)\n' "$AGENT_USER" >&4 ;;
		esac
		printf 'Password: ' >&4
		stty -echo <&4
		if [ -f "$d/cancel-next" ]; then
			# polkitd cancels the request (pkexec went away, say) before
			# it is answered.
			rm -f "$d/cancel-next"
			sleep 0.2
			stty echo <&4
			printf '\nCancelled\n' >&4
			printf '\033[1;31m==== AUTHENTICATION FAILED ====\n\033[0m' >&4
			echo cancel > "$d/result"
			continue
		fi
		if ! rd pw; then
			# End of file: the listener cancels the session.
			stty echo <&4
			printf '\n' >&4
			echo cancel > "$d/result"
			printf '\033[1;31m==== AUTHENTICATION FAILED ====\n\033[0m' >&4
			continue
		fi
		stty echo <&4
		printf '\n' >&4
		printf '%s\n' "$pw" >> "$d/agent.got"
		if [ "${AGENT_MODE:-}" = twoprompts ]; then
			# PAM asks for more than the password (a second factor, say).
			printf 'Verification code: ' >&4
			stty -echo <&4
			if ! rd code; then
				stty echo <&4
				printf '\n' >&4
				echo cancel > "$d/result"
				printf '\033[1;31m==== AUTHENTICATION FAILED ====\n\033[0m' >&4
				continue
			fi
			stty echo <&4
			printf '%s\n' "$code" >> "$d/agent.got"
		fi
		if [ "$pw" = "$AGENT_WANT" ]; then
			echo ok > "$d/result"
			printf '\033[1;31m==== AUTHENTICATION COMPLETE ====\n\033[0m' >&4
		else
			echo fail > "$d/result"
			printf '\033[1;31m==== AUTHENTICATION FAILED ====\n\033[0m' >&4
		fi
	fi
	sleep 0.02
	kill -0 "$p" 2>/dev/null || exit 0
done
`
