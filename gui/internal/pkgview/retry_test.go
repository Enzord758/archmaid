package pkgview

import (
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/pkgdb"
)

func TestRetryAfterFailure(t *testing.T) {
	r := newRig(repoPkg("firefox", "1"))
	r.aur.err = errAUR
	r.s.SetQuery("fire")
	r.clock.Advance(time.Second)
	r.s.Retry()
	if len(r.aur.searches) != 2 {
		t.Fatalf("searches %q: Retry did not ask again at once", r.aur.searches)
	}
	r.aur.err, r.aur.hits = nil, []pkgdb.Package{aurPkg("fire-aur", "1")}
	r.s.Retry()
	if len(r.aur.searches) != 3 || r.s.Results().AURState != AURReady {
		t.Fatalf("retry after a failure: %q", r.aur.searches)
	}
	r.s.Retry() // after a success: nothing
	if len(r.aur.searches) != 3 {
		t.Fatalf("searches %q", r.aur.searches)
	}
}
