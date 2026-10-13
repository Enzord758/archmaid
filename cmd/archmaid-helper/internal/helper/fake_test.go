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
	// fail: the command couldn't be run at all (Query's and Stream's err).
	fail error
}

// fakeRunner records every argv and answers from a table keyed by the
// space-joined argv; unknown commands exit 0 with no output. It never
// executes anything. A recorded argv is exact; the lookup falls back to
// the argv without its pacman `--color <when>` pair, so a reply set up
// for `pacman -Qdtq` answers `pacman -Qdtq --color never` too
// (TestColourPerCall pins the pairs themselves).
type fakeRunner struct {
	mu      sync.Mutex
	replies map[string]reply
	queries [][]string
	streams [][]string
	// streamOut is printed (stdout) by every Stream call.
	streamOut string
	// onStream, if set, is called with every Stream argv before its reply,
	// so a test can look at (or change) the world as the command starts.
	onStream func(argv []string)
}

func newFake() *fakeRunner { return &fakeRunner{replies: map[string]reply{}} }

func (f *fakeRunner) on(argv []string, r reply) *fakeRunner {
	f.replies[strings.Join(argv, " ")] = r
	return f
}

// answer is argv's reply: by the exact argv, else by argv without its
// `--color <when>` pair. f.mu must be held.
func (f *fakeRunner) answer(argv []string) reply {
	if r, ok := f.replies[strings.Join(argv, " ")]; ok {
		return r
	}
	return f.replies[strings.Join(withoutColour(argv), " ")]
}

// withoutColour is argv without its first `--color <when>` pair.
func withoutColour(argv []string) []string {
	i := slices.Index(argv, "--color")
	if i < 0 || i+1 >= len(argv) {
		return argv
	}
	return slices.Concat(argv[:i], argv[i+2:])
}

func (f *fakeRunner) Query(argv []string) (string, string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, slices.Clone(argv))
	r := f.answer(argv)
	if r.fail != nil {
		return "", "", -1, r.fail
	}
	return r.out, r.err, r.exit, nil
}

func (f *fakeRunner) Stream(argv []string, line func(stream, text string)) (int, error) {
	f.mu.Lock()
	f.streams = append(f.streams, slices.Clone(argv))
	r := f.answer(argv)
	hook := f.onStream
	f.mu.Unlock()
	if hook != nil {
		hook(argv)
	}
	if r.fail != nil {
		return -1, r.fail
	}
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
		// the temp dir is the test user's, not root's
		lockDirCheck: func(string) error { return nil },
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
