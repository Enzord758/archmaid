package helper

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// installedFake answers `pacman -Qq` with names, one per line.
func installedFake(names ...string) *fakeRunner {
	return newFake().on(installedArgv(), reply{out: strings.Join(names, "\n") + "\n"})
}

// manyNames returns n distinct valid package names.
func manyNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("pkg%d", i)
	}
	return out
}

// TestCheckFilesNamesRefused: check-files with package names refuses, with
// exit 2 and before any pacman call, every list that isn't 1–256 distinct
// valid names. The error names the reason, so these fail on the old
// check-files (which refused any argument with "takes no arguments").
func TestCheckFilesNamesRefused(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string // in the result's error
	}{
		{"short flag", []string{"-x"}, `invalid package name "-x"`},
		{"long flag", []string{"--root=/"}, `invalid package name "--root=/"`},
		{"dbpath flag after a good name", []string{"a", "--dbpath=/tmp/evil"}, `invalid package name "--dbpath=/tmp/evil"`},
		{"bare double dash", []string{"--"}, `invalid package name "--"`},
		{"path traversal", []string{"../../etc/shadow"}, `invalid package name "../../etc/shadow"`},
		{"dot dot", []string{".."}, `invalid package name ".."`},
		{"slash", []string{"a/b"}, `invalid package name "a/b"`},
		{"absolute path", []string{"/usr/bin/pacman"}, `invalid package name "/usr/bin/pacman"`},
		{"unicode letter", []string{"pаcman"}, "invalid package name"}, // Cyrillic а
		{"unicode bidi override", []string{"a\u202eb"}, "invalid package name"},
		{"zero-width space", []string{"a\u200bb"}, "invalid package name"},
		{"space", []string{"a b"}, `invalid package name "a b"`},
		{"newline", []string{"a\nb"}, "invalid package name"},
		{"NUL", []string{"a\x00b"}, "invalid package name"},
		{"glob", []string{"a*"}, `invalid package name "a*"`},
		{"shell", []string{"a;rm"}, `invalid package name "a;rm"`},
		{"empty", []string{""}, `invalid package name ""`},
		{"129 chars", []string{strings.Repeat("a", 129)}, "invalid package name"},
		{"duplicate", []string{"a", "b", "a"}, `package "a" is named twice`},
		{"257 names", manyNames(257), "at most 256 package names, got 257"},
		{"10k names", manyNames(10000), "at most 256 package names, got 10000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Every name "installed", so only validation can refuse.
			f := installedFake(c.args...)
			o, out := rootOpts(t, f)
			if code := Run(append([]string{"check-files"}, c.args...), o); code != ExitUsage {
				t.Fatalf("exit %d, want %d; output:\n%s", code, ExitUsage, out)
			}
			if msg, _ := result(t, out)["error"].(string); !strings.Contains(msg, c.want) {
				t.Errorf("error %q, want it to contain %q", msg, c.want)
			}
			if len(f.queries) != 0 || len(f.streams) != 0 {
				t.Errorf("ran %q / %q, want nothing before validation passes", join(f.queries), join(f.streams))
			}
		})
	}
}

// TestCheckFilesNotInstalledRefused: a valid name pacman -Qq doesn't list is
// refused (exit 2) without running -Qk; the check asks the helper's own
// fixed-path pacman.
func TestCheckFilesNotInstalledRefused(t *testing.T) {
	f := installedFake("a", "b")
	o, out := rootOpts(t, f)
	if code := Run([]string{"check-files", "a", "ghost", "b"}, o); code != ExitUsage {
		t.Fatalf("exit %d, want %d; output:\n%s", code, ExitUsage, out)
	}
	if msg, _ := result(t, out)["error"].(string); !strings.Contains(msg, `package "ghost" is not installed`) {
		t.Errorf("error %q, want it to name ghost as not installed", msg)
	}
	if got, want := join(f.queries), []string{"/usr/bin/pacman -Qq"}; !slices.Equal(got, want) {
		t.Errorf("queries %q, want %q", got, want)
	}
	if len(f.streams) != 0 {
		t.Errorf("ran %q, want no -Qk", join(f.streams))
	}
}

