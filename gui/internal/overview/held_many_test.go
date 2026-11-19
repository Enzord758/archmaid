package overview

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The VM had 469 repo and 6 AUR updates (2 held) and the sheet said "Nothing
// is held back now." (#300): the held ones are found behind the repo entries.
func TestReadHeldBehindManyRepoUpdates(t *testing.T) {
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
	h := ReadHeld(dir)
	if len(h.Cooldown) != 2 || h.Cooldown[0].Name != "h1" || h.Cooldown[1].Name != "h2" {
		t.Fatalf("held %+v", h.Cooldown)
	}
}
