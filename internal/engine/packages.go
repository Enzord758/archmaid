package engine

import (
	"errors"
	"fmt"
	"slices"

	"github.com/Enzord758/archmaid/internal/actions"
	runreport "github.com/Enzord758/archmaid/internal/report"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The Packages page's run (docs/design/packages-contract.md, section 3).
//
// StartPackages(req) enters PhaseDiscovering ("Checking the packages…") and
// asks the helper for its plan (actions.PackagesDiscover). Then exactly one
// of:
//
//   - PackagesPreview{Prompt, Plan}, immediately followed by NeedsConfirm
//     {ID: Prompt, Kind: ConfirmPackages}: the sheet. Confirm(Prompt, true)
//     runs the queue (QueueStarted, the polkit step, Finished as any run);
//     false or a cancel ends it Finished{OutcomeCancelled}.
//   - PackagesRefused / PackagesUpdatesPending, then Finished
//     {OutcomeNotice}: the helper refused, nothing changed. (A plan that
//     could not be made at all is a DiscoveryResult, Notice, Finished.)
//
// A root run the helper finds stale or refused (the system changed after the
// confirm) is that step's failure (StepDone Failed) followed by
// PackagesStale, PackagesUpdatesPending or PackagesRefused, then Finished
// as the queue's end. Nothing was changed in any of these cases.

// PackagesPreview is the sheet's content: the helper's checked plan. It is
// followed at once by the NeedsConfirm that carries its Prompt.
type PackagesPreview struct {
	Prompt PromptID
	Plan   steps.PkgPlan
}

// PackagesRefused: the helper refused the request (Refusal.Reason: conflict,
// protected, not-found, not-installed, required-by, busy, bad-request).
// Refusal.Names are the packages it is about and Refusal.Detail a sanitized
// one-line reason. Nothing was changed.
type PackagesRefused struct {
	Req     steps.PkgRequest
	Refusal steps.PkgRefusal
}

// PackagesUpdatesPending: updates are waiting (Count of them, from the
// helper's pacman -Qu); an install is blocked until the system is updated.
// The page offers "Update system first" (Start{Action: actions.IDTopgrade}).
type PackagesUpdatesPending struct {
	Req   steps.PkgRequest
	Count int
}

// PackagesStale: the confirmed plan no longer matches (the helper ran
// nothing). Plan is the helper's new plan when it sent one, else nil. The
// page says "the list changed, review again" and may StartPackages(Req)
// again.
type PackagesStale struct {
	Req  steps.PkgRequest
	Plan *steps.PkgPlan
}

func (PackagesPreview) isEvent()        {}
func (PackagesRefused) isEvent()        {}
func (PackagesUpdatesPending) isEvent() {}
func (PackagesStale) isEvent()          {}

// StartPackages is Session.StartPackages.
type StartPackages struct{ Req steps.PkgRequest }

func (StartPackages) isCommand() {}

// StartPackages runs a Packages request: see the comment above. A call
// while an action runs is ignored, as Start.
func (s *Session) StartPackages(req steps.PkgRequest) Output {
	if s.run != nil {
		return Output{}
	}
	s.run = &run{action: actions.IDPackages, started: s.now(), cur: -1, pkgReq: req}
	var o Output
	s.run.discoverSeq = s.seq()
	s.setPhase(&o, PhaseDiscovering, "Checking the packages…")
	o.effect(Discover{Seq: s.run.discoverSeq, Action: actions.IDPackages,
		Run: actions.PackagesDiscover(req), Env: s.env})
	return o
}

// packagesPreview opens the sheet.
func (s *Session) packagesPreview(o *Output, d actions.PkgPreview) {
	r := s.run
	p := d.Plan
	r.pkgSteps = d.Steps
	r.prompt, r.promptFor = s.promptID(), promptPackages
	title, body, yes := packagesQuestion(p)
	o.event(PackagesPreview{Prompt: r.prompt, Plan: p})
	o.event(NeedsConfirm{
		ID: r.prompt, Kind: ConfirmPackages, Title: title, Body: body, Msg: title + "\n\n" + body,
		Items: shown(p.Targets), Destructive: p.Mode == steps.PkgRemove, YesLabel: yes,
	})
}

// packagesQuestion is the sheet's question in words, from the checked plan.
func packagesQuestion(p steps.PkgPlan) (title, body, yes string) {
	var dl, inst int64
	for _, r := range p.Rows {
		dl += r.Download
		inst += r.Installed
	}
	n := len(p.Rows)
	if p.Mode == steps.PkgRemove {
		title, yes = fmt.Sprintf("Remove %s?", count(len(p.Targets), "package")), "Remove"
		body = fmt.Sprintf("%s will be removed", count(n, "package"))
		if n > len(p.Targets) {
			body += fmt.Sprintf(", %d of them dependencies nothing else needs", n-len(p.Targets))
		}
		if inst > 0 {
			body += ", freeing " + runreport.HumanBytes(uint64(inst))
		}
		if p.WithConfig {
			body += ". Their configuration files are deleted too"
		}
		body += "."
	} else {
		title, yes = fmt.Sprintf("Install %s?", count(len(p.Targets), "package")), "Install"
		body = fmt.Sprintf("%s will be installed", count(n, "package"))
		if n > len(p.Targets) {
			body += fmt.Sprintf(", %d of them dependencies", n-len(p.Targets))
		}
		if dl > 0 {
			body += ": " + runreport.HumanBytes(uint64(dl)) + " to download"
		}
		if inst > 0 {
			body += ", " + runreport.HumanBytes(uint64(inst)) + " on disk"
		}
		body += "."
	}
	body += "\n\nIt runs as root and asks for your password. Once it has started it can't be stopped."
	for _, w := range p.Warnings {
		body += "\n" + sanitize.OneLine(w)
	}
	return title, body, yes
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// packagesRefused ends the run at its discovery with the helper's refusal.
func (s *Session) packagesRefused(o *Output, d actions.PkgRefused) {
	s.refusalEvent(o, d.Req, d.Refusal)
	s.finish(o, OutcomeNotice)
}

// refusalEvent publishes the event a refusal stands for.
func (s *Session) refusalEvent(o *Output, req steps.PkgRequest, ref *steps.PkgRefusal) {
	switch ref.Reason {
	case steps.RefuseUpdatesPending:
		o.event(PackagesUpdatesPending{Req: req, Count: ref.Count})
	case steps.RefuseStale:
		o.event(PackagesStale{Req: req, Plan: ref.Plan})
	default:
		cp := *ref
		cp.Plan = nil
		cp.Detail = sanitize.Clip(sanitize.OneLine(cp.Detail), maxDetailLen)
		o.event(PackagesRefused{Req: req, Refusal: cp})
	}
}

// packagesStepFailed turns a package step's refusal (the helper refused or
// found the plan stale after the confirm) into its event, after the step's
// StepDone. Other failures are only the step's.
func (s *Session) packagesStepFailed(o *Output, err error) {
	var ref *steps.PkgRefusal
	if err == nil || !errors.As(err, &ref) || s.run.action != actions.IDPackages {
		return
	}
	s.refusalEvent(o, s.run.pkgReq, ref)
}

// packagesNotice ends a Packages run whose plan could not be made: a
// DiscoveryResult (nothing logged: it is not a menu action's record), the
// Notice and Finished{OutcomeNotice}.
func (s *Session) packagesNotice(o *Output, n actions.Notice) {
	outcome, errText := DiscoveryNothing, ""
	if n.Err != "" {
		outcome, errText = DiscoveryFailed, sanitize.OneLine(n.Err)
	}
	text := sanitize.OneLine(n.Text)
	s.emitResult(o, outcome, text, errText, "")
	msg := text
	if errText != "" {
		msg += " " + errText
	}
	o.event(Notice{Text: sanitize.String(msg, sanitize.Plain)})
	s.finish(o, OutcomeNotice)
}

// answerPackages is the sheet's answer.
func (s *Session) answerPackages(o *Output, yes bool) {
	r := s.run
	if !yes {
		s.finish(o, OutcomeCancelled)
		return
	}
	queue := slices.Clone(r.pkgSteps)
	r.pkgSteps = nil
	s.plan(o, queue, "")
}
