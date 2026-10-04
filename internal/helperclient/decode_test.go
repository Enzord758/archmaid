package helperclient

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// decodeAll feeds ls to a decoder for st and returns the verdict for exit.
func decodeAll(st steps.Step, exit int, ls ...string) Outcome {
	d := newDecoder(st)
	for _, l := range ls {
		d.feed([]byte(l))
	}
	return d.outcome(exit, nil, "")
}

// TestDecodeOrder: the protocol's order and consistency rules. Each case
// ends with a good result and the matching exit, so a client that only
// looks at the result and the exit status passes them all.
func TestDecodeOrder(t *testing.T) {
	paccache := steps.Step{Verb: steps.VerbPaccache}
	startPaccache := `{"v":1,"t":"start","verb":"paccache","version":"0.1.0"}`
	check := func(c, b string) string { return fmt.Sprintf(`{"v":1,"t":"check","checked":%s,"broken":%s}`, c, b) }
	cases := []struct {
		name string
		st   steps.Step
		ls   []string
		want string
	}{
		{"line before start", repairCheck, []string{`{"v":1,"t":"line","stream":"out","text":"x"}`, startCheck, resultOK}, "before the start line"},
		{"start for another verb", repairCheck, []string{startPaccache, resultOK}, "start line for verb"},
		{"second start", repairCheck, []string{startCheck, startCheck, resultOK}, "second start"},
		{"line after result", repairCheck, []string{startCheck, resultOK, `{"v":1,"t":"line","stream":"out","text":"x"}`}, "after the result"},
		{"second result", repairCheck, []string{startCheck, resultOK, resultOK}, "after the result"},
		{"ok but code 1", repairCheck, []string{startCheck, `{"v":1,"t":"result","ok":true,"code":1}`}, `"ok" and "code"`},
		{"no code", repairCheck, []string{startCheck, `{"v":1,"t":"result","ok":true}`}, `"ok" and "code"`},
		{"line without text", repairCheck, []string{startCheck, `{"v":1,"t":"line","stream":"out"}`, resultOK}, `without "text"`},
		{"text not a string", repairCheck, []string{startCheck, `{"v":1,"t":"line","text":5}`, resultOK}, "malformed"},
		{"check from paccache", paccache, []string{startPaccache, check(`["a"]`, `["a"]`), resultOK}, "check event from paccache"},
		{"two checks", repairCheck, []string{startCheck, check(`["big"]`, `[]`), check(`["big"]`, `["big"]`), resultOK}, "second check"},
		{"broken null", repairCheck, []string{startCheck, check(`["big"]`, `null`), resultOK}, "both lists"},
		{"checked missing", repairCheck, []string{startCheck, `{"v":1,"t":"check","broken":[]}`, resultOK}, "both lists"},
		{"broken not checked", repairCheck, []string{startCheck, check(`["big"]`, `["amd-ucode"]`), resultOK}, "not one checked"},
		{"broken twice", repairCheck, []string{startCheck, check(`["big"]`, `["big","big"]`), resultOK}, "not one checked"},
		{"checked not asked", repairCheck, []string{startCheck, check(`["big","glibc"]`, `["glibc"]`), resultOK}, "not asked for"},
		{"checked twice", repairCheck, []string{startCheck, check(`["big","big"]`, `[]`), resultOK}, "twice"},
		{"checked flag", steps.Step{Verb: steps.VerbCheckFiles}, []string{startCheck, check(`["--root=/"]`, `["--root=/"]`), resultOK}, "invalid package name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := decodeAll(c.st, 0, c.ls...)
			if !errors.Is(o.Err, ErrProtocol) || !strings.Contains(o.Err.Error(), c.want) {
				t.Fatalf("Err %v, want a protocol error with %q", o.Err, c.want)
			}
			if o.Check != nil {
				t.Errorf("Check %+v kept on a protocol error", o.Check)
			}
		})
	}
}

// TestDecodeResultExitMismatch: a result that disagrees with the exit
// status fails closed.
func TestDecodeResultExitMismatch(t *testing.T) {
	if o := decodeAll(repairCheck, 1, startCheck, resultOK); !errors.Is(o.Err, ErrProtocol) {
		t.Fatalf("Err %v", o.Err)
	}
}

// TestDecodeFullCheck: the full check (no Items) takes any package names.
func TestDecodeFullCheck(t *testing.T) {
	o := decodeAll(steps.Step{ID: steps.IDIntegrity, Verb: steps.VerbCheckFiles}, 0, startCheck,
		`{"v":1,"t":"check","checked":["a","b","glibc"],"broken":["glibc"]}`, resultOK)
	if o.Err != nil || o.Check == nil || len(o.Check.Checked) != 3 {
		t.Fatalf("outcome %+v", o)
	}
}

// TestDecodeCheckDroppedOnFailure: a check event before a failed result is
// not kept (the helper never sends one, but if it did, nothing may be
// queued from it).
func TestDecodeCheckDroppedOnFailure(t *testing.T) {
	o := decodeAll(repairCheck, 1, startCheck, `{"v":1,"t":"check","checked":["big"],"broken":["big"]}`,
		`{"v":1,"t":"result","ok":false,"code":1,"error":"boom"}`)
	if o.Err == nil || o.Check != nil {
		t.Fatalf("outcome %+v", o)
	}
}
