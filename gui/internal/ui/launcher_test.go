package ui

import (
	"errors"
	"slices"
	"sync"
	"testing"
)

// recordLauncher is the harness's Launcher: it opens nothing, records each
// call ("file <path>", "folder <path>", "choose <start>"), and answers
// ChooseFolder with Choose (or ErrCancelled-like Err when set).
type recordLauncher struct {
	mu     sync.Mutex
	calls  []string
	Choose string
	Err    error
}

var _ Launcher = (*recordLauncher)(nil)

func (r *recordLauncher) add(s string) {
	r.mu.Lock()
	r.calls = append(r.calls, s)
	r.mu.Unlock()
}

func (r *recordLauncher) OpenFile(path string) error   { r.add("file " + path); return nil }
func (r *recordLauncher) OpenFolder(path string) error { r.add("folder " + path); return nil }
func (r *recordLauncher) ChooseFolder(start string, done func(string, error)) {
	r.add("choose " + start)
	if r.Err != nil {
		done("", r.Err)
		return
	}
	done(r.Choose, nil)
}

// Calls is what was asked of it, in order.
func (r *recordLauncher) Calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

func TestRecordLauncher(t *testing.T) {
	r := &recordLauncher{Choose: "/tmp/x"}
	_ = r.OpenFile("/a/topgrade.toml")
	_ = r.OpenFolder("/a/state")
	var got string
	r.ChooseFolder("/home/u", func(p string, err error) {
		if err != nil {
			t.Fatal(err)
		}
		got = p
	})
	if got != "/tmp/x" {
		t.Fatalf("chose %q", got)
	}
	r.Err = errors.New("cancelled")
	r.ChooseFolder("", func(p string, err error) {
		if err == nil || p != "" {
			t.Fatalf("a cancelled choice gave %q, %v", p, err)
		}
	})
	if want := []string{"file /a/topgrade.toml", "folder /a/state", "choose /home/u", "choose "}; !slices.Equal(r.Calls(), want) {
		t.Fatalf("calls %q, want %q", r.Calls(), want)
	}
}
