package plural

import (
	"os"
	"testing"

	"github.com/Enzord758/archmaid/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

func TestN(t *testing.T) {
	for _, c := range []struct {
		n    int
		want string
	}{{0, "0 steps"}, {1, "1 step"}, {2, "2 steps"}, {-1, "-1 step"}, {15, "15 steps"}} {
		if got := N(c.n, "step", "steps"); got != c.want {
			t.Errorf("N(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// TestRefused: the words every place that reports a run shares.
func TestRefused(t *testing.T) {
	for _, c := range []struct {
		n          int
		want, also string
	}{{0, "0 steps not authorised", ""}, {1, "1 step not authorised", " · 1 step not authorised"}, {5, "5 steps not authorised", " · 5 steps not authorised"}} {
		if got := Refused(c.n); got != c.want {
			t.Errorf("Refused(%d) = %q, want %q", c.n, got, c.want)
		}
		if got := AlsoRefused(c.n); got != c.also {
			t.Errorf("AlsoRefused(%d) = %q, want %q", c.n, got, c.also)
		}
	}
}
