package health

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The real seams: statFS reports a real folder's size and device and fails on
// a missing one; Exists is an lstat.
func TestDefaultSeams(t *testing.T) {
	dir := t.TempDir()
	fi, err := statFS(dir)
	if err != nil || fi.Total == 0 || fi.Avail > fi.Total || fi.Dev == 0 {
		t.Errorf("statFS(%s) = %+v, %v", dir, fi, err)
	}
	if _, err := statFS(filepath.Join(dir, "no-such-folder")); err == nil {
		t.Error("statFS of a missing folder succeeded")
	}
	d := DefaultDeps()
	if !d.Exists(dir) || d.Exists(filepath.Join(dir, "no-such-folder")) {
		t.Error("DefaultDeps().Exists: a folder is missing, or a missing one is there")
	}
	// A dangling link exists as far as lstat goes.
	link := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "gone"), link); err != nil {
		t.Fatal(err)
	}
	if !d.Exists(link) {
		t.Error("a dangling symlink doesn't exist for DefaultDeps().Exists")
	}
}

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestReadCappedReadError(t *testing.T) {
	if _, err := readCapped(failReader{}, 10); err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Errorf("readCapped on a failing reader: %v", err)
	}
	if _, err := readCapped(strings.NewReader(strings.Repeat("x", 11)), 10); err == nil {
		t.Error("readCapped over its cap succeeded")
	}
}

// A file holding more than MaxIgnored keys reads as the first MaxIgnored.
func TestIgnoreLoadStopsAtTheCap(t *testing.T) {
	dir := t.TempDir()
	doc := ignoreDoc{V: 1, Ignored: map[string]string{}}
	for i := range MaxIgnored + 5 {
		doc.Ignored[fmt.Sprintf("key:%04d", i)] = "2026-11-09T12:00:00Z"
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(b)) > maxIgnoreFile {
		t.Skipf("%d keys don't fit the %d byte cap", len(doc.Ignored), maxIgnoreFile)
	}
	if err := os.WriteFile(filepath.Join(dir, IgnoreFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := (&IgnoreStore{Dir: dir}).Load(); len(got) != MaxIgnored {
		t.Errorf("%d keys loaded from %d, want the cap %d", len(got), len(doc.Ignored), MaxIgnored)
	}
}

// A state dir that can't be made (a file stands where its parent should be)
// is an error from Ignore, and a relative Dir is no store at all.
func TestIgnoreWriteCantMakeTheDir(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (&IgnoreStore{Dir: filepath.Join(file, "state")}).Ignore("a"); err == nil {
		t.Error("Ignore under a regular file succeeded")
	}
	s := &IgnoreStore{Dir: "relative/state"}
	if err := s.Ignore("a"); err == nil {
		t.Error("Ignore with a relative state dir succeeded")
	}
	if got := s.Load(); len(got) != 0 {
		t.Errorf("a relative state dir loaded %v", got)
	}
}

// A drive property that isn't a JSON list of strings reads as none.
func TestStrsNotAList(t *testing.T) {
	if got := strs(variant{Data: json.RawMessage(`{"a":1}`)}, 10); got != nil {
		t.Errorf("strs of an object: %v", got)
	}
}
