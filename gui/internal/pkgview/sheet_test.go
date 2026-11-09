package pkgview

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/steps"
)

func repoPlan() steps.PkgPlan {
	return steps.PkgPlan{Targets: []string{"gvim"}, Hash: "sha256:" + strings.Repeat("a", 64), Rows: []steps.PkgRow{
		{Name: "gvim", Version: "9.1-1", Repo: "extra", Target: true, Download: 3 << 20, Installed: 10 << 20},
		{Name: "gpm", Version: "1.20-1", Repo: "extra", Download: 100 << 10, Installed: 300 << 10},
	}}
}

// aurPlan is the shape actions.AURDiscover builds: Rows are the repository
// dependencies only, the AUR bases ride in plan.AUR.
func aurPlan(withRepoDeps bool) steps.PkgPlan {
	p := steps.PkgPlan{Targets: []string{"paru"}, Hash: "sha256:" + strings.Repeat("b", 64)}
	if withRepoDeps {
		p.Rows = append(p.Rows, steps.PkgRow{Name: "cargo", Version: "1.9-1", Repo: "extra", Target: true, Download: 20 << 20, Installed: 90 << 20})
	}
	p.AUR = []steps.AURPlan{{Base: "paru", Names: []string{"paru"}, Version: "2.0-1", Commit: "abc1234",
		Files: []steps.AURFile{{Path: "PKGBUILD", Kind: steps.AURFilePKGBUILD, Size: 40, Text: "pkgname=paru\n"}}}}
	return p
}

func TestPromptCount(t *testing.T) {
	if n := Prompts(aurPlan(false)); n != 1 {
		t.Errorf("a plain AUR package: %d prompts, want 1", n)
	}
	if n := Prompts(aurPlan(true)); n != 2 {
		t.Errorf("an AUR package with repo dependencies: %d prompts, want 2", n)
	}
	if n := Prompts(repoPlan()); n != 1 {
		t.Errorf("a repo install with dependencies: %d prompts, want 1", n)
	}
}

func startSheet(t *testing.T, req steps.PkgRequest) *Sheet {
	s := NewSheet()
	s.Begin(req)
	if s.State() != SheetPlanning || s.View().Body != "Checking the packages…" {
		t.Fatalf("state %v", s.State())
	}
	return s
}

func TestSheetRepoInstallFlow(t *testing.T) {
	s := startSheet(t, RequestFor(ModeInstall, []string{"gvim"}, false))
	s.DBAge = 3 * 24 * time.Hour
	s.Apply(Preview{Prompt: 4, Plan: repoPlan()})
	v := s.View()
	if v.State != SheetReview || !v.YesEnabled || v.YesLabel != "Install" || v.NoLabel != "Cancel" || v.Destructive {
		t.Fatalf("view %+v", v)
	}
	if len(v.Rows) != 2 || v.Rows[0].Kind != "new" || v.Rows[1].Kind != "dependency" {
		t.Fatalf("rows %+v", v.Rows)
	}
	if v.Totals != "Download 3.1 MiB · takes 10.3 MiB on disk" {
		t.Fatalf("totals %q", v.Totals)
	}
	var tags []string
	for _, n := range v.Notes {
		tags = append(tags, n.Tag)
	}
	if !slices.Contains(tags, "root") || !slices.Contains(tags, "asks") {
		t.Fatalf("notes %+v: want the root tag and the prompt count", v.Notes)
	}
	if !strings.Contains(joinNotes(v), "1 password prompt") || !strings.Contains(joinNotes(v), "3 days old") {
		t.Fatalf("notes %q", joinNotes(v))
	}
	p, ok := s.Confirm()
	if !ok || p != 4 || s.State() != SheetRunning {
		t.Fatalf("confirm %d %v state %v", p, ok, s.State())
	}
	if !s.Apply(RunStarted{}) || s.State() != SheetClosed {
		t.Fatal("RunStarted did not close the sheet and show the Run page")
	}
}

func joinNotes(v SheetView) string {
	var b strings.Builder
	for _, n := range v.Notes {
		b.WriteString(n.Text + "\n")
	}
	return b.String()
}

