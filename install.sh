#!/usr/bin/env bash
# archmaid installer — builds the TUI for the current user.
#
# Installs (all under $HOME, no root needed):
#   - binary  → ~/.local/bin/archmaid
#   - icon    → ~/.local/share/icons/hicolor/scalable/apps/archmaid.svg
#
# The TUI has no desktop entry (the GUI has its own): start it from a shell
# with `archmaid`. A TUI entry an older install.sh wrote is removed, on
# install and on --uninstall, if it is archmaid's own.
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
ICON="$ICONDIR/archmaid.svg"
# Only ever removed, never written: the stale TUI entry of an older install.sh.
STALE_DESKTOP="$APPDIR/archmaid.desktop"

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

uninstall() {
	echo "Removing archmaid…"
	rm -fv "$BIN" "$ICON"
	remove_stale_desktop
	refresh_caches
	echo "Done."
}

do_install() {
	command -v go >/dev/null || {
		echo "error: Go is required to build (pacman -S go)" >&2
		exit 1
	}

	echo "Building archmaid…"
	mkdir -p "$BINDIR" "$ICONDIR"
	( cd "$SCRIPT_DIR" && go build -trimpath -ldflags='-s -w' -o "$BIN" ./cmd/archmaid )
	info "binary  → $BIN"

	install -m 0644 "$SCRIPT_DIR/packaging/archmaid.svg" "$ICON"
	info "icon    → $ICON"

	remove_stale_desktop
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
