package privilege

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/proc"
)

// TestPeerVerifyAURHelpers: paru is vetted exactly as yay is. The same tree
// with paru as sudo's caller passes every Verify check, and the chain it
// reports is the one the kept password answers (topgrade and the helper's
// package path); a paru that is not /usr/bin/paru is verified like any other
// caller (it is in the step's tree) but never gets the kept password; a
// process outside the step's tree is refused whatever it is called.
func TestPeerVerifyAURHelpers(t *testing.T) {
	treeWith := func(comm, exe string) map[int]proc.Info {
		tree := fakeTree()
		c := tree[fakeCallerPid]
		c.Comm, c.Exe = comm, exe
		tree[fakeCallerPid] = c
		return tree
	}
	for _, c := range []struct {
		name, comm, exe string
		kept            bool
	}{
		{"yay", "yay", "/usr/bin/yay", true},
		{"paru", "paru", "/usr/bin/paru", true},
		{"a paru on PATH elsewhere", "paru", "/home/me/.local/bin/paru", false},
		{"paru-bin's other name", "paru", "/usr/bin/paru-bin", false},
		{"a shell called paru", "paru", "/usr/bin/bash", false},
		{"pikaur", "pikaur", "/usr/bin/pikaur", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			from, err := verifyPeer(helperPeer(), fakeStepPid, 100, fakeOptions(treeWith(c.comm, c.exe)))
			if err != nil {
				t.Fatalf("refused the step's own sudo: %v", err)
			}
			if from.CallerExe != c.exe || from.KeptMayAnswer() != c.kept {
				t.Errorf("caller %q, may answer %v; want %q, %v (chain %q)", from.CallerExe, from.KeptMayAnswer(), c.exe, c.kept, from.Chain)
			}
		})
	}
	// paru outside the step's tree gets nothing, as yay would not.
	tree := treeWith("paru", "/usr/bin/paru")
	c := tree[fakeCallerPid]
	c.PPid = 1
	tree[fakeCallerPid] = c
	if _, err := verifyPeer(helperPeer(), fakeStepPid, 100, fakeOptions(tree)); err == nil {
		t.Error("a paru outside the step's process tree was verified")
	}
}
