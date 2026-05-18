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
  mem=$(free -h | awk '/^Mem:/{print $4}')
  cache=$(du -sh /var/cache/pacman/pkg 2>/dev/null | awk '{print $1}')
  echo " Free: $disk   RAM ledig: $mem   Cache: ${cache:-N/A}"
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

# run_task "Spinner titel" "Log label" cmd [args…]
#
# Kører cmd i baggrunden, viser gum spin mens den kører,
# og printer end_msg/fail_msg med timing når den er færdig.
# Virker med både binaries og shell-funktioner via 'bash -c'.
run_task() {
  local spin_title="$1"
  local label="$2"
  shift 2

  local tmp_exit start end dur
  tmp_exit=$(mktemp)
  start=$(date +%s)

  # Kør kommandoen i en subshell i baggrunden
  (
    "$@" >/dev/null 2>&1
    echo $? >"$tmp_exit"
  ) &
  local cmd_pid=$!

  # Kør gum spin i baggrunden – den spinner indtil vi dræber den
  gum spin --spinner dot \
    --title " $spin_title" \
    --spinner.foreground="#7aa2f7" \
    -- sleep 86400 &
  local spin_pid=$!

  # Vent på at kommandoen er færdig, stop så spinneren
  wait "$cmd_pid"
  kill "$spin_pid" 2>/dev/null
  wait "$spin_pid" 2>/dev/null

  end=$(date +%s)
  dur=$((end - start))

  local exit_code
  exit_code=$(<"$tmp_exit")
  rm -f "$tmp_exit"

  # Ryd spinner-linjen op
  printf '\r\033[K'

  if [[ "$exit_code" -eq 0 ]]; then
    end_msg "$label (${dur}s)"
    return 0
  else
    fail_msg "$label fejlede (${dur}s)"
    return 1
  fi
}

