package privilege

import (
	"os"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/proc"
)

// Who may have the password. Anything that inherited SUDO_ASKPASS can connect
// to the socket — under topgrade that includes every AUR build script — so a
// request is answered only when it comes from archmaid's own helper, run by
// sudo, inside the running step's process tree. Verify reads /proc through
// VerifyOptions.ReadProc, so these tests stand in a fake process table: wrong
// uid, wrong binary, a parent that isn't sudo, a chain that never reaches the
// step, reused pids.

// Fake pids sit above the kernel's PID_MAX_LIMIT (2^22), so none can name a
// real process.
const (
	fakeStepPid   = 5_000_100 // the running step: topgrade
	fakeCallerPid = 5_000_150 // yay, which runs sudo
	fakeSudoPid   = 5_000_200
	fakeHelperPid = 5_000_300 // archmaid --askpass, exec'd by the wrapper
)

// fakeUID is our uid in the fake process table. It is fixed, never the real
// uid: the suite must give the same answers as root (CI's container) and as a
// user, and "a user process" must never coincide with root's uid 0.
const fakeUID = 1000

// fakeExe is archmaid's binary in the fake process table.
const fakeExe = "/usr/bin/archmaid"

// fakeTree is the happy path, topgrade → yay → sudo → archmaid --askpass, as
// a user sees it: sudo's exe link is unreadable (setuid root), but it runs
// with euid 0 under the name "sudo".
func fakeTree() map[int]proc.Info {
	return map[int]proc.Info{
		fakeStepPid:   {PPid: 4_999_999, Start: 100, Comm: "topgrade", EUID: fakeUID, Exe: "/usr/bin/topgrade"},
		fakeCallerPid: {PPid: fakeStepPid, Start: 200, Comm: "yay", EUID: fakeUID, Exe: "/usr/bin/yay"},
		fakeSudoPid:   {PPid: fakeCallerPid, Start: 300, Comm: "sudo", EUID: 0},
		fakeHelperPid: {PPid: fakeSudoPid, Start: 400, Comm: "askpass.sh", EUID: fakeUID, Exe: fakeExe},
	}
}

// fakeOptions verifies against tree as fakeUID running fakeExe.
func fakeOptions(tree map[int]proc.Info) VerifyOptions {
	return VerifyOptions{
		UID: func() int { return fakeUID },
		Exe: fakeExe,
		ReadProc: func(pid int) (proc.Info, error) {
			if p, ok := tree[pid]; ok {
				return p, nil
			}
			return proc.Info{}, os.ErrNotExist
		},
	}
}

// helperPeer is the helper as the server records it at accept.
func helperPeer() Peer { return Peer{Pid: fakeHelperPid, UID: fakeUID, Start: 400} }

