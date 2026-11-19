package health

import (
	"fmt"
	"strings"
	"testing"
)

// The held AUR names behind 469 repo entries (#300) are found, and the cap
// is on the names kept.
func TestParseCooldownHeldBehindManyRepoEntries(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"packages":[`)
	for i := 0; i < 469; i++ {
		fmt.Fprintf(&b, `{"name":"r%d","src":"repo"},`, i)
	}
	b.WriteString(`{"name":"h1","src":"aur","held":true},{"name":"e1","src":"aur","held":false},{"name":"h2","src":"aur","held":true}]}`)
	got := parseCooldownHeld([]byte(b.String()))
	if len(got) != 2 || !got["h1"] || !got["h2"] {
		t.Fatalf("held %v", got)
	}
}
