package engine_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/actions"
	. "github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/enginetest"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The Packages page's run (packages.go): the transcripts of
// docs/design/packages-contract.md section 3 on a Session under polkit, the
// helper's plan handed in as the Discovery the actions would return.

var installReq = steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{"foo"}}

func installPlan() steps.PkgPlan {
	return steps.PkgPlan{Mode: steps.PkgInstall, Targets: []string{"foo"},
		Hash: "sha256:" + strings.Repeat("a", 64),
		Rows: []steps.PkgRow{
			{Name: "foo", Version: "1.2-1", Repo: "extra", Target: true, Download: 3 << 20, Installed: 12 << 20},
			{Name: "libfoo", Version: "4.0-2", Repo: "core", Download: 1 << 20, Installed: 4 << 20},
		}}
}

func removePlan(config bool) steps.PkgPlan {
	return steps.PkgPlan{Mode: steps.PkgRemove, WithConfig: config, Targets: []string{"foo"},
		Hash: "sha256:" + strings.Repeat("b", 64),
		Rows: []steps.PkgRow{
			{Name: "foo", Version: "1.2-1", Target: true, Installed: 48 << 20},
			{Name: "libfoo", Version: "1.2-1", Installed: 6 << 20},
		}}
}

func pkgHarness(t *testing.T) *harness {
	t.Helper()
	return newHarness(t, Options{Auth: AuthPolkit})
}

func (h *harness) startPackages(req steps.PkgRequest) Output {
	h.t.Helper()
	return h.do(h.s.StartPackages(req))
}

// preview plans p and returns the sheet's Output.
func (h *harness) preview(req steps.PkgRequest, p steps.PkgPlan) Output {
	h.t.Helper()
	h.startPackages(req)
	return h.discovered(actions.PkgPreview{Plan: p, Steps: actions.Packages(p)})
}

// TestPackagesStartDiscovers: StartPackages checks the packages in a
// Discover effect named after the Packages action; a second Start is
// ignored while it runs.
func TestPackagesStartDiscovers(t *testing.T) {
	h := pkgHarness(t)
	o := h.startPackages(installReq)
	d := effect[Discover](t, o)
	if d.Action != actions.IDPackages || d.Run == nil {
		t.Errorf("Discover = %+v", d)
	}
	if ph := event[PhaseChanged](t, o); ph.Phase != PhaseDiscovering || ph.Busy == "" {
		t.Errorf("phase = %+v", ph)
	}
	noOutput(t, "StartPackages while it runs", h.do(h.s.StartPackages(installReq)))
	noOutput(t, "Start while it runs", h.start(actions.IDTopgrade))
}

// TestPackagesInstallTranscript: the sheet, the yes, the faillock read, the
// one polkit step with the plan, its end.
func TestPackagesInstallTranscript(t *testing.T) {
	h := pkgHarness(t)
	p := installPlan()
	o := h.preview(installReq, p)
	pv := event[PackagesPreview](t, o)
	nc := event[NeedsConfirm](t, o)
	if pv.Prompt != nc.ID || nc.Kind != ConfirmPackages || nc.Default || nc.Destructive || nc.YesLabel != "Install" {
		t.Errorf("preview %+v, confirm %+v", pv, nc)
	}
	if len(pv.Plan.Rows) != 2 || nc.Items[0] != "foo" {
		t.Errorf("plan %+v, items %v", pv.Plan, nc.Items)
	}
	for _, want := range []string{"Install 1 package?", "2 packages", "1 of them dependencies", "can't be stopped", "asks for your password"} {
		if !strings.Contains(nc.Msg, want) {
			t.Errorf("sheet text lacks %q:\n%s", want, nc.Msg)
		}
	}
	o = h.confirm(true)
	if qs := event[QueueStarted](t, o); qs.Action != actions.IDPackages || len(qs.Steps) != 1 || qs.Steps[0].ID != steps.IDPkgInstall ||
		qs.Steps[0].Verb != steps.PkgVerbInstall || !qs.Steps[0].NeedsPriv {
		t.Errorf("QueueStarted = %+v", qs)
	}
	if _, ok := find[ReadFaillock](o.Effects); !ok {
		t.Fatalf("no faillock read before the polkit prompt: %#v", o.Effects)
	}
	o = h.handle(FaillockRead{Deny: 3})
	st := effect[StartStep](t, o).Step
	if st.PkgPlan == nil || st.PkgPlan.Hash != p.Hash || st.Verb != steps.PkgVerbInstall {
		t.Errorf("the step lost its plan: %+v", st)
	}
	o = h.ok()
	if d := doneOf(o)[steps.IDPkgInstall]; d.Status != steps.OK {
		t.Errorf("StepDone = %+v", d)
	}
	if fin := event[Finished](t, o); fin.Outcome != OutcomeCompleted || fin.Errs != 0 || fin.Action != actions.IDPackages {
		t.Errorf("Finished = %+v", fin)
	}
	if rec := h.lastRun(); rec.Outcome != logstore.OutcomeOK || rec.Action != actions.IDPackages {
		t.Errorf("record = %+v", rec)
	}
	enginetest.CheckContract(t, h.evs)
}