// TestCheckFilesInstalledQueryFails: a failing pacman -Qq is a failure
// (exit 1), not a pass.
func TestCheckFilesInstalledQueryFails(t *testing.T) {
	f := newFake().on(installedArgv(), reply{err: "error: db broken\n", exit: 1})
	o, out := rootOpts(t, f)
	if code := Run([]string{"check-files", "a"}, o); code != ExitFailed {
		t.Fatalf("exit %d, want %d; output:\n%s", code, ExitFailed, out)
	}
	if len(f.streams) != 0 {
		t.Errorf("ran %q, want nothing", join(f.streams))
	}
}

// TestCheckFilesNames: valid installed names run exactly `pacman -Qk --
// names…` (in the caller's order), relay its output, and report the
// packages it checked and the broken ones in a check event before the
// result. Exit 1 from pacman (missing files found) is a result.
func TestCheckFilesNames(t *testing.T) {
	names := []string{"amd-ucode", "big", "nfs-utils", "gone"}
	f := installedFake("amd-ucode", "big", "nfs-utils", "gone", "other").
		on(checkPackagesArgv(names), reply{
			out: "amd-ucode: 3 total files, 0 missing files\n" +
				"big: 40 total files, 2 missing files\n" +
				"nfs-utils: 120 total files, 0 missing files\n",
			err:  "warning: big: /usr/share/big/one (No such file or directory)\n",
			exit: 1,
		})
	o, out := rootOpts(t, f)
	if code := Run(append([]string{"check-files"}, names...), o); code != ExitOK {
		t.Fatalf("exit %d, want 0; output:\n%s", code, out)
	}
	if got, want := join(f.streams), []string{"/usr/bin/pacman -Qk -- amd-ucode big nfs-utils gone"}; !slices.Equal(got, want) {
		t.Fatalf("ran %q, want %q", got, want)
	}
	evs := events(t, out)
	checks := ofType(evs, EventCheck)
	if len(checks) != 1 {
		t.Fatalf("want one check event, got %d:\n%s", len(checks), out)
	}
	if evs[len(evs)-2]["t"] != EventCheck {
		t.Errorf("the check event must come right before the result:\n%s", out)
	}
	strs := func(v any) []string {
		var r []string
		for _, s := range v.([]any) {
			r = append(r, s.(string))
		}
		return r
	}
	if got, want := strs(checks[0]["checked"]), []string{"amd-ucode", "big", "nfs-utils"}; !slices.Equal(got, want) {
		t.Errorf("checked %q, want %q", got, want)
	}
	if got, want := strs(checks[0]["broken"]), []string{"big"}; !slices.Equal(got, want) {
		t.Errorf("broken %q, want %q", got, want)
	}
	if got := skippedNames(t, out); len(got) != 1 || got["gone"] != "could not check its files" {
		t.Errorf("skipped %v, want just gone (no summary line)", got)
	}
	if res := result(t, out); res["child_exit"] != float64(1) {
		t.Errorf("child_exit %v, want 1", res["child_exit"])
	}
	if len(ofType(evs, EventLine)) != 4 {
		t.Errorf("want pacman's 4 lines relayed:\n%s", out)
	}
}

// TestCheckFilesNamesNoneBroken: nothing missing is an empty broken list,
// never null, so a front end can tell it from an old helper's output.
func TestCheckFilesNamesNoneBroken(t *testing.T) {
	f := installedFake("a").on(checkPackagesArgv([]string{"a"}), reply{out: "a: 3 total files, 0 missing files\n"})
	o, out := rootOpts(t, f)
	if code := Run([]string{"check-files", "a"}, o); code != ExitOK {
		t.Fatalf("exit %d, want 0; output:\n%s", code, out)
	}
	if !strings.Contains(out.String(), `{"v":1,"t":"check","checked":["a"],"broken":[]}`) {
		t.Errorf("want an exact check line with broken [], got:\n%s", out)
	}
}

