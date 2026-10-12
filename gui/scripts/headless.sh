#!/usr/bin/env bash
# Runs a command (default: the GUI's display tests) against a private,
# headless weston, so no window ever appears on the desktop:
#
#   gui/scripts/headless.sh                            # go test -v ./internal/ui
#   gui/scripts/headless.sh go test -v -run Throughput ./internal/ui
#   ARCHMAID_GUI_PERF=1 gui/scripts/headless.sh        # also enforce the budgets
#   ARCHMAID_GUI_SHOTS=$(mktemp -d) gui/scripts/headless.sh   # PNGs of every scenario
#
# weston and the command are bounded by `timeout` (HEADLESS_TIMEOUT seconds,
# default 60; the default run is two halves of ~40 s here), so nothing outlives a run
# on the owner's machine; CI raises it for its slower runner.
#
# It sets WAYLAND_DISPLAY to weston's socket as an absolute path (the tests'
# testenv moves XDG_RUNTIME_DIR to a scratch dir), GDK_BACKEND=wayland, drops
# DISPLAY (no X fallback onto a live session), runs the command on a private
# session bus (dbus-run-session, so nothing reaches the desktop's bus) and
# sets ARCHMAID_GUI_DISPLAY_TESTS=1, which the display tests require. Run it
# from anywhere: it cds to the GUI module.
set -euo pipefail
self="$(realpath "${BASH_SOURCE[0]}")"
cd "$(dirname "$self")/.."

limit="${HEADLESS_TIMEOUT:-60}"
# The whole display suite takes ~135 s, past the bound, so the default runs
# it in thirteen parts of at most ~40 s, each with its own weston and timeout:
# the prompts, the real Driver and Stop, the Full Cleanup and the shimmed
# password and services scenarios, the engine stopping and the instance
# check with the 630x372 windows, the narrow/toast/tones/Logs/Settings
# windows, the wide dialogs and the breakpoint toggle, the wide dialogs
# with long content, the Overview's
# starts, the coverage scenarios (cover_harness_test.go), the first frame
# and throughput proofs, the rest. The wide dialogs (TestGUIWideDialogs)
# run their 1460x930 window on an output of 1600x1000 (HEADLESS_WIDTH,
# HEADLESS_HEIGHT; default 1280x800): weston's output bounds the window.
# TestGUIMinWidthToggle (~12 s) shares that part (~28 s): with the narrow
# or the states part it would be ~43 or ~47 s. TestGUIWideDialogsLong
# (~25 s, the same output) is a part of its own: with them it would be ~53 s.
if [[ $# -eq 0 ]]; then
	long='^TestGUIPrompts$'
	real='^TestGUI(RealDriver|StopAndQuit)$'
	svc='^TestGUI(FullCleanup|OnePassword|RealServices)$'
	stops='^TestGUI(EngineStops|StubOwnInstance|Shots|DMSColoursWin|SmallWindow)$'
	narrow='^TestGUI(NarrowNav|ToastPlain|MinWidth|LogsPage|SettingsCards)$'
	tones='^TestGUI(LastRunTones|AuditRerun)$'
	states='^TestGUIMinWidthStates$'
	wide='^TestGUI(WideDialogs|MinWidthToggle)$'
	widelong='^TestGUIWideDialogsLong'
	starts='^TestGUI(DryFirst|ConfirmFlag|RepairNothingFound|OverviewStarts|ClearRerun)$'
	cov='^TestGUICov'
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
	"$self" go test "${tags[@]}" -count=1 -v -run "$starts" ./internal/ui
	"$self" go test "${tags[@]}" -count=1 -v -run "$cov" ./internal/ui
	"$self" go test "${tags[@]}" -count=1 -v -run "$perf" ./internal/ui
	exec "$self" go test "${tags[@]}" -count=1 -v -skip "$long|$real|$svc|$stops|$narrow|$tones|$states|$wide|$widelong|$starts|$cov|$perf" ./internal/ui
fi

# A private runtime dir for weston's socket (CI containers have none, and
# nothing here should land in the desktop's /run/user/$UID).
XDG_RUNTIME_DIR="$(mktemp -d)"
chmod 700 "$XDG_RUNTIME_DIR"
export XDG_RUNTIME_DIR

sock="archmaid-gui-headless"
log="$XDG_RUNTIME_DIR/weston.log"
timeout -k 5 "$limit" weston --backend=headless --renderer="${WESTON_RENDERER:-pixman}" --no-config \
	--socket="$sock" --width="${HEADLESS_WIDTH:-1280}" --height="${HEADLESS_HEIGHT:-800}" --idle-time=0 >"$log" 2>&1 &
weston=$!
trap 'kill "$weston" 2>/dev/null; wait "$weston" 2>/dev/null; rm -rf "$XDG_RUNTIME_DIR"' EXIT

for _ in $(seq 100); do
	[[ -S "$XDG_RUNTIME_DIR/$sock" ]] && break
	if ! kill -0 "$weston" 2>/dev/null; then
		echo "weston failed to start:" >&2
		cat "$log" >&2
		exit 1
	fi
	sleep 0.05
done
if [[ ! -S "$XDG_RUNTIME_DIR/$sock" ]]; then
	echo "weston's socket never appeared" >&2
	cat "$log" >&2
	exit 1
fi

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

export WAYLAND_DISPLAY="$XDG_RUNTIME_DIR/$sock" GDK_BACKEND=wayland ARCHMAID_GUI_DISPLAY_TESTS=1
export NO_AT_BRIDGE=1 GTK_A11Y=none ADW_DISABLE_PORTAL=1 GDK_DEBUG=no-portals
unset DISPLAY DBUS_SESSION_BUS_ADDRESS HYPRLAND_INSTANCE_SIGNATURE
timeout -k 5 "$limit" dbus-run-session --config-file="$XDG_RUNTIME_DIR/bus.conf" -- "$@"
