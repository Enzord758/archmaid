package polkitagent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"testing"
)

// The terminal check (procView.holders) on a fake /proc: a tree of
// <pid>/fd/<n> links (named as readlink would show them), the open file
// behind each one (stat) and each process's session.

// The agent's terminal: pkttyagent's pty, /dev/pts/3 on the host's devpts
// (0:25); its pid leads its session.
const agentPid = 1000

var (
	ourPty    = fileID{dev: [2]uint32{0, 25}, rdev: [2]uint32{136, 3}}
	ourPty4   = fileID{dev: [2]uint32{0, 25}, rdev: [2]uint32{136, 4}}
	otherPty3 = fileID{dev: [2]uint32{0, 61}, rdev: [2]uint32{136, 3}} // a sandbox's own devpts
	devTTY    = fileID{dev: [2]uint32{0, 6}, rdev: ctty}
	otherTTY  = fileID{dev: [2]uint32{0, 88}, rdev: ctty} // a container's /dev/tty node
	plainFile = fileID{dev: [2]uint32{259, 2}}
)

type fakeFD struct {
	name string // the link, as readlink shows it
	id   fileID
	err  error // stat's
}

type fakeProcess struct {
	fds      []fakeFD
	session  int
	sessErr  error
	noFDRead bool // the fd table isn't ours to read (another uid's, root's)
}

// gonePath is the error /proc gives for a process (or fd) that has gone.
func gonePath(path string, errno syscall.Errno) error {
	return &fs.PathError{Op: "open", Path: path, Err: errno}
}

func fakeView(t *testing.T, procs map[int]fakeProcess) procView {
	t.Helper()
	root := t.TempDir()
	stats := map[string]fakeFD{}
	for pid, p := range procs {
		dir := filepath.Join(root, strconv.Itoa(pid), "fd")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for i, fd := range p.fds {
			link := dir + "/" + strconv.Itoa(i)
			if err := os.Symlink(fd.name, link); err != nil {
				t.Fatal(err)
			}
			stats[link] = fd
		}
		if p.noFDRead {
			if err := os.Chmod(dir, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		}
	}
	// Not a process: skipped.
	if err := os.MkdirAll(filepath.Join(root, "self", "fd"), 0o700); err != nil {
		t.Fatal(err)
	}
	return procView{
		root: root,
		stat: func(link string) (fileID, error) {
			fd, ok := stats[link]
			if !ok {
				t.Errorf("stat of an unknown link %s", link)
				return fileID{}, errors.New("unknown link")
			}
			return fd.id, fd.err
		},
		session: func(pid int) (int, error) {
			p, ok := procs[pid]
			if !ok {
				t.Errorf("session of an unknown pid %d", pid)
				return 0, errors.New("unknown pid")
			}
			return p.session, p.sessErr
		},
	}
}

type holderCase struct {
	name   string
	proc   fakeProcess
	holder bool
}

// check runs each case as the one process besides the agent, so a wrong
// verdict names its case.
func check(t *testing.T, cases []holderCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const pid = 2000
			v := fakeView(t, map[int]fakeProcess{
				agentPid: {fds: []fakeFD{{"/dev/pts/3", ourPty, nil}, {"/dev/tty", devTTY, nil}}, session: agentPid},
				pid:      tc.proc,
			})
			got := v.holders(agentPid, ourPty)
			want := []int(nil)
			if tc.holder {
				want = []int{pid}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("holders = %v, want %v", got, want)
			}
		})
	}
}

