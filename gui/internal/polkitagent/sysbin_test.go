package polkitagent

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/sysbin"
)

// TestStartRunsSysbinPkttyagent: with no Options.Path (the product's case),
// Start runs sysbin.Pkttyagent (in the product /usr/bin/pkttyagent), never
// a pkttyagent found on PATH, which would read the password polkit asks
// for.
func TestStartRunsSysbinPkttyagent(t *testing.T) {
	path, dir := shim(t)
	old := sysbin.Pkttyagent
	sysbin.Pkttyagent = path // an absolute path, not on PATH
	t.Cleanup(func() { sysbin.Pkttyagent = old })

	decoyDir := t.TempDir()
	decoyLog := filepath.Join(decoyDir, "decoy.log")
	if err := os.WriteFile(filepath.Join(decoyDir, "pkttyagent"),
		[]byte("#!/bin/sh\nprintf '%s\\n' \"pkttyagent $*\" >> '"+decoyLog+"'\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", decoyDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	a := start(t, "")
	self, _ := proc.Read(os.Getpid())
	if args := read(t, dir, "agent.args"); args != "--process "+strconv.Itoa(os.Getpid())+","+strconv.FormatUint(self.Start, 10)+" --notify-fd 3" {
		t.Fatalf("the sysbin pkttyagent ran with %q", args)
	}
	a.Stop()
	if b, _ := os.ReadFile(decoyLog); len(b) > 0 {
		t.Errorf("ran from PATH instead of internal/sysbin:\n%s", b)
	}
}
