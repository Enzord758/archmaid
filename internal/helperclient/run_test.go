package helperclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// These tests run Run against a fake pkexec: a sh script put first on PATH,
// ahead of testenv's trap, which records its argv and prints what a helper
// would. The real pkexec and helper are never reached.

const (
	startCheck = `{"v":1,"t":"start","verb":"check-files","version":"0.1.0"}`
	resultOK   = `{"v":1,"t":"result","ok":true,"code":0,"child_exit":0,"skipped":[]}`
)

// shim installs a fake pkexec whose body is a sh script fragment, and
// returns the file its argv is recorded in (one arg per line).
func shim(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argv + "'\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "pkexec"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argv
}

// lines is a shim body that prints each JSON line as is, then exits code.
func lines(code string, ls ...string) string {
	var b strings.Builder
	b.WriteString("cat <<'EOF'\n")
	for _, l := range ls {
		b.WriteString(l + "\n")
	}
	b.WriteString("EOF\nexit " + code)
	return b.String()
}

type recorded struct {
	inputs []engine.Input
	out    Outcome
}

func (r recorded) granted() int {
	n := 0
	for _, in := range r.inputs {
		if _, ok := in.(engine.PrivilegeGranted); ok {
			n++
		}
	}
	return n
}

// checks is every HelperCheck, which may only come right before StepExited.
func (r recorded) checks(t *testing.T) []engine.HelperCheck {
	t.Helper()
	var out []engine.HelperCheck
	for i, in := range r.inputs {
		if c, ok := in.(engine.HelperCheck); ok {
			if i != len(r.inputs)-2 {
				t.Errorf("HelperCheck at %d of %d inputs, want right before StepExited", i, len(r.inputs))
			}
			if c.Seq != seq {
				t.Errorf("seq %d", c.Seq)
			}
			out = append(out, c)
		}
	}
	return out
}

func (r recorded) output() []string {
	var out []string
	for _, in := range r.inputs {
		if o, ok := in.(engine.StepOutput); ok {
			out = append(out, o.Lines...)
		}
	}
	return out
}

func (r recorded) exited(t *testing.T) engine.StepExited {
	t.Helper()
	last, ok := r.inputs[len(r.inputs)-1].(engine.StepExited)
	if !ok {
		t.Fatalf("last input %#v, want StepExited", r.inputs[len(r.inputs)-1])
	}
	for _, in := range r.inputs[:len(r.inputs)-1] {
		if _, ok := in.(engine.StepExited); ok {
			t.Fatalf("more than one StepExited: %#v", r.inputs)
		}
	}
	return last
}

const seq = 7

func runStep(t *testing.T, st steps.Step) recorded {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var r recorded
	r.out = Run(ctx, seq, st, func(in engine.Input) {
		switch in := in.(type) {
		case engine.StepOutput:
			if in.Seq != seq {
				t.Errorf("seq %d", in.Seq)
			}
		case engine.PrivilegeGranted:
			if in.Seq != seq {
				t.Errorf("seq %d", in.Seq)
			}
		case engine.StepExited:
			if in.Seq != seq {
				t.Errorf("seq %d", in.Seq)
			}
		}
		r.inputs = append(r.inputs, in)
	})
	return r
}

var repairCheck = actions.RepairCheckStep([]string{"amd-ucode", "big", "nfs-utils"})

