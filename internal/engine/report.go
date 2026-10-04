package engine

import (
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// A report, as the TUI's audit screen: OutputReport shows the text (and the
// audit as data), and is the action's end. "Yes" on one with an Accept runs
// it as a hook (the audit's snapshot save for its high findings), says how
// that went (ReportAccepted, the TUI's notice text) and finishes; "no", a
// cancel, an Abort or "yes" with nothing to accept dismiss it. Either way
// the action ends OutcomeReport, with no last-run record, as in the TUI.

// report is an open report: the discovery, its prompt, and the hook
// accepting it while that runs.
type report struct {
	d         actions.Report
	id        PromptID
	acceptSeq uint64
}

// openReport shows d.
func (s *Session) openReport(o *Output, d actions.Report) {
	r := s.run
	r.prompt, r.promptFor = s.promptID(), promptReport
	r.report = &report{d: d, id: r.prompt}
	o.event(OutputReport{ID: r.prompt, Text: sanitize.String(d.Text, sanitize.Plain), Audit: auditReport(d)})
}

// auditReport is d's audit as data, sanitized; nil if d isn't an audit.
func auditReport(d actions.Report) *AuditReport {
	res := d.Audit
	if res == nil {
		return nil
	}
	ar := &AuditReport{Total: res.Total, CanAccept: d.Accept != nil}
	for _, f := range res.Findings {
		af := AuditFinding{Name: sanitize.QuoteName(f.Name, audit.FieldMax), High: f.High, Reason: sanitize.OneLine(f.Reason)}
		for _, fl := range f.Flags {
			af.Flags = append(af.Flags, sanitize.OneLine(fl))
		}
		ar.Findings = append(ar.Findings, af)
	}
	return ar
}

// answerReport takes Confirm's answer to the open report.
func (s *Session) answerReport(o *Output, yes bool) {
	r := s.run
	if yes && r.report.d.Accept != nil {
		r.report.acceptSeq = s.seq()
		o.effect(RunHook{Seq: r.report.acceptSeq, Fn: r.report.d.Accept})
		return
	}
	s.finish(o, OutcomeReport)
}

// hookDone ends a report whose Accept ran. Other hooks need nothing: one
// that fails is logged by the hook, and never blocks its step.
func (s *Session) hookDone(o *Output, in HookDone) {
	r := s.run
	if r == nil || r.report == nil || in.Seq == 0 || in.Seq != r.report.acceptSeq {
		return
	}
	ev := ReportAccepted{ID: r.report.id, Saved: true, Text: "Maintainer changes accepted: they won't be flagged again."}
	if in.Err != nil {
		ev.Saved = false
		ev.Text = "Snapshot not saved (" + audit.ShortErr(in.Err, 0) + "): the changes stay flagged."
	}
	o.event(ev)
	s.finish(o, OutcomeReport)
}
