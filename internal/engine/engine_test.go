package engine

import (
	"encoding"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// The frozen API, checked by the compiler: changing a signature breaks the
// build here. From E0 on, changes are additive only.
var (
	_ func(Options) *Session                    = New
	_ func(*Session, string) Output             = (*Session).Start
	_ func(*Session, PromptID, bool) Output     = (*Session).Confirm
	_ func(*Session, PromptID, Secret) Output   = (*Session).SubmitPassword
	_ func(*Session, PromptID) Output           = (*Session).CancelPrompt
	_ func(*Session, PromptID, []string) Output = (*Session).Select
	_ func(*Session) Output                     = (*Session).Abort
	_ func(*Session, Input) Output              = (*Session).Handle

	_ func(Driver) <-chan Event  = Driver.Events
	_ func(Driver, Command) bool = Driver.Send
	_ func(Driver)               = Driver.Close
	_ fmt.Stringer               = Secret("")
	_ fmt.Formatter              = Secret("")
	_ encoding.TextMarshaler     = Secret("")
	_ fmt.Stringer               = OutcomeCompleted
	_                            = Options{DryRun: true, Now: time.Now, Auth: AuthPolkit}
)

// Every member of each closed set, as a zero value. TestSealedSets keeps
// these lists in step with the source, so a new type is checked below.
var (
	allEvents = []Event{
		QueueStarted{}, StepInserted{}, PlanChanged{}, StepStarted{}, StepDone{},
		OutputChunk{}, Aborted{}, LogWritten{}, NeedsConfirm{}, NeedsPassword{},
		NeedsSelection{}, PromptClosed{}, AuthWaiting{}, AuthResolved{},
		FaillockState{}, PhaseChanged{}, StatusUpdated{}, Notice{}, OutputReport{},
		Finished{}, ReportAccepted{}, DiscoveryResult{}, RunReport{},
	}
	allEffects = []Effect{
		Probe{}, Authenticate{}, KeepAlive{}, ReadFaillock{}, Discover{}, StartStep{},
		Stop{}, RunHook{}, WriteLog{}, WriteLastRun{}, StartPrefetch{},
		CancelPrefetch{}, AnswerAskpass{}, HangUpAskpass{}, RefreshUpdates{},
	}
	allInputs = []Input{
		Tick{}, ProbeDone{}, AuthDone{}, FaillockRead{}, Discovered{}, StepOutput{},
		PrivilegeGranted{}, StepExited{}, StepResult{}, HookDone{}, AskpassRequest{},
		AskpassAnswered{}, HelperCheck{}, PartStarted{}, PartDone{},
	}
	allCommands = []Command{
		Start{}, Confirm{}, SubmitPassword{}, CancelPrompt{}, Select{}, Abort{},
	}
)

func names[T any](vs []T) []string {
	var out []string
	for _, v := range vs {
		out = append(out, reflect.TypeOf(v).Name())
	}
	slices.Sort(out)
	return out
}

// markers reads the package's non-test source and returns, per marker
// method (isEvent, …), the types that declare it.
func markers(t *testing.T) map[string][]string {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string][]string{}
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !strings.HasPrefix(fn.Name.Name, "is") {
				continue
			}
			if id, ok := fn.Recv.List[0].Type.(*ast.Ident); ok {
				out[fn.Name.Name] = append(out[fn.Name.Name], id.Name)
			}
		}
	}
	for k := range out {
		slices.Sort(out[k])
	}
	return out
}

func TestSealedSets(t *testing.T) {
	m := markers(t)
	for _, c := range []struct {
		marker string
		listed []string
	}{
		{"isEvent", names(allEvents)},
		{"isEffect", names(allEffects)},
		{"isInput", names(allInputs)},
		{"isCommand", names(allCommands)},
	} {
		if !slices.Equal(m[c.marker], c.listed) {
			t.Errorf("%s types in the source:\n  %v\nlisted in this test:\n  %v", c.marker, m[c.marker], c.listed)
		}
	}
}

// passwordish matches a field name that could hold a password.
var passwordish = regexp.MustCompile(`(?i)(^pw|pw$|passw|passphrase|secret|token|cred)`)

var secretType = reflect.TypeOf(Secret(""))