func TestSheetAURNeedsTheBoxes(t *testing.T) {
	s := startSheet(t, RequestFor(ModeInstall, []string{"paru"}, false))
	plan := aurPlan(true)
	plan.AUR[0].Audit = []steps.AURFinding{{Name: "paru", High: true, Reason: "downloads and runs a script"}, {Name: "paru", Reason: "no checksums"}}
	s.Apply(Preview{Prompt: 2, Plan: plan})
	v := s.View()
	if v.YesEnabled || len(v.Checks) != 2 {
		t.Fatalf("enabled %v checks %+v: an AUR install with a high finding needs both boxes", v.YesEnabled, v.Checks)
	}
	if !strings.Contains(joinNotes(v), "asked for your password up to twice") {
		t.Fatalf("the sheet must say what the two root steps cost up front: %q", joinNotes(v))
	}
	if _, ok := s.Confirm(); ok {
		t.Fatal("confirmed with nothing ticked")
	}
	s.Tick(CheckPKGBUILD, true)
	if s.CanConfirm() {
		t.Fatal("confirmed with the findings unaccepted")
	}
	s.Tick(CheckFindings, true)
	if !s.CanConfirm() || !s.View().YesEnabled {
		t.Fatal("both boxes ticked and still not enabled")
	}
	s.Tick(CheckPKGBUILD, false)
	if s.CanConfirm() {
		t.Fatal("unticking the PKGBUILD box left Install enabled")
	}
}

func TestSheetAURWithoutHighFindingHasOneBox(t *testing.T) {
	s := startSheet(t, RequestFor(ModeInstall, []string{"paru"}, false))
	plan := aurPlan(false)
	plan.AUR[0].Audit = []steps.AURFinding{{Name: "paru", Reason: "unusual source"}}
	s.Apply(Preview{Prompt: 2, Plan: plan})
	if v := s.View(); len(v.Checks) != 1 || v.Checks[0].ID != CheckPKGBUILD {
		t.Fatalf("checks %+v", v.Checks)
	}
	s.Tick(CheckPKGBUILD, true)
	if !s.CanConfirm() {
		t.Fatal("not confirmable with the one box ticked")
	}
	if !strings.Contains(joinNotes(s.View()), "1 password prompt") {
		t.Fatal("a plain AUR package must say 1 prompt")
	}
}

func TestSheetRemoval(t *testing.T) {
	s := startSheet(t, RequestFor(ModeRemove, []string{"vim"}, true))
	plan := steps.PkgPlan{Mode: steps.PkgRemove, WithConfig: true, Targets: []string{"vim"}, Rows: []steps.PkgRow{
		{Name: "vim", Version: "9-1", Target: true, Installed: 30 << 20},
		{Name: "vim-runtime", Version: "9-1", Installed: 20 << 20}}}
	s.Apply(Preview{Prompt: 1, Plan: plan})
	v := s.View()
	if v.YesLabel != "Remove" || !v.Destructive || !v.WithConfig || v.Totals != "Frees 50.0 MiB" {
		t.Fatalf("view %+v", v)
	}
	if v.Rows[0].Kind != "removed" || v.Heading != "Remove 1 package" {
		t.Fatalf("rows %+v heading %q", v.Rows, v.Heading)
	}
	// Updates waiting never block a removal.
	s.Blocked = 5
	if !s.CanConfirm() {
		t.Fatal("updates waiting blocked a removal")
	}
}

func TestSheetUpdatesWaitingBlockInstall(t *testing.T) {
	s := startSheet(t, RequestFor(ModeInstall, []string{"gvim"}, false))
	s.Blocked = 7
	s.Apply(Preview{Prompt: 1, Plan: repoPlan()})
	v := s.View()
	if v.YesEnabled || !v.UpdateFirst {
		t.Fatalf("enabled %v updateFirst %v", v.YesEnabled, v.UpdateFirst)
	}
	if !strings.Contains(joinNotes(v), "7 repository updates are waiting") {
		t.Fatalf("notes %q", joinNotes(v))
	}
	if _, ok := s.Confirm(); ok {
		t.Fatal("confirmed while updates are waiting")
	}
}

