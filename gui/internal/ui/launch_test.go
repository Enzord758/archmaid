package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/core/glib"

	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/engine/stub"
)

// TestParseLaunch: only --stub runs the stub. ARCHMAID_GUI_STUB and
// ARCHMAID_GUI_SCRIPT, whatever their value, leave a launch without it on
// the real Driver; with it, the script is read and an unknown one is
// refused instead of falling through to the Catalog.
func TestParseLaunch(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	stray := env(map[string]string{"ARCHMAID_GUI_STUB": "1", ScriptEnv: "flood"})
	for _, args := range [][]string{{"archmaid-gui"}, {"archmaid-gui", "--gapplication-service"}, {"--stub"}} {
		l, err := parseLaunch(args, stray)
		if err != nil || l.stub || l.script != "" || !slices.Equal(l.args, args) {
			t.Errorf("parseLaunch(%q) with stray env = %+v, %v; want the real Driver", args, l, err)
		}
	}
	for _, s := range stubScripts {
		l, err := parseLaunch([]string{"archmaid-gui", "--stub"}, env(map[string]string{ScriptEnv: s}))
		if err != nil || !l.stub || l.script != s || !slices.Equal(l.args, []string{"archmaid-gui"}) {
			t.Errorf("--stub with script %q = %+v, %v", s, l, err)
		}
	}
	// --confirm takes fullcleanup only, once, as --confirm X or --confirm=X.
	for _, args := range [][]string{{"archmaid-gui", "--confirm", "fullcleanup"}, {"archmaid-gui", "--confirm=fullcleanup", "--stub"}} {
		l, err := parseLaunch(args, env(nil))
		if err != nil || l.confirm != "fullcleanup" || !slices.Equal(l.args, []string{"archmaid-gui"}) {
			t.Errorf("%q = %+v, %v", args, l, err)
		}
	}
	for _, args := range [][]string{{"archmaid-gui", "--confirm"}, {"archmaid-gui", "--confirm", "topgrade"}, {"archmaid-gui", "--confirm=bogus"},
		{"archmaid-gui", "--confirm", ""}, {"archmaid-gui", "--confirm", "fullcleanup", "--confirm", "fullcleanup"}} {
		if l, err := parseLaunch(args, env(nil)); err == nil {
			t.Errorf("%q = %+v, want refused", args, l)
		}
	}
	for _, s := range []string{"0", "no", " ", "Flood", "bogus"} {
		if l, err := parseLaunch([]string{"archmaid-gui", "--stub"}, env(map[string]string{ScriptEnv: s})); err == nil {
			t.Errorf("--stub with script %q = %+v, want refused", s, l)
		}
	}
}

// TestPasswordTextClipsPrompt: sudo's askpass prompt comes from whatever
// asked, so the dialog shows it on one line, clipped to 48 runes, as the
// TUI's popup does; who asked is the caller line of its own.
func TestPasswordTextClipsPrompt(t *testing.T) {
	long := "[sudo] password for someone\nwith a very long prompt that goes on and on and on"
	head, caller, body := passwordText(engine.NeedsPassword{Kind: engine.PasswordAskpass, Requester: "topgrade (pid 7)", Prompt: long})
	if head != "sudo Asks for Your Password" || caller != "Asked by topgrade (pid 7)." || body != `sudo says: "[sudo] password for someone with a very long pr…"` {
		t.Errorf("%q / %q / %q", head, caller, body)
	}
	if _, caller, body := passwordText(engine.NeedsPassword{Kind: engine.PasswordAskpass, Prompt: "[sudo] password: "}); caller != "A running step needs root." || body != `sudo says: "[sudo] password:"` {
		t.Errorf("short prompt: %q / %q", caller, body)
	}
}

// TestPasswordTextPromptCantPose: whatever runs sudo sets its prompt (sudo
// -p), so the prompt is shown quoted as sudo's, never as a line that could
// pass for the caller line (Dev 3).
func TestPasswordTextPromptCantPose(t *testing.T) {
	_, caller, body := passwordText(engine.NeedsPassword{Kind: engine.PasswordAskpass,
		Requester: "/usr/bin/bash (pid 9)", Prompt: "Asked by /usr/bin/topgrade."})
	if caller != "Asked by /usr/bin/bash (pid 9)." || body != `sudo says: "Asked by /usr/bin/topgrade."` {
		t.Errorf("caller %q body %q", caller, body)
	}
	if strings.HasPrefix(body, "Asked by") {
		t.Errorf("the prompt reads as a caller line: %q", body)
	}
}

