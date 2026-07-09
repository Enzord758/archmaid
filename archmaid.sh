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
LOG_FILE="$HOME/.archmaid.log"
: >"$LOG_FILE"

log() {
  printf "[%s] %s\n" "$(date '+%F %T')" "$1" >>"$LOG_FILE"
}

# ─────────────────────────────────────────────
# Helpers
# ─────────────────────────────────────────────

system_status() {
  disk=$(df -h / | awk 'NR==2{print $4}')
  mem=$(free -h | awk '/^Mem:/{print $4}')
  cache=$(du -sh /var/cache/pacman/pkg 2>/dev/null | awk '{print $1}')
  echo " Free: $disk   RAM free: $mem   Cache: ${cache:-N/A}"
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

# log_output "Log label" "text…"
#
# Appends a delimited block of real command output to the log file. Only
# called on failure, so the log stays focused on what actually went wrong
# instead of ballooning with output from every successful run.
log_output() {
  local label="$1"
  local text="$2"
  [[ -n "$text" ]] || return 0
  {
    printf -- '--- %s output ---\n' "$label"
    printf '%s\n' "$text"
    printf -- '--- end output ---\n'
  } >>"$LOG_FILE"
}

# run_task "Spinner title" "Log label" cmd [args…]
#
# Runs cmd under gum spin (which natively blocks until cmd finishes and
# forwards its exit code) while capturing its real stdout/stderr — gum spin
# discards command output by default, so without this the log only ever
# saw START/END/FAIL markers, never what the command actually did or why
# it failed. Output is always logged; on failure it's also shown in a
# pager since the spinner hid it while running.
run_task() {
  local spin_title="$1"
  local label="$2"
  shift 2

  local start end dur exit_code tmp_out
  tmp_out=$(mktemp)
  start=$(date +%s)

  gum spin --spinner dot \
    --title " $spin_title" \
    --spinner.foreground="#7aa2f7" \
    -- bash -c 'out="$1"; shift; exec >"$out" 2>&1; exec "$@"' bash "$tmp_out" "$@"
  exit_code=$?

  end=$(date +%s)
  dur=$((end - start))

  if [[ "$exit_code" -eq 0 ]]; then
    end_msg "$label (${dur}s)"
  else
    log_output "$label" "$(<"$tmp_out")"
    fail_msg "$label failed (${dur}s)"
    if [[ -s "$tmp_out" ]]; then
      echo
      gum pager <"$tmp_out"
    fi
  fi

  rm -f "$tmp_out"
  return "$exit_code"
}

view_log() {
  if [[ ! -s "$LOG_FILE" ]]; then
    gum style --foreground "$TOKYO_GRAY" "Log is empty."
    return
  fi

  sed -E \
    -e $'s/START:/\e[36mSTART:\e[0m/' \
    -e $'s/END:/\e[32mEND:\e[0m/' \
    -e $'s/FAIL:/\e[31mFAIL:\e[0m/' \
    "$LOG_FILE" | gum pager
}

# ─────────────────────────────────────────────
# Maintenance Functions
# ─────────────────────────────────────────────

full_cleanup() {
  local errors=0

  start_msg "Full System Cleanup"
  gum confirm "Proceed with full system cleanup?" || return

  # Authenticate once, up front — run_task backgrounds commands with output
  # suppressed, so a sudo password prompt triggered mid-spinner is unreliable.
  sudo -v || {
    fail_msg "sudo authentication failed"
    return
  }

  # 1. Package cache
  if command -v paccache &>/dev/null; then
    start_msg "Cleaning old package cache (keeps 3 versions)"
    run_task "Cleaning package cache…" "Old package cache cleaned" sudo paccache -r || ((errors++))

    if gum confirm "Delete ENTIRE package cache? (destructive – can't reinstall without internet)"; then
      run_task "Deleting entire cache…" "Entire package cache removed" sudo paccache -rk0 || ((errors++))
    else
      gum style --foreground "$TOKYO_GRAY" "Skipped full cache deletion"
    fi
  else
    fail_msg "paccache not found (install pacman-contrib)"
    ((errors++))
  fi

  # 2. Orphaned packages
  start_msg "Checking for orphaned packages"
  mapfile -t orphans < <(pacman -Qdtq 2>/dev/null)
  if [[ ${#orphans[@]} -eq 0 ]]; then
    gum style --foreground "$TOKYO_GRAY" "No orphaned packages found"
  else
    gum style --foreground "$TOKYO_CYAN" "Found ${#orphans[@]} orphaned packages: ${orphans[*]}"
    if gum confirm "Remove orphaned packages?"; then
      run_task "Removing orphaned packages…" "Orphaned packages removed" \
        sudo pacman -Rns --noconfirm "${orphans[@]}" || ((errors++))
    else
      gum style --foreground "$TOKYO_GRAY" "Skipped orphan removal"
    fi
  fi

  # 3. AUR cache
  if command -v yay &>/dev/null; then
    start_msg "Cleaning AUR cache"
    run_task "Cleaning AUR cache…" "AUR cache cleaned" yay -Sc --noconfirm || ((errors++))
  fi

  # 4. Temp files older than 2 days (avoid deleting sockets/lockfiles other
  # running processes still need from the current session)
  start_msg "Removing old temporary files"
  run_task "Cleaning temp files…" "Old temporary files removed" \
    sudo find /tmp /var/tmp -mindepth 1 -maxdepth 1 -mtime +2 -exec rm -rf {} + || ((errors++))

  # 5. Logs
  start_msg "Vacuuming system logs"
  run_task "Vacuuming logs…" "Old logs removed" sudo journalctl --vacuum-time=2weeks || ((errors++))

  # 6. Package integrity
  start_msg "Running package integrity check"
  run_task "Checking package integrity…" "Package integrity check completed" sudo pacman -Qk || ((errors++))

  # 7. System update via topgrade — shown live (it's long-running, and a
  # blank spinner screen for a multi-minute update isn't reassuring)
  start_msg "Updating system with topgrade"
  if command -v topgrade &>/dev/null; then
    local tg_output tg_tmp tg_start tg_end tg_dur tg_exit
    tg_tmp=$(mktemp)
    tg_start=$(date +%s)
    topgrade -y 2>&1 | tee "$tg_tmp"
    tg_exit=${PIPESTATUS[0]}
    tg_end=$(date +%s)
    tg_dur=$((tg_end - tg_start))
    if [[ $tg_exit -eq 0 ]]; then
      end_msg "Topgrade completed (${tg_dur}s)"
    else
      tg_output=$(<"$tg_tmp")
      log_output "Topgrade" "$tg_output"
      fail_msg "Topgrade failed (${tg_dur}s)"
      ((errors++))
    fi
    rm -f "$tg_tmp"
  else
    fail_msg "topgrade not found – falling back to pacman"
    run_task "Updating system…" "System update completed" sudo pacman -Syu --noconfirm || ((errors++))
  fi

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
  run_task "Cleaning user cache…" "User cache cleared" rm -rf "$HOME/.cache/"*
}

repair_packages() {
  start_msg "Repair broken packages"

  sudo -v || {
    fail_msg "sudo authentication failed"
    return
  }

  mapfile -t missing < <(sudo pacman -Qk 2>/dev/null | grep -v '0 missing files' | awk '{print $1}' | sed 's/:$//' | sort -u)

  if [[ ${#missing[@]} -eq 0 ]]; then
    gum style --foreground "$TOKYO_GREEN" "No broken packages found"
    return
  fi

  gum style --foreground "$TOKYO_CYAN" "Found ${#missing[@]} package(s) with missing files:"
  printf '  %s\n' "${missing[@]}"

  gum confirm "Reinstall broken packages?" || return
  run_task "Reinstalling packages…" "Package repair completed" \
    sudo pacman -S --noconfirm "${missing[@]}"
}

delete_old_downloads() {
  start_msg "Delete old downloads"

  local count
  count=$(find "$HOME/Downloads" -maxdepth 1 -mtime +30 2>/dev/null | wc -l)

  if [[ "$count" -eq 0 ]]; then
    gum style --foreground "$TOKYO_GRAY" "No files older than 30 days found"
    return
  fi

  gum confirm "Delete $count file(s) older than 30 days in Downloads?" || return
  run_task "Deleting old downloads…" "Old downloads deleted" \
    find "$HOME/Downloads" -maxdepth 1 -mtime +30 -exec rm -rf {} +
}

run_topgrade() {
  start_msg "System update with topgrade"
  if ! command -v topgrade &>/dev/null; then
    fail_msg "topgrade is not installed (yay -S topgrade)"
    return
  fi
  gum confirm "Run topgrade now?" || return

  # Shown live — see the note in full_cleanup's topgrade step
  local output tmp_out start end dur exit_code
  tmp_out=$(mktemp)
  start=$(date +%s)
  topgrade -y 2>&1 | tee "$tmp_out"
  exit_code=${PIPESTATUS[0]}
  end=$(date +%s)
  dur=$((end - start))

  if [[ $exit_code -eq 0 ]]; then
    end_msg "Topgrade completed (${dur}s)"
  else
    output=$(<"$tmp_out")
    log_output "Topgrade" "$output"
    fail_msg "Topgrade failed (${dur}s)"
  fi
  rm -f "$tmp_out"
}

edit_topgrade_config() {
  local config="$HOME/.config/topgrade.toml"

  start_msg "Topgrade configuration"

  if [[ ! -f "$config" ]]; then
    if gum confirm "No config found. Create one with recommended defaults?"; then
      mkdir -p "$(dirname "$config")"
      cat >"$config" <<'EOF'
# topgrade configuration
# Documentation: https://github.com/topgrade-rs/topgrade/wiki/Configuration

[misc]
# Don't confirm every single update
assume_yes = true
# Clean up after updating
cleanup = true

[linux]
# Use yay for AUR
aur_command = "yay"

# Disable steps you don't want by setting them to false:
# [skip_steps]
# flatpak = false
EOF
      gum style --foreground "$TOKYO_GREEN" "Config created: $config"
    else
      return
    fi
  fi

  local editor="${VISUAL:-${EDITOR:-nano}}"
  gum style --foreground "$TOKYO_GRAY" "Opening with: $editor"
  "$editor" "$config"
  end_msg "Config saved"
}

disable_unused_services() {
  start_msg "Disable unused services"

  sudo -v || {
    fail_msg "sudo authentication failed"
    return
  }

  # Only services that are enabled but not currently running.
  # Cross-references the two lists with comm, filtering out systemd-internal/
  # oneshot/initrd services.
  mapfile -t enabled_inactive < <(
    comm -12 \
      <(systemctl list-unit-files --type=service --state=enabled --no-pager --no-legend |
        awk '{print $1}' | sort) \
      <(systemctl list-units --type=service --state=inactive --no-pager --no-legend |
        awk '{print $1}' | sort)
  )

  # Single-quoted strings don't support backslash-newline continuation in
  # bash — the previous version embedded a literal newline mid-pattern,
  # which not only broke the "shadow" match but also left a bare "@" as
  # its own alternative, matching any templated unit (foo@1.service).
  # Build the pattern as an array joined by "|" instead.
  local skip_patterns=(
    systemd- getty@ emergency rescue initrd plymouth shadow
    pkgstats man-db mkinitcpio modprobe@ audit dirmngr gpg-agent keyboxd
    dm-event lm_sensors nmb archlinux-keyring-wkd-sync
  )
  local skip_pattern
  skip_pattern=$(
    IFS='|'
    echo "${skip_patterns[*]}"
  )

  mapfile -t services < <(
    printf '%s\n' "${enabled_inactive[@]}" |
      grep -Ev "$skip_pattern"
  )

  if [[ ${#services[@]} -eq 0 ]]; then
    gum style --foreground "$TOKYO_GREEN" "No enabled-but-inactive services found"
    return
  fi

  gum style --foreground "$TOKYO_CYAN" "Enabled but not running services (${#services[@]}):"
  printf '  %s\n' "${services[@]}"
  echo
  gum style --foreground "$TOKYO_YELLOW" "⚠ Review the list carefully – disable thoughtfully"

  # Multi-select so the user picks exactly what gets disabled
  mapfile -t to_disable < <(
    gum choose --no-limit "${services[@]}"
  )

  if [[ ${#to_disable[@]} -eq 0 ]]; then
    gum style --foreground "$TOKYO_GRAY" "No services selected"
    return
  fi

  run_task "Disabling services…" "Services disabled" \
    sudo systemctl disable --now "${to_disable[@]}"
}

# ─────────────────────────────────────────────
# Main Menu
# ─────────────────────────────────────────────

while true; do

  # GUM_CHOOSE_ITEM_FOREGROUND sets the color of all items –
  # we highlight the first item with a workaround: prefix it with an ANSI
  # bold+color code directly in the string, reset afterward.
  FULL_CLEANUP_ITEM="$(printf '\e[1;33m󰓦  Full Cleanup (Recommended)\e[0m')"

  header="$(gum style --foreground "$TOKYO_PURPLE" --bold 'archmaid — Arch maintenance')"$'\n'"$(gum style --foreground "$TOKYO_GRAY" "$(system_status)")"$'\n\n'"$(gum style --foreground "$TOKYO_YELLOW" --bold 'Recommended: Full Cleanup')"

  menu_content=$(gum choose \
    "$FULL_CLEANUP_ITEM" \
    "󰃨  Clear user cache" \
    "󰚰  Repair broken packages" \
    "󰋊  Delete old downloads" \
    "󰏗  Run topgrade (system update)" \
    "󰒔  Disable unused services" \
    "󰙪  Edit topgrade config" \
    "󰑇  View log" \
    "󰗼  Exit" \
    --header "$header" \
    --height 13)

  # Strip ANSI escape codes from the choice so the case match is clean
  menu_clean=$(printf '%s' "$menu_content" | sed 's/\x1b\[[0-9;]*m//g')

  clear

  gum style \
    --border rounded \
    --padding "1 2" \
    --foreground "$TOKYO_FG" \
    --border-foreground "$TOKYO_BLUE" \
    " Selected: ${menu_clean:-Exit}"

  echo

  case "$menu_clean" in
  "󰓦  Full Cleanup (Recommended)") full_cleanup ;;
  "󰃨  Clear user cache") clear_user_cache ;;
  "󰚰  Repair broken packages") repair_packages ;;
  "󰋊  Delete old downloads") delete_old_downloads ;;
  "󰏗  Run topgrade (system update)") run_topgrade ;;
  "󰒔  Disable unused services") disable_unused_services ;;
  "󰙪  Edit topgrade config") edit_topgrade_config ;;
  "󰑇  View log") view_log ;;
  "󰗼  Exit" | "") exit 0 ;;
  esac

  pause_and_clear
done