func TestSheetRefusedStates(t *testing.T) {
	cases := []struct {
		r       steps.PkgRefusal
		heading string
		in      string
	}{
		{steps.PkgRefusal{Reason: steps.RefuseConflict, Names: []string{"vim"}}, "It replaces an installed package", "Remove the old package first"},
		{steps.PkgRefusal{Reason: steps.RefuseProtected, Names: []string{"glibc"}}, "It would remove a protected package", "glibc is protected"},
		{steps.PkgRefusal{Reason: steps.RefuseRequired, Names: []string{"a", "b"}}, "Other packages need it", "a and b"},
		{steps.PkgRefusal{Reason: steps.RefuseNotFound, Names: []string{"nope"}, Detail: "dependency libx not found"}, "Not found", "dependency libx not found"},
		{steps.PkgRefusal{Reason: steps.RefuseBusy}, "pacman is busy", "Try again"},
	}
	for _, c := range cases {
		s := startSheet(t, RequestFor(ModeInstall, []string{"x"}, false))
		r := c.r
		s.Apply(Refused{Refusal: &r})
		v := s.View()
		if v.State != SheetRefused || v.Heading != c.heading || !strings.Contains(v.Body, c.in) || !strings.HasSuffix(v.Body, "Nothing changed.") {
			t.Errorf("%s: %+v", c.r.Reason, v)
		}
		if v.YesEnabled || v.NoLabel != "Close" {
			t.Errorf("%s: a refusal offers Install: %+v", c.r.Reason, v)
		}
		// Done after the refusal keeps the explanation up.
		s.Apply(Done{})
		if s.State() != SheetRefused {
			t.Errorf("%s: Finished closed the refusal", c.r.Reason)
		}
	}
}

func TestSheetUpdatesPendingFromTheHelper(t *testing.T) {
	s := startSheet(t, RequestFor(ModeInstall, []string{"x"}, false))
	s.Apply(UpdatesPending{Count: 1})
	v := s.View()
	if v.State != SheetPending || !v.UpdateFirst || v.Body != "1 repository update is waiting. Arch does not support installing on a partly updated system." {
		t.Fatalf("%+v", v)
	}
}

func TestSheetStaleReissues(t *testing.T) {
	req := RequestFor(ModeInstall, []string{"gvim"}, false)
	s := startSheet(t, req)
	s.Apply(Preview{Prompt: 1, Plan: repoPlan()})
	s.Confirm()
	s.Apply(Stale{Req: req, Plan: repoPlan()})
	v := s.View()
	if v.State != SheetStale || v.Retry != "Review again" || !strings.Contains(v.Body, "Nothing was installed or removed") {
		t.Fatalf("%+v", v)
	}
	got, ok := s.Retry()
	if !ok || !slices.Equal(got.Names, req.Names) || s.State() != SheetPlanning {
		t.Fatalf("retry %+v %v state %v", got, ok, s.State())
	}
	if _, ok := s.Retry(); ok {
		t.Fatal("Retry worked outside the stale state")
	}
}

func TestSheetDismiss(t *testing.T) {
	s := startSheet(t, RequestFor(ModeInstall, []string{"gvim"}, false))
	if _, answer, abort := s.Dismiss(); answer || !abort {
		t.Fatalf("planning: answer %v abort %v, want an abort", answer, abort)
	}
	s = startSheet(t, RequestFor(ModeInstall, []string{"gvim"}, false))
	s.Apply(Preview{Prompt: 9, Plan: repoPlan()})
	if p, answer, abort := s.Dismiss(); p != 9 || !answer || abort || s.Open() {
		t.Fatalf("review: %d %v %v", p, answer, abort)
	}
}

