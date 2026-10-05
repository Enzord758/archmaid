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
// other authenticates as someone else. Like the real text listener
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
while :; do
	if [ -f "$d/request" ]; then
		action=$(cat "$d/request"); rm -f "$d/request"
		printf '\033[1;31m==== AUTHENTICATING FOR %s ====\n\033[0m' "$action" >&4
		printf 'Authentication is needed to run archmaid'"'"'s root helper.\n' >&4
		case "${AGENT_IDENTS:-}" in
		multi)
			printf 'Multiple identities can be used for authentication:\n 1.  root\n 2.  Me Myself (%s)\n' "$AGENT_USER" >&4
			printf 'Choose identity to authenticate as (1-2): ' >&4
			IFS= read -r n <&4
			printf '%s\n' "$n" > "$d/chosen"
			;;
		other) printf 'Authenticating as: Someone Else (other)\n' >&4 ;;
		*) printf 'Authenticating as: Me Myself (%s)\n' "$AGENT_USER" >&4 ;;
		esac
		printf 'Password: ' >&4
		stty -echo <&4
		IFS= read -r pw <&4
		stty echo <&4
		printf '\n' >&4
		printf '%s\n' "$pw" >> "$d/agent.got"
		if [ "$pw" = "$AGENT_WANT" ]; then
			echo ok > "$d/result"
			printf '\033[1;31m==== AUTHENTICATION COMPLETE ====\n\033[0m' >&4
		else
			echo fail > "$d/result"
			printf '\033[1;31m==== AUTHENTICATION FAILED ====\n\033[0m' >&4
		fi
	fi
	sleep 0.02
done
`
