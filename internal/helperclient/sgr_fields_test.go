package helperclient

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// Task #112: SGR is allowed in exactly one place of the helper's stream,
// the text of a line event. In every field the client parses or shows as a
// message (package names, a plan's lists, the result's error and skipped
// entries, a progress label, a done event's error) an SGR sequence is
// either refused (a name that is not a package name) or dropped with the
// rest of the escapes, so it never reaches an error message, a Report, a
// log or last-run.json.

var sgrSeq = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

const sgrNoise = "\x1b[31m\x1b[1;38;5;196m\x1b]8;;http://x\x07\x1b[0m"

// jsonEsc is s as the JSON string body the helper's emitter would write.
func jsonEsc(s string) string {
	r := strings.NewReplacer("\x1b", `\u001b`, "\x07", `\u0007`, `"`, `\"`, `\`, `\\`)
	return r.Replace(s)
}

func hasEsc(ss ...string) bool {
	return slices.ContainsFunc(ss, func(s string) bool { return strings.ContainsAny(s, "\x1b\x07\u009b") })
}

func TestSGRInParsedFieldsNeverSurvives(t *testing.T) {
	startPaccache := `{"v":1,"t":"start","verb":"paccache","version":"0.1.0"}`

	t.Run("result error and skipped", func(t *testing.T) {
		o := decodeAll(steps.Step{Verb: steps.VerbPaccache}, 1, startPaccache,
			`{"v":1,"t":"result","ok":false,"code":1,"child_exit":1,"skipped":[{"name":"`+jsonEsc(sgrNoise)+`pkg","reason":"`+jsonEsc(sgrNoise)+`why"}],"error":"`+jsonEsc("\x1b[31mboom"+sgrNoise)+`"}`)
		if o.Err == nil || o.Result == nil {
			t.Fatalf("Err %v, Result %+v", o.Err, o.Result)
		}
		fields := []string{o.Err.Error(), o.Result.Error}
		for _, s := range o.Result.Skipped {
			fields = append(fields, s.Name, s.Reason)
		}
		if hasEsc(fields...) {
			t.Errorf("an escape survived in %q", fields)
		}
		if !strings.Contains(o.Err.Error(), "boom") {
			t.Errorf("Err %q lost the message", o.Err)
		}
	})

	t.Run("skipped entries become output lines, plain", func(t *testing.T) {
		d := newDecoder(steps.Step{Verb: steps.VerbPaccache})
		d.feed([]byte(startPaccache))
		out, _ := d.feed([]byte(`{"v":1,"t":"result","ok":true,"code":0,"skipped":[{"name":"` + jsonEsc(sgrNoise) + `n","reason":"` + jsonEsc(sgrNoise) + `r"}]}`))
		if hasEsc(out...) || len(out) != 1 {
			t.Errorf("lines %q", out)
		}
	})

	t.Run("check names", func(t *testing.T) {
		for _, name := range []string{"\x1b[31mbig", "big\x1b[0m", "bi\x1b[31mg", "\x9bbig", "big\u009b"} {
			o := decodeAll(steps.Step{Verb: steps.VerbCheckFiles}, 0, startCheck,
				`{"v":1,"t":"check","checked":["`+jsonEsc(name)+`"],"broken":[]}`, resultOK)
			if o.Err == nil || o.Check != nil || hasEsc(o.Err.Error()) {
				t.Errorf("name %q: Err %v, Check %+v: want a refusal without an escape", name, o.Err, o.Check)
			}
		}
	})

	t.Run("plan lists", func(t *testing.T) {
		good := steps.OrphanPlan{Direct: []string{"a"}, Cascade: []string{"a 1-1"}}
		good.Hash = PlanHash(good.Direct, good.Cascade)
		for _, p := range []steps.OrphanPlan{
			{Direct: []string{"\x1b[31ma"}, Cascade: good.Cascade},
			{Direct: good.Direct, Cascade: []string{"a \x1b[31m1-1"}},
			{Direct: good.Direct, Cascade: []string{"a\x1b[0m 1-1"}},
			{Direct: good.Direct, Cascade: good.Cascade, Hash: "sha256:\x1b[31m" + strings.Repeat("0", 64)},
		} {
			if p.Hash == "" {
				p.Hash = PlanHash(p.Direct, p.Cascade)
			}
			err := CheckPlan(p)
			if err == nil || hasEsc(err.Error()) {
				t.Errorf("plan %+v: err %v: want a refusal without an escape", p, err)
			}
		}
	})

	t.Run("line events keep SGR only", func(t *testing.T) {
		d := newDecoder(steps.Step{Verb: steps.VerbPaccache})
		d.feed([]byte(startPaccache))
		out, _ := d.feed([]byte(`{"v":1,"t":"line","stream":"out","text":"` + jsonEsc("\x1b[31mok\x1b[0m"+sgrNoise+"\x1b[2K\x1b[?25l\x1b[8mx\x1b[31") + `"}`))
		if len(out) != 1 {
			t.Fatalf("lines %q", out)
		}
		if got := out[0]; strings.Count(got, "\x1b") != len(sgrSeq.FindAllString(got, -1)) || strings.ContainsAny(got, "\x07\u009b") {
			t.Errorf("line %q keeps more than SGR", got)
		}
	})
}
