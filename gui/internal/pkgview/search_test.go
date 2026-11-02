package pkgview

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/pkgdb"
)

func names(ps []pkgdb.Package) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func TestDebounceRepoAndAUR(t *testing.T) {
	r := newRig(repoPkg("firefox", "1"), repoPkg("firefox-developer-edition", "2"))
	r.aur.hits = []pkgdb.Package{aurPkg("firefox-nightly", "9")}

	r.s.SetQuery("fi")
	if r.s.RepoRuns() != 0 || r.s.AURRuns() != 0 {
		t.Fatal("a search started before its debounce")
	}
	r.clock.Advance(RepoDebounce - time.Millisecond)
	if r.s.RepoRuns() != 0 {
		t.Fatal("the repo search started 1 ms early")
	}
	r.clock.Advance(time.Millisecond)
	if r.s.RepoRuns() != 1 || r.s.AURRuns() != 0 {
		t.Fatalf("at 120 ms: repo %d aur %d, want 1 and 0", r.s.RepoRuns(), r.s.AURRuns())
	}
	r.clock.Advance(AURDebounce - RepoDebounce - time.Millisecond)
	if r.s.AURRuns() != 0 {
		t.Fatal("the AUR search started before 500 ms")
	}
	r.clock.Advance(time.Millisecond)
	if r.s.AURRuns() != 1 {
		t.Fatal("no AUR search at 500 ms")
	}
	res := r.s.Results()
	if res.AURState != AURReady || len(res.Repo) != 2 || len(res.AUR) != 1 {
		t.Fatalf("results %+v", res)
	}
}

func TestTypingResetsTheDebounce(t *testing.T) {
	r := newRig(repoPkg("firefox", "1"))
	r.s.SetQuery("fi")
	r.clock.Advance(100 * time.Millisecond)
	r.s.SetQuery("fir")
	r.clock.Advance(100 * time.Millisecond)
	if r.s.RepoRuns() != 0 {
		t.Fatal("the first keystroke's timer fired")
	}
	r.clock.Advance(20 * time.Millisecond)
	if r.s.RepoRuns() != 1 || len(r.cat.calls) != 1 || r.cat.calls[0] != "fir" {
		t.Fatalf("runs %d calls %q, want one search for fir", r.s.RepoRuns(), r.cat.calls)
	}
	r.clock.Advance(time.Second)
	if len(r.aur.searches) != 1 || r.aur.searches[0] != "fir" {
		t.Fatalf("AUR searches %q, want one for the last term", r.aur.searches)
	}
}

func TestShortTermSearchesNothing(t *testing.T) {
	r := newRig(repoPkg("firefox", "1"))
	for _, q := range []string{"", " ", "f", "  f  "} {
		r.s.SetQuery(q)
		r.clock.Advance(2 * time.Second)
		if res := r.s.Results(); res.Active || len(res.Repo) != 0 {
			t.Errorf("%q: active results %+v", q, res)
		}
	}
	if len(r.cat.calls) != 0 || len(r.aur.searches) != 0 {
		t.Fatalf("searched for a short term: repo %q aur %q", r.cat.calls, r.aur.searches)
	}
	// Two runes count as two characters, not bytes.
	r.s.SetQuery("é")
	r.clock.Advance(2 * time.Second)
	if len(r.aur.searches) != 0 {
		t.Fatal("one rune of two bytes searched the AUR")
	}
	r.s.SetQuery("éa")
	r.clock.Advance(2 * time.Second)
	if len(r.aur.searches) != 1 {
		t.Fatal("two runes did not search the AUR")
	}
}

func TestNewTermCancelsTheAURRequest(t *testing.T) {
	r := newRig()
	r.aur.hits = []pkgdb.Package{aurPkg("aaa", "1")}
	r.s.SetQuery("aa")
	r.clock.Advance(AURDebounce)
	if len(r.aur.ctxs) != 1 {
		t.Fatal("no request")
	}
	first := r.aur.ctxs[0]
	// With the inline fake the first request already finished and its
	// context was released; a request still in flight is cancelled by the
	// next term: simulate by starting one that the worker has not returned.
	held := newRig()
	var release func()
	held.s = NewSearch(Deps{Catalog: held.cat, AUR: held.aur, Clock: held.clock,
		Go:   func(f func()) { release = f },
		Post: func(f func()) { f() }, Changed: func() {}})
	held.s.SetQuery("bb")
	held.clock.Advance(AURDebounce)
	run := release
	ctx := held.s.aurStopCtx()
	held.s.SetQuery("bbb")
	if ctx.Err() == nil {
		t.Fatal("the request in flight was not cancelled by a new term")
	}
	run() // the old worker returns late: its result is dropped
	if res := held.s.Results(); res.AURState == AURReady || len(res.AUR) != 0 {
		t.Fatalf("a cancelled request's result landed: %+v", res)
	}
	_ = first
}