// TestPackagesRemoveSheet: a removal is Destructive with its own words, and
// -Rns says the configuration goes too.
func TestPackagesRemoveSheet(t *testing.T) {
	h := pkgHarness(t)
	req := steps.PkgRequest{Mode: steps.PkgRemove, Names: []string{"foo"}, WithConfig: true}
	nc := event[NeedsConfirm](t, h.preview(req, removePlan(true)))
	if !nc.Destructive || nc.YesLabel != "Remove" || nc.Default || nc.Kind != ConfirmPackages {
		t.Errorf("confirm = %+v", nc)
	}
	for _, want := range []string{"Remove 1 package?", "1 of them dependencies nothing else needs", "freeing 54 MiB", "configuration files are deleted"} {
		if !strings.Contains(nc.Msg, want) {
			t.Errorf("sheet text lacks %q:\n%s", want, nc.Msg)
		}
	}
	o := h.confirm(true)
	h.handle(FaillockRead{Deny: 3})
	if st := effect[StartStep](t, h.out).Step; st.ID != steps.IDPkgRemove || st.Verb != steps.PkgVerbRemove || !st.PkgPlan.WithConfig {
		t.Errorf("step = %+v", st)
	}
	_ = o
}

// TestPackagesDeclined: no, and a cancel, end the run cancelled before any
// step or password, and leave no last-run record (nothing ran).
func TestPackagesDeclined(t *testing.T) {
	for name, answer := range map[string]func(*harness, PromptID) Output{
		"no":     func(h *harness, id PromptID) Output { return h.do(h.s.Confirm(id, false)) },
		"cancel": func(h *harness, id PromptID) Output { return h.cancel(id) },
	} {
		t.Run(name, func(t *testing.T) {
			h := pkgHarness(t)
			id := event[NeedsConfirm](t, h.preview(installReq, installPlan())).ID
			o := answer(h, id)
			if fin := event[Finished](t, o); fin.Outcome != OutcomeCancelled {
				t.Errorf("Finished = %+v", fin)
			}
			if _, ok := find[StartStep](o.Effects); ok {
				t.Error("a step started after a no")
			}
			if _, ok := find[WriteLastRun](o.Effects); ok {
				t.Error("a declined sheet wrote last-run.json")
			}
			enginetest.CheckContract(t, h.evs)
		})
	}
}

// TestPackagesAbortAtTheSheet: Abort withdraws the sheet.
func TestPackagesAbortAtTheSheet(t *testing.T) {
	h := pkgHarness(t)
	id := event[NeedsConfirm](t, h.preview(installReq, installPlan())).ID
	o := h.abort()
	if pc := event[PromptClosed](t, o); pc.ID != id {
		t.Errorf("PromptClosed = %+v", pc)
	}
	if fin := event[Finished](t, o); fin.Outcome != OutcomeCancelled {
		t.Errorf("Finished = %+v", fin)
	}
	enginetest.CheckContract(t, h.evs)
}

// TestPackagesRefusedAtPlan: the helper's refusal (conflict, protected, …)
// is PackagesRefused and an end with nothing run and no sheet.
func TestPackagesRefusedAtPlan(t *testing.T) {
	for _, reason := range []steps.RefuseReason{steps.RefuseConflict, steps.RefuseProtected, steps.RefuseNotFound,
		steps.RefuseRequired, steps.RefuseBusy, steps.RefuseBadRequest} {
		t.Run(string(reason), func(t *testing.T) {
			h := pkgHarness(t)
			h.startPackages(installReq)
			ref := &steps.PkgRefusal{Reason: reason, Names: []string{"vim"}, Detail: "gvim replaces vim\x1b[31m"}
			o := h.discovered(actions.PkgRefused{Req: installReq, Refusal: ref})
			pr := event[PackagesRefused](t, o)
			if pr.Refusal.Reason != reason || pr.Refusal.Names[0] != "vim" || pr.Req.Names[0] != "foo" || strings.Contains(pr.Refusal.Detail, "\x1b") {
				t.Errorf("PackagesRefused = %+v (the detail is shown, so it is cleaned)", pr)
			}
			if _, ok := find[NeedsConfirm](o.Events); ok {
				t.Error("a sheet for a refused request")
			}
			if fin := event[Finished](t, o); fin.Outcome != OutcomeNotice {
				t.Errorf("Finished = %+v", fin)
			}
			enginetest.CheckContract(t, h.evs)
		})
	}
}

// TestPackagesUpdatesPendingAtPlan: its own event with the count.
func TestPackagesUpdatesPendingAtPlan(t *testing.T) {
	h := pkgHarness(t)
	h.startPackages(installReq)
	o := h.discovered(actions.PkgRefused{Req: installReq,
		Refusal: &steps.PkgRefusal{Reason: steps.RefuseUpdatesPending, Count: 7}})
	if up := event[PackagesUpdatesPending](t, o); up.Count != 7 || up.Req.Names[0] != "foo" {
		t.Errorf("PackagesUpdatesPending = %+v", up)
	}
	if _, ok := find[PackagesRefused](o.Events); ok {
		t.Error("updates pending also came as a plain refusal")
	}
	event[Finished](t, o)
	enginetest.CheckContract(t, h.evs)
}

