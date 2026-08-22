package main

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ── system status ────────────────────────────────────────────────────────────

type statusMsg string

// refreshStatus recomputes the disk/ram/cache line every few seconds so the
// header stays live (the bit gum couldn't do without redrawing the world).
func refreshStatus() tea.Cmd {
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg {
		return statusMsg(systemStatus())
	})
}

func systemStatus() string {
	disk := shellField("df -h / | awk 'NR==2{print $4}'")
	mem := shellField("free -h | awk '/^Mem:/{print $4}'")
	cache := shellField("du -sh /var/cache/pacman/pkg 2>/dev/null | awk '{print $1}'")
	if cache == "" {
		cache = "N/A"
	}
	return fmt.Sprintf(" Free: %s   RAM free: %s   Cache: %s", disk, mem, cache)
}

func shellField(cmd string) string {
	out, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