func TestShortenedTermCancelsToo(t *testing.T) {
	held := newRig()
	var release func()
	held.s = NewSearch(Deps{Catalog: held.cat, AUR: held.aur, Clock: held.clock,
		Go: func(f func()) { release = f }, Post: func(f func()) { f() }, Changed: func() {}})
	held.s.SetQuery("bb")
	held.clock.Advance(AURDebounce)
	ctx := held.s.aurStopCtx()
	held.s.SetQuery("b")
	if ctx.Err() == nil {
		t.Fatal("backspacing to one character left the request running")
	}
	release()
	if held.s.Results().AURState != AURIdle {
		t.Fatalf("state %v after a short term", held.s.Results().AURState)
	}
}

func TestMergeOrderAndDedupe(t *testing.T) {
	inst := installed(aurPkg("yay", "12.0"))
	inst.InstalledVersion = "11.0"
	foreign := installed(pkgdb.Package{Name: "yay", Version: "11.0", Foreign: true})
	foreign.Repo = ""
	r := newRig(repoPkg("vim", "9"), repoPkg("vimb", "3"), foreign)
	r.aur.hits = []pkgdb.Package{aurPkg("vim", "AUR-dup"), aurPkg("vim-aur", "1"), inst}

	r.s.SetQuery("vi")
	r.clock.Advance(time.Second)
	res := r.s.Results()
	if got := names(res.Repo); !slices.Equal(got, []string{"vim", "vimb"}) {
		t.Fatalf("repo %q", got)
	}
	if got := names(res.AUR); !slices.Equal(got, []string{"vim-aur", "yay"}) {
		t.Fatalf("AUR %q: a repo name must go, an AUR name stay", got)
	}
	if got := names(res.AllHits()); !slices.Equal(got, []string{"vim", "vimb", "vim-aur", "yay"}) {
		t.Fatalf("order %q, want repo first then AUR", got)
	}
	y := res.AUR[1]
	if !y.Installed || y.InstalledVersion != "11.0" || !y.Update {
		t.Fatalf("an installed AUR hit lost its state: %+v", y)
	}
}

func TestMergeCaps(t *testing.T) {
	var many []pkgdb.Package
	var raw []pkgdb.Package
	for i := 0; i < 80; i++ {
		many = append(many, repoPkg(fmt.Sprintf("lib%02d", i), "1"))
		raw = append(raw, aurPkg(fmt.Sprintf("aur%02d", i), "1"))
	}
	r := newRig(many...)
	r.aur.hits = raw
	r.s.SetQuery("lib")
	r.clock.Advance(time.Second)
	res := r.s.Results()
	if len(res.Repo) != RepoMax || len(res.AUR) != AURMax {
		t.Fatalf("repo %d aur %d, want %d and %d", len(res.Repo), len(res.AUR), RepoMax, AURMax)
	}
}

func TestAURFailureKeepsRepoResults(t *testing.T) {
	r := newRig(repoPkg("firefox", "1"))
	r.aur.err = errAUR
	r.s.SetQuery("fire")
	r.clock.Advance(time.Second)
	res := r.s.Results()
	if res.AURState != AURFailed || res.AURNote == "" || len(res.AUR) != 0 {
		t.Fatalf("AUR state %v note %q", res.AURState, res.AURNote)
	}
	if got := names(res.Repo); !slices.Equal(got, []string{"firefox"}) {
		t.Fatalf("repo results hidden by the AUR failure: %q", got)
	}
}

func TestStaleRepoResultIsDropped(t *testing.T) {
	r := newRig(repoPkg("firefox", "1"), repoPkg("foot", "2"))
	var pending []func()
	r.s = NewSearch(Deps{Catalog: r.cat, AUR: r.aur, Clock: r.clock,
		Go: func(f func()) { pending = append(pending, f) }, Post: func(f func()) { f() }, Changed: func() {}})
	r.s.SetQuery("fire")
	r.clock.Advance(RepoDebounce)
	r.s.SetQuery("foot")
	r.clock.Advance(RepoDebounce)
	if len(pending) < 2 {
		t.Fatalf("workers %d", len(pending))
	}
	// The newer one finishes first, then the old one: it must not win.
	pending[len(pending)-1]()
	pending[0]()
	if got := names(r.s.Results().Repo); !slices.Equal(got, []string{"foot"}) {
		t.Fatalf("results %q, want only the newer term's", got)
	}
}