func TestSheetPreviewOnlyWhilePlanning(t *testing.T) {
	s := NewSheet()
	s.Apply(Preview{Prompt: 1, Plan: repoPlan()})
	if s.Open() {
		t.Fatal("a preview opened a sheet nobody asked for")
	}
	s = startSheet(t, RequestFor(ModeInstall, []string{"gvim"}, false))
	s.Apply(Done{})
	if s.Open() {
		t.Fatal("Finished left the planning sheet open")
	}
}

func TestSheetNeverDiscardsWarnings(t *testing.T) {
	s := startSheet(t, RequestFor(ModeInstall, []string{"gvim"}, false))
	p := repoPlan()
	p.Warnings = []string{"gpm is\nnew   on this system"}
	s.Apply(Preview{Prompt: 1, Plan: p})
	if !strings.Contains(joinNotes(s.View()), "gpm is new on this system") {
		t.Fatalf("%q", joinNotes(s.View()))
	}
}

// ---- the controller: gating and the whole flow ----

type ctlRig struct {
	c       *Controller
	be      *fakeBackend
	clock   *FakeClock
	updates overview.Updates
	runs    int
	reloads int
}

func newCtl(pkgs ...pkgdb.Package) *ctlRig {
	r := newRig(pkgs...)
	cr := &ctlRig{be: &fakeBackend{}, clock: r.clock}
	cr.c = NewController(Config{Search: r.s, Catalog: r.cat, Backend: cr.be, Clock: r.clock,
		Updates: func() overview.Updates { return cr.updates },
		DBAge:   func() time.Duration { return time.Hour },
		Reload:  func() bool { cr.reloads++; return true },
		ShowRun: func() { cr.runs++ }})
	return cr
}

func (cr *ctlRig) setUpdates(repo, aur int, age time.Duration) {
	cr.updates = overview.Updates{Known: true, Count: repo + aur, Repo: repo, AUR: aur, Checked: cr.clock.Now().Add(-age)}
	cr.c.RefreshUpdates()
}

func TestGatingBlocksInstallNotRemove(t *testing.T) {
	cr := newCtl()
	cr.setUpdates(3, 0, time.Hour)
	if g := cr.c.Gating(ModeInstall); !g.Blocked || g.Pending != 3 || g.Text != "3 repository updates are waiting" {
		t.Fatalf("gating %+v", g)
	}
	if cr.c.Gating(ModeRemove).Blocked {
		t.Fatal("updates waiting block a removal")
	}
	if err := cr.c.Now(repoPkg("gvim", "9"), ModeInstall); err != ErrBlocked {
		t.Fatalf("Now: %v", err)
	}
	if len(cr.be.started) != 0 || cr.c.Sheet.Open() {
		t.Fatal("a blocked install reached the engine")
	}
	if cr.c.List.Len() != 1 {
		t.Fatal("the blocked install was not kept in the list")
	}
	if err := cr.c.Now(installed(repoPkg("vim", "9")), ModeRemove); ErrKind(err) != AddOtherMode {
		t.Fatalf("a removal over the kept installs: %v", err)
	}
}

func TestGatingRules(t *testing.T) {
	cr := newCtl()
	cases := []struct {
		name         string
		repo, aur    int
		age          time.Duration
		known        bool
		wantBlocking bool
	}{
		{"fresh repo updates", 2, 0, time.Hour, true, true},
		{"AUR only does not block", 0, 4, time.Hour, true, false},
		{"stale cache does not block", 5, 0, 25 * time.Hour, true, false},
		{"just inside 24 h blocks", 5, 0, 24 * time.Hour, true, true},
		{"no cache does not block", 0, 0, 0, false, false},
		{"zero updates", 0, 0, time.Hour, true, false},
		{"future check does not block", 5, 0, -time.Hour, true, false},
	}
	for _, c := range cases {
		if c.known {
			cr.setUpdates(c.repo, c.aur, c.age)
		} else {
			cr.updates = overview.Updates{}
			cr.c.RefreshUpdates()
		}
		if got := cr.c.Gating(ModeInstall).Blocked; got != c.wantBlocking {
			t.Errorf("%s: blocked %v", c.name, got)
		}
	}
}

