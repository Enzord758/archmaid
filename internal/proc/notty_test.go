package proc

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
)

// ttyProbe says which of the child's standard streams are terminals.
const ttyProbe = `for fd in 0 1 2; do if [ -t $fd ]; then echo "fd$fd=tty"; else echo "fd$fd=pipe"; fi; done`

// TestChildrenNeverRunOnATerminal (task #112): pacman's `Color` setting in
// pacman.conf, and the tools that colour "when stdout is a terminal", only
// act on a tty. Every child archmaid starts, a step's (Start) and a parsed
// query's (Query: pacman -Qdtq, -Rs --print, -Qkq, -Qqm), has pipes or
// /dev/null for its streams, even when archmaid itself runs on a terminal
// (a TUI), so a parsed command can't get colour from the owner's
// pacman.conf. There is no pty anywhere in the tree to change that.
func TestChildrenNeverRunOnATerminal(t *testing.T) {
	// the test process itself may have a tty on its own streams
	t.Run("Query", func(t *testing.T) {
		out, err := Query(context.Background(), "sh", "-c", ttyProbe)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := strings.Fields(string(out)), []string{"fd0=pipe", "fd1=pipe", "fd2=pipe"}; !slices.Equal(got, want) {
			t.Errorf("Query child: %q, want %q", got, want)
		}
	})
	t.Run("Start", func(t *testing.T) {
		ch := make(chan Msg, 16)
		New(Options{}).Start(Spec{Name: "sh", Args: []string{"-c", ttyProbe}}, ch, nil)
		lines, err := drain(t, ch)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"fd0=pipe", "fd1=pipe", "fd2=pipe"}; !slices.Equal(lines, want) {
			t.Errorf("Start child: %q, want %q", lines, want)
		}
	})
	t.Run("the tree has no pty", func(t *testing.T) {
		// A source scan: no package of the module opens a pseudo-terminal
		// or wraps a child in script(1)/unbuffer. If one ever does, this
		// test is the place to decide that its parsed commands get
		// --color never.
		for _, dir := range []string{"../../internal", "../../cmd"} {
			scanNoPty(t, dir)
		}
	})
}

func scanNoPty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		p := dir + "/" + e.Name()
		if e.IsDir() {
			scanNoPty(t, p)
			continue
		}
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"creack/pty", "/dev/ptmx", "posix_openpt", "openpty", `"unbuffer"`, `"script", "-`} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s mentions %q: a child may now run on a pty, where pacman.conf's Color and CLICOLOR apply", p, bad)
			}
		}
	}
}