view_log() {
  if [[ ! -s "$LOG_FILE" ]]; then
    gum style --foreground "$TOKYO_GRAY" "Log is empty."
    return
  fi

  sed -E \
    -e "s/START:/$'\e[36m'START:$'\e[0m'/" \
    -e "s/END:/$'\e[32m'END:$'\e[0m'/" \
    -e "s/FAIL:/$'\e[31m'FAIL:$'\e[0m'/" \
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
    start_msg "Cleaning old package cache (beholder 3 versioner)"
    run_task "Renser package cache…" "Old package cache cleaned" sudo paccache -r || ((errors++))

    if gum confirm "Slet HELE package cache? (destruktivt – kan ikke geninstallere uden net)"; then
      run_task "Sletter hele cache…" "Entire package cache removed" sudo paccache -rk0 || ((errors++))
    else
      gum style --foreground "$TOKYO_GRAY" "Sprang over fuld cache-sletning"
    fi
  else
    fail_msg "paccache ikke fundet (installer pacman-contrib)"
    ((errors++))
  fi

  # 2. Orphaned packages
  start_msg "Checking for orphaned packages"
  mapfile -t orphans < <(pacman -Qdtq 2>/dev/null)
  if [[ ${#orphans[@]} -eq 0 ]]; then
    gum style --foreground "$TOKYO_GRAY" "No orphaned packages found"
  else
    gum style --foreground "$TOKYO_CYAN" "Fandt ${#orphans[@]} forældreløse pakker: ${orphans[*]}"
    if gum confirm "Remove orphaned packages?"; then
      run_task "Fjerner forældreløse pakker…" "Orphaned packages removed" \
        sudo pacman -Rns --noconfirm "${orphans[@]}" || ((errors++))
    else
      gum style --foreground "$TOKYO_GRAY" "Skipped orphan removal"
    fi
  fi

  # 3. AUR cache
  if command -v yay &>/dev/null; then
    start_msg "Cleaning AUR cache"
    run_task "Renser AUR cache…" "AUR cache cleaned" yay -Sc --noconfirm || ((errors++))
  fi

  # 4. Temp files
  start_msg "Removing temporary files"
  run_task "Sletter temp-filer…" "Temporary files removed" sudo rm -rf /tmp/* /var/tmp/* || ((errors++))

  # 5. Logs
  start_msg "Vacuuming system logs"
  run_task "Vacuuming logs…" "Old logs removed" sudo journalctl --vacuum-time=2weeks || ((errors++))

  # 6. Package integrity
  start_msg "Running package integrity check"
  run_task "Tjekker pakke-integritet…" "Package integrity check completed" sudo pacman -Qk || ((errors++))

  # 7. System update via topgrade (silent – viser kun output ved fejl)
  start_msg "Updating system med topgrade"
  if command -v topgrade &>/dev/null; then
    local tg_output tg_start tg_end tg_dur
    tg_start=$(date +%s)
    tg_output=$(topgrade 2>&1)
    local tg_exit=$?
    tg_end=$(date +%s)
    tg_dur=$((tg_end - tg_start))
    if [[ $tg_exit -eq 0 ]]; then
      end_msg "Topgrade completed (${tg_dur}s)"
      log "END: Topgrade completed (${tg_dur}s)"
    else
      fail_msg "Topgrade fejlede (${tg_dur}s) – viser output:"
      log "FAIL: Topgrade fejlede (${tg_dur}s)"
      echo
      echo "$tg_output" | gum pager
      ((errors++))
    fi
  else
    fail_msg "topgrade ikke fundet – falder tilbage på pacman"
    run_task "Opdaterer system…" "System update completed" sudo pacman -Syu --noconfirm || ((errors++))
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
  run_task "Renser user cache…" "User cache cleared" rm -rf "$HOME/.cache/"*
}

repair_packages() {
  start_msg "Repair broken packages"

  mapfile -t missing < <(sudo pacman -Qk 2>/dev/null | grep -v '0 missing files' | awk '{print $1}' | sed 's/:$//' | sort -u)

  if [[ ${#missing[@]} -eq 0 ]]; then
    gum style --foreground "$TOKYO_GREEN" "No broken packages found"
    return
  fi

  gum style --foreground "$TOKYO_CYAN" "Fandt ${#missing[@]} pakke(r) med manglende filer:"
  printf '  %s\n' "${missing[@]}"

  gum confirm "Reinstall broken packages?" || return
  run_task "Geninstallerer pakker…" "Package repair completed" \
    sudo pacman -S --noconfirm "${missing[@]}"
}

delete_old_downloads() {
  start_msg "Delete old downloads"

  local count
  count=$(find "$HOME/Downloads" -maxdepth 1 -mtime +30 2>/dev/null | wc -l)

  if [[ "$count" -eq 0 ]]; then
    gum style --foreground "$TOKYO_GRAY" "Ingen filer ældre end 30 dage fundet"
    return
  fi

  gum confirm "Slet $count fil(er) ældre end 30 dage i Downloads?" || return
  run_task "Sletter gamle downloads…" "Old downloads deleted" \
    find "$HOME/Downloads" -maxdepth 1 -mtime +30 -exec rm -rf {} +
}

run_topgrade() {
  start_msg "System update med topgrade"
  if ! command -v topgrade &>/dev/null; then
    fail_msg "topgrade er ikke installeret (yay -S topgrade)"
    return
  fi
  gum confirm "Kør topgrade nu?" || return

  # Kør silent – vis kun output hvis der er en fejl
  local output start end dur
  start=$(date +%s)
  output=$(topgrade 2>&1)
  local exit_code=$?
  end=$(date +%s)
  dur=$((end - start))

  if [[ $exit_code -eq 0 ]]; then
    end_msg "Topgrade completed (${dur}s)"
    log "END: Topgrade completed (${dur}s)"
  else
    fail_msg "Topgrade fejlede (${dur}s) – viser output:"
    log "FAIL: Topgrade fejlede (${dur}s)"
    echo
    echo "$output" | gum pager
  fi
}

edit_topgrade_config() {
  local config="$HOME/.config/topgrade.toml"

  start_msg "Topgrade konfiguration"

  if [[ ! -f "$config" ]]; then
    if gum confirm "Ingen config fundet. Opret en med anbefalede defaults?"; then
      mkdir -p "$(dirname "$config")"
      cat >"$config" <<'EOF'
# topgrade konfiguration
# Dokumentation: https://github.com/topgrade-rs/topgrade/wiki/Configuration

[misc]
# Bekræft ikke hver enkelt opdatering
assume_yes = true
# Ryd op efter opdatering
cleanup = true

[linux]
# Brug yay til AUR
aur_command = "yay"

# Deaktiver trin du ikke ønsker ved at sætte dem til false:
# [skip_steps]
# flatpak = false
EOF
      gum style --foreground "$TOKYO_GREEN" "Config oprettet: $config"
    else
      return
    fi
  fi

  local editor="${VISUAL:-${EDITOR:-nano}}"
  gum style --foreground "$TOKYO_GRAY" "Åbner med: $editor"
  "$editor" "$config"
  end_msg "Config gemt"
}

disable_unused_services() {
  start_msg "Disable unused services"

  # Kun services der er enabled men ikke kører lige nu.
  # Krydser to lister med comm og filtrerer systemd-interne/oneshot/initrd services fra.
  mapfile -t enabled_inactive < <(
    comm -12 \
      <(systemctl list-unit-files --type=service --state=enabled --no-pager --no-legend |
        awk '{print $1}' | sort) \
      <(systemctl list-units --type=service --state=inactive --no-pager --no-legend |
        awk '{print $1}' | sort)
  )

  local skip_pattern
  skip_pattern='systemd-|getty@|emergency|rescue|initrd|plymouth|shadow\
|@|pkgstats|man-db|mkinitcpio|modprobe@|audit|dirmngr|gpg-agent|keyboxd\
|dm-event|lm_sensors|nmb|archlinux-keyring-wkd-sync'

  mapfile -t services < <(
    printf '%s\n' "${enabled_inactive[@]}" |
      grep -Ev "$skip_pattern"
  )

  if [[ ${#services[@]} -eq 0 ]]; then
    gum style --foreground "$TOKYO_GREEN" "Ingen enabled-men-inaktive services fundet"
    return
  fi

  gum style --foreground "$TOKYO_CYAN" "Enabled men ikke kørende services (${#services[@]} stk):"
  printf '  %s\n' "${services[@]}"
  echo
  gum style --foreground "$TOKYO_YELLOW" "⚠ Gennemgå listen omhyggeligt – disable med omtanke"

  # Multi-select så brugeren selv vælger præcis hvad der skal disables
  mapfile -t to_disable < <(
    gum choose --no-limit "${services[@]}"
  )

  if [[ ${#to_disable[@]} -eq 0 ]]; then
    gum style --foreground "$TOKYO_GRAY" "Ingen services valgt"
    return
  fi

  run_task "Disabler services…" "Services disabled" \
    sudo systemctl disable --now "${to_disable[@]}"
}

# ─────────────────────────────────────────────
# Main Menu
# ─────────────────────────────────────────────

while true; do

  # GUM_CHOOSE_ITEM_FOREGROUND sætter farve på alle items –
  # vi bruger en highlight-farve på første item via et workaround:
  # præfix med ANSI bold+farve direkte i strengen, reset bagefter.
  FULL_CLEANUP_ITEM="$(printf '\e[1;33m󰓦  Full Cleanup (Recommended)\e[0m')"

  header="$(gum style --foreground "$TOKYO_PURPLE" --bold 'Arch Linux Maintenance')"$'\n'"$(gum style --foreground "$TOKYO_GRAY" "$(system_status)")"$'\n\n'"$(gum style --foreground "$TOKYO_YELLOW" --bold 'Anbefalet: Full Cleanup')"

  menu_content=$(gum choose \
    "$FULL_CLEANUP_ITEM" \
    "󰃨  Clear user cache" \
    "󰚰  Repair broken packages" \
    "󰋊  Delete old downloads" \
    "󰏗  Kør topgrade (system update)" \
    "󰒔  Disable unused services" \
    "󰙪  Rediger topgrade config" \
    "󰑇  View log" \
    "󰗼  Exit" \
    --header "$header" \
    --height 13)

  # Strip ANSI escape koder fra valget så case matcher rent
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
  "󰏗  Kør topgrade (system update)") run_topgrade ;;
  "󰒔  Disable unused services") disable_unused_services ;;
  "󰙪  Rediger topgrade config") edit_topgrade_config ;;
  "󰑇  View log") view_log ;;
  "󰗼  Exit" | "") exit 0 ;;
  esac

  pause_and_clear
done
