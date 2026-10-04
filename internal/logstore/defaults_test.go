package logstore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// requireEmptyDir fails unless dir holds nothing at all.
func requireEmptyDir(t *testing.T, dir string) {
	t.Helper()
	var found []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && p != dir {
			found = append(found, p)
		}
		return nil
	})
	if len(found) > 0 {
		t.Fatalf("%s should be empty, holds %q", dir, found)
	}
}

// TestUnknownCategoryRefused: a category names a file, so only the four known
// ones do. Anything else ("../x" included) has no Path, and Append and Read
// refuse it without touching the disk.
func TestUnknownCategoryRefused(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "state")
	s := newStore(t, root)
	for _, cat := range []Category{"../escape", "../../escape", "", "Cleanup", "audit/x"} {
		if p := s.Path(cat); p != "" {
			t.Errorf("Path(%q) = %q, want \"\"", cat, p)
		}
		if err := s.Append(cat, "OK", "label", "body"); !errors.Is(err, ErrUnknownCategory) {
			t.Errorf("Append(%q) = %v, want ErrUnknownCategory", cat, err)
		}
		if _, err := s.Read(cat); !errors.Is(err, ErrUnknownCategory) {
			t.Errorf("Read(%q) = %v, want ErrUnknownCategory", cat, err)
		}
	}
	requireEmptyDir(t, parent)
	for _, ci := range Categories {
		if !ci.ID.Valid() {
			t.Errorf("%q is listed but not Valid", ci.ID)
		}
	}
}

// TestNoHomeNoRelativeStateDir: with $HOME unset or relative, and with a
// relative Dir, the store refuses to write rather than use .local/state under
// whatever the working directory is.
func TestNoHomeNoRelativeStateDir(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T) Options{
		"HOME unset":    func(t *testing.T) Options { t.Setenv("HOME", ""); return Options{} },
		"HOME relative": func(t *testing.T) Options { t.Setenv("HOME", "relative/home"); return Options{} },
		"Dir relative":  func(t *testing.T) Options { return Options{Dir: "state"} },
	} {
		t.Run(name, func(t *testing.T) {
			cwd := t.TempDir()
			t.Chdir(cwd)
			s := New(setup(t))
			if err := s.Append(Cleanup, "OK", "label", "body"); !errors.Is(err, ErrNoStateDir) {
				t.Errorf("Append = %v, want ErrNoStateDir", err)
			}
			if err := s.WriteLastRun(RunRecord{Outcome: OutcomeOK}); !errors.Is(err, ErrNoStateDir) {
				t.Errorf("WriteLastRun = %v, want ErrNoStateDir", err)
			}
			if _, err := s.Read(Cleanup); !errors.Is(err, ErrNoStateDir) {
				t.Errorf("Read = %v, want ErrNoStateDir", err)
			}
			if _, err := s.ReadLastRun(); !errors.Is(err, ErrNoStateDir) {
				t.Errorf("ReadLastRun = %v, want ErrNoStateDir", err)
			}
			s.Prune(0)
			if s.Root() != "" || s.RunDir() != "" || s.Path(Cleanup) != "" {
				t.Errorf("Root/RunDir/Path = %q, %q, %q; want all empty", s.Root(), s.RunDir(), s.Path(Cleanup))
			}
			requireEmptyDir(t, cwd)
		})
	}
}

// TestAbsoluteHomeStillWorks: the default under an absolute $HOME is
// unchanged.
func TestAbsoluteHomeStillWorks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s := New(Options{})
	if want := filepath.Join(home, ".local", "state", "archmaid"); s.Root() != want {
		t.Fatalf("Root = %q, want %q", s.Root(), want)
	}
	if err := s.Append(Cleanup, "OK", "label", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Path(Cleanup)); err != nil {
		t.Fatal(err)
	}
}
