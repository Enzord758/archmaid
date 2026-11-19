package held

import (
	"fmt"
	"strings"
	"testing"
)

// manyRepoCache is a cache as the VM had it (#300): 469 repo updates and 6 AUR
// ones, 2 of them held. The repo entries come first, so the AUR ones sit
// past index 200; the writer now lists the AUR entries inside the cap, but a
// reader must not depend on where they are.
func manyRepoCache() string {
	var b strings.Builder
	b.WriteString(`{"v":3,"count":473,"repo":469,"aur":4,"aur_held":2,"checked":"2026-11-02T10:00:00+01:00","packages":[`)
	for i := 0; i < 469; i++ {
		fmt.Fprintf(&b, `{"name":"r%d","from":"1-1","to":"2-1","src":"repo"},`, i)
	}
	b.WriteString(`{"name":"e1","from":"1-1","to":"2-1","src":"aur","held":false,"age":"5d"},` +
		`{"name":"h1","from":"1-1","to":"2-1","src":"aur","held":true,"age":"2d4h"},` +
		`{"name":"e2","from":"1-1","to":"2-1","src":"aur","held":false,"age":"4d"},` +
		`{"name":"h2","from":"1-1","to":"2-1","src":"aur","held":true,"age":"6h"},` +
		`{"name":"e3","from":"1-1","to":"2-1","src":"aur","held":false,"age":"3d"},` +
		`{"name":"e4","from":"1-1","to":"2-1","src":"aur","held":false,"age":"9d"}]}`)
	return b.String()
}

func TestParseHeldFindsTheHeldBehindManyRepoEntries(t *testing.T) {
	got := ParseHeld([]byte(manyRepoCache()))
	if len(got) != 2 || got[0].Name != "h1" || got[1].Name != "h2" {
		t.Fatalf("held %+v, want h1 and h2", got)
	}
}

func TestParseHeldKeepsAtMostMaxHeld(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"packages":[`)
	for i := 0; i < maxHeld+20; i++ {
		fmt.Fprintf(&b, `{"name":"a%d","from":"1","to":"2","src":"aur","held":true},`, i)
	}
	b.WriteString(`{"name":"z","from":"1","to":"2","src":"repo"}]}`)
	if n := len(ParseHeld([]byte(b.String()))); n != maxHeld {
		t.Fatalf("kept %d, want %d", n, maxHeld)
	}
}
