package tui

// The transcript harness: runs a whole menu action against shimmed programs
// and records what happened — which programs ran, what was asked, the plan,
// every step transition, its output and its log entries — as a
// frontend-agnostic text transcript, golden-compared under
// testdata/transcripts/. Nothing is rendered.
//
// It's two halves. internal/testtranscript's Recorder owns the format and the
// answer script; a Target runs the action and reports events.
// tuiEngineTarget (transcript_engine_test.go) drives the TUI's model on a
// syncDriver; sessionTarget drives engine.Session and driverTarget the
// syncDriver alone, against the same shims, and the same goldens hold them
// to the same behaviour. See TESTING.md → Transcripts.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/steps"

	tt "github.com/Enzord758/archmaid/internal/testtranscript"
)

var updateTranscripts = flag.Bool("update-transcripts", false, "rewrite testdata/transcripts/*.golden")

// ── shims ────────────────────────────────────────────────────────────────────

// Rule answers the invocations of a shimmed command whose argv ("$*") starts
// with Prefix ("" matches anything). Rules are tried in order; the first match
// runs Script (raw sh), prints Out, then exits with Exit — or, with Block,
// execs `sleep 30` instead so the step can be aborted.
type Rule struct {
	Prefix string
	Out    string
	Exit   int
	Script string
	Block  bool
	// TrapINT makes the whole shim ignore SIGINT from its first line (before
	// it logs, so an abort can't race the trap); with Block, only SIGKILL
	// ends it.
	TrapINT bool
}

// Shims maps a command name to its rules. A command with no matching rule
// prints nothing and exits 0.
type Shims map[string][]Rule

