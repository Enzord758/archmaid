package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/cmd/archmaid-helper/internal/helper"
)

// TestTerminalSignalsDontReachChildren re-execs the test binary three levels
// deep: the test starts a "helper" (in a process group of its own, standing
// in for the terminal's foreground job), the helper starts a child through
// the real helper.ExecRunner, and the child catches the signals a terminal
// sends and reports which ones arrived. Ctrl-C, Ctrl-\, Ctrl-Z and a hangup
// go to the helper's whole process group; none of them may reach the child,
// and the helper must still finish with its result line.
const (
	sessionChildEnv      = "ARCHMAID_HELPER_SESSION_CHILD"
	sessionDirEnv        = "ARCHMAID_HELPER_SESSION_DIR"
	sessionGrandchildArg = "archmaid-helper-session-grandchild"
)

// terminalSignals are what a terminal sends its foreground process group.
var terminalSignals = []syscall.Signal{syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTSTP}

func getsid(pid int) int {
	sid, _, errno := syscall.RawSyscall(syscall.SYS_GETSID, uintptr(pid), 0, 0)
	if errno != 0 {
		return -1
	}
	return int(sid)
}

// sessionChild is the helper: it sets up its signal handling the way main
// does, runs the grandchild, and writes a result line.
func sessionChild(dir string) {
	catchSignals()
	exe, err := os.Executable()
	if err != nil {
		os.Exit(1)
	}
	em := helper.NewEmitter(os.Stdout)
	exit, err := helper.ExecRunner{}.Stream([]string{exe, sessionGrandchildArg, dir}, em.Line)
	code, msg := helper.ExitOK, ""
	if err != nil || exit != 0 {
		code, msg = helper.ExitFailed, fmt.Sprintf("grandchild: exit %d, %v", exit, err)
	}
	em.Result(code, &exit, nil, msg)
}

// sessionGrandchild is the child: it reports its session and process group
// in dir/ready, waits for dir/done, then prints the signals it caught.
func sessionGrandchild(dir string) {
	ch := make(chan os.Signal, 16)
	sigs := make([]os.Signal, len(terminalSignals))
	for i, s := range terminalSignals {
		sigs[i] = s
	}
	signal.Notify(ch, sigs...)
	ready := fmt.Sprintf("%d %d %d", os.Getpid(), getsid(0), syscall.Getpgrp())
	tmp := filepath.Join(dir, "ready.tmp")
	if os.WriteFile(tmp, []byte(ready), 0o600) != nil || os.Rename(tmp, filepath.Join(dir, "ready")) != nil {
		os.Exit(1)
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(dir, "done")); err == nil {
			break
		}
	}
	var got []string
drain:
	for {
		select {
		case s := <-ch:
			got = append(got, s.String())
		default:
			break drain
		}
	}
	fmt.Println("caught:" + strings.Join(got, ","))
}

func TestTerminalSignalsDontReachChildren(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Env = childEnv(sessionChildEnv+"=1", sessionDirEnv+"="+dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	helperPid := cmd.Process.Pid
	finished := false
	defer func() {
		if !finished {
			syscall.Kill(-helperPid, syscall.SIGKILL)
			os.WriteFile(filepath.Join(dir, "done"), nil, 0o600) // lets the grandchild go
			cmd.Wait()
		}
	}()

	var ready []byte
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if ready, err = os.ReadFile(filepath.Join(dir, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the grandchild never started")
		}
	}
	var pid, sid, pgid int
	if _, err := fmt.Sscan(string(ready), &pid, &sid, &pgid); err != nil {
		t.Fatalf("ready %q: %v", ready, err)
	}
	if helperSid := getsid(helperPid); sid == helperSid || sid != pid {
		t.Errorf("child session %d, helper session %d: the child must lead a session of its own", sid, helperSid)
	}
	if pgid == helperPid {
		t.Errorf("the child is in the helper's process group %d", pgid)
	}

	for _, s := range terminalSignals {
		if err := syscall.Kill(-helperPid, s); err != nil {
			t.Fatalf("kill(-%d, %v): %v", helperPid, s, err)
		}
	}
	time.Sleep(200 * time.Millisecond) // a signal that got through has landed by now
	if err := os.WriteFile(filepath.Join(dir, "done"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	finished = true
	if err != nil {
		t.Fatalf("the helper did not survive the signals: %v\n%s", err, out.String())
	}

	var caught string
	var last map[string]any
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("not a JSON line: %q", sc.Text())
		}
		if text, _ := ev["text"].(string); ev["t"] == helper.EventLine && strings.HasPrefix(text, "caught:") {
			caught = text
		}
		last = ev
	}
	if caught != "caught:" {
		t.Errorf("signals sent to the helper's process group reached its child: %q", caught)
	}
	if last == nil || last["t"] != helper.EventResult || last["ok"] != true {
		t.Errorf("the helper's last line is not an ok result: %v", last)
	}
}
