package helperclient

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
	"golang.org/x/sys/unix"
)

// The client's edges: helper output with a field of the wrong type, a
// combined call's odd part events, an install check or pkexec that can't
// be used, and pipes the kernel won't give. Each fails the step closed,
// with no check event and nothing started where nothing should be.

// TestDecodeMalformedFields: a known event whose fields don't decode is a
// protocol error for every event type, the combined call's included.
func TestDecodeMalformedFields(t *testing.T) {
	paccache := steps.Step{Verb: steps.VerbPaccache}
	startPaccache := `{"v":1,"t":"start","verb":"paccache","version":"0.1.0"}`
	plan := steps.Step{Verb: steps.VerbPlanOrphans}
	full := fullCleanupCall(false, false, false)
	startFull := `{"v":1,"t":"start","verb":"full-cleanup","version":"0.1.0"}`
	cases := map[string]struct {
		st steps.Step
		ls []string
	}{
		"start":    {paccache, []string{`{"v":1,"t":"start","verb":5}`, resultOK}},
		"check":    {repairCheck, []string{startCheck, `{"v":1,"t":"check","checked":"big","broken":[]}`, resultOK}},
		"result":   {paccache, []string{startPaccache, `{"v":1,"t":"result","ok":"yes","code":0}`}},
		"plan":     {plan, []string{startPlan, `{"v":1,"t":"plan","targets":[],"items":[],"total":"0","hash":"x"}`, resultOK}},
		"progress": {full, []string{startFull, `{"v":1,"t":"progress","step":"1","of":4,"part":"paccache","label":"x"}`, resultOK}},
		"done":     {full, []string{startFull, progressLine(1, 4, "paccache", "x"), `{"v":1,"t":"done","step":1,"of":4,"part":"paccache","ok":true,"code":"0"}`, resultOK}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			o := decodeAll(c.st, 0, c.ls...)
			if !errors.Is(o.Err, ErrProtocol) || !strings.Contains(o.Err.Error(), "malformed") {
				t.Fatalf("Err %v, want a malformed-line protocol error", o.Err)
			}
			if o.Check != nil {
				t.Errorf("Check %+v kept on a protocol error", o.Check)
			}
		})
	}
}

// feedParts feeds ls to a decoder for st, and returns the output lines and
// the part events, in order, as short strings.
func feedParts(st steps.Step, ls ...string) (d *decoder, trace []string) {
	d = newDecoder(st)
	for _, l := range ls {
		out, _ := d.feed([]byte(l))
		for _, e := range d.takeParts() {
			s := "start " + e.step
			if e.done {
				s = "done " + e.step
				if e.err != nil {
					s += ": " + e.err.Error()
				}
			}
			trace = append(trace, s)
		}
		for _, o := range out {
			trace = append(trace, "| "+o)
		}
	}
	return d, trace
}

// TestCombinedPartEdges: a part's label that sanitizes to nothing is no
// output line; a part that failed without a message still fails ("failed");
// a done event whose ok and code disagree fails the call.
func TestCombinedPartEdges(t *testing.T) {
	full := fullCleanupCall(false, false, false) // paccache, clean-tmp, vacuum-journal, check-files
	start := `{"v":1,"t":"start","verb":"full-cleanup","version":"0.1.0"}`
	d, trace := feedParts(full, start,
		`{"v":1,"t":"progress","step":1,"of":4,"label":"\u001b[2J\u001b]0;x\u0007","part":"paccache"}`,
		doneLine(1, 4, "paccache", 1, ""),
		progressLine(2, 4, "clean-tmp", "tmp"))
	want := []string{"done paccache: root helper (exit 1): failed", "start tmpfiles", "| tmp"}
	if fmt.Sprint(trace) != fmt.Sprint(want) || d.err != nil {
		t.Fatalf("trace %q (err %v), want %q", trace, d.err, want)
	}

	for name, done := range map[string]string{
		"ok with code 1": `{"v":1,"t":"done","step":1,"of":4,"part":"paccache","ok":true,"code":1}`,
		"no ok":          `{"v":1,"t":"done","step":1,"of":4,"part":"paccache","code":0}`,
		"no code":        `{"v":1,"t":"done","step":1,"of":4,"part":"paccache","ok":true}`,
	} {
		o := decodeAll(full, 0, start, progressLine(1, 4, "paccache", "x"), done, resultOK)
		if !errors.Is(o.Err, ErrProtocol) || !strings.Contains(o.Err.Error(), `consistent "ok" and "code"`) {
			t.Errorf("%s: Err %v, want a protocol error", name, o.Err)
		}
	}
}

