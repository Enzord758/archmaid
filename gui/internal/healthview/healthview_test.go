package healthview

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

func rep(checks ...Check) Report { return Report{Checks: checks} }

func TestAttentionCountsLookOnly(t *testing.T) {
	r := rep(Check{State: Look}, Check{State: Unknown}, Check{State: OK}, Check{State: Look})
	if r.Attention() != 2 {
		t.Errorf("attention %d, want 2 (unknown never counts)", r.Attention())
	}
	for n, want := range map[int]string{1: "1 thing needs your attention", 2: "2 things need your attention", 5: "5 things need your attention"} {
		if got := Attention(n); got != want {
			t.Errorf("Attention(%d) = %q", n, got)
		}
	}
}

func TestCellFor(t *testing.T) {
	if c := CellFor(nil, true); c.Line != "Checking…" || c.Emphasised || c.Tag != "" {
		t.Errorf("first run: %+v", c)
	}
	if c := CellFor(nil, false); c != (Cell{}) {
		t.Errorf("nothing yet: %+v", c)
	}
	calm := rep(Check{State: OK}, Check{State: Unknown})
	if c := CellFor(&calm, false); c != (Cell{}) {
		t.Errorf("calm: %+v", c)
	}
	if c := CellFor(&calm, true); c != (Cell{}) {
		t.Errorf("calm, rechecking: %+v", c) // a re-run keeps the last answer
	}
	look := rep(Check{State: Look}, Check{State: Look})
	if c := CellFor(&look, false); !c.Emphasised || c.Tag != "look" || c.Line != "2 things need your attention" {
		t.Errorf("look: %+v", c)
	}
}

func TestSortedLookUnknownOKInGroupOrder(t *testing.T) {
	r := rep(
		Check{ID: "disk", State: OK, Group: "hardware"},
		Check{ID: "pacnew", State: Look, Group: "packages"},
		Check{ID: "mirror", State: Unknown, Group: "packages"},
		Check{ID: "units", State: OK, Group: "system"},
		Check{ID: "news", State: Look, Group: "packages"},
		Check{ID: "restart", State: Look, Group: "system"},
		Check{ID: "smart", State: Unknown, Group: "hardware"},
		Check{ID: "time", State: OK, Group: "system"},
	)
	var got []string
	for _, c := range r.Sorted() {
		got = append(got, c.ID)
	}
	want := []string{"restart", "pacnew", "news", "mirror", "smart", "units", "time", "disk"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
	if r.Checks[0].ID != "disk" {
		t.Error("Sorted changed the report")
	}
}

func TestSections(t *testing.T) {
	r := rep(
		Check{ID: "disk", State: OK, Group: "hardware"},
		Check{ID: "pacnew", State: Look, Group: "packages"},
		Check{ID: "mirror", State: Unknown, Group: "packages"},
		Check{ID: "units", State: OK, Group: "system"},
		Check{ID: "restart", State: Look, Group: "system"},
		Check{ID: "aur", State: OK, Group: "packages"},
		Check{ID: "x", State: OK, Group: ""},
	)
	var got []string
	for _, s := range r.Sections() {
		ids := ""
		for _, c := range s.Checks {
			ids += " " + c.ID
		}
		got = append(got, s.Header+":"+ids)
	}
	want := []string{"Needs your attention: restart pacnew", "System: units", "Packages: mirror aur", "Hardware: disk", "Other: x"}
	if len(got) != len(want) {
		t.Fatalf("%q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("section %d: %q, want %q", i, got[i], want[i])
		}
	}
	calm := rep(Check{ID: "a", State: OK, Group: "system"})
	if s := calm.Sections(); len(s) != 1 || s[0].Header != "System" {
		t.Errorf("calm: %+v", s)
	}
}

func TestAgoAndSubtitle(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{0: "just now", 80 * time.Second: "just now", 5 * time.Minute: "5 min ago", 3 * time.Hour: "3 h ago", 50 * time.Hour: "2 d ago"} {
		if got := Ago(now.Add(-d), now); got != want {
			t.Errorf("Ago(%v) = %q, want %q", d, got, want)
		}
	}
	r := Report{At: now.Add(-5 * time.Minute)}
	if got := Subtitle(&r, now); got != "Read-only: nothing is changed and no password is asked. Checked 5 min ago." {
		t.Errorf("subtitle %q", got)
	}
	if got := Subtitle(nil, now); got != "Read-only: nothing is changed and no password is asked." {
		t.Errorf("subtitle without a report %q", got)
	}
}

func TestSafeLink(t *testing.T) {
	for link, want := range map[string]bool{
		"https://archlinux.org/news/foo/":             true,
		"http://archlinux.org/news/":                  false,
		"https://archlinux.org":                       false,
		"https://evil.example/https://archlinux.org/": false,
		"https://archlinux.org.evil.example/x":        false,
		"https://archlinux.org/a b":                   false,
		"file:///etc/passwd":                          false,
		"":                                            false,
		"https://archlinux.org/é":                     false,
	} {
		if got := SafeLink(link); got != want {
			t.Errorf("SafeLink(%q) = %v, want %v", link, got, want)
		}
	}
}

// Never two runs at once: of many goroutines asking, one is let through
// until it ends.
func TestRunnerNeverTwoAtOnce(t *testing.T) {
	var r Runner
	var granted, peak, now atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !r.Begin() {
				return
			}
			granted.Add(1)
			n := now.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			now.Add(-1)
			r.End(time.Now())
		}()
	}
	wg.Wait()
	if peak.Load() != 1 || granted.Load() < 1 {
		t.Errorf("peak %d concurrent, %d granted", peak.Load(), granted.Load())
	}
	if r.Running() {
		t.Error("still running")
	}
}

func TestRunnerStale(t *testing.T) {
	var r Runner
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	if !r.Stale(now, 5*time.Minute) {
		t.Error("never run is stale")
	}
	r.Begin()
	r.End(now)
	if r.Stale(now.Add(4*time.Minute), 5*time.Minute) || !r.Stale(now.Add(6*time.Minute), 5*time.Minute) {
		t.Error("5 minute rule")
	}
}
