package actions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/steps"
)

// TestParseBrokenPackages: `pacman -Qkq` prints "pkg /path" per missing file;
// a package with several missing files is listed once, and stray lines (a
// warning, a bare word) are ignored.
func TestParseBrokenPackages(t *testing.T) {
	out := "warning: something odd\n" +
		"amd-ucode /boot/amd-ucode.img\n" +
		"big /usr/share/big/one\n" +
		"big /usr/share/big/two\n" +
		"stray\n"
	got := parseBrokenPackages(out)
	want := []string{"amd-ucode", "big"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := parseBrokenPackages(""); len(got) != 0 {
		t.Fatalf("empty output should be no packages, got %v", got)
	}
}

// TestParseBrokenPackagesFixture parses a capture of real -Qkq output shape.
func TestParseBrokenPackagesFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/pacman_Qkq.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := parseBrokenPackages(string(data))
	want := []string{"amd-ucode", "nfs-utils", "big"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// TestChunkStrings: at most n contiguous chunks that together are s in order,
// sizes differing by at most one; never an empty chunk.
func TestChunkStrings(t *testing.T) {
	s := strings.Fields("a b c d e f g h i j")
	for _, n := range []int{-1, 0, 1, 3, 4, 10, 16} {
		chunks := chunkStrings(s, n)
		if want := max(1, min(n, len(s))); len(chunks) != want {
			t.Fatalf("n=%d: %d chunks, want %d", n, len(chunks), want)
		}
		lo, hi := len(s), 0
		for _, c := range chunks {
			lo, hi = min(lo, len(c)), max(hi, len(c))
		}
		if lo == 0 || hi-lo > 1 {
			t.Fatalf("n=%d: uneven chunks %v", n, chunks)
		}
		if got := slices.Concat(chunks...); !reflect.DeepEqual(got, s) {
			t.Fatalf("n=%d: chunks %v don't add up to %v", n, chunks, s)
		}
	}
	if got := chunkStrings(nil, 4); len(got) != 0 {
		t.Fatalf("nothing installed should be no chunks, got %v", got)
	}
}

// TestRescanRepairPolkit: under polkit the root recheck runs no sudo (PATH
// is empty, so any command would fail into a notice) and is one helper step:
// verb check-files, NeedsPriv, over exactly the candidates, as Items and as
// the names after "--", in order.
func TestRescanRepairPolkit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	cands := []string{"amd-ucode", "nfs-utils", "big"}
	d := rescanRepair(cands, 1422)(context.Background(), Env{Polkit: true})
	p, ok := d.(Pending)
	if !ok || len(p.Steps) != 1 {
		t.Fatalf("got %#v, want one pending step", d)
	}
	s := p.Steps[0]
	if s.ID != steps.IDRepairCheck || s.Verb != steps.VerbCheckFiles || !s.NeedsPriv || s.Run != nil || s.Confirm != "" {
		t.Fatalf("step %+v: want a NeedsPriv check-files child with no confirm", s)
	}
	if !slices.Equal(s.Items, cands) {
		t.Errorf("Items %q, want exactly %q", s.Items, cands)
	}
	i := slices.Index(s.Argv, "--")
	if i < 0 || !slices.Equal(s.Argv[i+1:], cands) || slices.Contains(s.Argv, "sudo") {
		t.Errorf("Argv %q: want no sudo and exactly %q after --", s.Argv, cands)
	}
	cands[0] = "changed"
	if s.Items[0] != "amd-ucode" || s.Argv[i+1] != "amd-ucode" {
		t.Errorf("the step must not share the caller's slice: %q / %q", s.Items, s.Argv)
	}
}

// TestRescanRepairPolkitTooMany: more candidates than the helper takes is a
// notice up front, not a step the helper is sure to refuse.
func TestRescanRepairPolkitTooMany(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	cands := make([]string, HelperMaxNames+1)
	for i := range cands {
		cands[i] = fmt.Sprintf("p%d", i)
	}
	d := rescanRepair(cands, 1422)(context.Background(), Env{Polkit: true})
	if n, ok := d.(Notice); !ok || !strings.Contains(n.Text, "Too many flagged packages (257)") {
		t.Fatalf("got %#v, want the too-many notice", d)
	}
	if d := rescanRepair(cands[:HelperMaxNames], 1422)(context.Background(), Env{Polkit: true}); d.(Pending).Steps[0].Verb != steps.VerbCheckFiles {
		t.Fatalf("exactly %d candidates must still be one check step, got %#v", HelperMaxNames, d)
	}
}

// TestRescanRepairSudoUnchanged: without Polkit the recheck still runs
// `sudo -n pacman -Qkq -- candidates` (a shim here) and queues the reinstall
// of exactly what it reports.
func TestRescanRepairSudoUnchanged(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + argsFile + "\necho 'big /usr/share/big/one'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	d := rescanRepair([]string{"amd-ucode", "big"}, 1422)(context.Background(), Env{})
	p, ok := d.(Pending)
	if !ok || len(p.Steps) != 1 {
		t.Fatalf("got %#v, want one pending step", d)
	}
	want := []string{"sudo", "pacman", "-S", "--noconfirm", "--", "big"}
	if s := p.Steps[0]; s.ID != steps.IDRepair || s.Verb != steps.VerbRepair || !slices.Equal(s.Argv, want) ||
		s.Confirm != "Reinstall 1 broken package(s)?" {
		t.Fatalf("step %+v, want the reinstall of big", s)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil || string(got) != "-n pacman -Qkq --color never -- amd-ucode big\n" {
		t.Fatalf("sudo ran with %q (%v), want the -n recheck of both candidates", got, err)
	}
}

// TestQkqOutput: pacman's exit 1 with a report on stdout is a result; an
// error with an empty stdout is reported with stderr, or the exit status.
func TestQkqOutput(t *testing.T) {
	out, err := qkqOutput(context.Background(), "sh", "-c", "echo 'bad /usr/bad'; exit 1")
	if err != nil || out != "bad /usr/bad\n" {
		t.Fatalf("exit 1 with a report: got %q, %v", out, err)
	}
	if _, err := qkqOutput(context.Background(), "sh", "-c", "echo nope >&2; exit 1"); err == nil || err.Error() != "nope" {
		t.Fatalf("want stderr as the error, got %v", err)
	}
	if _, err := qkqOutput(context.Background(), "sh", "-c", "exit 3"); err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("want the exit status, got %v", err)
	}
}

// TestRepairStepItems: the reinstall step carries its packages as Items, the
// typed field the root helper's argv is built from, as its own copy.
func TestRepairStepItems(t *testing.T) {
	pkgs := []string{"a", "b"}
	s := RepairStep(pkgs)
	if !slices.Equal(s.Items, pkgs) || s.Verb != steps.VerbRepair {
		t.Fatalf("Items %q verb %q, want %q and repair", s.Items, s.Verb, pkgs)
	}
	pkgs[0] = "x"
	if s.Items[0] != "a" {
		t.Errorf("the step must not share the caller's slice: %q", s.Items)
	}
}
