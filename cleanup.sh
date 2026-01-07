#!/usr/bin/env bash

# ─────────────────────────────────────────────
# Requirements
# ─────────────────────────────────────────────
command -v gum >/dev/null || {
  echo "gum is required"
  exit 1
}

# ─────────────────────────────────────────────
# Tokyo Night Midnight Theme
# ─────────────────────────────────────────────
TOKYO_BG="#1a1b26"
TOKYO_FG="#c0caf5"
TOKYO_BLUE="#7aa2f7"
TOKYO_CYAN="#7dcfff"
TOKYO_GREEN="#9ece6a"
TOKYO_RED="#f7768e"
TOKYO_YELLOW="#e0af68"
TOKYO_PURPLE="#bb9af7"
TOKYO_GRAY="#565f89"

export GUM_CHOOSE_CURSOR="➤ "
export GUM_CHOOSE_CURSOR_FOREGROUND="$TOKYO_BLUE"
export GUM_CHOOSE_SELECTED_FOREGROUND="$TOKYO_GREEN"
export GUM_CHOOSE_UNSELECTED_FOREGROUND="$TOKYO_FG"

clear

# ─────────────────────────────────────────────
# Runtime / Logging
# ─────────────────────────────────────────────
LOG_FILE="$HOME/.maintenance_tui.log"
: >"$LOG_FILE"

log() {
  printf "[%s] %s\n" "$(date '+%F %T')" "$1" >>"$LOG_FILE"
}

# ─────────────────────────────────────────────
# Helpers
# ─────────────────────────────────────────────

system_status() {
  disk=$(df -h / | awk 'NR==2{print $4}')
  cache=$(du -sh /var/cache/pacman/pkg 2>/dev/null | awk '{print $1}')
  echo " Free: $disk    Cache: ${cache:-N/A}"
}

status_footer() {
  gum style --foreground "$TOKYO_GRAY" "$(system_status)"
}

pause_and_clear() {
  echo
  status_footer
  echo
  gum confirm --affirmative "Back to menu" --negative "Exit" "Continue?" || exit 0
  clear
}

run_spin() {
  gum spin --spinner moon --title "$1" --show-output -- "${@:2}"
}

start_msg() {
  gum style --foreground "$TOKYO_CYAN" --bold "▶ $1"
  log "START: $1"
}

end_msg() {
  gum style --foreground "$TOKYO_GREEN" --bold "✔ $1"
  log "END: $1"
}

fail_msg() {
  gum style --foreground "$TOKYO_RED" --bold "✖ $1"
  log "FAIL: $1"
}

run_timed() {
  local label="$1"
  shift
  local start end dur
  start=$(date +%s)
  if "$@"; then
    end=$(date +%s)
    dur=$((end - start))
    end_msg "$label (${dur}s)"
    return 0
  else
    end=$(date +%s)
    dur=$((end - start))
    fail_msg "$label failed (${dur}s)"
    return 1
  fi
}

view_log() {
  if [[ ! -s "$LOG_FILE" ]]; then
    gum style --foreground "$TOKYO_GRAY" "Log is empty."
    return
  fi

  # Colorize log levels for readability
  sed -E \
    -e "s/START:/[36mSTART:[0m/" \
    -e "s/END:/[32mEND:[0m/" \
    -e "s/FAIL:/[31mFAIL:[0m/" \
    "$LOG_FILE" | gum pager
}

# ─────────────────────────────────────────────
# Maintenance Functions
# ─────────────────────────────────────────────

