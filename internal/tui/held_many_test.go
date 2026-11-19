package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/held"
)

// TestHeldScreenListsTheHeldBehindManyRepoUpdates (#300): the VM had 469 repo
// and 6 AUR updates (2 held); the screen said "Nothing is held back now."
func TestHeldScreenListsTheHeldBehindManyRepoUpdates(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"v":3,"count":473,"repo":469,"aur":4,"aur_held":2,"checked":"2026-11-02T10:00:00+01:00","packages":[`)
	for i := 0; i < 469; i++ {
		fmt.Fprintf(&b, `{"name":"r%d","from":"1-1","to":"2-1","src":"repo"},`, i)
	}
	b.WriteString(`{"name":"e1","from":"1-1","to":"2-1","src":"aur","held":false,"age":"5d"},` +
		`{"name":"h1","from":"1-1","to":"2-1","src":"aur","held":true,"age":"2d4h"},` +
		`{"name":"h2","from":"1-1","to":"2-1","src":"aur","held":true,"age":"6h"}]}`)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "updates-cache.json"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	heldSeams(t, held.Set{}, "paru")
	heldRead = func() held.Set { return held.Read(dir) }
	m := sizedModel()
	next, _ := m.openHeld()
	m = update(next.(model), tea.WindowSizeMsg{Width: 100, Height: 60})
	v := m.View()
	if strings.Contains(v, "Nothing is held back") || !strings.Contains(v, "paru -S h1") || !strings.Contains(v, "paru -S h2") ||
		strings.Contains(v, "paru -S e1") || !strings.Contains(v, "2 held") {
		t.Fatalf("held screen:\n%s", v)
	}
}
