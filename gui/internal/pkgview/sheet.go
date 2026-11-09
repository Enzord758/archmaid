package pkgview

import (
	"fmt"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Event is what the engine tells the page about a packages request. The
// contract's engine events (PackagesPreview + NeedsConfirm, PackagesRefused,
// PackagesUpdatesPending, PackagesStale, QueueStarted, Finished) map one to
// one; the ui package holds the one adapter (packages_adapter.go).
type Event interface{ isEvent() }

// Preview is PackagesPreview followed by NeedsConfirm{ID: Prompt}: the
// sheet's content. Plan.AUR is the AUR review payload (empty for repo
// packages).
type Preview struct {
	Prompt int
	Plan   steps.PkgPlan
}

// Refused is PackagesRefused: nothing changed.
type Refused struct {
	Req     steps.PkgRequest
	Refusal *steps.PkgRefusal
}

// UpdatesPending is PackagesUpdatesPending: the helper's `pacman -Qu` is
// not empty.
type UpdatesPending struct{ Count int }

// Stale is PackagesStale: the plan changed between the confirm and the run.
type Stale struct {
	Req  steps.PkgRequest
	Plan steps.PkgPlan
}

// RunStarted is QueueStarted: the run is going, the Run page takes over.
type RunStarted struct{}

// Done is Finished: the request is over (any outcome).
type Done struct{ OK bool }

func (Preview) isEvent()        {}
func (Refused) isEvent()        {}
func (UpdatesPending) isEvent() {}
func (Stale) isEvent()          {}
func (RunStarted) isEvent()     {}
func (Done) isEvent()           {}

// Backend is what the page asks of the engine (engine.StartPackages,
// engine.Confirm, engine.Abort and engine.Start{Action: topgrade}).
type Backend interface {
	StartPackages(req steps.PkgRequest)
	Answer(prompt int, yes bool)
	Abort()
	UpdateSystem()
}

// HighFindings counts the high audit findings over all the plans' bases.
func HighFindings(rs []steps.AURPlan) int {
	n := 0
	for _, r := range rs {
		for _, f := range r.Audit {
			if f.High {
				n++
			}
		}
	}
	return n
}

// SheetState is where the confirm sheet is.
type SheetState int

const (
	SheetClosed   SheetState = iota
	SheetPlanning            // "Checking the packages…"
	SheetReview              // the plan, waiting for the answer
	SheetRefused             // refused with a reason
	SheetPending             // updates are waiting
	SheetStale               // the list changed: review again
	SheetRunning             // confirmed, waiting for the run to start
)

// Check ids.
const (
	CheckPKGBUILD = "pkgbuild"
	CheckFindings = "findings"
)

// Note is a line of the sheet's "Before you start".
type Note struct {
	Tag  string // "root", "asks", "blocked", "AUR", ""
	Tone Tone
	Text string
}

// Check is a box the user ticks before Install enables.
type Check struct {
	ID    string
	Label string
	On    bool
}

// SheetRow is one package of the plan.
type SheetRow struct {
	Name, Version, Old, Repo string
	Target                   bool
	Size                     string
	Kind                     string // "new", "upgrade", "dependency", "removed"
}

// SheetView is everything the sheet draws.
type SheetView struct {
	State    SheetState
	Mode     Mode
	Heading  string
	Body     string // planning, refused, pending and stale: the one message
	Rows     []SheetRow
	Totals   string
	Notes    []Note
	Reviews  []steps.AURPlan
	Checks   []Check
	YesLabel string
	NoLabel  string
	// YesEnabled: the primary button works (all required boxes ticked,
	// nothing blocks). Cancel is always the default.
	YesEnabled bool
	// UpdateFirst: show "Update system first".
	UpdateFirst bool
	// Retry is the primary button of the stale state ("Review again").
	Retry string
	// Destructive: a removal (the button is red).
	Destructive bool
	// WithConfig: the removal also deletes config files.
	WithConfig bool
}

// Sheet is the confirm sheet's state machine.
type Sheet struct {
	state   SheetState
	req     steps.PkgRequest
	plan    steps.PkgPlan
	hasPlan bool
	prompt  int
	reviews []steps.AURPlan
	refusal *steps.PkgRefusal
	pending int
	ticks   map[string]bool
	// ran: the run started after the confirm, so the sheet is closed while
	// the helper may still refuse it (a stale plan, a conflict that came
	// up, pacman busy): those answers reopen it.
	ran bool

	// Inputs the controller keeps current.
	DBAge   time.Duration // < 0: unknown
	Blocked int           // repo updates waiting by the cache, 0: none (install only)
	// Dry: the run is a dry run (nothing asks for a password). Kept: the
	// GUI keeps the run's first password, so a second root step does not ask
	// again (the Driver's KeepsPassword and no PATH-guard KeepOff).
	Dry, Kept bool
}

// NewSheet makes a closed sheet.
func NewSheet() *Sheet { return &Sheet{DBAge: -1, ticks: map[string]bool{}} }

// State is the sheet's state.
func (s *Sheet) State() SheetState { return s.state }

// Open: the sheet shows something.
func (s *Sheet) Open() bool { return s.state != SheetClosed }

// Req is the request the sheet was begun with.
func (s *Sheet) Req() steps.PkgRequest { return s.req }

// Prompt is the confirm's id (SheetReview).
func (s *Sheet) Prompt() int { return s.prompt }

// Begin opens the sheet for req, in the planning state.
func (s *Sheet) Begin(req steps.PkgRequest) {
	s.state, s.req, s.hasPlan = SheetPlanning, req, false
	s.reviews, s.refusal, s.pending, s.prompt, s.ran = nil, nil, 0, 0, false
	s.ticks = map[string]bool{}
}

// Apply folds an engine event in. It returns whether the Run page should
// come up (RunStarted).
func (s *Sheet) Apply(ev Event) (showRun bool) {
	switch e := ev.(type) {
	case Preview:
		if s.state != SheetPlanning {
			return false
		}
		s.state, s.plan, s.hasPlan, s.prompt, s.reviews = SheetReview, e.Plan, true, e.Prompt, e.Plan.AUR
	case Refused:
		if s.state == SheetClosed && !s.ran {
			return false
		}
		s.state, s.refusal = SheetRefused, e.Refusal
	case UpdatesPending:
		if s.state == SheetClosed && !s.ran {
			return false
		}
		s.state, s.pending = SheetPending, e.Count
	case Stale:
		if s.state == SheetClosed && !s.ran {
			return false
		}
		s.state, s.req, s.plan, s.hasPlan = SheetStale, e.Req, e.Plan, true
	case RunStarted:
		if s.state == SheetRunning || s.state == SheetReview {
			s.state = SheetClosed
		}
		s.ran = true
		return true
	case Done:
		switch s.state {
		case SheetPlanning, SheetReview, SheetRunning:
			s.state = SheetClosed
		}
	}
	return false
}

// Tick sets a check box.
func (s *Sheet) Tick(id string, on bool) {
	if s.state == SheetReview {
		s.ticks[id] = on
	}
}

// Prompts is the number of password prompts the run costs: one for the
// install or removal, plus one first for the repo dependencies of an AUR
// build (contract section 5 and the lead's confirmation). The sheet says so
// up front. An AUR plan's Rows are the repository dependencies only
// (actions.AURDiscover), so rows mean the first prompt.
func Prompts(plan steps.PkgPlan) int {
	if len(plan.AUR) > 0 && plan.Mode != steps.PkgRemove && len(plan.Rows) > 0 {
		return 2
	}
	return 1
}

// DryNoPassword is the sheet's password note in a dry run (the gate's words).
const DryNoPassword = "Nothing asks for a password or authorisation in a dry run."

// Checks are the boxes the sheet shows now.
func (s *Sheet) checks() []Check {
	if s.state != SheetReview || len(s.reviews) == 0 {
		return nil
	}
	cs := []Check{{CheckPKGBUILD, "I read the PKGBUILD. I understand this runs the package author's code as root.", s.ticks[CheckPKGBUILD]}}
	if HighFindings(s.reviews) > 0 {
		cs = append(cs, Check{CheckFindings, "I've read the findings and accept them.", s.ticks[CheckFindings]})
	}
	return cs
}

// CanConfirm: the review state, every box ticked, nothing blocking.
func (s *Sheet) CanConfirm() bool {
	if s.state != SheetReview {
		return false
	}
	if s.req.Mode == steps.PkgInstall && s.Blocked > 0 {
		return false
	}
	for _, c := range s.checks() {
		if !c.On {
			return false
		}
	}
	return true
}

// Confirm answers the engine's confirm yes, when it may. It returns the
// prompt and true; the sheet then waits for the run (SheetRunning).
func (s *Sheet) Confirm() (prompt int, ok bool) {
	if !s.CanConfirm() {
		return 0, false
	}
	s.state = SheetRunning
	return s.prompt, true
}

// Dismiss closes the sheet from the user's side. In the review state the
// engine's confirm is answered no (the returned prompt, answer true);
// while planning the request is aborted (abort true). In every other
// state it just closes.
func (s *Sheet) Dismiss() (prompt int, answer, abort bool) {
	switch s.state {
	case SheetReview:
		p := s.prompt
		s.state = SheetClosed
		return p, true, false
	case SheetPlanning:
		s.state = SheetClosed
		return 0, false, true
	}
	s.state = SheetClosed
	return 0, false, false
}

// Retry reopens a stale sheet for the same request ("Review again").
func (s *Sheet) Retry() (steps.PkgRequest, bool) {
	if s.state != SheetStale {
		return steps.PkgRequest{}, false
	}
	req := s.req
	s.Begin(req)
	return req, true
}

// View is what the sheet draws now.
func (s *Sheet) View() SheetView {
	v := SheetView{State: s.state, Mode: ModeInstall, NoLabel: "Cancel", YesLabel: "Install"}
	remove := s.req.Mode == steps.PkgRemove
	if remove {
		v.Mode, v.YesLabel, v.Destructive, v.WithConfig = ModeRemove, "Remove", true, s.req.WithConfig
	}
	names := len(s.req.Names)
	v.Heading = fmt.Sprintf("%s %s", map[bool]string{true: "Remove", false: "Install"}[remove], N(names, "package", "packages"))
	switch s.state {
	case SheetPlanning:
		v.Body = "Checking the packages…"
		v.NoLabel = "Cancel"
	case SheetRefused:
		v.Heading, v.Body = RefusalHeading(s.refusal), RefusalTextFor(s.refusal, s.req.Names)
		v.NoLabel = "Close"
		if s.refusal != nil && s.refusal.Reason == steps.RefuseUpdatesPending {
			v.UpdateFirst = true
		}
	case SheetPending:
		v.Heading = "Update the system first"
		v.Body = fmt.Sprintf("%s waiting. Arch does not support installing on a partly updated system.", N(s.pending, "repository update is", "repository updates are"))
		v.NoLabel, v.UpdateFirst = "Close", true
	case SheetStale:
		v.Heading = "The list changed"
		v.Body = "The packages changed since you looked. Nothing was installed or removed. Review the list again."
		v.NoLabel, v.Retry = "Close", "Review again"
	case SheetReview, SheetRunning:
		s.fillReview(&v)
	}
	return v
}

func (s *Sheet) fillReview(v *SheetView) {
	p := s.plan
	remove := p.Mode == steps.PkgRemove
	var dl, inst int64
	// An AUR plan lists the packages to build first, in build order; its
	// Rows are the repository dependencies installed before them.
	targets := map[string]bool{}
	for _, t := range p.Targets {
		targets[t] = true
	}
	for _, a := range p.AUR {
		for _, n := range a.Names {
			v.Rows = append(v.Rows, SheetRow{Name: n, Version: a.Version, Repo: pkgdb.RepoAUR, Target: targets[n], Kind: "built", Size: "built here"})
		}
	}
	for _, r := range p.Rows {
		row := SheetRow{Name: r.Name, Version: r.Version, Old: r.OldVersion, Repo: r.Repo, Target: r.Target}
		switch {
		case remove:
			row.Kind, row.Size = "removed", Size(r.Installed)
		case r.OldVersion != "":
			row.Kind, row.Size = "upgrade", Size(r.Download)
		case r.Target && len(p.AUR) == 0:
			row.Kind, row.Size = "new", Size(r.Download)
		default:
			row.Kind, row.Size = "dependency", Size(r.Download)
		}
		dl += r.Download
		inst += r.Installed
		v.Rows = append(v.Rows, row)
	}
	switch {
	case remove:
		v.Totals = "Frees " + Size(inst)
	case len(p.AUR) > 0 && len(p.Rows) == 0:
		v.Totals = "Downloaded and built by makepkg on this machine"
	default:
		v.Totals = "Download " + Size(dl) + " · takes " + Size(inst) + " on disk"
	}
	v.Reviews, v.Checks = s.reviews, s.checks()
	v.Notes = s.notes(p, remove)
	v.YesEnabled = s.state == SheetReview && s.CanConfirm()
	if !remove && s.Blocked > 0 {
		v.UpdateFirst = true
	}
}

func (s *Sheet) notes(p steps.PkgPlan, remove bool) []Note {
	var ns []Note
	ns = append(ns, Note{"root", ToneWarn, "Runs as root, after you authorise it. Once it has started it can't be stopped."})
	prompts := Prompts(p)
	switch {
	case s.Dry:
		ns = append(ns, Note{"asks", ToneNeutral, DryNoPassword})
	case prompts == 2 && s.Kept:
		ns = append(ns, Note{"asks", ToneNeutral, "Two root steps (dependencies, then the AUR). You're asked for your password once."})
	case prompts == 2:
		ns = append(ns, Note{"asks", ToneNeutral, "Two root steps (dependencies, then the AUR). You're asked for your password up to twice."})
	default:
		ns = append(ns, Note{"asks", ToneNeutral, "1 password prompt."})
	}
	if s.DBAge >= 0 {
		t := ToneNeutral
		if s.DBAge > 7*24*time.Hour {
			t = ToneWarn
		}
		ns = append(ns, Note{"", t, "Package lists are " + Age(s.DBAge) + " old. archmaid does not refresh them."})
	}
	if !remove && s.Blocked > 0 {
		ns = append(ns, Note{"blocked", ToneWarn, N(s.Blocked, "repository update is", "repository updates are") + " waiting. Update the system first."})
	}
	if remove {
		if p.WithConfig {
			ns = append(ns, Note{"", ToneNeutral, "Also deletes the packages' config files."})
		}
		ns = append(ns, Note{"", ToneNeutral, "Dependencies nothing else needs go with it."})
	}
	if len(s.reviews) > 0 {
		ns = append(ns, Note{"AUR", ToneNeutral, "Not reviewed by Arch. The build runs as you; the install runs the author's code as root."})
	}
	for _, w := range p.Warnings {
		ns = append(ns, Note{"", ToneWarn, oneLine(w)})
	}
	return ns
}

// oneLine is a helper's warning cut to one plain line of at most 200 runes.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:199]) + "…"
	}
	return s
}