// Edge (a): a process that held /dev/tty when its fd was listed, and was
// reaped before its session could be read (or whose fd went first), holds
// nothing: it isn't counted, so it can't refuse a legitimate prompt. An
// error that doesn't say "gone" still counts as a holder (fail closed).
func TestHoldersGoneProcessHoldsNothing(t *testing.T) {
	tty := func(sessErr error) fakeProcess {
		return fakeProcess{fds: []fakeFD{{"/dev/tty", devTTY, nil}}, session: agentPid, sessErr: sessErr}
	}
	check(t, []holderCase{
		{"reaped before its stat was read (ENOENT)", tty(gonePath("/proc/2000/stat", syscall.ENOENT)), false},
		{"exiting while its stat was read (ESRCH)", tty(gonePath("/proc/2000/stat", syscall.ESRCH)), false},
		{"its fd closed before the stat (ENOENT)", fakeProcess{fds: []fakeFD{{"/dev/pts/3", fileID{}, gonePath("/proc/2000/fd/0", syscall.ENOENT)}}}, false},
		{"reaped before the stat of its fd (ESRCH)", fakeProcess{fds: []fakeFD{{"/dev/pts/3", fileID{}, gonePath("/proc/2000/fd/0", syscall.ESRCH)}}}, false},
		// Fail closed: anything but "gone".
		{"stat unreadable (EACCES)", tty(gonePath("/proc/2000/stat", syscall.EACCES)), true},
		{"stat malformed", tty(errors.New("pid 2000: malformed stat")), true},
		{"fd unstatable (EIO)", fakeProcess{fds: []fakeFD{{"/dev/pts/3", fileID{}, gonePath("/proc/2000/fd/0", syscall.EIO)}}}, true},
		{"fd unstatable (ENOTCONN)", fakeProcess{fds: []fakeFD{{"/mnt/sshfs/x", fileID{}, gonePath("/proc/2000/fd/0", syscall.ENOTCONN)}}}, true},
	})
}

// A process of our uid whose fds are listed but whose links the kernel
// won't let us follow (ptrace's read check: the user's systemd has
// CAP_WAKE_ALARM, which we lack) is not ours to read, like an fd table
// we may not list: not a holder, as before. Failing closed there refused
// every prompt on a machine running systemd --user.
func TestHoldersUnreadableLinks(t *testing.T) {
	check(t, []holderCase{
		{"EACCES", fakeProcess{fds: []fakeFD{{"/dev/pts/3", fileID{}, gonePath("/proc/2000/fd/0", syscall.EACCES)}}, session: agentPid}, false},
		{"EPERM", fakeProcess{fds: []fakeFD{{"/dev/tty", fileID{}, gonePath("/proc/2000/fd/0", syscall.EPERM)}}, session: agentPid}, false},
	})
}

// Edge (b): the pty is told apart by its device numbers, not the name its
// fd link shows. Another devpts instance's /dev/pts/3 (a flatpak sandbox's,
// say) isn't ours; our pty under another name (a bind mount in another
// mount namespace) is; any /dev/tty node (5:0) is the controlling terminal
// of whoever opened it, so it counts only in the agent's session.
func TestHoldersByDevice(t *testing.T) {
	check(t, []holderCase{
		{"another instance's /dev/pts/3", fakeProcess{fds: []fakeFD{{"/dev/pts/3", otherPty3, nil}}, session: 77}, false},
		{"our pty under another name", fakeProcess{fds: []fakeFD{{"/run/host/console", ourPty, nil}}, session: 77}, true},
		{"our pty, its name deleted", fakeProcess{fds: []fakeFD{{"/dev/pts/3 (deleted)", ourPty, nil}}, session: 77}, true},
		{"another pty of our instance", fakeProcess{fds: []fakeFD{{"/dev/pts/4", ourPty4, nil}}, session: 77}, false},
		{"a 5:0 node by another name, in the agent's session", fakeProcess{fds: []fakeFD{{"/newroot/dev/tty", otherTTY, nil}}, session: agentPid}, true},
		{"/dev/tty in another session", fakeProcess{fds: []fakeFD{{"/dev/tty", devTTY, nil}}, session: 77}, false},
		{"a file named like our pty", fakeProcess{fds: []fakeFD{{"/dev/pts/3", plainFile, nil}}, session: agentPid}, false},
	})
}