// TestDecodeFailedWithoutMessage: a failed result with no error text still
// fails the step, as "failed".
func TestDecodeFailedWithoutMessage(t *testing.T) {
	o := decodeAll(steps.Step{Verb: steps.VerbPaccache}, 1,
		`{"v":1,"t":"start","verb":"paccache","version":"0.1.0"}`,
		`{"v":1,"t":"result","ok":false,"code":1,"child_exit":1,"skipped":[]}`)
	if o.Err == nil || o.Err.Error() != "root helper (exit 1): failed" {
		t.Fatalf("Err %v", o.Err)
	}
}

// TestCheckPlanCascadeTwice: a cascade that lists a package twice is not a
// plan pacman printed: refused, whatever its hash says.
func TestCheckPlanCascadeTwice(t *testing.T) {
	cascade := []string{"libfoo 1-1", "libfoo 1-1"}
	p := steps.OrphanPlan{Direct: []string{"libfoo"}, Cascade: cascade, Hash: PlanHash([]string{"libfoo"}, cascade)}
	if err := CheckPlan(p); !errors.Is(err, ErrBadPlan) || !strings.Contains(err.Error(), "listed twice") {
		t.Fatalf("CheckPlan = %v, want ErrBadPlan", err)
	}
}

// TestRunInstallCheckFails: a helper path that can't be checked (it runs
// through a file) fails the step without starting pkexec, and is not a
// refusal.
func TestRunInstallCheckFails(t *testing.T) {
	argvFile := shim(t, "exit 0")
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setHelperFile(t, filepath.Join(file, "archmaid-helper"))
	r := runStep(t, repairCheck)
	ex := r.exited(t)
	if ex.NotGranted || ex.Err == nil || !strings.Contains(ex.Err.Error(), "can't check the root helper") || len(r.inputs) != 1 {
		t.Fatalf("inputs %#v", r.inputs)
	}
	if _, err := os.Stat(argvFile); !errors.Is(err, fs.ErrNotExist) {
		t.Error("pkexec ran")
	}
}

// TestRunNoPkexec: pkexec that can't be started fails the step: not a
// refusal (nobody was asked), and not granted.
func TestRunNoPkexec(t *testing.T) {
	fakeHelperInstalled(t, t.TempDir())
	t.Setenv("PATH", t.TempDir())
	r := runStep(t, repairCheck)
	ex := r.exited(t)
	if ex.NotGranted || ex.Err == nil || !strings.Contains(ex.Err.Error(), "can't start "+Pkexec) || len(r.inputs) != 1 {
		t.Fatalf("inputs %#v", r.inputs)
	}
	if r.out.Granted || r.out.Exit != -1 {
		t.Errorf("outcome %+v", r.out)
	}
}

// TestStoppedLateText: a late Stop's message says what the root helper
// did: its exit, its root command's and how many it skipped.
func TestStoppedLateText(t *testing.T) {
	zero := 0
	got := resultText(&Result{OK: true, ChildExit: &zero, Skipped: []Skip{{Name: "a", Reason: "x"}, {Name: "b", Reason: "y"}}})
	if got != "ok (exit 0, child exit 0, 2 skipped)" {
		t.Fatalf("resultText = %q", got)
	}
}

// TestExitHelpers: a Wait error that isn't an exit status is exit -1,
// never 0 (success); exit -1 with no error reads "killed"; stderr's tail
// keeps the last bytes written.
func TestExitHelpers(t *testing.T) {
	if got := exitCode(errors.New("exec: WaitDelay expired before I/O complete")); got != -1 {
		t.Errorf("exitCode(other error) = %d, want -1", got)
	}
	if got := exitText(-1, nil); got != "killed" {
		t.Errorf("exitText(-1, nil) = %q", got)
	}
	if got := exitText(-1, errors.New("signal: killed\x1b[2J")); got != "signal: killed" {
		t.Errorf("exitText(-1, err) = %q", got)
	}
	tl := &tail{max: 4}
	for _, s := range []string{"abc", "defg", "hi"} {
		if n, err := tl.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", s, n, err)
		}
	}
	if got := tl.String(); got != "fghi" {
		t.Errorf("tail = %q, want the last 4 bytes", got)
	}
}