// TestRunGranted: the whole mapping on a good run. The argv pkexec got is
// exactly Argv's; the start line is PrivilegeGranted before any output;
// line events are output; the check event is Outcome.Check; the result and
// exit 0 are StepExited with no error.
func TestRunGranted(t *testing.T) {
	argvFile := shim(t, lines("0",
		startCheck,
		`{"v":1,"t":"progress","step":1,"of":1,"label":"Checking 3 package(s) for missing files"}`,
		`{"v":1,"t":"line","stream":"out","text":"amd-ucode: 3 total files, 0 missing files"}`,
		`{"v":1,"t":"line","stream":"err","text":"warning: big: /usr/share/big/one (No such file or directory)"}`,
		`{"v":1,"t":"check","checked":["amd-ucode","big","nfs-utils"],"broken":["big"]}`,
		`{"v":1,"t":"result","ok":true,"code":0,"child_exit":1,"skipped":[]}`,
	))
	r := runStep(t, repairCheck)
	got, _ := os.ReadFile(argvFile)
	if want := HelperPath + "\ncheck-files\namd-ucode\nbig\nnfs-utils\n"; string(got) != want {
		t.Errorf("pkexec got argv %q, want %q", got, want)
	}
	if _, ok := r.inputs[0].(engine.PrivilegeGranted); !ok || r.granted() != 1 {
		t.Errorf("want PrivilegeGranted first and once: %#v", r.inputs)
	}
	if ex := r.exited(t); ex.Err != nil || ex.NotGranted {
		t.Fatalf("StepExited %+v, want success", ex)
	}
	wantOut := []string{"amd-ucode: 3 total files, 0 missing files", "warning: big: /usr/share/big/one (No such file or directory)"}
	if !slices.Equal(r.output(), wantOut) {
		t.Errorf("output %q, want %q", r.output(), wantOut)
	}
	o := r.out
	if !o.Granted || o.NotGranted || o.Exit != 0 || o.Result == nil || *o.Result.ChildExit != 1 || o.Unknown != 0 {
		t.Errorf("outcome %+v", o)
	}
	if o.Check == nil || !slices.Equal(o.Check.Broken, []string{"big"}) || len(o.Check.Checked) != 3 {
		t.Errorf("check %+v, want big broken of 3", o.Check)
	}
	if c := r.checks(t); len(c) != 1 || !slices.Equal(c[0].Broken, []string{"big"}) {
		t.Errorf("HelperCheck %+v, want one with big", c)
	}
}

// TestRunFullCheck: Integrity's full check-files also ends in a
// HelperCheck (the engine only acts on Repair's recheck), with broken []
// when nothing is.
func TestRunFullCheck(t *testing.T) {
	shim(t, lines("0", startCheck,
		`{"v":1,"t":"check","checked":["a","glibc"],"broken":[]}`, resultOK))
	r := runStep(t, steps.Step{ID: steps.IDIntegrity, Verb: steps.VerbCheckFiles})
	if ex := r.exited(t); ex.Err != nil {
		t.Fatal(ex.Err)
	}
	if c := r.checks(t); len(c) != 1 || c[0].Broken == nil || len(c[0].Broken) != 0 {
		t.Errorf("HelperCheck %+v, want one with broken []", c)
	}
}

// TestRunNoCheckWithoutSuccess: a check event in a run that then fails is
// never handed on.
func TestRunNoCheckWithoutSuccess(t *testing.T) {
	for name, tail := range map[string][]string{
		"failed result":  {`{"v":1,"t":"result","ok":false,"code":1,"child_exit":2,"skipped":[],"error":"boom"}`},
		"no result":      nil,
		"bad line after": {`{"v":1,"t":"line"`, resultOK},
	} {
		t.Run(name, func(t *testing.T) {
			code := "0"
			if name == "failed result" {
				code = "1"
			}
			shim(t, lines(code, append([]string{startCheck, `{"v":1,"t":"check","checked":["big"],"broken":["big"]}`}, tail...)...))
			r := runStep(t, repairCheck)
			if ex := r.exited(t); ex.Err == nil {
				t.Fatal("want a failure")
			}
			if c := r.checks(t); len(c) != 0 {
				t.Errorf("HelperCheck %+v handed on from a failed run", c)
			}
		})
	}
}

// TestRunRefused: pkexec exits 127 before any line (wrong password, locked
// account, and per docs/polkit-live-test.md also a cancel): NotGranted,
// no PrivilegeGranted, no output. Fails on a client that reads a missing
// start line as a plain failure.
func TestRunRefused(t *testing.T) {
	for _, code := range []string{"127", "126"} {
		t.Run(code, func(t *testing.T) {
			shim(t, "echo 'Error executing command as another user: Not authorized' >&2\nexit "+code)
			r := runStep(t, repairCheck)
			ex := r.exited(t)
			if !ex.NotGranted || !errors.Is(ex.Err, ErrNotGranted) {
				t.Fatalf("StepExited %+v, want NotGranted", ex)
			}
			if len(r.inputs) != 1 || r.out.Granted || r.out.Check != nil {
				t.Errorf("inputs %#v, outcome %+v: want just StepExited", r.inputs, r.out)
			}
		})
	}
}