full_cleanup() {

  local errors=0

  start_msg "Full System Cleanup"

  gum confirm "Proceed with full system cleanup?" || return

  # 1. Package cache
  if command -v paccache &>/dev/null; then
    start_msg "Cleaning old package cache"
    run_timed "Old package cache cleaned" run_spin "Working…" sudo paccache -r || ((errors++))

    start_msg "Removing entire package cache"
    run_timed "Entire package cache removed" run_spin "Working…" sudo paccache -r -k0 || ((errors++))
  else
    fail_msg "paccache not found (install pacman-contrib)"
    ((errors++))
  fi

  # 2. Orphaned packages
  start_msg "Checking for orphaned packages"
  orphans=$(pacman -Qdtq)
  if [[ -z "$orphans" ]]; then
    gum style --foreground "$TOKYO_GRAY" "No orphaned packages found"
  else
    gum confirm "Remove orphaned packages?" &&
      run_timed "Orphaned packages removed" run_spin "Removing orphans…" sudo pacman -Rns --noconfirm $orphans ||
      gum style --foreground "$TOKYO_GRAY" "Skipped orphan removal"
  fi

  # 3. AUR cache
  if command -v yay &>/dev/null; then
    start_msg "Cleaning AUR cache"
    run_timed "AUR cache cleaned" run_spin "Working…" yay -Sc --noconfirm || ((errors++))
  fi

  # 4. Temp files
  start_msg "Removing temporary files"
  run_timed "Temporary files removed" run_spin "Working…" sudo rm -rf /tmp/* /var/tmp/* || ((errors++))

  # 5. Logs
  start_msg "Vacuuming system logs"
  run_timed "Old logs removed" run_spin "Working…" sudo journalctl --vacuum-time=2weeks || ((errors++))

  # 6. Package integrity
  start_msg "Running package integrity check"
  run_timed "Package integrity check completed" run_spin "Working…" sudo pacman -Qk &>/dev/null || ((errors++))

  # 7. System update
  start_msg "Updating system packages"
  run_timed "System update completed" run_spin "Working…" sudo pacman -Syu --noconfirm || ((errors++))

  if ((errors > 0)); then
    gum style --foreground "$TOKYO_YELLOW" --bold "⚠ Full cleanup completed with $errors issue(s)"
  else
    end_msg "Full cleanup completed successfully"
  fi

  gum confirm "View log?" && view_log
}

clear_user_cache() {
  start_msg "Clear user cache"
  gum confirm "Clear all user cache?" || return
  run_timed "User cache cleared" run_spin "Working…" rm -rf "$HOME/.cache/"*
}

repair_packages() {

  start_msg "Repair broken packages"

  missing=$(sudo pacman -Qk | grep -v '0 missing files' | awk '{print $1}' | sed 's/:$//' | sort -u)

  if [[ -z "$missing" ]]; then
    gum style --foreground "$TOKYO_GREEN" "No broken packages found"
    return
  fi

  gum confirm "Reinstall broken packages?" || return
  run_timed "Package repair completed" run_spin "Reinstalling packages…" bash -c "echo '$missing' | xargs sudo pacman -S --noconfirm"
}

delete_old_downloads() {
  start_msg "Delete old downloads"
  gum confirm "Delete downloads older than 30 days?" || return
  run_timed "Old downloads deleted" run_spin "Working…" find "$HOME/Downloads" -mtime +30 -exec rm -rf {} \;
}

update_aur_helpers() {

  start_msg "Update AUR packages"

  gum confirm "Update AUR packages?" || return

  if command -v yay &>/dev/null; then
    run_timed "Yay updates completed" run_spin "Updating with yay…" yay -Syu --noconfirm
  fi

  if command -v paru &>/dev/null; then
    run_timed "Paru updates completed" run_spin "Updating with paru…" paru -Syu --noconfirm
  fi

  end_msg "AUR updates finished"
}

update_flatpak() {
  start_msg "Update Flatpak packages"
  gum confirm "Update Flatpak packages?" || return
  run_timed "Flatpak updates completed" run_spin "Working…" flatpak update -y
}

disable_unused_services() {

  start_msg "Disable unused services"

  services=$(systemctl list-units --type=service --state=running --no-pager | grep "loaded inactive" | awk '{print $1}')

  if [[ -z "$services" ]]; then
    gum style --foreground "$TOKYO_GREEN" "No unnecessary services found"
    return
  fi

  gum style --foreground "$TOKYO_CYAN" "Services to disable:"
  echo "$services"

  gum confirm "Disable these services now?" || return

  run_timed "Unused services disabled" run_spin "Disabling services…" bash -c "echo '$services' | xargs sudo systemctl disable --now"
}

# ─────────────────────────────────────────────
# Main Menu (Header shows live system status)
# ─────────────────────────────────────────────

while true; do

  header="$(gum style --foreground \"$TOKYO_PURPLE\" --bold 'Arch Linux Maintenance')"$'
'"$(gum style --foreground \"$TOKYO_GRAY\" "$(system_status)")"$'
'$'
'"$(gum style --foreground \"$TOKYO_YELLOW\" --bold '★ Recommended: Full Cleanup')"

  menu_content=$(gum choose \
    "★ 󰓦  Full Cleanup (Recommended)" \
    "󰃨  Clear user cache" \
    "󰚰  Repair broken packages" \
    "󰋊  Delete old downloads" \
    "󰏗  Update AUR packages" \
    "󰏗  Update Flatpak packages" \
    "󰒔  Disable unused services" \
    "󰑇  View log" \
    "󰗼  Exit" \
    --header "$header" \
    --height 13)

  clear

  gum style \
    --border rounded \
    --padding "1 2" \
    --foreground "$TOKYO_FG" \
    --border-foreground "$TOKYO_BLUE" \
    " Selected: ${menu_content:-Exit}"

  echo

  case "$menu_content" in
  "★ 󰓦  Full Cleanup (Recommended)") full_cleanup ;;
  "󰃨  Clear user cache") clear_user_cache ;;
  "󰚰  Repair broken packages") repair_packages ;;
  "󰋊  Delete old downloads") delete_old_downloads ;;
  "󰏗  Update AUR packages") update_aur_helpers ;;
  "󰏗  Update Flatpak packages") update_flatpak ;;
  "󰒔  Disable unused services") disable_unused_services ;;
  "󰑇  View log") view_log ;;
  "󰗼  Exit" | "") exit 0 ;;
  esac

  pause_and_clear
done