func TestUpdateFirstKeepsTheList(t *testing.T) {
	cr := newCtl()
	cr.setUpdates(2, 0, time.Hour)
	_ = cr.c.Add(repoPkg("gvim", "9"), ModeInstall)
	cr.c.UpdateFirst()
	if cr.be.updates != 1 || cr.c.List.Len() != 1 {
		t.Fatalf("updates %d list %d", cr.be.updates, cr.c.List.Len())
	}
	// The updates run finishes, the cache is re-read: Install works again.
	cr.updates = overview.Updates{Known: true, Checked: cr.clock.Now()}
	if !cr.c.RefreshUpdates() {
		t.Fatal("the new cache was not noticed")
	}
	if err := cr.c.Review(); err != nil || len(cr.be.started) != 1 {
		t.Fatalf("review after the update: %v, started %d", err, len(cr.be.started))
	}
}

func TestUpdateFirstFromAnOpenReviewAnswersNo(t *testing.T) {
	cr := newCtl()
	_ = cr.c.Now(repoPkg("gvim", "9"), ModeInstall)
	cr.c.Handle(Preview{Prompt: 3, Plan: repoPlan()})
	cr.c.UpdateFirst()
	if len(cr.be.answers) != 1 || cr.be.answers[0] != "3 no" || cr.c.Sheet.Open() {
		t.Fatalf("answers %v open %v", cr.be.answers, cr.c.Sheet.Open())
	}
}

func TestControllerHappyPath(t *testing.T) {
	cr := newCtl()
	if err := cr.c.Now(repoPkg("gvim", "9"), ModeInstall); err != nil {
		t.Fatal(err)
	}
	if len(cr.be.started) != 1 || cr.be.started[0].Names[0] != "gvim" {
		t.Fatalf("started %+v", cr.be.started)
	}
	cr.c.Handle(Preview{Prompt: 5, Plan: repoPlan()})
	if !cr.c.Confirm() || cr.be.answers[0] != "5 yes" {
		t.Fatalf("answers %v", cr.be.answers)
	}
	cr.c.Handle(RunStarted{})
	if cr.runs != 1 {
		t.Fatal("the Run page did not come up")
	}
	// A finished, successful run empties the list and re-reads the DBs.
	cr.c.Handle(Done{OK: true})
	if cr.c.List.Len() != 0 || cr.reloads != 1 {
		t.Fatalf("list %d reloads %d", cr.c.List.Len(), cr.reloads)
	}
}

// TestFailedRunKeepsTheList: B5, a failed run keeps the list but still reloads.
func TestFailedRunKeepsTheList(t *testing.T) {
	cr := newCtl()
	_ = cr.c.Now(repoPkg("gvim", "9"), ModeInstall)
	cr.c.Handle(Preview{Prompt: 1, Plan: repoPlan()})
	cr.c.Confirm()
	cr.c.Handle(RunStarted{})
	cr.c.Handle(Done{OK: false})
	// The list stays; the DBs are re-read (a failed run may have changed them).
	if cr.c.List.Len() != 1 || cr.reloads != 1 {
		t.Fatalf("list %d reloads %d", cr.c.List.Len(), cr.reloads)
	}
}

func TestRefusedKeepsTheListAndLearnsProtected(t *testing.T) {
	cr := newCtl(installed(repoPkg("nano", "1")))
	_ = cr.c.Now(installed(repoPkg("nano", "1")), ModeRemove)
	cr.c.Handle(Refused{Refusal: &steps.PkgRefusal{Reason: steps.RefuseProtected, Names: []string{"nano"}, Detail: "Needed to boot."}})
	cr.c.Handle(Done{})
	if cr.c.List.Len() != 1 || cr.c.Sheet.State() != SheetRefused {
		t.Fatalf("list %d sheet %v", cr.c.List.Len(), cr.c.Sheet.State())
	}
	cr.c.Dismiss()
	cr.c.List.Clear()
	rows, _ := cr.c.Installed.Rows()
	if len(rows) != 1 || rows[0].Removable || rows[0].Why != "Needed to boot." {
		t.Fatalf("the refused package is not greyed: %+v", rows)
	}
}