// TestPackagesStaleAfterTheConfirm: the helper recomputed a different plan
// as root and ran nothing: the step fails, PackagesStale carries the new
// plan for "review again", and the queue ends with that one failure.
func TestPackagesStaleAfterTheConfirm(t *testing.T) {
	h := pkgHarness(t)
	h.preview(installReq, installPlan())
	h.confirm(true)
	h.handle(FaillockRead{Deny: 3})
	newer := installPlan()
	newer.Rows = append(newer.Rows, steps.PkgRow{Name: "libnew", Version: "1-1", Repo: "extra"})
	o := h.handle(StepExited{Seq: h.seq(), Err: &steps.PkgRefusal{Reason: steps.RefuseStale, Detail: "stale", Plan: &newer}})
	if d := doneOf(o)[steps.IDPkgInstall]; d.Status != steps.Failed {
		t.Errorf("StepDone = %+v, want Failed", d)
	}
	ps := event[PackagesStale](t, o)
	if ps.Plan == nil || len(ps.Plan.Rows) != 3 || ps.Req.Names[0] != "foo" {
		t.Errorf("PackagesStale = %+v", ps)
	}
	if fin := event[Finished](t, o); fin.Outcome != OutcomeCompleted || fin.Errs != 1 {
		t.Errorf("Finished = %+v", fin)
	}
	enginetest.CheckContract(t, h.evs)
}

// TestPackagesRefusedByTheRootVerb: updates pending or protected found only
// under lock (the system changed after the confirm) are the same events
// after the step's failure; an ordinary failure has none.
func TestPackagesRefusedByTheRootVerb(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"updates": {&steps.PkgRefusal{Reason: steps.RefuseUpdatesPending, Count: 2}, "PackagesUpdatesPending"},
		"protect": {&steps.PkgRefusal{Reason: steps.RefuseProtected, Names: []string{"glibc"}, Detail: "protected"}, "PackagesRefused"},
		"plain":   {errors.New("root helper (exit 1): pacman failed"), ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := pkgHarness(t)
			h.preview(installReq, installPlan())
			h.confirm(true)
			h.handle(FaillockRead{Deny: 3})
			o := h.handle(StepExited{Seq: h.seq(), Err: tc.err})
			got := ""
			if _, ok := find[PackagesUpdatesPending](o.Events); ok {
				got = "PackagesUpdatesPending"
			}
			if _, ok := find[PackagesRefused](o.Events); ok {
				got = "PackagesRefused"
			}
			if got != tc.want {
				t.Errorf("event %q, want %q", got, tc.want)
			}
			if fin := event[Finished](t, o); fin.Errs != 1 {
				t.Errorf("Finished = %+v", fin)
			}
			enginetest.CheckContract(t, h.evs)
		})
	}
}

// TestPackagesPolkitCancel: the polkit dialog cancelled after the sheet is
// the user's no, as for every helper step.
func TestPackagesPolkitCancel(t *testing.T) {
	h := pkgHarness(t)
	h.preview(installReq, installPlan())
	h.confirm(true)
	h.handle(FaillockRead{Deny: 3})
	seq := h.seq()
	np := event[NeedsPassword](t, h.handle(AskpassRequest{Req: 1, Prompt: "Password: ", Polkit: true,
		Requester: "polkit's agent (pkttyagent)", Action: "org.archmaid.helper.pkg-install"}))
	h.cancel(np.ID)
	o := h.handle(StepExited{Seq: seq, Err: exit127, NotGranted: true})
	if d := doneOf(o)[steps.IDPkgInstall]; d.Status != steps.Skipped {
		t.Errorf("StepDone = %+v, want Skipped", d)
	}
	if fin := event[Finished](t, o); fin.Outcome != OutcomeCancelled || fin.Refused != 0 {
		t.Errorf("Finished = %+v", fin)
	}
	enginetest.CheckContract(t, h.evs)
}

// TestPackagesNoticeWhenThePlanFails: a plan that could not be made at all
// (no helper, a protocol error) is a failed discovery with a Notice, and
// writes no log.
func TestPackagesNoticeWhenThePlanFails(t *testing.T) {
	h := pkgHarness(t)
	h.startPackages(installReq)
	o := h.discovered(actions.Notice{Text: "Couldn't plan the package change.", Err: "the root helper is not installed"})
	if dr := event[DiscoveryResult](t, o); dr.Outcome != DiscoveryFailed || dr.Err == "" || dr.Category != "" {
		t.Errorf("DiscoveryResult = %+v", dr)
	}
	if n := event[Notice](t, o); !strings.Contains(n.Text, "not installed") {
		t.Errorf("Notice = %+v", n)
	}
	for _, fx := range o.Effects {
		if _, ok := fx.(WriteLog); ok {
			t.Error("a log entry for a packages notice")
		}
	}
	if fin := event[Finished](t, o); fin.Outcome != OutcomeNotice {
		t.Errorf("Finished = %+v", fin)
	}
	enginetest.CheckContract(t, h.evs)
}
