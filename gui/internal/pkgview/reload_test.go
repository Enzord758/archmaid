package pkgview

import (
	"testing"
)

// TestReloadRefreshesResultsAndDetail (B5): after the databases changed, the
// open detail and the search results show the new state and the selection
// stays; unchanged databases leave everything alone.
func TestReloadRefreshesResultsAndDetail(t *testing.T) {
	r := newRig(repoPkg("preload", "0.6"), repoPkg("help2man", "1.49"))
	changed := true
	calls := 0
	c := NewController(Config{Search: r.s, Catalog: r.cat, Clock: r.clock,
		Reload: func() bool { calls++; return changed }})
	r.s.SetQuery("preload")
	r.clock.Advance(RepoDebounce)
	if h := r.s.Results().Repo; len(h) != 1 || h[0].Installed {
		t.Fatalf("results %+v", h)
	}
	r.s.Select(r.s.Results().Repo[0])
	if r.s.Detail().Pkg.Installed {
		t.Fatal("preload starts not installed")
	}

	// pacman installs it behind the page's back.
	r.cat.pkgs[0] = installed(r.cat.pkgs[0])
	c.Reload()
	if calls != 1 {
		t.Fatalf("reload calls %d", calls)
	}
	if h := r.s.Results().Repo; len(h) != 1 || !h[0].Installed {
		t.Errorf("the result row is stale: %+v", h)
	}
	d := r.s.Detail()
	if !d.Valid || d.Pkg.Name != "preload" || !d.Pkg.Installed {
		t.Errorf("the detail is stale or lost: %+v", d.Pkg)
	}

	// Unchanged databases: nothing is redone.
	changed = false
	r.cat.pkgs[0].Installed = false
	c.Reload()
	if !r.s.Detail().Pkg.Installed {
		t.Error("an unchanged DB refreshed the detail")
	}

	// The package disappears from every database: the detail clears.
	changed = true
	r.cat.pkgs = r.cat.pkgs[1:]
	c.Reload()
	if r.s.Detail().Valid {
		t.Error("the detail of a vanished package stays")
	}
}

// TestReloadCoalesces: a reload asked for while one runs is run once more
// after it, not dropped and not run in parallel.
func TestReloadCoalesces(t *testing.T) {
	r := newRig()
	var jobs []func()
	calls := 0
	c := NewController(Config{Search: r.s, Catalog: r.cat, Clock: r.clock,
		Reload: func() bool { calls++; return false },
		Go:     func(f func()) { jobs = append(jobs, f) }})
	c.Reload()
	c.Reload()
	c.Reload()
	if len(jobs) != 1 {
		t.Fatalf("%d jobs started at once", len(jobs))
	}
	jobs[0]()
	if len(jobs) != 2 {
		t.Fatalf("%d jobs after the first: the queued reload was lost or repeated", len(jobs))
	}
	jobs[1]()
	if calls != 2 || len(jobs) != 2 {
		t.Fatalf("calls %d jobs %d", calls, len(jobs))
	}
}