// TestRunCancelled: the dialog is dismissed while pkexec waits (here: the
// shim is killed by ctx before printing anything). Not granted is decided
// by pkexec's exit only; a kill is a failure with no NotGranted, and Run
// returns promptly.
func TestRunCancelled(t *testing.T) {
	shim(t, "exec sleep 20")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	var inputs []engine.Input
	start := time.Now()
	o := Run(ctx, seq, repairCheck, func(in engine.Input) { inputs = append(inputs, in) })
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("Run took %v after the cancel", d)
	}
	if o.Err == nil || o.NotGranted || o.Granted || o.Exit != -1 || len(inputs) != 1 {
		t.Fatalf("outcome %+v, inputs %#v: want one failed StepExited", o, inputs)
	}
}

// TestRunExit127AfterStart: 127 after the start line is the helper's (or a
// crash's) exit, not a refusal: privilege was granted. Fails on a client
// that maps every 127 to NotGranted.
func TestRunExit127AfterStart(t *testing.T) {
	shim(t, lines("127", startCheck))
	r := runStep(t, repairCheck)
	ex := r.exited(t)
	if ex.NotGranted || ex.Err == nil || r.granted() != 1 {
		t.Fatalf("StepExited %+v (granted %d), want a failure after a grant", ex, r.granted())
	}
}

// TestRunJunkBeforeRefusal: output before a 127 means something ran; it is
// a protocol error, not a refusal.
func TestRunJunkBeforeRefusal(t *testing.T) {
	shim(t, lines("127", `{"v":1,"t":"line","stream":"out","text":"hi"}`))
	r := runStep(t, repairCheck)
	if ex := r.exited(t); ex.NotGranted || !errors.Is(ex.Err, ErrProtocol) {
		t.Fatalf("StepExited %+v, want a protocol error", ex)
	}
}

// TestRunNeverStarted: any other exit without a start line (pkexec can't
// run, the shim's own failure) is a failure naming stderr's last line.
func TestRunNeverStarted(t *testing.T) {
	shim(t, "echo 'pkexec must be setuid root' >&2\nexit 1")
	r := runStep(t, repairCheck)
	ex := r.exited(t)
	if ex.NotGranted || ex.Err == nil || !strings.Contains(ex.Err.Error(), "pkexec must be setuid root") {
		t.Fatalf("StepExited %+v", ex)
	}
}

// TestRunHelperValidation: the helper refusing its input (exit 2) is a
// failure with its message, after a grant.
func TestRunHelperValidation(t *testing.T) {
	shim(t, lines("2", startCheck,
		`{"v":1,"t":"result","ok":false,"code":2,"child_exit":null,"skipped":[],"error":"package \"ghost\" is not installed"}`))
	r := runStep(t, repairCheck)
	ex := r.exited(t)
	if ex.NotGranted || ex.Err == nil || !strings.Contains(ex.Err.Error(), `package "ghost" is not installed`) {
		t.Fatalf("StepExited %+v", ex)
	}
	if r.out.Result == nil || r.out.Result.Code != 2 || r.out.Result.ChildExit != nil || r.out.Check != nil {
		t.Errorf("outcome %+v", r.out)
	}
}

// TestRunSkippedAreOutput: the result's skipped entries are output lines,
// sanitized, and Result.Skipped.
func TestRunSkippedAreOutput(t *testing.T) {
	shim(t, lines("3", `{"v":1,"t":"start","verb":"repair","version":"0.1.0"}`,
		`{"v":1,"t":"result","ok":false,"code":3,"child_exit":null,"skipped":[{"name":"a\u001b]52;c;eA==\u0007","reason":"repo has a different version"}],"error":"nothing to reinstall"}`))
	r := runStep(t, actions.RepairStep([]string{"a"}))
	if want := []string{"Skipped a: repo has a different version"}; !slices.Equal(r.output(), want) {
		t.Errorf("output %q, want %q", r.output(), want)
	}
	if ex := r.exited(t); ex.Err == nil || len(r.out.Result.Skipped) != 1 {
		t.Errorf("StepExited %+v, outcome %+v", ex, r.out)
	}
}