// No regression of what the check is for: a process that holds our pty
// (by its own name, as /dev/tty from inside the agent's session, or past
// other fds) is a holder, the agent itself never is, and a /proc that
// can't be listed refuses outright.
func TestHoldersFindOurPty(t *testing.T) {
	sock := fakeFD{"socket:[4711]", fileID{dev: [2]uint32{0, 8}}, nil}
	check(t, []holderCase{
		{"our pty by its name", fakeProcess{fds: []fakeFD{{"/dev/pts/3", ourPty, nil}}, session: 77}, true},
		{"our pty past other fds", fakeProcess{fds: []fakeFD{sock, {"/dev/null", fileID{dev: [2]uint32{0, 6}, rdev: [2]uint32{1, 3}}, nil}, {"/dev/pts/3", ourPty, nil}}, session: 77}, true},
		{"/dev/tty in the agent's session", fakeProcess{fds: []fakeFD{sock, {"/dev/tty", devTTY, nil}}, session: agentPid}, true},
		{"nothing of ours", fakeProcess{fds: []fakeFD{sock, {"/dev/pts/4", ourPty4, nil}}, session: agentPid}, false},
		// Trusted, as before: root's (and another uid's) fd tables are
		// unreadable to us.
		{"an fd table we may not read", fakeProcess{fds: []fakeFD{{"/dev/pts/3", ourPty, nil}}, session: agentPid, noFDRead: true}, false},
	})

	t.Run("several holders, the agent left out", func(t *testing.T) {
		v := fakeView(t, map[int]fakeProcess{
			agentPid: {fds: []fakeFD{{"/dev/pts/3", ourPty, nil}}, session: agentPid},
			2001:     {fds: []fakeFD{{"/dev/pts/3", ourPty, nil}}, session: 2001},
			2002:     {fds: []fakeFD{{"/dev/tty", devTTY, nil}}, session: agentPid},
			2003:     {fds: []fakeFD{{"/dev/pts/3", otherPty3, nil}}, session: 2003},
		})
		got := v.holders(agentPid, ourPty)
		slices.Sort(got)
		if !slices.Equal(got, []int{2001, 2002}) {
			t.Fatalf("holders = %v, want [2001 2002]", got)
		}
	})

	t.Run("no /proc", func(t *testing.T) {
		v := procView{root: filepath.Join(t.TempDir(), "missing")}
		if got := v.holders(agentPid, ourPty); !slices.Equal(got, []int{-1}) {
			t.Fatalf("holders = %v, want [-1] (can't tell)", got)
		}
	})
}

// The real stat: a pty's fileID is the same through its fd and through
// /proc's link to it, two ptys share their instance but not their index,
// and /dev/tty is 5:0.
func TestFileIDReal(t *testing.T) {
	m1, s1, err := openPTY()
	if err != nil {
		t.Fatal(err)
	}
	defer m1.Close()
	defer s1.Close()
	m2, s2, err := openPTY()
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	defer s2.Close()
	id1, err := fileIDOf(s1)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := fileIDOf(s2)
	if err != nil {
		t.Fatal(err)
	}
	if id1.dev != id2.dev || id1.rdev == id2.rdev {
		t.Fatalf("two ptys: %+v and %+v, want one instance, two indexes", id1, id2)
	}
	rc, err := s1.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var fd uintptr
	if err := rc.Control(func(f uintptr) { fd = f }); err != nil {
		t.Fatal(err)
	}
	viaProc, err := statFD("/proc/self/fd/" + strconv.Itoa(int(fd)))
	if err != nil {
		t.Fatal(err)
	}
	if viaProc != id1 {
		t.Fatalf("through /proc %+v, through the fd %+v", viaProc, id1)
	}
	tty, err := statFD("/dev/tty")
	if err != nil {
		t.Fatal(err)
	}
	if tty.rdev != ctty {
		t.Fatalf("/dev/tty is %v, want 5:0", tty.rdev)
	}
	if _, err := statFD("/proc/self/fd/987654"); !gone(err) {
		t.Fatalf("a closed fd: %v, want it gone", err)
	}
	m3, s3, err := openPTY()
	if err != nil {
		t.Fatal(err)
	}
	m3.Close()
	s3.Close()
	if _, err := fileIDOf(s3); err == nil {
		t.Fatal("fileIDOf a closed terminal succeeded")
	}
}
