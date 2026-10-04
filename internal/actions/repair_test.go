package actions

import (
	"context"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
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