// TestRunKilledMidStream: the helper dies after the grant, before its
// result: the output so far is delivered, then a failure (not NotGranted).
func TestRunKilledMidStream(t *testing.T) {
	shim(t, "cat <<'EOF'\n"+startCheck+"\n"+`{"v":1,"t":"line","stream":"out","text":"half"}`+"\nEOF\nkill -9 $$")
	r := runStep(t, repairCheck)
	ex := r.exited(t)
	if ex.NotGranted || ex.Err == nil || !strings.Contains(ex.Err.Error(), "without a result") {
		t.Fatalf("StepExited %+v", ex)
	}
	if r.granted() != 1 || !slices.Equal(r.output(), []string{"half"}) || r.out.Exit != -1 {
		t.Errorf("inputs %#v, outcome %+v", r.inputs, r.out)
	}
}

// TestRunKilledMidLine: a kill in the middle of a line leaves it
// unterminated: a protocol error, not a parsed half-line.
func TestRunKilledMidLine(t *testing.T) {
	shim(t, "echo '"+startCheck+"'\nprintf '%s' '{\"v\":1,\"t\":\"result\",\"ok\":true,\"code\":0'\nkill -9 $$")
	r := runStep(t, repairCheck)
	if ex := r.exited(t); !errors.Is(ex.Err, ErrProtocol) {
		t.Fatalf("StepExited %+v, want a protocol error", ex)
	}
}

// TestRunMalformedJSON: a broken line fails the step closed even though a
// good result and exit 0 follow; the rest isn't interpreted. Fails on a
// client that skips lines it can't parse.
func TestRunMalformedJSON(t *testing.T) {
	shim(t, lines("0", startCheck,
		`{"v":1,"t":"line","text":`,
		`{"v":1,"t":"line","stream":"out","text":"after"}`,
		`{"v":1,"t":"check","checked":["big"],"broken":["big"]}`,
		resultOK))
	r := runStep(t, repairCheck)
	ex := r.exited(t)
	if !errors.Is(ex.Err, ErrProtocol) || ex.NotGranted {
		t.Fatalf("StepExited %+v, want a protocol error", ex)
	}
	if len(r.output()) != 0 || r.out.Check != nil || r.out.Result != nil {
		t.Errorf("nothing after the broken line may count: output %q, outcome %+v", r.output(), r.out)
	}
}

// TestRunHugeLine: a 1 MiB line is refused without being buffered whole or
// crashing, and fails the step although a good result follows.
func TestRunHugeLine(t *testing.T) {
	shim(t, "echo '"+startCheck+"'\n"+
		`printf '{"v":1,"t":"line","stream":"out","text":"'`+"\n"+
		`head -c 1048576 /dev/zero | tr '\0' a`+"\n"+
		`printf '"}\n'`+"\n"+
		"echo '"+resultOK+"'\nexit 0")
	r := runStep(t, repairCheck)
	ex := r.exited(t)
	if !errors.Is(ex.Err, ErrProtocol) || !strings.Contains(ex.Err.Error(), "over") {
		t.Fatalf("StepExited %+v, want a protocol error for the long line", ex)
	}
	if len(r.output()) != 0 {
		t.Errorf("relayed %d lines of the huge one", len(r.output()))
	}
}

// TestRunLongLineUnderCap: a line just under the cap is fine.
func TestRunLongLineUnderCap(t *testing.T) {
	shim(t, "echo '"+startCheck+"'\n"+
		`printf '{"v":1,"t":"line","stream":"out","text":"'`+"\n"+
		`head -c 400000 /dev/zero | tr '\0' a`+"\n"+
		`printf '"}\n'`+"\n"+
		"echo '"+resultOK+"'\nexit 0")
	r := runStep(t, repairCheck)
	if ex := r.exited(t); ex.Err != nil || len(r.output()) != 1 || len(r.output()[0]) != 400000 {
		t.Fatalf("StepExited %+v, %d lines", ex, len(r.output()))
	}
}

