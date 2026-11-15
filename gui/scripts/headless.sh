#!/usr/bin/env bash
# Runs a command (default: the GUI's display tests) on a private, headless
# display: GTK's Broadway backend against scripts/nullbroadway, a display
# server that shows nothing. No window ever appears on the desktop, and no
# compositor or other package beyond gtk4 is needed:
#
#   gui/scripts/headless.sh                            # go test -v ./internal/ui
#   gui/scripts/headless.sh go test -v -run Throughput ./internal/ui
#   ARCHMAID_GUI_PERF=1 gui/scripts/headless.sh        # also enforce the budgets
#   ARCHMAID_GUI_SHOTS=$(mktemp -d) gui/scripts/headless.sh   # PNGs of every scenario
#
# The server and the command are bounded by `timeout` (HEADLESS_TIMEOUT
# seconds, default 60), so nothing outlives a run on the owner's machine; CI
# raises it for its slower runner.
#
# nullbroadway listens on one Unix socket in a fresh 0700 runtime dir and
# nothing else (no web server: gtk4-broadwayd's is what it replaces, see its
# doc comment for why); the script checks that it holds no IP socket before
# anything runs. Its screen is HEADLESS_WIDTH x HEADLESS_HEIGHT (default
# 1280x800), which bounds a window as an output does. GDK finds the display
# by number only (BROADWAY_DISPLAY=:0 is $XDG_RUNTIME_DIR/broadway1.socket);
# the tests' testenv moves XDG_RUNTIME_DIR to a scratch dir, and the ui
# package links the socket into it (display_test.go). The script sets
# GDK_BACKEND=broadway, drops WAYLAND_DISPLAY and DISPLAY (no fallback onto
# a live session), runs the command on a private session bus
# (dbus-run-session, so nothing reaches the desktop's bus) and sets
# ARCHMAID_GUI_DISPLAY_TESTS=1, which the display tests require. The
# renderer is the caller's GSK_RENDERER, else archmaid-gui's cairo. Run it
# from anywhere: it cds to the GUI module.
set -euo pipefail
self="$(realpath "${BASH_SOURCE[0]}")"
cd "$(dirname "$self")/.."

