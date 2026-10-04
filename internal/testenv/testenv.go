// Package testenv keeps a package's tests away from the developer's real home.
//
// Real run directories under ~/.local/state/archmaid are pruned to the newest
// few, so a test that writes there pushes real history out. A package whose
// tests could reach the home directory calls Run from its TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }
//
// Run points HOME and the XDG_* directories at a fresh temp dir for the whole
// package run, then checks the real state directory (resolved from the
// ORIGINAL environment) was not touched, and fails the run if it was.
package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const unset = "\x00unset"

// Run isolates the environment, runs the package's tests and returns the exit
// code for os.Exit: m.Run's, or 1 if a test touched the real state directory.
func Run(m interface{ Run() int }) int {
	realState := realStateDir()
	before := snapshot(realState)
	start := time.Now()

	home, err := os.MkdirTemp("", "archmaid-test-home")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		return 1
	}
	defer os.RemoveAll(home)
	run := filepath.Join(home, "run")
	if err := os.Mkdir(run, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		return 1
	}

	saved := map[string]string{}
	for k, v := range map[string]string{
		"HOME":            home,
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"XDG_RUNTIME_DIR": run,
	} {
		if old, ok := os.LookupEnv(k); ok {
			saved[k] = old
		} else {
			saved[k] = unset
		}
		os.Setenv(k, v)
	}

	code := m.Run()

	for k, v := range saved {
		if v == unset {
			os.Unsetenv(k)
		} else {
			os.Setenv(k, v)
		}
	}
	if bad := touched(realState, before, start); len(bad) > 0 {
		fmt.Fprintf(os.Stderr, "\ntestenv: FAIL: tests wrote into the real state dir %s:\n", realState)
		for _, b := range bad {
			fmt.Fprintln(os.Stderr, "  ", b)
		}
		return 1
	}
	return code
}

// realStateDir is where archmaid keeps its logs for the user who started the
// test run, from the environment as it was before Run overrides anything.
func realStateDir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "archmaid")
}

// snapshot maps each entry of dir (and the dir itself, as ".") to its
// modification time. A missing dir gives an empty map.
func snapshot(dir string) map[string]time.Time {
	out := map[string]time.Time{}
	if dir == "" {
		return out
	}
	if fi, err := os.Stat(dir); err == nil {
		out["."] = fi.ModTime()
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if fi, err := e.Info(); err == nil {
			out[e.Name()] = fi.ModTime()
		}
	}
	return out
}

// touched lists entries of dir that are new, changed, or newer than start.
func touched(dir string, before map[string]time.Time, start time.Time) []string {
	var bad []string
	for name, mt := range snapshot(dir) {
		old, existed := before[name]
		if !existed || !mt.Equal(old) || mt.After(start) {
			bad = append(bad, name)
		}
	}
	return bad
}
