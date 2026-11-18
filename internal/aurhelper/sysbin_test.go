package aurhelper

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Enzord758/archmaid/internal/sysbin"
	"github.com/Enzord758/archmaid/internal/testenv"
)

func noConfig(lp func(string) (string, error)) Deps {
	return Deps{
		LookPath: lp,
		ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist },
		Getenv:   func(string) string { return "" },
		Home:     func() (string, error) { return "/home/u", nil },
	}
}

// TestDetectAsksForSysbinPaths (#292): Detect looks up internal/sysbin's
// paths and nothing else, so a paru or yay earlier on PATH can't be chosen.
func TestDetectAsksForSysbinPaths(t *testing.T) {
	oldParu, oldYay := sysbin.Paru, sysbin.Yay
	t.Cleanup(func() { sysbin.Paru, sysbin.Yay = oldParu, oldYay })
	sysbin.Paru, sysbin.Yay = "/usr/bin/paru", "/usr/bin/yay"
	var asked []string
	if h := Detect(noConfig(func(f string) (string, error) { asked = append(asked, f); return f, nil })); h != (Helper{Paru, "/usr/bin/paru"}) {
		t.Errorf("Detect = %+v, want paru at /usr/bin/paru", h)
	}
	h := Detect(noConfig(func(f string) (string, error) {
		asked = append(asked, f)
		if f == "/usr/bin/yay" {
			return f, nil
		}
		return "", errors.New("not found")
	}))
	if h != (Helper{Yay, "/usr/bin/yay"}) {
		t.Errorf("Detect = %+v, want yay at /usr/bin/yay", h)
	}
	for _, a := range asked {
		if a != "/usr/bin/paru" && a != "/usr/bin/yay" {
			t.Errorf("Detect looked up %q, want only the sysbin paths", a)
		}
	}
}

// TestDetectIgnoresPathHelpers: with the real LookPath, a user paru and yay
// first on PATH are never found; the file at the sysbin path is.
func TestDetectIgnoresPathHelpers(t *testing.T) {
	userBin := t.TempDir()
	t.Setenv("PATH", userBin)
	for _, n := range []string{"paru", "yay"} {
		if err := os.WriteFile(filepath.Join(userBin, n), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldParu, oldYay := sysbin.Paru, sysbin.Yay
	t.Cleanup(func() { sysbin.Paru, sysbin.Yay = oldParu, oldYay })
	sysbin.Paru = filepath.Join(t.TempDir(), "no-paru")
	sysbin.Yay = filepath.Join(t.TempDir(), "no-yay")
	if h := Detect(noConfig(nil)); !h.None() {
		t.Errorf("a helper on PATH was chosen: %+v", h)
	}
	path := testenv.UseSysbin(t, &sysbin.Yay, "exit 0")
	if h := Detect(noConfig(nil)); h.Name != Yay || h.Path != path {
		t.Errorf("Detect = %+v, want yay at %s", h, path)
	}
}