limit="${HEADLESS_TIMEOUT:-60}"
if [[ $# -eq 0 ]]; then
	# The default run: the display suite in parts, HEADLESS_JOBS (default 4,
	# at most 8) at a time, each part its own display, session bus and
	# timeout (the parts' runtime dirs, sockets and testenv markers are
	# their own, so none sees another's processes). The test binary is built
	# once per tag set, outside the timeouts, and the parts run it, so no part
	# pays for a build or a link. A part's output is printed whole when it
	# ends, in the order below, then a table of every part's time. HEADLESS_JOBS=1
	# runs them one after another (the old, slower, order).
	#
	# A part is "name|width|height|tags|regex": the screen (the wide dialogs'
	# 1460x930 window needs 1600x1000: the screen bounds the window), tags
	# "s" (built with -tags shots: the screenshot capture, shot.go) or "t"
	# (the same only when ARCHMAID_GUI_SHOTS asks for screenshots), and the
	# -run regex. The last part, "rest", is everything the others skip.
	# Where a part's time is (docs/TESTING.md has the table): they are sized to
	# end together, ~25-45 s alone each, the longest first.
	parts=(
		'health|1600|1000|s|^TestGUIHealth$'
		'wide|1600|1000|t|^TestGUI(WideDialogs|MinWidthToggle|GateNotesFit|RunReport)$'
		'glass|1280|800|s|^TestGUIGlass$'
		'pkgs|1280|800|t|^TestGUIPackages$'
		'minheight|1280|800|s|^TestGUIMinHeight$'
		'pkgflow|1280|800|t|^TestGUIPackages(Flow|TabsAndStatus)$'
		'align|2000|1000|s|^TestGUI(OverviewAlign|OverviewHero|LogsAlign)$'
		'perf|1280|800|t|^TestGUI(FirstFrameAndIdleRAM|Throughput)$'
		'stops|1280|800|s|^TestGUI(EngineStops|StubOwnInstance|Shots|DMSColoursWin|SmallWindow)$'
		'glass2|1280|800|s|^TestGUI(GlassShots|GlassLayout|ColourSchemeLive|SettingsScroll)$'
		'real|1280|800|t|^TestGUI(RealDriver|StopAndQuit|RunColour)$'
		'states|1280|800|s|^TestGUIMinWidthStates$'
		'narrow|1280|800|s|^TestGUI(NarrowNav|ToastPlain|MinWidth|LogsPage|SettingsCards|NavSync)$'
		'tones|1280|800|s|^TestGUI(LastRunTones|AuditRerun|AgeTick)$'
		'prompts|1280|800|t|^TestGUIPrompts$'
		'starts|1280|800|t|^TestGUI(DryFirst|ConfirmFlag|RepairNothingFound|OverviewStarts|ClearRerun)$'
		'svc|1280|800|t|^TestGUI(FullCleanup|OnePassword|RealServices)$'
		'cov-2|1280|800|t|^TestGUICov[N-Z]'
		'held|1600|1000|s|^TestGUIHeld$'
		'widelong|1600|1000|t|^TestGUIWideDialogsLong$'
		'cov-1|1280|800|t|^TestGUICov[A-M]'
		'widelongr|1600|1000|t|^TestGUIWideDialogsLongResize$'
		'healthign|1600|1000|s|^TestGUIHealthIgnore$'
	)
	jobs="${HEADLESS_JOBS:-4}"
	[[ "$jobs" =~ ^[1-8]$ ]] || {
		echo "HEADLESS_JOBS must be 1..8, not $jobs" >&2
		exit 2
	}
	# Several parts at once run slower than one alone: a part's own cap is
	# generous unless the caller sets one (the run's own cap is the make
	# target's `timeout`).
	export HEADLESS_TIMEOUT="${HEADLESS_TIMEOUT:-180}"
	# The perf part measures the machine; with the budgets on it runs alone.
	alone=""
	[[ -n "${ARCHMAID_GUI_PERF:-}" ]] && alone=perf
	skip=""
	for p in "${parts[@]}"; do
		IFS='|' read -r _ _ _ _ re <<<"$p"
		skip+="${skip:+|}$re"
	done
	parts+=("rest|1280|800|t|")
	out="$(mktemp -d)"
	bins="$out/bin"
	mkdir -p "$bins"
	pids=()
	cleanup() {
		local pid
		for pid in "${pids[@]}"; do kill "$pid" 2>/dev/null || true; done
		rm -rf "$out"
	}
	trap cleanup EXIT
	trap 'exit 130' INT TERM
	# One test binary per tag set, built outside the timeouts. It runs from
	# the package's directory, as go test would run it.
	shotsonly=()
	[[ -n "${ARCHMAID_GUI_SHOTS:-}" ]] && shotsonly=(-tags shots)
	built=$SECONDS
	go test -c "${shotsonly[@]}" -o "$bins/ui-t" ./internal/ui
	go test -c -tags shots -o "$bins/ui-s" ./internal/ui
	echo "test binaries built in $((SECONDS - built)) s" >&2
	run_part() { # run_part name width height tags regex
		local name="$1" wd="$2" ht="$3" tg="$4" re="$5" bin="$bins/ui-$4" start=$SECONDS rc=0 sel
		if [[ -n "$re" ]]; then
			sel=(-test.run "$re")
		else
			sel=(-test.skip "$skip")
		fi
		HEADLESS_WIDTH="$wd" HEADLESS_HEIGHT="$ht" "$self" bash -c 'cd internal/ui && exec "$@"' _ "$bin" -test.count=1 -test.v "${sel[@]}" >"$out/$name.log" 2>&1 || rc=$?
		echo "$rc" >"$out/$name.rc"
		echo "$((SECONDS - start))" >"$out/$name.secs"
		echo "part $name: $((SECONDS - start)) s, $([[ $rc -eq 0 ]] && echo ok || echo "FAIL ($rc)")" >&2
	}
	started=$SECONDS
	running=0
	order=()
	for p in "${parts[@]}"; do
		IFS='|' read -r name wd ht tg re <<<"$p"
		[[ "$name" == "$alone" ]] && continue
		order+=("$name")
		while ((running >= jobs)); do
			wait -n || true
			running=$((running - 1))
		done
		run_part "$name" "$wd" "$ht" "$tg" "$re" &
		pids+=($!)
		running=$((running + 1))
	done
	wait || true
	if [[ -n "$alone" ]]; then
		for p in "${parts[@]}"; do
			IFS='|' read -r name wd ht tg re <<<"$p"
			[[ "$name" == "$alone" ]] || continue
			order+=("$name")
			run_part "$name" "$wd" "$ht" "$tg" "$re"
		done
	fi
	bad=0
	for name in "${order[@]}"; do
		echo "=== part $name"
		cat "$out/$name.log"
	done
	echo
	printf '%-10s %6s  %s\n' part secs result
	for name in "${order[@]}"; do
		rc="$(cat "$out/$name.rc" 2>/dev/null || echo 1)"
		[[ "$rc" -eq 0 ]] || bad=1
		printf '%-10s %6s  %s\n' "$name" "$(cat "$out/$name.secs" 2>/dev/null || echo ?)" "$([[ "$rc" -eq 0 ]] && echo ok || echo "FAIL ($rc)")"
	done
	echo "all parts: $((SECONDS - started)) s wall, $jobs at a time"
	exit "$bad"
fi



# A private runtime dir for the display's socket (CI containers have none,
# and nothing here should land in the desktop's /run/user/$UID).
XDG_RUNTIME_DIR="$(mktemp -d)"
chmod 700 "$XDG_RUNTIME_DIR"
export XDG_RUNTIME_DIR

display=:0 # GDK's socket for it: $XDG_RUNTIME_DIR/broadway1.socket
sock="$XDG_RUNTIME_DIR/broadway1.socket"
log="$XDG_RUNTIME_DIR/nullbroadway.log"
server=""
# testenv.Run records its scratch dirs here; a leftover fails the run
# (scripts/testenv-leftovers.sh).
created="$XDG_RUNTIME_DIR.created"
export ARCHMAID_TESTENV_CREATED="$created"
finish() {
	local rc=$?
	# A failed kill or wait (the server already gone, or ended by the
	# signal) must not end the cleanup half-way under errexit.
	set +e
	if [[ -n "$server" ]]; then
		kill "$server" 2>/dev/null
		wait "$server" 2>/dev/null
	fi
	if [[ "$rc" -ne 0 && -s "$log" ]]; then
		echo "nullbroadway:" >&2
		cat "$log" >&2
	fi
	rm -rf "$XDG_RUNTIME_DIR"
	../scripts/testenv-leftovers.sh "$created" || rc=1
	rm -f "$created"
	exit "$rc"
}
trap finish EXIT

# The display server, built outside the timeouts (cached after the first),
# or HEADLESS_SERVER, one built already (nullbroadway's own tests).
bin="${HEADLESS_SERVER:-$XDG_RUNTIME_DIR/nullbroadway}"
if [[ -z "${HEADLESS_SERVER:-}" ]]; then
	go build -o "$bin" ./scripts/nullbroadway
fi
timeout -k 5 "$limit" "$bin" -socket "$sock" \
	-width "${HEADLESS_WIDTH:-1280}" -height "${HEADLESS_HEIGHT:-800}" 2>"$log" &
server=$!
for _ in $(seq 100); do
	[[ -S "$sock" ]] && break
	if ! kill -0 "$server" 2>/dev/null; then
		echo "nullbroadway failed to start:" >&2
		cat "$log" >&2
		exit 1
	fi
	sleep 0.05
done
if [[ ! -S "$sock" ]]; then
	echo "nullbroadway's socket never appeared" >&2
	cat "$log" >&2
	exit 1
fi

# The display must not be reachable from the network: nullbroadway has no
# network code, and this proves it from its file descriptors: none may be
# an IP socket (in /proc/net/tcp, tcp6, udp or udp6, any state).
pid="$(cat "/proc/$server/task/$server/children")"
pid="${pid%% *}"
if [[ -z "$pid" ]]; then
	echo "nullbroadway: no process under its timeout" >&2
	exit 1
fi
for fd in "/proc/$pid/fd/"*; do
	link="$(readlink "$fd" || true)"
	[[ "$link" == "socket:["*"]" ]] || continue
	inode="${link#socket:[}"
	inode="${inode%]}"
	if awk -v ino="$inode" '$10 == ino { found = 1 } END { exit !found }' /proc/net/tcp /proc/net/tcp6 /proc/net/udp /proc/net/udp6; then
		echo "nullbroadway holds an IP socket (inode $inode): refusing to run" >&2
		exit 1
	fi
done

# The private bus activates nothing: no service dirs, so no portal, no
# at-spi and no gvfs start for the test (they would mount FUSE dirs and
# reach for the desktop). GTK and libadwaita fall back to their defaults.
cat >"$XDG_RUNTIME_DIR/bus.conf" <<CONF
<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>unix:dir=$XDG_RUNTIME_DIR</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow send_destination="*" eavesdrop="true"/>
    <allow eavesdrop="true"/>
    <allow own="*"/>
  </policy>
</busconfig>
CONF

export BROADWAY_DISPLAY="$display" GDK_BACKEND=broadway ARCHMAID_GUI_DISPLAY_TESTS=1
export NO_AT_BRIDGE=1 GTK_A11Y=none ADW_DISABLE_PORTAL=1 GDK_DEBUG=no-portals
unset DISPLAY WAYLAND_DISPLAY DBUS_SESSION_BUS_ADDRESS HYPRLAND_INSTANCE_SIGNATURE
timeout -k 5 "$limit" dbus-run-session --config-file="$XDG_RUNTIME_DIR/bus.conf" -- "$@"