// RefusalHeading is a refusal's title.
func RefusalHeading(r *steps.PkgRefusal) string {
	if r == nil {
		return "Refused"
	}
	switch r.Reason {
	case steps.RefuseConflict:
		return "It replaces an installed package"
	case steps.RefuseProtected:
		return "It would remove a protected package"
	case steps.RefuseRequired:
		return "Other packages need it"
	case steps.RefuseNotFound:
		return "Not found"
	case steps.RefuseNotInstalled:
		return "Not installed"
	case steps.RefuseBusy:
		return "pacman is busy"
	case steps.RefuseUpdatesPending:
		return "Update the system first"
	case steps.RefuseStale:
		return "The list changed"
	}
	return "Refused"
}

// joinAnd is "A", "A and B" or "A, B and C"; past six names the rest count.
func joinAnd(n []string) string {
	if len(n) > 6 {
		return strings.Join(n[:6], ", ") + fmt.Sprintf(" and %d more", len(n)-6)
	}
	if len(n) == 1 {
		return n[0]
	}
	return strings.Join(n[:len(n)-1], ", ") + " and " + n[len(n)-1]
}

// RefusalText is the reason in a few plain words, with the names.
func RefusalText(r *steps.PkgRefusal) string { return RefusalTextFor(r, nil) }

