#!/usr/bin/env bash
# archmaid installer — builds the TUI for the current user.
#
# Installs (all under $HOME, no root needed):
#   - binary  → ~/.local/bin/archmaid
#
# The TUI has no desktop entry and no icon (the GUI has its own): start it
# from a shell with `archmaid`. A TUI entry and icon an older install.sh wrote
# are removed, on install and on --uninstall, if they are archmaid's own.
#
# Usage:
#   ./install.sh              # build + install
#   ./install.sh --uninstall  # remove everything it installed
#
# Env:
#   PREFIX               install prefix (default: ~/.local)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PREFIX="${PREFIX:-$HOME/.local}"
BINDIR="$PREFIX/bin"
ICONDIR="$PREFIX/share/icons/hicolor/scalable/apps"
APPDIR="$PREFIX/share/applications"

BIN="$BINDIR/archmaid"
# Only ever removed, never written: the stale TUI entry and icon of an older
# install.sh.
STALE_DESKTOP="$APPDIR/archmaid.desktop"
STALE_ICON="$ICONDIR/archmaid.svg"

info() { printf '  %s\n' "$1"; }

refresh_caches() {
	command -v update-desktop-database >/dev/null && update-desktop-database "$APPDIR" 2>/dev/null || true
	command -v gtk-update-icon-cache >/dev/null &&
		gtk-update-icon-cache -f "$PREFIX/share/icons/hicolor" 2>/dev/null || true
}

# Remove the stale entry only if it is archmaid's own: Name=archmaid with an
# Exec that runs archmaid, or a stub that is just Hidden=true. Anything else
# (another program's file, a symlink) is left alone.
remove_stale_desktop() {
	[[ -f "$STALE_DESKTOP" && ! -L "$STALE_DESKTOP" ]] || return 0
	if grep -qx 'Name=archmaid' "$STALE_DESKTOP" && grep -qE '^Exec=.*archmaid[[:space:]]*$' "$STALE_DESKTOP"; then
		:
	elif [[ "$(grep -v '^[[:space:]]*$' "$STALE_DESKTOP")" == $'[Desktop Entry]\nHidden=true' ]]; then
		:
	else
		return 0
	fi
	rm -fv "$STALE_DESKTOP"
}

# Remove the stale icon only if it is the project icon, byte for byte, and a
# regular file (not a symlink).
remove_stale_icon() {
	[[ -f "$STALE_ICON" && ! -L "$STALE_ICON" ]] || return 0
	cmp -s "$STALE_ICON" "$SCRIPT_DIR/packaging/archmaid.svg" || return 0
	rm -fv "$STALE_ICON"
}

uninstall() {
	echo "Removing archmaid…"
	rm -fv "$BIN"
	remove_stale_desktop
	remove_stale_icon
	refresh_caches
	echo "Done."
}

do_install() {
	command -v go >/dev/null || {
		echo "error: Go is required to build (pacman -S go)" >&2
		exit 1
	}

	echo "Building archmaid…"
	mkdir -p "$BINDIR"
	( cd "$SCRIPT_DIR" && go build -trimpath -ldflags='-s -w' -o "$BIN" ./cmd/archmaid )
	info "binary  → $BIN"

	remove_stale_desktop
	remove_stale_icon
	refresh_caches

	echo
	echo "Installed. Run: archmaid"
	case ":$PATH:" in
	*":$BINDIR:"*) ;;
	*) echo "note: $BINDIR is not on your PATH — add it to run 'archmaid' directly." ;;
	esac
}

if [[ "${1:-}" == "--uninstall" ]]; then
	uninstall
else
	do_install
fi