func TestNoAURClientIsDisabled(t *testing.T) {
	r := newRig(repoPkg("firefox", "1"))
	r.s = NewSearch(Deps{Catalog: r.cat, Clock: r.clock, Go: func(f func()) { f() }, Post: func(f func()) { f() }})
	r.s.SetQuery("fire")
	r.clock.Advance(time.Second)
	if r.s.Results().AURState != AURDisabled || len(r.s.Results().Repo) != 1 {
		t.Fatalf("%+v", r.s.Results())
	}
}

func TestControlCharactersLeaveTheTerm(t *testing.T) {
	r := newRig(repoPkg("firefox", "1"))
	r.s.SetQuery("fi\x1b[31mre\n")
	r.clock.Advance(time.Second)
	if len(r.aur.searches) != 1 || r.aur.searches[0] != "fi[31mre" {
		t.Fatalf("term sent: %q", r.aur.searches)
	}
}

func TestOverlongTermSkipsTheAUR(t *testing.T) {
	r := newRig()
	long := ""
	for i := 0; i < 70; i++ {
		long += "a"
	}
	r.s.SetQuery(long)
	r.clock.Advance(time.Second)
	if len(r.aur.searches) != 0 {
		t.Fatal("a term over 64 bytes went to the AUR")
	}
}

func TestDetailAURAsksInfoOnce(t *testing.T) {
	full := aurPkg("paru", "2.0")
	full.AUR.MakeDepends = []pkgdb.Dep{{Name: "cargo", Raw: "cargo"}}
	full.Depends = []pkgdb.Dep{{Name: "git", Raw: "git"}, {Name: "libalpm", Raw: "libalpm>=15"}}
	full.AUR.OutOfDate = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	r := newRig(installed(repoPkg("git", "2")))
	r.aur.info["paru"] = full
	r.s.Select(aurPkg("paru", "2.0"))
	d := r.s.Detail()
	if len(r.aur.infos) != 1 || !d.AUR || d.Loading {
		t.Fatalf("infos %v detail %+v", r.aur.infos, d)
	}
	rows := map[string]DepRow{}
	for _, x := range d.Deps {
		rows[x.Name] = x
	}
	if !rows["git"].Installed || rows["git"].PulledIn {
		t.Errorf("git: %+v", rows["git"])
	}
	if !rows["libalpm"].PulledIn {
		t.Errorf("libalpm should be pulled in: %+v", rows["libalpm"])
	}
	if !rows["cargo"].Build || !rows["cargo"].PulledIn {
		t.Errorf("cargo: %+v", rows["cargo"])
	}
	if d.Notice != AURNotice || d.Flagged != "Flagged out of date on 2026-10-01" || d.Link != "aur.archlinux.org/packages/paru" {
		t.Errorf("AUR facts: %q %q %q", d.Notice, d.Flagged, d.Link)
	}
	// Selecting a repo package asks the AUR nothing.
	r.s.Select(repoPkg("git", "2"))
	if len(r.aur.infos) != 1 {
		t.Fatal("a repo package's detail called the AUR")
	}
}

func TestDetailAURInfoFailure(t *testing.T) {
	r := newRig()
	r.aur.err = errAUR
	r.s.Select(aurPkg("paru", "2.0"))
	d := r.s.Detail()
	if d.Err == "" || d.Loading || !d.AUR {
		t.Fatalf("%+v", d)
	}
}

func TestRowTags(t *testing.T) {
	cases := []struct {
		p    pkgdb.Package
		want []Tag
	}{
		{repoPkg("a", "1"), []Tag{{"extra", ToneNeutral}}},
		{func() pkgdb.Package { p := repoPkg("a", "1"); p.Repo = "cachyos-v3"; return p }(), []Tag{{"cachyos-v3", ToneNeutral}}},
		{installed(repoPkg("a", "1")), []Tag{{"extra", ToneNeutral}, {"installed", ToneOK}}},
		{func() pkgdb.Package { p := installed(repoPkg("a", "1")); p.Update = true; return p }(),
			[]Tag{{"extra", ToneNeutral}, {"installed", ToneOK}, {"update", ToneWarn}}},
		{aurPkg("a", "1"), []Tag{{"AUR", ToneNeutral}}},
	}
	for _, c := range cases {
		if got := RowTags(c.p); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v want %v", c.p.Name, got, c.want)
		}
	}
}
