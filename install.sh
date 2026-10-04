#!/usr/bin/env bash
# archmaid installer — builds the TUI and wires it up as a launchable app.
#
# Installs (all under $HOME, no root needed):
#   - binary  → ~/.local/bin/archmaid
#   - icon    → ~/.local/share/icons/hicolor/scalable/apps/archmaid.svg
#   - desktop → ~/.local/share/applications/archmaid.desktop
#
# The .desktop launches the TUI inside a terminal so it works from rofi and
# other application launchers.
#
# Usage:
#   ./install.sh              # build + install
#   ./install.sh --uninstall  # remove everything it installed
#
# Env:
#   PREFIX               install prefix (default: ~/.local)
#   ARCHMAID_TERMINAL    terminal command the .desktop uses (default: kitty),
#                        e.g. ARCHMAID_TERMINAL="alacritty -e"
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PREFIX="${PREFIX:-$HOME/.local}"
BINDIR="$PREFIX/bin"
ICONDIR="$PREFIX/share/icons/hicolor/scalable/apps"
APPDIR="$PREFIX/share/applications"

BIN="$BINDIR/archmaid"
ICON="$ICONDIR/archmaid.svg"
DESKTOP="$APPDIR/archmaid.desktop"

TERMINAL="${ARCHMAID_TERMINAL:-kitty}"

info() { printf '  %s\n' "$1"; }

refresh_caches() {
	command -v update-desktop-database >/dev/null && update-desktop-database "$APPDIR" 2>/dev/null || true
	command -v gtk-update-icon-cache >/dev/null &&
		gtk-update-icon-cache -f "$PREFIX/share/icons/hicolor" 2>/dev/null || true
}

uninstall() {
	echo "Removing archmaid…"
	rm -fv "$BIN" "$ICON" "$DESKTOP"
	refresh_caches
	echo "Done."
}

do_install() {
	command -v go >/dev/null || {
		echo "error: Go is required to build (pacman -S go)" >&2
		exit 1
	}

	echo "Building archmaid…"
	mkdir -p "$BINDIR" "$ICONDIR" "$APPDIR"
	( cd "$SCRIPT_DIR/tui" && go build -trimpath -ldflags='-s -w' -o "$BIN" . )
	info "binary  → $BIN"

	install -m 0644 "$SCRIPT_DIR/packaging/archmaid.svg" "$ICON"
	info "icon    → $ICON"

	cat >"$DESKTOP" <<EOF
[Desktop Entry]
Type=Application
Name=archmaid
GenericName=Arch maintenance
Comment=Clean caches, remove orphans, update, and tidy your Arch system
Exec=$TERMINAL $BIN
Icon=archmaid
Terminal=false
Categories=System;
Keywords=maintenance;cleanup;pacman;orphans;update;topgrade;arch;
StartupNotify=false
EOF
	info "desktop → $DESKTOP"

	refresh_caches

	echo
	echo "Installed. Launch it from rofi as \"archmaid\", or run: archmaid"
	case ":$PATH:" in
	*":$BINDIR:"*) ;;
	*) echo "note: $BINDIR is not on your PATH — add it to run 'archmaid' directly." ;;
	esac
	command -v "${TERMINAL%% *}" >/dev/null ||
		echo "note: terminal '${TERMINAL%% *}' not found — set ARCHMAID_TERMINAL and re-run."
}

if [[ "${1:-}" == "--uninstall" ]]; then
	uninstall
else
	do_install
fi