// leaks lists where a value of type t could carry a password, or something
// unverifiable (a func, channel or interface field), naming the path.
func leaks(t reflect.Type, path string, seen map[reflect.Type]bool) []string {
	switch t {
	case secretType:
		return []string{path + " is a Secret"}
	case reflect.TypeOf(time.Time{}), reflect.TypeOf(time.Duration(0)):
		return nil
	}
	switch t.Kind() {
	case reflect.Func, reflect.Chan, reflect.Interface, reflect.UnsafePointer:
		return []string{fmt.Sprintf("%s is a %s (events are plain data)", path, t.Kind())}
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return leaks(t.Elem(), path+"[]", seen)
	case reflect.Map:
		return append(leaks(t.Key(), path+"{key}", seen), leaks(t.Elem(), path+"{}", seen)...)
	case reflect.Struct:
		if seen[t] {
			return nil
		}
		seen[t] = true
		var out []string
		for i := range t.NumField() {
			f := t.Field(i)
			p := path + "." + f.Name
			if passwordish.MatchString(f.Name) && carriesText(f.Type) {
				out = append(out, p+" is named like a password")
			}
			out = append(out, leaks(f.Type, p, seen)...)
		}
		return out
	}
	return nil
}

// carriesText reports whether t is or holds a string or bytes.
func carriesText(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.String:
		return true
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return t.Elem().Kind() == reflect.Uint8 || carriesText(t.Elem())
	case reflect.Map:
		return carriesText(t.Key()) || carriesText(t.Elem())
	case reflect.Struct:
		for i := range t.NumField() {
			if carriesText(t.Field(i).Type) {
				return true
			}
		}
	}
	return false
}

func TestNoEventCarriesAPassword(t *testing.T) {
	for _, ev := range allEvents {
		typ := reflect.TypeOf(ev)
		for _, l := range leaks(typ, typ.Name(), map[reflect.Type]bool{}) {
			t.Error(l)
		}
	}
}

// The checker itself must catch each way a password could slip in.
func TestPasswordCheckerCatches(t *testing.T) {
	type inner struct{ UserPW string }
	for _, c := range []struct {
		v    any
		want string
	}{
		{struct{ Password string }{}, ".Password is named like a password"},
		{struct{ PW []byte }{}, ".PW is named like a password"},
		{struct{ Pass Secret }{}, ".Pass is a Secret"},
		{struct{ Nested []inner }{}, ".Nested[].UserPW is named like a password"},
		{struct{ Byname map[string]*inner }{}, ".Byname{}[].UserPW is named like a password"},
		{struct{ AuthToken string }{}, ".AuthToken is named like a password"},
		{struct{ Payload any }{}, ".Payload is a interface"},
		{struct{ Fn func() string }{}, ".Fn is a func"},
	} {
		got := strings.Join(leaks(reflect.TypeOf(c.v), "T", map[reflect.Type]bool{}), "; ")
		if !strings.Contains(got, c.want) {
			t.Errorf("%T: got %q, want it to report %q", c.v, got, c.want)
		}
	}
	// Names that merely contain the letters, and non-text fields, pass.
	type ok struct {
		Prompt, Upward, Bypass, Spawn string
		Secrets                       int
	}
	if got := leaks(reflect.TypeOf(ok{}), "T", map[reflect.Type]bool{}); len(got) != 0 {
		t.Errorf("false positives: %v", got)
	}
}

func TestSecretRedacted(t *testing.T) {
	const pw = "hunter2-correct"
	for _, v := range []any{
		Secret(pw),
		SubmitPassword{Prompt: 7, Password: pw},
		&SubmitPassword{Prompt: 7, Password: pw},
		Authenticate{Password: pw},
		AnswerAskpass{Req: 3, Password: pw},
		[]Effect{Authenticate{Password: pw}},
	} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%10.3s"} {
			if s := fmt.Sprintf(verb, v); strings.Contains(s, pw[:7]) || strings.Contains(s, fmt.Sprintf("%x", pw[:4])) {
				t.Errorf("Sprintf(%q, %T) = %q leaks the password", verb, v, s)
			}
		}
		b, err := json.Marshal(v)
		if err != nil || strings.Contains(string(b), pw[:7]) {
			t.Errorf("json.Marshal(%T) = %s, %v: leaks the password", v, b, err)
		}
	}
	if Secret(pw).String() != "<redacted>" {
		t.Errorf("String() = %q", Secret(pw).String())
	}
	if string(Secret(pw)) != pw {
		t.Error("string(Secret) must give the password back")
	}
}

func TestOutcomeString(t *testing.T) {
	for o, want := range map[Outcome]string{
		OutcomeCompleted: "completed", OutcomeAborted: "aborted", OutcomeCancelled: "cancelled",
		OutcomeNotice: "notice", OutcomeReport: "report", -1: "outcome?", OutcomeReport + 1: "outcome?",
	} {
		if got := o.String(); got != want {
			t.Errorf("Outcome(%d).String() = %q, want %q", o, got, want)
		}
	}
}