// TestRunUnknownEvents: an event type this client doesn't know is ignored
// and counted; the run still succeeds and its lines are delivered.
func TestRunUnknownEvents(t *testing.T) {
	shim(t, lines("0", startCheck,
		`{"v":1,"t":"telemetry","x":{"y":[1,2]}}`,
		`{"v":1,"t":"line","stream":"out","text":"hi"}`,
		`{"v":1,"t":"eta","text":5}`,
		resultOK))
	r := runStep(t, repairCheck)
	if ex := r.exited(t); ex.Err != nil {
		t.Fatalf("StepExited %+v", ex)
	}
	if r.out.Unknown != 2 || !slices.Equal(r.output(), []string{"hi"}) {
		t.Errorf("unknown %d, output %q", r.out.Unknown, r.output())
	}
}

// TestRunVersion: every line must carry "v":1; one that doesn't fails the
// step closed although a good result and exit 0 follow.
func TestRunVersion(t *testing.T) {
	for name, bad := range map[string]string{
		"no v":       `{"t":"line","stream":"out","text":"x"}`,
		"v 2":        `{"v":2,"t":"line","stream":"out","text":"x"}`,
		"v string":   `{"v":"1","t":"line","stream":"out","text":"x"}`,
		"v null":     `{"v":null,"t":"line","stream":"out","text":"x"}`,
		"no t":       `{"v":1,"stream":"out","text":"x"}`,
		"json null":  `null`,
		"json array": `[1]`,
		"empty line": ``,
		"plain text": `hello`,
	} {
		t.Run(name, func(t *testing.T) {
			shim(t, lines("0", startCheck, bad, resultOK))
			r := runStep(t, repairCheck)
			if ex := r.exited(t); !errors.Is(ex.Err, ErrProtocol) {
				t.Fatalf("StepExited %+v, want a protocol error", ex)
			}
		})
	}
}

// TestRunSanitizes: output is sanitized (no OSC 52, no bidi override), and
// an embedded newline only ever makes another line.
func TestRunSanitizes(t *testing.T) {
	shim(t, lines("0", startCheck,
		`{"v":1,"t":"line","stream":"out","text":"a\u001b]52;c;ZXZpbA==\u0007b‮c\nd\u001b[31mred\u001b[0m"}`,
		resultOK))
	r := runStep(t, repairCheck)
	if want := []string{"abc", "d\x1b[31mred\x1b[0m"}; !slices.Equal(r.output(), want) {
		t.Errorf("output %q, want %q", r.output(), want)
	}
}

// TestRunBatches: a flood arrives whole, in order, in StepOutputs of at
// most batchMax lines.
func TestRunBatches(t *testing.T) {
	shim(t, "echo '"+startCheck+"'\n"+
		`i=0; while [ $i -lt 3000 ]; do echo "{\"v\":1,\"t\":\"line\",\"stream\":\"out\",\"text\":\"$i\"}"; i=$((i+1)); done`+"\n"+
		"echo '"+resultOK+"'")
	r := runStep(t, repairCheck)
	if ex := r.exited(t); ex.Err != nil {
		t.Fatal(ex.Err)
	}
	out := r.output()
	if len(out) != 3000 || out[0] != "0" || out[2999] != "2999" {
		t.Fatalf("got %d lines", len(out))
	}
	for i, l := range out {
		if l != strconv.Itoa(i) {
			t.Fatalf("line %d is %q", i, l)
		}
	}
	for _, in := range r.inputs {
		if o, ok := in.(engine.StepOutput); ok && (len(o.Lines) == 0 || len(o.Lines) > batchMax) {
			t.Errorf("a StepOutput of %d lines", len(o.Lines))
		}
	}
}

// TestRunArgvRefused: a step Argv refuses starts nothing.
func TestRunArgvRefused(t *testing.T) {
	argvFile := shim(t, "exit 0")
	r := runStep(t, steps.Step{Verb: steps.VerbRepair, Items: []string{"--root=/"}})
	if ex := r.exited(t); !errors.Is(ex.Err, ErrBadItems) || len(r.inputs) != 1 {
		t.Fatalf("inputs %#v", r.inputs)
	}
	if _, err := os.Stat(argvFile); !os.IsNotExist(err) {
		t.Error("pkexec ran")
	}
}
