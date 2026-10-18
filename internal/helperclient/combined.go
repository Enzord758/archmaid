package helperclient

import (
	"errors"
	"fmt"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Full Cleanup under polkit runs its root steps as ONE full-cleanup call
// (one polkit dialog; engine: combine.go). The engine hands Run a step with
// Verb full-cleanup and the steps it covers as Parts; the helper reports
// each part with progress events carrying "part" and ends it with a done
// event (cmd/archmaid-helper README), which Run maps back to the parts as
// engine.PartStarted and engine.PartDone, so every step keeps its row.

// PartCleanUninstalled is the helper's full-cleanup part that is no verb of
// its own: pacman -Sc, the root half of yay -Sc. Its step
// (steps.IDPkgCacheUninstalled) carries Verb full-cleanup.
const PartCleanUninstalled = "clean-uninstalled"

// fullCleanupOrder is the order the helper runs full-cleanup's parts in,
// each named by its verb (partName); required are those it always runs.
var (
	fullCleanupOrder = []string{steps.VerbRemoveOrphans, steps.VerbPaccache,
		PartCleanUninstalled, steps.VerbCleanTmp, steps.VerbVacuumJournal, steps.VerbCheckFiles}
	fullCleanupRequired = map[string]bool{steps.VerbPaccache: true, steps.VerbCleanTmp: true,
		steps.VerbVacuumJournal: true, steps.VerbCheckFiles: true}
)

// ErrBadParts: a full-cleanup step's Parts are not a sequence the helper
// runs.
var ErrBadParts = errors.New("helperclient: bad parts for full-cleanup")

// partName is the full-cleanup part st stands for: its Verb, or for the
// one step whose part is no verb (Verb full-cleanup), that part's name.
func partName(st steps.Step) string {
	if st.Verb == steps.VerbFullCleanup && st.ID == steps.IDPkgCacheUninstalled {
		return PartCleanUninstalled
	}
	return st.Verb
}

// fullCleanupArgs builds full-cleanup's flags from st.Parts, which must be
// the helper's parts in its order: the four it always runs, plus the
// orphan removal (first, its Plan checked as for remove-orphans) and
// the uninstalled packages' cache where present. The cache wipe is not a
// part: it is the standalone paccache-wipe verb. Each part is validated as its own verb would
// be, and must itself be a plain step of the combined verb.
func fullCleanupArgs(st steps.Step) ([]string, error) {
	if len(st.Items) != 0 || st.Plan != nil {
		return nil, fmt.Errorf("%w: full-cleanup takes parts, not items or a plan", ErrBadParts)
	}
	args := []string{"--keep", PaccacheKeep, "--journal", JournalSpan}
	i := 0
	for _, verb := range fullCleanupOrder {
		if i == len(st.Parts) || partName(st.Parts[i]) != verb {
			if fullCleanupRequired[verb] {
				return nil, fmt.Errorf("%w: no %s part where the helper runs one", ErrBadParts, verb)
			}
			continue
		}
		p := st.Parts[i]
		i++
		if p.Combine != steps.VerbFullCleanup || len(p.Parts) != 0 || p.Run != nil {
			return nil, fmt.Errorf("%w: part %q is not a plain full-cleanup step", ErrBadParts, p.ID)
		}
		if verb == PartCleanUninstalled {
			if len(p.Items) != 0 || p.Plan != nil {
				return nil, fmt.Errorf("%w: %s takes nothing", ErrBadParts, verb)
			}
			args = append(args, "--uninstalled")
			continue
		}
		pa, err := verbArgs(p)
		if err != nil {
			return nil, err
		}
		switch verb {
		case steps.VerbRemoveOrphans:
			args = append(args, "--orphans", pa[0])
		case steps.VerbCheckFiles:
			if len(pa) != 0 {
				return nil, fmt.Errorf("%w: full-cleanup checks every package, not names", ErrBadParts)
			}
		}
	}
	if i != len(st.Parts) {
		return nil, fmt.Errorf("%w: part %q is out of order or not one of the helper's", ErrBadParts, st.Parts[i].ID)
	}
	return args, nil
}

// partEvent is a part's start or end, for Run to emit as an Input.
type partEvent struct {
	done bool
	step string
	err  error
}

func (e partEvent) input(seq uint64) engine.Input {
	if e.done {
		return engine.PartDone{Seq: seq, Step: e.step, Err: e.err}
	}
	return engine.PartStarted{Seq: seq, Step: e.step}
}

// combined is the decoder's state for a full-cleanup call.
type combined struct {
	parts   []steps.Step
	cur     int  // the running part's index, -1 before the first
	curDone bool // it has its done event
	done    int  // done events so far
	failed  bool // a part failed (not counting a stale orphan plan)
	stale   bool // the orphan part found the plan changed
	lastErr error
	events  []partEvent // not yet emitted
}

// takeParts returns the part events not yet emitted.
func (d *decoder) takeParts() []partEvent {
	if d.comb == nil || len(d.comb.events) == 0 {
		return nil
	}
	evs := d.comb.events
	d.comb.events = nil
	return evs
}

// partProgress takes a progress event of a combined call: a part's first
// one starts it (its predecessor must have ended), later ones repeat it.
// The label is relayed as an output line of the part.
func (d *decoder) partProgress(line []byte) []string {
	var e struct {
		Step  int    `json:"step"`
		Of    int    `json:"of"`
		Part  string `json:"part"`
		Label string `json:"label"`
	}
	if !d.decode(line, &e) {
		return nil
	}
	c := d.comb
	if e.Of != len(c.parts) || e.Step < 1 || e.Step > len(c.parts) || partName(c.parts[e.Step-1]) != e.Part {
		d.fail("progress for part %d/%d %q, want one of %d parts in order", e.Step, e.Of, sanitize.Field(e.Part, 40), len(c.parts))
		return nil
	}
	switch n := e.Step - 1; {
	case n == c.cur+1 && (c.cur < 0 || c.curDone):
		c.cur, c.curDone = n, false
		if n > 0 { // the first part runs from the call's start
			c.events = append(c.events, partEvent{step: c.parts[n].ID})
		}
	case n == c.cur && !c.curDone:
	default:
		d.fail("progress for part %d out of order", e.Step)
		return nil
	}
	if l := sanitize.OneLine(e.Label); l != "" {
		return []string{l}
	}
	return nil
}

// partDone takes a done event: it ends the running part with its own
// outcome. The last part's is kept for the call's StepExited, which ends
// it only once the result line agrees.
func (d *decoder) partDone(line []byte) {
	var e struct {
		Step  int    `json:"step"`
		Of    int    `json:"of"`
		Part  string `json:"part"`
		OK    *bool  `json:"ok"`
		Code  *int   `json:"code"`
		Error string `json:"error"`
	}
	if !d.decode(line, &e) {
		return
	}
	c := d.comb
	switch {
	case e.OK == nil || e.Code == nil || *e.OK != (*e.Code == 0):
		d.fail(`a done event without a consistent "ok" and "code"`)
		return
	case e.Of != len(c.parts) || e.Step-1 != c.cur || c.curDone || partName(c.parts[c.cur]) != e.Part:
		d.fail("done for part %d/%d %q, which isn't running", e.Step, e.Of, sanitize.Field(e.Part, 40))
		return
	}
	c.curDone = true
	c.done++
	var err error
	switch {
	case *e.OK:
	case *e.Code == exitStale && e.Part == steps.VerbRemoveOrphans:
		c.stale, err = true, ErrOrphansChanged
	default:
		msg := sanitize.OneLine(e.Error)
		if msg == "" {
			msg = "failed"
		}
		c.failed, err = true, fmt.Errorf("root helper (exit %d): %s", *e.Code, sanitize.Clip(msg, 300))
	}
	if c.cur == len(c.parts)-1 {
		c.lastErr = err
		return
	}
	c.events = append(c.events, partEvent{done: true, step: c.parts[c.cur].ID, err: err})
}

// combinedOutcome is the call's Err once its result line is in, for a call
// that ended a part or succeeded (outcome reports a failed result before
// any part ended, such as a refusal at the lock or the input, itself):
// every part must have ended, the result must agree with their outcomes,
// and the call's Err is the last part's own.
func (d *decoder) combinedOutcome() error {
	c := d.comb
	if c.done != len(c.parts) {
		return fmt.Errorf("%w: the result came after %d of %d parts", ErrProtocol, c.done, len(c.parts))
	}
	want := 0
	switch {
	case c.failed:
		want = 1
	case c.stale:
		want = exitStale
	}
	if d.res.Code != want {
		return fmt.Errorf("%w: result code %d, but the parts say %d", ErrProtocol, d.res.Code, want)
	}
	return c.lastErr
}

// feedCombined handles the event types only a combined call has, or reads
// differently. It reports whether it handled t.
func (d *decoder) feedCombined(t string, line []byte) (out []string, handled bool) {
	if d.comb == nil {
		return nil, false
	}
	switch t {
	case evProgress:
		return d.partProgress(line), true
	case evDone:
		d.partDone(line)
		return nil, true
	}
	return nil, false
}
