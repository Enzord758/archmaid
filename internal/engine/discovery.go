package engine

import (
	"slices"
	"strings"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// The discovery record (owner's decision): an action that ends at its
// discovery — nothing found, or the look failed — leaves a record like any
// other run. Its DiscoveryResult says what was checked; its log entry
// (WriteLog) goes to the action's discovery category (actions.Action.
// Record), so the run directory holds only discovery logs and never takes
// a real run's place in the history (logstore, KeepRuns); last-run.json
// says "nothing-found" or "failed" (record). A discovery that finds
// something emits a Found result at once, and its record is written into
// the log of the queue it leads to, when that starts (startQueue): a run
// that changes something is a real run from its first entry on, even if
// an Abort later leaves its step unlogged.

// discoveryRecord is a Found discovery's record, held until its queue
// starts.
type discoveryRecord struct {
	summary, body string
}

// maxDetailLen bounds one detail line in an event.
const maxDetailLen = 300

// cleanDetails is details as an Event carries them: plain, one line each,
// clipped.
func cleanDetails(details []string) []string {
	out := make([]string, 0, len(details))
	for _, d := range details {
		out = append(out, sanitize.Clip(sanitize.OneLine(d), maxDetailLen))
	}
	return out
}

// recordBody is a record's log body: the details, one per line, then the
// log-only text (commands and raw output) after a blank line.
func recordBody(details []string, log string) string {
	var b strings.Builder
	for _, d := range details {
		b.WriteString(d)
		b.WriteByte('\n')
	}
	if log = strings.TrimRight(log, "\n"); log != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(log)
		b.WriteByte('\n')
	}
	return b.String()
}

// found takes a discovery stage that found something: its result goes out
// now, its record waits for the queue (startQueue). A stage without a
// summary (Repair's polkit recheck, queued as a step) adds nothing.
func (s *Session) found(o *Output, c actions.Checked) {
	r := s.run
	if c.Summary == "" {
		return
	}
	details := cleanDetails(c.Details)
	r.details = append(r.details, details...)
	body := recordBody(details, c.Log)
	if r.rec != nil {
		body = r.rec.body + "\n" + body
	}
	r.rec = &discoveryRecord{summary: sanitize.OneLine(c.Summary), body: body}
	s.emitResult(o, DiscoveryFound, r.rec.summary, "", "")
}

// writeFound writes the held Found record into cat's log, once.
func (s *Session) writeFound(o *Output, cat logstore.Category) {
	r := s.run
	if r.rec == nil {
		return
	}
	s.log(o, cat, "CHECK", r.rec.summary, r.rec.body)
	r.rec = nil
}

// endDiscovery ends the action at its discovery with n: the result, its
// record in the action's discovery log (after any Found stage's record
// not yet written), the Notice the TUI shows, and Finished{OutcomeNotice},
// whose last-run record says how the discovery came out (finish).
func (s *Session) endDiscovery(o *Output, n actions.Notice) {
	r := s.run
	text := sanitize.OneLine(n.Text)
	details := cleanDetails(n.Details)
	r.details = append(r.details, details...)
	outcome, kind := DiscoveryNothing, "CHECK"
	errText := ""
	if n.Err != "" {
		outcome, kind, errText = DiscoveryFailed, "FAIL", sanitize.OneLine(n.Err)
	}
	body := recordBody(details, n.Log)
	if r.rec != nil {
		body = r.rec.body + "\n" + body
		r.rec = nil
	}
	cat := r.recordCat
	noticeText := n.Text
	if !cat.Discovery() {
		// An action without a discovery Record (none in the menu,
		// TestEveryDiscoveryHasARecord). Its record must not go into any
		// other category: it would count as a real run and could rotate a
		// real one out. So nothing is written, and the failure is said.
		outcome = DiscoveryFailed
		errText = "internal error: " + string(r.action) + " has no discovery log category; its record was not written"
		noticeText = n.Text + " (" + errText + ")"
		cat = ""
	}
	r.ended = &outcome
	s.emitResult(o, outcome, text, errText, cat)
	if cat != "" {
		s.log(o, cat, kind, text, body)
	}
	o.event(Notice{Text: sanitize.String(noticeText, sanitize.Plain)})
	s.finish(o, OutcomeNotice)
}

// emitResult publishes the discovery's result so far.
func (s *Session) emitResult(o *Output, outcome DiscoveryOutcome, summary, errText string, cat logstore.Category) {
	r := s.run
	o.event(DiscoveryResult{
		Action: r.action, Outcome: outcome, Summary: summary,
		Details: slices.Clone(r.details), Err: errText, Category: cat,
		Started: r.started, Elapsed: s.now().Sub(r.started),
	})
}

// discoveryRunRecord is last-run.json's outcome for an action that ended at
// its discovery.
func discoveryRunRecord(rec *logstore.RunRecord, outcome DiscoveryOutcome) {
	switch outcome {
	case DiscoveryFailed:
		rec.Outcome = logstore.OutcomeFailed
		rec.StepsFailed = max(rec.StepsFailed, 1)
	default:
		rec.Outcome = logstore.OutcomeNothingFound
	}
}
