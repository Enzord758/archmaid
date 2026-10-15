package realdriver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/sysbin"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// envTopgrade is a topgrade shim that reports the PATH, the askpass and
// the sudo it gets.
const envTopgrade = `#!/bin/sh
echo "PATH=$PATH"
echo "ASKPASS=$SUDO_ASKPASS"
echo "SUDO=$(command -v sudo)"
`

// TestGUIGetsNoSudoWrapper (task #153): the TUI's sudo wrapper is the
// TUI's only. The GUI's askpass Server writes none, even with sysbin.Sudo
// an absolute path (as in the package build), and its topgrade gets the
// askpass but archmaid-gui's own PATH, unchanged: its children have no
// terminal, so sudo asks through SUDO_ASKPASS by itself.
func TestGUIGetsNoSudoWrapper(t *testing.T) {
	testenv.UseSysbin(t, &sysbin.Sudo, "exit 1")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "topgrade"), []byte(envTopgrade), 0o700); err != nil {
		t.Fatal(err)
	}
	path := dir + string(os.PathListSeparator) + os.Getenv("PATH")
	t.Setenv("PATH", path)
	cfg := fakes(t, Config{})
	w := Start(cfg)
	defer w.Close()
	if !w.Askpass() {
		t.Fatal("no askpass Server")
	}
	if w.srv.SudoDir() != "" {
		t.Fatalf("the GUI's askpass Server wrote a sudo wrapper in %s", w.srv.SudoDir())
	}
	d := w.New(w.Options())
	defer d.Close()
	d.Send(engine.Start{Action: actions.IDTopgrade})
	var lines []string
	timeout := time.After(20 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-d.Events():
			if !ok {
				t.Fatal("Events closed early")
			}
			switch ev := ev.(type) {
			case engine.OutputChunk:
				lines = append(lines, ev.Lines...)
			case engine.NeedsConfirm:
				d.Send(engine.Confirm{Prompt: ev.ID, Yes: true})
			case engine.Finished:
				break loop
			}
		case <-timeout:
			t.Fatal("no Finished")
		}
	}
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "PATH="+path+"\n") {
		t.Errorf("topgrade's PATH isn't archmaid-gui's own (%s):\n%s", path, got)
	}
	if !strings.Contains(got, "ASKPASS="+w.srv.Wrapper()+"\n") {
		t.Errorf("topgrade didn't get the askpass %s:\n%s", w.srv.Wrapper(), got)
	}
	if strings.Contains(got, "SUDO="+cfg.RuntimeDir) {
		t.Errorf("topgrade's sudo is in the askpass Server's dir:\n%s", got)
	}
	entries, err := os.ReadDir(filepath.Dir(w.srv.Wrapper()))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("the GUI's askpass dir holds a directory %s", e.Name())
		}
	}
}
