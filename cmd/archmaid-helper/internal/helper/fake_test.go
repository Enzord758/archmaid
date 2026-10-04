package helper

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// reply is a fake command's outcome.
type reply struct {
	out, err string
	exit     int
}

// fakeRunner records every argv and answers from a table keyed by the
// space-joined argv; unknown commands exit 0 with no output. It never
// executes anything.
type fakeRunner struct {
	mu      sync.Mutex
	replies map[string]reply
	queries [][]string
	streams [][]string
	// streamOut is printed (stdout) by every Stream call.
	streamOut string
}

func newFake() *fakeRunner { return &fakeRunner{replies: map[string]reply{}} }

func (f *fakeRunner) on(argv []string, r reply) *fakeRunner {
	f.replies[strings.Join(argv, " ")] = r
	return f
}

func (f *fakeRunner) Query(argv []string) (string, string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, slices.Clone(argv))
	r := f.replies[strings.Join(argv, " ")]
	return r.out, r.err, r.exit, nil
}

func (f *fakeRunner) Stream(argv []string, line func(stream, text string)) (int, error) {
	f.mu.Lock()
	f.streams = append(f.streams, slices.Clone(argv))
	r := f.replies[strings.Join(argv, " ")]
	f.mu.Unlock()
	for _, l := range nonEmptyLines(f.streamOut + r.out) {
		line("out", l)
	}
	for _, l := range nonEmptyLines(r.err) {
		line("err", l)
	}
	return r.exit, nil
}

// rootStat fakes a correctly installed helper: everything root-owned 0755,
// the binary a regular file.
func rootStat(p string) (FileStat, error) {
	if p == InstallPath {
		return FileStat{Uid: 0, Mode: 0o755}, nil
	}
	return FileStat{Uid: 0, Mode: fs.ModeDir | 0o755}, nil
}

// rootOpts is a session that passes every privilege check without root.
func rootOpts(t *testing.T, r Runner) (Options, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	var out bytes.Buffer
	return Options{
		Runner:       r,
		Stdout:       &out,
		Euid:         0,
		CallerUID:    "1000",
		Exe:          func() (string, error) { return InstallPath, nil },
		Stat:         rootStat,
		LockPath:     filepath.Join(dir, "helper.lock"),
		PacmanDBLock: filepath.Join(dir, "db.lck"),
	}, &out
}

// events decodes the JSON lines and checks the envelope every line must have.
func events(t *testing.T, out *bytes.Buffer) []map[string]any {
	t.Helper()
	var evs []map[string]any
	for i, l := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		var ev map[string]any
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatalf("line %d is not JSON: %q: %v", i+1, l, err)
		}
		if ev["v"] != float64(1) {
			t.Fatalf("line %d: v = %v, want 1: %s", i+1, ev["v"], l)
		}
		evs = append(evs, ev)
	}
	if len(evs) < 2 || evs[0]["t"] != EventStart || evs[len(evs)-1]["t"] != EventResult {
		t.Fatalf("output must start with start and end with result:\n%s", out)
	}
	return evs
}

func result(t *testing.T, out *bytes.Buffer) map[string]any {
	t.Helper()
	evs := events(t, out)
	return evs[len(evs)-1]
}

func ofType(evs []map[string]any, typ string) []map[string]any {
	var r []map[string]any
	for _, e := range evs {
		if e["t"] == typ {
			r = append(r, e)
		}
	}
	return r
}

func skippedNames(t *testing.T, out *bytes.Buffer) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, s := range result(t, out)["skipped"].([]any) {
		sk := s.(map[string]any)
		m[sk["name"].(string)] = sk["reason"].(string)
	}
	return m
}

func join(argvs [][]string) []string {
	var r []string
	for _, a := range argvs {
		r = append(r, strings.Join(a, " "))
	}
	return r
}