// TestPeerVerify: the happy path names sudo's caller; every way a request can
// fail to be ours is refused.
func TestPeerVerify(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		from, err := verifyPeer(helperPeer(), fakeStepPid, 100, fakeOptions(fakeTree()))
		if err != nil {
			t.Fatalf("refused the step's own sudo: %v", err)
		}
		if from.CallerExe != "/usr/bin/yay" || from.CallerPid != fakeCallerPid {
			t.Fatalf("caller = %q (pid %d), want /usr/bin/yay (pid %d)", from.CallerExe, from.CallerPid, fakeCallerPid)
		}
	})

	t.Run("sudo is the step itself", func(t *testing.T) {
		// A plain `sudo -A …` step: the step's child is sudo.
		if _, err := verifyPeer(helperPeer(), fakeSudoPid, 300, fakeOptions(fakeTree())); err != nil {
			t.Fatalf("refused a direct sudo step: %v", err)
		}
	})

	t.Run("readable sudo exe", func(t *testing.T) {
		// As root the exe link is readable; then it must be sudo's binary.
		tree := fakeTree()
		sudo := tree[fakeSudoPid]
		sudo.Exe = sudoPath
		tree[fakeSudoPid] = sudo
		if _, err := verifyPeer(helperPeer(), fakeStepPid, 100, fakeOptions(tree)); err != nil {
			t.Fatalf("refused a sudo whose exe reads %s: %v", sudoPath, err)
		}
	})

	t.Run("caller unreadable", func(t *testing.T) {
		// yay exits between the walk and the caller lookup: still ours,
		// with no binary to name.
		o := fakeOptions(fakeTree())
		read, reads := o.ReadProc, 0
		o.ReadProc = func(pid int) (proc.Info, error) {
			if pid == fakeCallerPid {
				if reads++; reads > 1 {
					return proc.Info{}, os.ErrNotExist
				}
			}
			return read(pid)
		}
		from, err := verifyPeer(helperPeer(), fakeStepPid, 100, o)
		if err != nil || from.CallerExe != "" || from.CallerPid != fakeCallerPid {
			t.Fatalf("Verify = %+v, %v; want the caller's pid with no exe", from, err)
		}
	})

	refusals := []struct {
		name      string
		edit      func(tree map[int]proc.Info, peer *Peer)
		stepPid   int
		stepStart uint64
		want      string // in the error
	}{
		{name: "wrong uid", want: "not ours",
			edit: func(_ map[int]proc.Info, peer *Peer) { peer.UID = fakeUID + 1 }},
		{name: "helper gone", want: "gone",
			edit: func(tree map[int]proc.Info, _ *Peer) { delete(tree, fakeHelperPid) }},
		{name: "helper pid reused since accept", want: "reused",
			edit: func(_ map[int]proc.Info, peer *Peer) { peer.Start = 399 }},
		{name: "not archmaid", want: "/usr/bin/python3",
			edit: setProc(fakeHelperPid, func(p *proc.Info) { p.Exe = "/usr/bin/python3" })},
		{name: "helper exe unreadable", want: "unknown program",
			edit: setProc(fakeHelperPid, func(p *proc.Info) { p.Exe = "" })},
		{name: "user process named sudo", want: "not started by sudo",
			edit: setProc(fakeSudoPid, func(p *proc.Info) { p.EUID = fakeUID })},
		{name: "root process not named sudo", want: "not started by sudo",
			edit: setProc(fakeSudoPid, func(p *proc.Info) { p.Comm = "pkexec" })},
		{name: "readable exe that isn't sudo", want: "not started by sudo",
			edit: setProc(fakeSudoPid, func(p *proc.Info) { p.Exe = "/home/u/bin/sudo" })},
		{name: "parent gone", want: "not started by sudo",
			edit: func(tree map[int]proc.Info, _ *Peer) { delete(tree, fakeSudoPid) }},
		{name: "chain misses the step", stepPid: 4242, want: "not started by the running step"},
		{name: "chain broken", want: "not started by the running step",
			edit: func(tree map[int]proc.Info, _ *Peer) { delete(tree, fakeCallerPid) }},
		{name: "step pid reused", stepStart: 99, want: "step's pid"},
		{name: "ancestor pid reused", want: "reused",
			// a parent can't have started after its child
			edit: setProc(fakeCallerPid, func(p *proc.Info) { p.Start = 350 })},
		{name: "cycle", want: "not started by the running step",
			edit: setProc(fakeCallerPid, func(p *proc.Info) { p.PPid = fakeSudoPid; p.Start = 300 })},
	}
	for _, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			tree, peer := fakeTree(), helperPeer()
			if c.edit != nil {
				c.edit(tree, &peer)
			}
			stepPid, stepStart := fakeStepPid, uint64(100)
			if c.stepPid != 0 {
				stepPid = c.stepPid
			}
			if c.stepStart != 0 {
				stepStart = c.stepStart
			}
			_, err := verifyPeer(peer, stepPid, stepStart, fakeOptions(tree))
			if err == nil {
				t.Fatal("request accepted, want it refused")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %q, want it to mention %q", err, c.want)
			}
		})
	}
}

// TestPeerVerifyChainBounded: a parent chain longer than maxChain is refused
// rather than walked forever.
func TestPeerVerifyChainBounded(t *testing.T) {
	tree := fakeTree()
	// Put maxChain+1 ancestors between yay and the step.
	prev := fakeStepPid
	for i := 0; i <= maxChain; i++ {
		pid := 6_000_000 + i
		tree[pid] = proc.Info{PPid: prev, Start: 150, Comm: "sh", EUID: fakeUID}
		prev = pid
	}
	caller := tree[fakeCallerPid]
	caller.PPid = prev
	tree[fakeCallerPid] = caller
	if _, err := verifyPeer(helperPeer(), fakeStepPid, 100, fakeOptions(tree)); err == nil ||
		!strings.Contains(err.Error(), "not started by the running step") {
		t.Fatalf("Verify = %v, want the over-long chain refused", err)
	}
}

// TestPeerVerifyDefaults: with zero VerifyOptions Verify uses the real uid, our
// own binary and the real /proc — so our own process, which isn't run by
// sudo, is refused for that, not for its uid or binary.
func TestPeerVerifyDefaults(t *testing.T) {
	self, err := proc.Read(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	peer := Peer{Pid: os.Getpid(), UID: os.Getuid(), Start: self.Start}
	_, err = verifyPeer(peer, fakeStepPid, 100, VerifyOptions{})
	if err == nil || !strings.Contains(err.Error(), "not started by sudo") {
		t.Fatalf("Verify(self) = %v, want it refused as not started by sudo", err)
	}
}

// setProc returns an edit that changes pid's entry in the fake tree.
func setProc(pid int, change func(*proc.Info)) func(map[int]proc.Info, *Peer) {
	return func(tree map[int]proc.Info, _ *Peer) {
		p := tree[pid]
		change(&p)
		tree[pid] = p
	}
}