// installShims writes one #!/bin/sh shim per command into a fresh bin dir and
// makes that dir the whole PATH, so anything not shimmed is "not installed".
// passthrough symlinks real tools (resolved on the original PATH) into it, for
// steps that need e.g. bash/find under a scratch HOME. Every shim appends
// "<cmd> <argv>" to the returned log before doing anything else.
func installShims(t *testing.T, sh Shims, passthrough ...string) (shimLog string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "bin")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shimLog = filepath.Join(root, "exec.log")
	if err := os.WriteFile(shimLog, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := func(name string) {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return
		}
		real, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("passthrough %s: %v", name, err)
		}
		if err := os.Symlink(real, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	for name, rules := range sh {
		var b strings.Builder
		b.WriteString("#!/bin/sh\n")
		for _, r := range rules {
			if r.TrapINT {
				b.WriteString("trap '' INT\n")
				break
			}
		}
		fmt.Fprintf(&b, "printf '%%s\\n' %s >> \"$ARCHMAID_SHIM_LOG\"\n", shQuote(name)+`" $*"`)
		b.WriteString("case \"$*\" in\n")
		for _, r := range rules {
			fmt.Fprintf(&b, "%s*)\n", shQuote(r.Prefix))
			if r.Script != "" {
				b.WriteString(r.Script + "\n")
			}
			if r.Out != "" {
				for _, l := range strings.Split(strings.TrimSuffix(r.Out, "\n"), "\n") {
					fmt.Fprintf(&b, "printf '%%s\\n' %s\n", shQuote(l))
				}
			}
			if r.Block {
				link("sleep")
				b.WriteString("exec sleep 30\n")
			}
			fmt.Fprintf(&b, "exit %d;;\n", r.Exit)
		}
		b.WriteString("esac\nexit 0\n")
		if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range passthrough {
		link(name)
	}
	t.Setenv("PATH", dir)
	t.Setenv("ARCHMAID_SHIM_LOG", shimLog)
	return shimLog
}

// shQuote single-quotes s for sh.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ── script ───────────────────────────────────────────────────────────────────

// Answer is one scripted reply; see tt.Answer for the kinds. It's tt.Answer
// declared here (not aliased) so scenarios can write Answer{"confirm", …, "yes"}
// without go vet's composites check objecting to another package's struct.
type Answer struct{ Kind, Match, Reply string }

// shimPassword is the password the su shim accepts (see suRules).
const shimPassword = "hunter2"

// newTarget makes the Target a scenario runs on, once the scenario has set up
// its environment (scratch HOME and log dir, shims, fakes): tuiEngineTarget,
// sessionTarget or driverTarget.
type newTarget func(t *testing.T, shimLog string) tt.Target

// record runs the menu item matching item on a fresh target and records it,
// answering prompts from script. $HOME and the log dir are normalised.
func record(t *testing.T, mk newTarget, shimLog, item string, script ...Answer) *tt.Recorder {
	t.Helper()
	tg := mk(t, shimLog)
	answers := make([]tt.Answer, len(script))
	for i, a := range script {
		answers[i] = tt.Answer(a)
	}
	r := tt.NewRecorder(t, answers, os.Getenv("HOME"), "$HOME", logDir, "$LOGDIR")
	r.Run(tg, item)
	return r
}

// Driver is an action recorded through the TUI, for a test that also checks
// the model it left behind (d.m).
type Driver struct {
	*tt.Recorder
	*tuiEngineTarget
}

// run records item through a tuiEngineTarget and keeps the model.
func run(t *testing.T, shimLog, item string, script ...Answer) *Driver {
	t.Helper()
	var tg *tuiEngineTarget
	r := record(t, func(t *testing.T, shimLog string) tt.Target {
		tg = newTUIEngineTarget(t, shimLog)
		return tg
	}, shimLog, item, script...)
	return &Driver{r, tg}
}

// ── side effects ─────────────────────────────────────────────────────────────

// effects watches what an action leaves outside the frontend: the programs
// the shims logged, log entries and the AUR snapshot. It's target-agnostic: a
// sessionTarget running the engine against the same shims and log store reads
// them the same way.
type effects struct {
	shimLog string
	shimOff int
	logOff  map[string]int
	snap    string // last seen AUR snapshot content
}

// newEffects starts watching from now: only what the action writes counts.
func newEffects(shimLog string) *effects {
	fx := &effects{shimLog: shimLog, logOff: map[string]int{}}
	fx.snap, _ = readFileString(baselinePath())
	for _, f := range fx.logFiles() {
		b, _ := os.ReadFile(f)
		fx.logOff[f] = len(b)
	}
	return fx
}

// execs returns the shim invocations logged since the last call. Runs of one
// command with the same first argument are sorted: a pipeline's processes, or
// Repair's parallel `pacman -Qkq -- <chunk>` scans, start concurrently, so
// their relative order isn't behaviour (an order between different commands
// is, as is `pacman -Qq` before the scans it feeds).
func (fx *effects) execs() []tt.Event {
	b, _ := os.ReadFile(fx.shimLog)
	if len(b) <= fx.shimOff {
		return nil
	}
	chunk := string(b[fx.shimOff:])
	fx.shimOff = len(b)
	lines := strings.Split(strings.TrimSuffix(chunk, "\n"), "\n")
	runKey := func(l string) string {
		if f := strings.Fields(l); len(f) > 1 {
			return f[0] + " " + f[1]
		}
		return l
	}
	for i := 0; i < len(lines); {
		j := i + 1
		for j < len(lines) && runKey(lines[j]) == runKey(lines[i]) {
			j++
		}
		sort.Strings(lines[i:j])
		i = j
	}
	evs := make([]tt.Event, len(lines))
	for i, l := range lines {
		evs[i] = tt.Exec{Cmdline: l}
	}
	return evs
}

// awaitExec waits until a child has logged itself to its shim, so the shim's
// SIGINT trap (if any) is installed and its exec line is deterministic.
func (fx *effects) awaitExec(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		b, _ := os.ReadFile(fx.shimLog)
		if len(b) > fx.shimOff && b[len(b)-1] == '\n' {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

var logStamp = regexp.MustCompile(`^\[\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\] `)

func (fx *effects) logFiles() []string {
	files, _ := filepath.Glob(filepath.Join(resolveLogDir(), "*", "*.log"))
	sort.Strings(files)
	return files
}

// logs returns the new log entries (header lines only; bodies are the out
// lines already recorded), without timestamp or run directory, grouped by
// category file.
func (fx *effects) logs() []tt.Event {
	var evs []tt.Event
	for _, f := range fx.logFiles() {
		b, _ := os.ReadFile(f)
		if len(b) <= fx.logOff[f] {
			continue
		}
		cat := strings.TrimSuffix(filepath.Base(f), ".log")
		for _, l := range strings.Split(string(b[fx.logOff[f]:]), "\n") {
			if logStamp.MatchString(l) {
				evs = append(evs, tt.LogWritten{Category: cat, Header: logStamp.ReplaceAllString(l, "")})
			}
		}
		fx.logOff[f] = len(b)
	}
	return evs
}

// snapshot returns the AUR maintainer snapshot when its content changed.
func (fx *effects) snapshot() []tt.Event {
	s, _ := readFileString(baselinePath())
	if s == fx.snap {
		return nil
	}
	fx.snap = s
	var m map[string]snapEntry
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return []tt.Event{tt.SnapshotChanged{Unreadable: true}}
	}
	ev := tt.SnapshotChanged{Entries: []tt.SnapEntry{}}
	for n, e := range m {
		ev.Entries = append(ev.Entries, tt.SnapEntry{Name: n, Maintainer: e.Maintainer,
			HasCo: e.HasCo, CoMaintainers: e.CoMaintainers})
	}
	return []tt.Event{ev}
}

func readFileString(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

// planStatus is how the checklist draws s: every "didn't run" state is a skip.
func planStatus(s steps.Status) tt.Status {
	switch s {
	case steps.Pending:
		return tt.Pending
	case steps.Running:
		return tt.Running
	case steps.OK:
		return tt.OK
	case steps.Failed:
		return tt.Failed
	case steps.Skipped, steps.AuthRefused, steps.Aborted:
		return tt.Skipped
	}
	panic(fmt.Sprintf("unknown steps.Status %d", s))
}

// checkGolden compares got with testdata/transcripts/<name>.golden, or
// rewrites it under -update-transcripts.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "transcripts", name+".golden")
	if *updateTranscripts {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update-transcripts to create it)\n%s", err, got)
	}
	if string(want) == got {
		return
	}
	w, g := strings.Split(string(want), "\n"), strings.Split(got, "\n")
	for i := 0; i < max(len(w), len(g)); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			t.Fatalf("transcript %s differs at line %d:\n  want %q\n  got  %q\n(run with -update-transcripts if intended)\n\n%s",
				name, i+1, wl, gl, got)
		}
	}
}