// RefusalTextFor is RefusalText for a request of the given names: a helper
// detail that ends on "then install" gets the name it lacks, and every text
// ends on a full stop.
func RefusalTextFor(r *steps.PkgRefusal, req []string) string {
	if r == nil {
		return "The request was refused. Nothing changed."
	}
	names := strings.Join(r.Names, ", ")
	if len(r.Names) > 6 {
		names = strings.Join(r.Names[:6], ", ") + fmt.Sprintf(" and %d more", len(r.Names)-6)
	}
	detail := sentence(oneLine(r.Detail))
	if strings.HasSuffix(detail, "then install.") && len(req) > 0 {
		detail = strings.TrimSuffix(detail, "then install.") + "then install " + strings.Join(req, ", ") + "."
	}
	var s string
	switch r.Reason {
	case steps.RefuseConflict:
		s = "It would replace or remove an installed package"
		if names != "" {
			s += " (" + names + ")"
		}
		s += ". Remove the old package first, then install this one."
		if detail != "" && strings.HasSuffix(detail, ".") && strings.Contains(detail, "then install ") {
			s = detail // the helper names both packages
			detail = ""
		}
	case steps.RefuseProtected:
		switch {
		case detail != "":
			s, detail = detail, "" // "sudo is protected (reason) and is never removed here."
		case names != "":
			s = names + " is protected: removing it could leave the system unusable."
		default:
			s = "A protected package would be removed, which could leave the system unusable."
		}
	case steps.RefuseRequired:
		// One sentence: the package being removed, and what needs it.
		what, by := "It", "other installed packages"
		if len(req) > 0 {
			what = joinAnd(req)
		}
		if len(r.Names) > 0 {
			by = joinAnd(r.Names)
		}
		s = what + " is still needed by " + by + "."
		detail = "" // the helper's detail says the same
	case steps.RefuseNotFound:
		s = orIt(names) + " is not in the package lists, by that exact name."
	case steps.RefuseNotInstalled:
		s = orIt(names) + " is not installed."
	case steps.RefuseBusy:
		s = "Another package operation is running. Try again when it is done."
	case steps.RefuseUpdatesPending:
		s = N(r.Count, "repository update is", "repository updates are") + " waiting. Update the system first."
	default:
		s = "The request was refused."
	}
	if detail != "" && !strings.Contains(s, strings.TrimSuffix(detail, ".")) {
		s += " " + detail
	}
	return s + " Nothing changed."
}

// orIt is names, or "It" when there are none, so a text never starts blank.
func orIt(names string) string {
	if names == "" {
		return "It"
	}
	return names
}

// sentence ends s with a full stop (an empty s stays empty).
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}
