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
# The whole display suite takes ~135 s, past the bound, so the default runs
# it in fifteen parts of at most ~40 s, each with its own display and timeout:
# the prompts, the real Driver and Stop, the Full Cleanup and the shimmed
# password and services scenarios, the engine stopping and the instance
# check with the 630x372 windows, the narrow/toast/tones/Logs/Settings
# windows, the wide dialogs and the breakpoint toggle, the wide dialogs
# with long content, the glass skin (screenshots, ~40 s), the Overview's
# starts, the coverage scenarios (cover_harness_test.go), the first frame
# and throughput proofs (~41 s: Broadway's PNG uploads slow the floods to
# ~6 s each), the rest. The wide dialogs (TestGUIWideDialogs)
# run their 1460x930 window on a screen of 1600x1000 (HEADLESS_WIDTH,
# HEADLESS_HEIGHT; default 1280x800): the screen bounds the window.
# TestGUIMinWidthToggle (~12 s) shares that part (~28 s): with the narrow
# or the states part it would be ~43 or ~47 s. TestGUIWideDialogsLong
# (~25 s, the same screen) is a part of its own: with them it would be ~53 s;
# TestGUIWideDialogsLongResize (~41 s with them) is another.
if [[ $# -eq 0 ]]; then
	long='^TestGUIPrompts$'
	real='^TestGUI(RealDriver|StopAndQuit|RunColour)$'
	svc='^TestGUI(FullCleanup|OnePassword|RealServices)$'
	stops='^TestGUI(EngineStops|StubOwnInstance|Shots|DMSColoursWin|SmallWindow)$'
	narrow='^TestGUI(NarrowNav|ToastPlain|MinWidth|LogsPage|SettingsCards|NavSync)$'
	tones='^TestGUI(LastRunTones|AuditRerun)$'
	states='^TestGUIMinWidthStates$'
	wide='^TestGUI(WideDialogs|MinWidthToggle|GateNotesFit|RunReport)$'
	widelong='^TestGUIWideDialogsLong$'
	widelongr='^TestGUIWideDialogsLongResize$'
	starts='^TestGUI(DryFirst|ConfirmFlag|RepairNothingFound|OverviewStarts|ClearRerun)$'
	cov='^TestGUICov'
	pkgs='^TestGUIPackages$'
	pkgflow='^TestGUIPackagesFlow$'
	glass='^TestGUI(Glass|ColourSchemeLive)'
	perf='^TestGUI(FirstFrameAndIdleRAM|Throughput)$'
	# The screenshot capture (shot.go) is built only with -tags shots:
	# always for the group that tests it, and for every group when
	# ARCHMAID_GUI_SHOTS asks for screenshots.
	tags=()
	[[ -n "${ARCHMAID_GUI_SHOTS:-}" ]] && tags=(-tags shots)
	"$self" go test "${tags[@]}" -count=1 -v -run "$long" ./internal/ui
	"$self" go test "${tags[@]}" -count=1 -v -run "$real" ./internal/ui
	"$self" go test "${tags[@]}" -count=1 -v -run "$svc" ./internal/ui
	"$self" go test -tags shots -count=1 -v -run "$stops" ./internal/ui
	"$self" go test -tags shots -count=1 -v -run "$narrow" ./internal/ui
	"$self" go test -tags shots -count=1 -v -run "$tones" ./internal/ui
	"$self" go test -tags shots -count=1 -v -run "$states" ./internal/ui
	HEADLESS_WIDTH=1600 HEADLESS_HEIGHT=1000 "$self" go test "${tags[@]}" -count=1 -v -run "$wide" ./internal/ui
	HEADLESS_WIDTH=1600 HEADLESS_HEIGHT=1000 "$self" go test "${tags[@]}" -count=1 -v -run "$widelong" ./internal/ui
	HEADLESS_WIDTH=1600 HEADLESS_HEIGHT=1000 "$self" go test "${tags[@]}" -count=1 -v -run "$widelongr" ./internal/ui
	"$self" go test "${tags[@]}" -count=1 -v -run "$starts" ./internal/ui
	"$self" go test "${tags[@]}" -count=1 -v -run "$cov" ./internal/ui
	"$self" go test "${tags[@]}" -count=1 -v -run "$pkgs" ./internal/ui
	"$self" go test "${tags[@]}" -count=1 -v -run "$pkgflow" ./internal/ui
	"$self" go test -tags shots -count=1 -v -run "$glass" ./internal/ui
	"$self" go test "${tags[@]}" -count=1 -v -run "$perf" ./internal/ui
	exec "$self" go test "${tags[@]}" -count=1 -v -skip "$long|$real|$svc|$stops|$narrow|$tones|$states|$wide|$widelong|$widelongr|$starts|$cov|$pkgs|$pkgflow|$glass|$perf" ./internal/ui
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