// TestPasswordTextKind: the dialog's kind and title come from the prompt's
// data, never from the Requester string. polkit's prompt has its own title
// and shows the action id verbatim on its caller line, without PAM's raw
// "Password:" prompt; a sudo requester whose path says "polkit" (whatever
// ran sudo names itself) still gets the sudo dialog.
func TestPasswordTextKind(t *testing.T) {
	head, caller, body := passwordText(engine.NeedsPassword{Kind: engine.PasswordAskpass, Polkit: true,
		Action: "org.archmaid.helper.full-cleanup", Requester: "polkit's agent (pkttyagent)", Prompt: "Password: "})
	if head != PolkitTitle || caller != "Asked by polkit for org.archmaid.helper.full-cleanup." || body != "" {
		t.Errorf("polkit: %q / %q / %q", head, caller, body)
	}
	_, _, body = passwordText(engine.NeedsPassword{Kind: engine.PasswordAskpass, Polkit: true, Prompt: "Password: ",
		Action: "org.archmaid.helper.full-cleanup", Error: "Incorrect password — 2 attempts left", AttemptsLeft: 2})
	if body != "Incorrect password — 2 attempts left" {
		t.Errorf("polkit after a wrong password: %q", body)
	}
	head, caller, body = passwordText(engine.NeedsPassword{Kind: engine.PasswordAskpass,
		Requester: "/home/me/polkit, for org.archmaid.helper.full-cleanup (pid 9)", Prompt: "[sudo] password for me: "})
	if head != "sudo Asks for Your Password" || caller != "Asked by /home/me/polkit, for org.archmaid.helper.full-cleanup (pid 9)." || strings.Contains(body, "Action:") {
		t.Errorf("a sudo requester named polkit: %q / %q / %q", head, caller, body)
	}
}

// TestPasswordTextCounts: the attempts left are said once, in the singular
// for one: the engine's error already counts them; a lockout notice in its
// place doesn't, so the dialog adds the count.
func TestPasswordTextCounts(t *testing.T) {
	_, _, body := passwordText(engine.NeedsPassword{Kind: engine.PasswordUpFront, AttemptsLeft: 1, Error: "Incorrect password — 1 attempt left"})
	if want := "Your password lets archmaid run the steps that need root.\n\nIncorrect password — 1 attempt left"; body != want {
		t.Errorf("body %q, want %q", body, want)
	}
	_, _, body = passwordText(engine.NeedsPassword{Kind: engine.PasswordUpFront, AttemptsLeft: 1, Error: "Account locked soon"})
	if !strings.HasSuffix(body, "Account locked soon\n1 attempt left.") {
		t.Errorf("body %q, want the lockout notice, then 1 attempt left.", body)
	}
}

// The instance child's environment.
const (
	envInstance = "ARCHMAID_GUI_INSTANCE" // "real" or "stub"
	envAppID    = "ARCHMAID_GUI_APPID"    // the test's own base app id
	envHold     = "ARCHMAID_GUI_HOLD"     // ms its window stays up
)

// runInstanceChild runs the app as unique (flags 0, as archmaid-gui does)
// under the test's own app id, as the real or the stub app, on the stub
// Driver either way (nothing is run). It reports "window" when its own
// window came up, "none" when its activation went to another instance;
// path+".up" marks the window up.
func runInstanceChild(mode string) int {
	path := os.Getenv("ARCHMAID_GUI_REPORT")
	hold, _ := strconv.Atoi(os.Getenv(envHold))
	got := "none"
	opts := Options{
		Stub:  mode == "stub",
		appID: os.Getenv(envAppID),
		NewDriver: func(engine.Options) engine.Driver {
			return stub.New(nil, stub.Options{Scripts: stub.Catalog(time.Now())})
		},
		hook: func(w *Window) {
			got = "window"
			_ = os.WriteFile(path+".up", nil, 0o600)
			glib.TimeoutAdd(uint(hold), func() { w.win.Close() })
		},
	}
	code := Run([]string{"archmaid-gui-test"}, opts)
	if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
		return 3
	}
	return code
}

// startInstance starts an instance child; wait returns what it reported.
func startInstance(t *testing.T, mode, appID string, hold time.Duration) (path string, wait func() string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "report")
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), envInstance+"="+mode, envAppID+"="+appID,
		envHold+"="+strconv.Itoa(int(hold/time.Millisecond)), "ARCHMAID_GUI_REPORT="+path)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return path, func() string {
		t.Helper()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s instance: %v", mode, err)
			}
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill()
			t.Fatalf("%s instance still running after 30s", mode)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
}

// TestGUIStubOwnInstance: the stub and the real app never share an
// instance. With a real window up (unique, on headless.sh's private
// session bus), a stub launch gets its own window rather than handing
// its activation to the real one; a second real launch does hand it over
// (which shows uniqueness is in effect, so the first check means
// something). The app id is the test's own, never AppID, so nothing here
// can meet an archmaid-gui the owner has open.
func TestGUIStubOwnInstance(t *testing.T) {
	needDisplay(t)
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Skip("no session bus: run it through gui/scripts/headless.sh (a private one)")
	}
	id := fmt.Sprintf("io.github.enzord758.archmaid.test%d", os.Getpid())
	upPath, waitReal := startInstance(t, "real", id, 6*time.Second)
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(upPath + ".up"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the real instance's window never came up")
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, waitStub := startInstance(t, "stub", id, 300*time.Millisecond)
	if got := waitStub(); got != "window" {
		t.Errorf("a stub launch with a real window up: %q, want its own window", got)
	}
	_, waitSecond := startInstance(t, "real", id, 300*time.Millisecond)
	if got := waitSecond(); got != "none" {
		t.Errorf("a second real launch: %q, want it handed to the first (uniqueness in effect)", got)
	}
	if got := waitReal(); got != "window" {
		t.Errorf("the real instance: %q", got)
	}
}
