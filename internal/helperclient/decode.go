package helperclient

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Check is the helper's check event (check-files): Checked are the packages
// pacman printed a summary for, Broken those of them missing files.
type Check struct {
	Checked, Broken []string
}

// Skip is one input the helper deliberately did not act on (its result's
// skipped list), sanitized.
type Skip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Result is the helper's result line, sanitized.
type Result struct {
	OK        bool
	Code      int
	ChildExit *int // exit status of its last root command; nil if none ran
	Skipped   []Skip
	Error     string
}

// The helper's event types ("t") that Run reads.
const (
	evStart    = "start"
	evProgress = "progress"
	evLine     = "line"
	evPlan     = "plan"
	evCheck    = "check"
	evResult   = "result"
)

// decoder reads one helper run's JSON lines. The first protocol error is
// sticky: from then on nothing is interpreted (the caller still drains the
// pipe) and the step fails closed.
type decoder struct {
	verb    string
	items   []string // check-files' requested names; nil for the full check
	granted bool
	res     *Result
	check   *Check
	plan    *steps.OrphanPlan // plan-orphans' plan event (plan.go)
	unknown int
	err     error
}

func newDecoder(st steps.Step) *decoder {
	d := &decoder{verb: st.Verb}
	if st.Verb == steps.VerbCheckFiles && len(st.Items) > 0 {
		d.items = st.Items
	}
	return d
}

func (d *decoder) fail(format string, a ...any) {
	if d.err == nil {
		d.err = fmt.Errorf("%w: %s", ErrProtocol, fmt.Sprintf(format, a...))
	}
}

// header is what every line must have.
type header struct {
	V *int    `json:"v"`
	T *string `json:"t"`
}

// feed takes one line (without its newline) and returns the output lines it
// relays, and whether it was the start line (privilege granted).
func (d *decoder) feed(line []byte) (out []string, start bool) {
	if d.err != nil {
		return nil, false
	}
	var h header
	if err := json.Unmarshal(line, &h); err != nil {
		d.fail("malformed line: %v", err)
		return nil, false
	}
	if h.V == nil || *h.V != SchemaVersion {
		d.fail(`a line without "v":%d`, SchemaVersion)
		return nil, false
	}
	if h.T == nil {
		d.fail(`a line without "t"`)
		return nil, false
	}
	t := *h.T
	switch {
	case d.res != nil:
		d.fail("%q after the result line", t)
		return nil, false
	case !d.granted && t != evStart:
		d.fail("%q before the start line", t)
		return nil, false
	}
	switch t {
	case evStart:
		var e struct {
			Verb string `json:"verb"`
		}
		if !d.decode(line, &e) {
			return nil, false
		}
		if d.granted {
			d.fail("a second start line")
			return nil, false
		}
		if e.Verb != d.verb {
			d.fail("start line for verb %q, want %q", sanitize.Field(e.Verb, 40), d.verb)
			return nil, false
		}
		d.granted = true
		return nil, true
	case evLine:
		var e struct {
			Text *string `json:"text"`
		}
		if !d.decode(line, &e) {
			return nil, false
		}
		if e.Text == nil {
			d.fail(`a line event without "text"`)
			return nil, false
		}
		// The helper sends one line, already sanitized; a newline here
		// would still only ever make more lines, never a control sequence.
		return strings.Split(sanitize.String(*e.Text, sanitize.KeepSGR), "\n"), false
	case evProgress:
		return nil, false // known; nothing for the engine
	case evPlan:
		d.readPlan(line)
		return nil, false
	case evCheck:
		d.readCheck(line)
		return nil, false
	case evResult:
		return d.readResult(line), false
	default:
		d.unknown++
		return nil, false
	}
}

func (d *decoder) decode(line []byte, v any) bool {
	if err := json.Unmarshal(line, v); err != nil {
		d.fail("malformed %s line: %v", d.verb, err)
		return false
	}
	return true
}

// readCheck takes the check event: only for check-files, at most once, both
// lists present, every name a package name, none twice, Broken within
// Checked, and with names requested, Checked within them. Anything else
// fails the step: a broken list is what a repair is queued from.
func (d *decoder) readCheck(line []byte) {
	var e struct {
		Checked *[]string `json:"checked"`
		Broken  *[]string `json:"broken"`
	}
	if !d.decode(line, &e) {
		return
	}
	switch {
	case d.verb != steps.VerbCheckFiles:
		d.fail("a check event from %s", d.verb)
		return
	case d.check != nil:
		d.fail("a second check event")
		return
	case e.Checked == nil || e.Broken == nil:
		d.fail("a check event without both lists")
		return
	}
	checked := make(map[string]bool, len(*e.Checked))
	for _, n := range *e.Checked {
		if !namePattern.MatchString(n) {
			d.fail("check event: invalid package name %q", sanitize.Field(n, 60))
			return
		}
		if checked[n] {
			d.fail("check event: %q listed twice", n)
			return
		}
		if d.items != nil && !slices.Contains(d.items, n) {
			d.fail("check event: %q was not asked for", n)
			return
		}
		checked[n] = true
	}
	broken := map[string]bool{}
	for _, n := range *e.Broken {
		if !checked[n] || broken[n] {
			d.fail("check event: broken %q is not one checked package", sanitize.Field(n, 60))
			return
		}
		broken[n] = true
	}
	d.check = &Check{Checked: slices.Clone(*e.Checked), Broken: slices.Clone(*e.Broken)}
}

// readResult takes the result line and returns its skipped entries as
// output lines.
func (d *decoder) readResult(line []byte) []string {
	var e struct {
		OK        *bool  `json:"ok"`
		Code      *int   `json:"code"`
		ChildExit *int   `json:"child_exit"`
		Skipped   []Skip `json:"skipped"`
		Error     string `json:"error"`
	}
	if !d.decode(line, &e) {
		return nil
	}
	if e.OK == nil || e.Code == nil || *e.OK != (*e.Code == 0) {
		d.fail(`a result line without a consistent "ok" and "code"`)
		return nil
	}
	r := &Result{OK: *e.OK, Code: *e.Code, ChildExit: e.ChildExit, Error: sanitize.OneLine(e.Error)}
	var out []string
	for _, s := range e.Skipped {
		s = Skip{Name: sanitize.Field(s.Name, 128), Reason: sanitize.OneLine(s.Reason)}
		r.Skipped = append(r.Skipped, s)
		out = append(out, fmt.Sprintf("Skipped %s: %s", s.Name, s.Reason))
	}
	d.res = r
	return out
}
