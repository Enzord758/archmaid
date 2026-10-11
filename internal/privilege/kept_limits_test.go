package privilege

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// What KeptPassword and the su check do when the system says no: a page
// that can't be mapped or locked keeps nothing, and a user the system can't
// name never reaches su. Each runs in a child (this test binary again)
// whose limits or user namespace can't touch the rest of the suite.

// envKeptChild names the child's case; its value is the case.
const envKeptChild = "ARCHMAID_KEPT_CHILD"

// runKeptChild runs TestKeptChild as case c with attr (nil: a plain child)
// and fails with its output if the child failed.
func runKeptChild(t *testing.T, c string, attr *syscall.SysProcAttr, env ...string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestKeptChild$", "-test.count=1", "-test.v")
	cmd.Env = append(append(os.Environ(), envKeptChild+"="+c), env...)
	cmd.SysProcAttr = attr
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child %s: %v\n%s", c, err, out)
	}
	if !strings.Contains(string(out), "--- PASS: TestKeptChild") {
		t.Fatalf("child %s did not run its case:\n%s", c, out)
	}
}

// TestKeptChild is the child's half; on its own it does nothing.
func TestKeptChild(t *testing.T) {
	switch os.Getenv(envKeptChild) {
	case "":
		t.Skip("run by the tests below")
	case "memlock":
		keptUnder(t, unix.RLIMIT_MEMLOCK, func(uint64) uint64 { return 0 })
	case "as":
		keptUnder(t, unix.RLIMIT_AS, func(uint64) uint64 { return vmSize(t) })
	case "nouser":
		noUserChild(t)
	default:
		t.Fatalf("unknown case %q", os.Getenv(envKeptChild))
	}
}

// keptUnder lowers resource res's soft limit to limit(current soft) for
// one Keep, then restores it, and requires that Keep held nothing.
func keptUnder(t *testing.T, res int, limit func(uint64) uint64) {
	var old unix.Rlimit
	if err := unix.Getrlimit(res, &old); err != nil {
		t.Fatal(err)
	}
	k := NewKeptPassword(time.Hour)
	// Nothing in this window may need memory from the system: the
	// runtime would throw instead of failing a Keep.
	runtime.GC()
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	low := old
	low.Cur = limit(old.Cur)
	if err := unix.Setrlimit(res, &low); err != nil {
		t.Fatal(err)
	}
	kept := k.Keep([]byte("hunter2"))
	if err := unix.Setrlimit(res, &old); err != nil {
		t.Fatal(err)
	}
	if kept || k.Has() {
		t.Fatal("kept a password in a page that couldn't be mapped and locked")
	}
	if pw, err := k.Copy(); err != ErrNotKept || pw != nil {
		t.Fatalf("Copy = %q, %v", pw, err)
	}
}

// vmSize is this process's address space now, in bytes.
func vmSize(t *testing.T) uint64 {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "VmSize:"); ok {
			kb, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			return kb << 10
		}
	}
	t.Fatal("no VmSize in /proc/self/status")
	return 0
}

// TestKeepUnlockablePage: with RLIMIT_MEMLOCK at 0 the page can't be
// locked, so the password is not kept (it could be swapped out); the page
// is given back and Keep says false, so the run asks as it always did.
func TestKeepUnlockablePage(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root (CAP_IPC_LOCK) locks pages past RLIMIT_MEMLOCK")
	}
	runKeptChild(t, "memlock", nil)
}

// TestKeepUnmappablePage: with no address space left the page can't be
// mapped, and nothing is kept.
func TestKeepUnmappablePage(t *testing.T) {
	runKeptChild(t, "as", nil)
}

// noUserUID is the child's uid in its user namespace: one no passwd has.
const noUserUID = 3999917

// noUserChild: in a user namespace where our uid has no passwd entry (and
// no $USER to fall back on), CheckPassword and CheckPasswordBytes fail
// before su runs: su with no user name would check root's password.
func noUserChild(t *testing.T) {
	if os.Getuid() != noUserUID {
		t.Fatalf("uid %d, want %d", os.Getuid(), noUserUID)
	}
	marker := os.Getenv("ARCHMAID_SU_MARKER")
	for name, check := range map[string]func() error{
		"CheckPassword": func() error { _, err := CheckPassword(context.Background(), "secret"); return err },
		"CheckPasswordBytes": func() error {
			_, err := CheckPasswordBytes(context.Background(), []byte("secret"))
			return err
		},
	} {
		if err := check(); err == nil {
			t.Errorf("%s passed with no current user", name)
		}
		if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s ran su (%v)", name, err)
		}
	}
}

// TestCheckPasswordNoUser: who the current user is can't be found, so no
// su runs at all (see noUserChild). Needs unprivileged user namespaces.
func TestCheckPasswordNoUser(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "su.ran")
	if err := os.WriteFile(filepath.Join(dir, "su"), []byte("#!/bin/sh\n: > '"+marker+"'\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	attr := &syscall.SysProcAttr{
		Cloneflags:                 syscall.CLONE_NEWUSER,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: noUserUID, HostID: os.Getuid(), Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: noUserUID, HostID: os.Getgid(), Size: 1}},
		GidMappingsEnableSetgroups: false,
	}
	probe := exec.Command("/bin/true")
	probe.SysProcAttr = attr
	if err := probe.Run(); err != nil {
		t.Skipf("no unprivileged user namespaces here: %v", err)
	}
	// No $USER: without cgo, os/user would build the user from it.
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "USER=") && !strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestKeptChild$", "-test.count=1", "-test.v")
	cmd.Env = append(env, envKeptChild+"=nouser", "ARCHMAID_SU_MARKER="+marker, "PATH="+dir+":"+os.Getenv("PATH"))
	cmd.SysProcAttr = attr
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: TestKeptChild") {
		t.Fatalf("child: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("su ran for a user that couldn't be named")
	}
}