func TestStaleRetryReissues(t *testing.T) {
	cr := newCtl()
	_ = cr.c.Now(repoPkg("gvim", "9"), ModeInstall)
	cr.c.Handle(Preview{Prompt: 1, Plan: repoPlan()})
	cr.c.Confirm()
	cr.c.Handle(Stale{Req: cr.be.started[0], Plan: repoPlan()})
	cr.c.Handle(Done{})
	if cr.c.Sheet.State() != SheetStale {
		t.Fatalf("state %v", cr.c.Sheet.State())
	}
	cr.c.Retry()
	if len(cr.be.started) != 2 || cr.c.Sheet.State() != SheetPlanning {
		t.Fatalf("started %d state %v", len(cr.be.started), cr.c.Sheet.State())
	}
}

func TestSheetSeesTheDBAgeAndGate(t *testing.T) {
	cr := newCtl()
	cr.setUpdates(1, 0, time.Minute)
	cr.c.Sheet.Begin(RequestFor(ModeInstall, []string{"x"}, false))
	cr.c.Handle(Preview{Prompt: 1, Plan: repoPlan()})
	if v := cr.c.Sheet.View(); v.YesEnabled || !strings.Contains(joinNotes(v), "1 h old") {
		t.Fatalf("%+v %q", v, joinNotes(v))
	}
}

// An AUR plan's sheet lists the bases to build, then the repository
// dependencies installed first; with none, the totals say it is built here.
func TestSheetAURRows(t *testing.T) {
	s := startSheet(t, RequestFor(ModeInstall, []string{"paru"}, false))
	s.Apply(Preview{Prompt: 2, Plan: aurPlan(true)})
	v := s.View()
	if len(v.Rows) != 2 || v.Rows[0].Name != "paru" || v.Rows[0].Repo != "aur" || v.Rows[0].Kind != "built" || !v.Rows[0].Target ||
		v.Rows[1].Name != "cargo" || v.Rows[1].Kind != "dependency" {
		t.Fatalf("rows %+v", v.Rows)
	}
	if !strings.HasPrefix(v.Totals, "Download 20.0 MiB") {
		t.Fatalf("totals %q", v.Totals)
	}
	s = startSheet(t, RequestFor(ModeInstall, []string{"paru"}, false))
	s.Apply(Preview{Prompt: 2, Plan: aurPlan(false)})
	if v := s.View(); len(v.Rows) != 1 || !strings.Contains(v.Totals, "built by makepkg") {
		t.Fatalf("rows %+v totals %q", v.Rows, v.Totals)
	}
}

// The helper can still refuse after the confirm, with the run started and
// the sheet closed (a stale plan, pacman busy): the answer reopens it.
func TestSheetReopensForAnAnswerAfterTheRunStarted(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ev    Event
		state SheetState
	}{
		{"stale", Stale{Req: RequestFor(ModeInstall, []string{"gvim"}, false), Plan: repoPlan()}, SheetStale},
		{"busy", Refused{Refusal: &steps.PkgRefusal{Reason: steps.RefuseBusy}}, SheetRefused},
		{"pending", UpdatesPending{Count: 3}, SheetPending},
	} {
		s := startSheet(t, RequestFor(ModeInstall, []string{"gvim"}, false))
		s.Apply(Preview{Prompt: 2, Plan: repoPlan()})
		if _, ok := s.Confirm(); !ok {
			t.Fatalf("%s: not confirmable", tc.name)
		}
		s.Apply(RunStarted{})
		if s.State() != SheetClosed {
			t.Fatalf("%s: the sheet stays up over the Run page", tc.name)
		}
		s.Apply(tc.ev)
		if s.State() != tc.state {
			t.Fatalf("%s: state %v, want %v", tc.name, s.State(), tc.state)
		}
	}
	// With no request in flight an answer opens nothing.
	s := NewSheet()
	s.Apply(Stale{})
	if s.Open() {
		t.Fatal("a stale answer opened a sheet nobody asked for")
	}
}