// TestCheckFilesNamesPacmanFails: any pacman exit but 0 and 1 is a failure,
// with no check event (its lists would be guesses).
func TestCheckFilesNamesPacmanFails(t *testing.T) {
	f := installedFake("a").on(checkPackagesArgv([]string{"a"}), reply{err: "error: failed to init\n", exit: 2})
	o, out := rootOpts(t, f)
	if code := Run([]string{"check-files", "a"}, o); code != ExitFailed {
		t.Fatalf("exit %d, want %d; output:\n%s", code, ExitFailed, out)
	}
	if n := len(ofType(events(t, out), EventCheck)); n != 0 {
		t.Errorf("a failed check must not report a check event, got %d", n)
	}
}

// TestCheckFilesNamesMax: exactly maxNames names is accepted.
func TestCheckFilesNamesMax(t *testing.T) {
	names := manyNames(maxNames)
	f := installedFake(names...)
	o, out := rootOpts(t, f)
	if code := Run(append([]string{"check-files"}, names...), o); code != ExitOK {
		t.Fatalf("exit %d, want 0; output:\n%s", code, out)
	}
	if len(f.streams) != 1 || len(f.streams[0]) != 3+maxNames || f.streams[0][2] != "--" {
		t.Fatalf("want one pacman -Qk -- with %d names, got %d argvs", maxNames, len(f.streams))
	}
}

// TestCheckFilesFullCheckEvent: the full check (no names) also ends in a
// check event, listing every package pacman printed a summary for, in its
// order, and the broken ones. It fails on a helper whose full check only
// relays pacman's text.
func TestCheckFilesFullCheckEvent(t *testing.T) {
	f := newFake().on(checkFilesArgv(), reply{
		out: "a: 3 total files, 0 missing files\n" +
			"b: 1 total file, 1 missing file\n" +
			"c: 40 total files, 2 missing files\n",
		err:  "warning: b: /usr/bin/b (No such file or directory)\n",
		exit: 1,
	})
	o, out := rootOpts(t, f)
	if code := Run([]string{"check-files"}, o); code != ExitOK {
		t.Fatalf("exit %d, want 0; output:\n%s", code, out)
	}
	if got, want := join(f.streams), []string{"/usr/bin/pacman -Qk"}; !slices.Equal(got, want) {
		t.Fatalf("ran %q, want %q", got, want)
	}
	if !strings.Contains(out.String(), `{"v":1,"t":"check","checked":["a","b","c"],"broken":["b","c"]}`) {
		t.Errorf("want the full check's check event, got:\n%s", out)
	}
	evs := events(t, out)
	if evs[len(evs)-2]["t"] != EventCheck {
		t.Errorf("the check event must come right before the result:\n%s", out)
	}
	if len(ofType(evs, EventLine)) != 4 {
		t.Errorf("want pacman's 4 lines relayed:\n%s", out)
	}
	if res := result(t, out); res["child_exit"] != float64(1) {
		t.Errorf("child_exit %v, want 1", res["child_exit"])
	}
}

// TestCheckFilesFullNoneBroken: a clean full check is broken [], never null.
func TestCheckFilesFullNoneBroken(t *testing.T) {
	f := newFake().on(checkFilesArgv(), reply{out: "a: 3 total files, 0 missing files\n"})
	o, out := rootOpts(t, f)
	if code := Run([]string{"check-files"}, o); code != ExitOK {
		t.Fatalf("exit %d, want 0; output:\n%s", code, out)
	}
	if !strings.Contains(out.String(), `{"v":1,"t":"check","checked":["a"],"broken":[]}`) {
		t.Errorf("want an exact check line with broken [], got:\n%s", out)
	}
}

// TestCheckFilesFullPacmanFails: a failing full check has no check event.
func TestCheckFilesFullPacmanFails(t *testing.T) {
	f := newFake().on(checkFilesArgv(), reply{err: "error: failed to init\n", exit: 2})
	o, out := rootOpts(t, f)
	if code := Run([]string{"check-files"}, o); code != ExitFailed {
		t.Fatalf("exit %d, want %d; output:\n%s", code, ExitFailed, out)
	}
	if n := len(ofType(events(t, out), EventCheck)); n != 0 {
		t.Errorf("a failed check must not report a check event, got %d", n)
	}
}