// envNoFDChild runs TestHelperclientNoFDChild: this test binary again,
// with one file descriptor left, so no pipe can be made.
const envNoFDChild = "ARCHMAID_HELPERCLIENT_NOFD_CHILD"

// TestHelperclientNoFDChild: Run ("run") and plan-orphans ("plan") with
// no pipe to be had fail before anything is started.
func TestHelperclientNoFDChild(t *testing.T) {
	which := os.Getenv(envNoFDChild)
	if which == "" {
		t.Skip("run by TestNoPipes")
	}
	helper := os.Getenv("ARCHMAID_NOFD_HELPER")
	switch which {
	case "run":
		setHelperFile(t, helper)
		var inputs []engine.Input
		var o Outcome
		withOneFreeFD(t, func() {
			o = Run(context.Background(), seq, repairCheck, func(in engine.Input) { inputs = append(inputs, in) })
		})
		ex, ok := inputs[len(inputs)-1].(engine.StepExited)
		// "helper: …" is Run's own pipe; without it Start fails on its
		// stderr pipe instead ("can't start pkexec: pipe2: …").
		if len(inputs) != 1 || !ok || ex.NotGranted || !pipeFailed(ex.Err) || !strings.HasPrefix(ex.Err.Error(), "helper: ") || o.Granted {
			t.Fatalf("inputs %#v, outcome %+v", inputs, o)
		}
	case "plan":
		var err error
		withOneFreeFD(t, func() { _, err = planOrphans(context.Background(), helper) })
		if !pipeFailed(err) {
			t.Fatalf("planOrphans = %v, want pipe2's EMFILE", err)
		}
	}
}

// pipeFailed: err is the pipe's own EMFILE, not a later step's (Start's
// open of /dev/null fails the same way when the guard is gone).
func pipeFailed(err error) bool {
	var se *os.SyscallError
	return errors.Is(err, syscall.EMFILE) && errors.As(err, &se) && se.Syscall == "pipe2"
}

// withOneFreeFD runs f with one file descriptor left under a lowered
// RLIMIT_NOFILE, then gives them all back.
func withOneFreeFD(t *testing.T, f func()) {
	t.Helper()
	r, w, err := os.Pipe() // the poller exists before the table fills
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	w.Close()
	var old unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &old); err != nil {
		t.Fatal(err)
	}
	low := old
	low.Cur = 256
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &low); err != nil {
		t.Fatal(err)
	}
	defer unix.Setrlimit(unix.RLIMIT_NOFILE, &old)
	var fds []int
	defer func() {
		for _, fd := range fds {
			unix.Close(fd)
		}
	}()
	for {
		fd, err := unix.Open("/dev/null", unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.EMFILE) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		fds = append(fds, fd)
	}
	if len(fds) == 0 {
		t.Fatal("no fd to free")
	}
	unix.Close(fds[len(fds)-1])
	fds = fds[:len(fds)-1]
	f()
}

// TestNoPipes: Run and plan-orphans fail when the kernel won't give them
// a pipe, and neither pkexec nor the helper runs.
func TestNoPipes(t *testing.T) {
	for _, which := range []string{"run", "plan"} {
		argvFile := shim(t, "exit 0")                 // pkexec
		helper, helperArgv := fakeHelper(t, "exit 0") // plan-orphans' helper
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperclientNoFDChild$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), envNoFDChild+"="+which, "ARCHMAID_NOFD_HELPER="+helper)
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "--- PASS: TestHelperclientNoFDChild") {
			t.Fatalf("%s: child: %v\n%s", which, err, out)
		}
		for _, f := range []string{argvFile, helperArgv} {
			if _, err := os.Stat(f); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("%s: %s ran", which, filepath.Base(filepath.Dir(f)))
			}
		}
	}
}
