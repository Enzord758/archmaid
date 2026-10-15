package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/sysbin"
	"github.com/Enzord758/archmaid/internal/testenv"
)

// The TUI's sudo wrapper (privilege's sudowrap.go) on a step's PATH under
// the PATH guard. The guard only runs under KeepPassword, which only the
// GUI sets, and the GUI never puts the wrapper on a PATH; should both ever
// meet, the wrapper must read as archmaid's own way to the real sudo, not
// as a stand-in for it.

// wrapPriv is fakePriv with privilege.Sudo's environment: the askpass and
// the sudo wrapper's dir first on a NeedsPriv step's PATH.
type wrapPriv struct {
	*fakePriv
	sudo privilege.Sudo
}

func (w wrapPriv) Command(argv []string, needsPriv bool) proc.Spec {
	return proc.Spec{Name: argv[0], Args: argv[1:], Env: w.sudo.Env(needsPriv)}
}

// wrapperRig starts the guard rig with a Server's sudo wrapper first on
// the step's PATH, before a dir with a sudo of its own (out of reach
// behind the wrapper) and the real sudo's dir; touch runs on the
// wrapper's file before the step starts.
func wrapperRig(t *testing.T, touch func(path string)) (*guardRig, string) {
	t.Helper()
	testenv.UseSysbin(t, &sysbin.Su, `read pw; [ "$pw" = good ]`)
	real := realSudoDir(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := privilege.Listen(t.TempDir(), exe, privilege.ServerOptions{SudoWrapper: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	own := filepath.Join(srv.SudoDir(), "sudo")
	if srv.SudoDir() == "" {
		t.Fatal("no sudo wrapper")
	}
	if touch != nil {
		touch(own)
	}
	behind := dirWith(t, "sudo", 0o755)
	t.Setenv("PATH", strings.Join([]string{behind, real, os.Getenv("PATH")}, string(os.PathListSeparator)))
	priv := wrapPriv{&fakePriv{cached: true, password: "good"}, privilege.Sudo{Wrapper: srv.Wrapper(), SudoDir: srv.SudoDir()}}
	return startGuardRig(t, Deps{Priv: priv}), own
}

// The wrapper first on the step's PATH, as the TUI writes it: no shadow,
// the kept password stays on.
func TestPathGuardOwnSudoWrapper(t *testing.T) {
	g, _ := wrapperRig(t, nil)
	requireOn(t, g)
}

// A file put in the wrapper's place (the same text, a new file) is not
// archmaid's: off, and named.
func TestPathGuardReplacedSudoWrapper(t *testing.T) {
	g, own := wrapperRig(t, func(path string) {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o700); err != nil {
			t.Fatal(err)
		}
	})
	requireOff(t, g, own)
}
