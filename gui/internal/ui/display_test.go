package ui

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	glibv2 "github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
)

// The display tests run on headless.sh's private Broadway display
// (GDK_BACKEND=broadway, scripts/nullbroadway). GDK finds a broadway display only by number:
// BROADWAY_DISPLAY=":N" is $XDG_RUNTIME_DIR/broadway<N+1>.socket, with no
// absolute form (a WAYLAND_DISPLAY can be a path). testenv.Run moves
// XDG_RUNTIME_DIR to a scratch dir, so the window's children would look for
// the socket there: displayRun links it in, after testenv set the scratch
// environment up and before any test runs.

// broadwaySocket is the socket file GDK connects to for display (":N") in
// runtime; "" when display is no broadway display number.
func broadwaySocket(runtime, display string) string {
	n, ok := strings.CutPrefix(display, ":")
	if !ok || runtime == "" {
		return ""
	}
	num, err := strconv.Atoi(n)
	if err != nil || num < 0 {
		return ""
	}
	return filepath.Join(runtime, fmt.Sprintf("broadway%d.socket", num+1))
}

// linkBroadway links the broadway socket under the runtime dir from (the
// one headless.sh started broadwayd in) into to (testenv's scratch one).
// Nothing to do unless GDK_BACKEND is broadway; a missing socket is an
// error, so a broken setup fails at once instead of every scenario timing
// out without a window.
func linkBroadway(from, to string) error {
	if os.Getenv("GDK_BACKEND") != "broadway" {
		return nil
	}
	display := os.Getenv("BROADWAY_DISPLAY")
	src, dst := broadwaySocket(from, display), broadwaySocket(to, display)
	if src == "" || dst == "" {
		return fmt.Errorf("GDK_BACKEND=broadway needs BROADWAY_DISPLAY=:N and XDG_RUNTIME_DIR (have %q, %q)", display, from)
	}
	if fi, err := os.Stat(src); err != nil {
		return err
	} else if fi.Mode().Type() != os.ModeSocket {
		return fmt.Errorf("%s is no socket", src)
	}
	return os.Symlink(src, dst)
}

// displayRun is the package's tests, run with the display linked in.
type displayRun struct {
	m       *testing.M
	runtime string // XDG_RUNTIME_DIR before testenv.Run moved it
}

func (d displayRun) Run() int {
	if err := linkBroadway(d.runtime, os.Getenv("XDG_RUNTIME_DIR")); err != nil {
		fmt.Fprintln(os.Stderr, "display:", err)
		return 1
	}
	return d.m.Run()
}

// settleDisplay opens the broadway display and handles the events
// broadwayd sent as it connected, before the app builds its window.
// broadwayd tells a new client its screen size in an event, and until GDK
// handles it the monitor is 1024x768: a window presented in between (the
// app's, at once from activate) is clamped to that, and stays so when the
// real size arrives. Nothing to do on another backend.
func settleDisplay() {
	if os.Getenv("GDK_BACKEND") != "broadway" {
		return
	}
	gtk.Init()
	if d := gdk.DisplayGetDefault(); d != nil {
		d.Sync()
	}
	for ctx := glibv2.MainContextDefault(); ctx.Iteration(false); {
	}
}

// broadwayFrameTimings is the warning GDK's Broadway backend logs once per
// process on its own: its roundtrip notify marks every pending frame
// submitted again with a fresh value (gdksurface-broadway.c
// _gdk_broadway_roundtrip_notify), which gdkframetimings.c reports. It
// comes with gtk4-broadwayd and a browser too, and says nothing about the
// app.
const broadwayFrameTimings = "Duplicate call to gdk_frame_timings_submitted() with different values"

// broadwayNoise: l is that warning, on the Broadway backend. The checks
// that fail on any GTK warning (glibWarned) let it through there only.
func broadwayNoise(l string) bool {
	return os.Getenv("GDK_BACKEND") == "broadway" && strings.Contains(l, broadwayFrameTimings)
}

func TestBroadwaySocket(t *testing.T) {
	for _, c := range []struct{ runtime, display, want string }{
		{"/r", ":0", "/r/broadway1.socket"},
		{"/r", ":7", "/r/broadway8.socket"},
		{"/r", "0", ""},
		{"/r", ":x", ""},
		{"/r", ":-1", ""},
		{"", ":0", ""},
	} {
		if got := broadwaySocket(c.runtime, c.display); got != c.want {
			t.Errorf("broadwaySocket(%q, %q) = %q, want %q", c.runtime, c.display, got, c.want)
		}
	}
}

// The scratch runtime dir gets a link to headless.sh's socket; a missing
// socket or display is an error; another backend needs nothing.
func TestLinkBroadway(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	t.Setenv("GDK_BACKEND", "wayland")
	if err := linkBroadway(from, to); err != nil {
		t.Fatalf("wayland: %v", err)
	}
	t.Setenv("GDK_BACKEND", "broadway")
	t.Setenv("BROADWAY_DISPLAY", ":0")
	if err := linkBroadway(from, to); err == nil {
		t.Error("no socket: no error")
	}
	sock := filepath.Join(from, "broadway1.socket")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := linkBroadway(from, to); err != nil {
		t.Fatal(err)
	}
	if dst, err := os.Readlink(filepath.Join(to, "broadway1.socket")); err != nil || dst != sock {
		t.Errorf("link %q, %v; want %q", dst, err, sock)
	}
	t.Setenv("BROADWAY_DISPLAY", "")
	if err := linkBroadway(from, t.TempDir()); err == nil {
		t.Error("no display: no error")
	}
}

// Only the Broadway frame-timings warning, and only on Broadway, is let
// through the checks for GTK warnings.
func TestBroadwayNoise(t *testing.T) {
	l := "2026/10/16 22:18:41 WARN " + broadwayFrameTimings + ". priority=4 code_file=../gtk/gdk/gdkframetimings.c"
	other := "2026/10/16 22:18:41 WARN Failed to open display priority=4"
	t.Setenv("GDK_BACKEND", "broadway")
	if glibWarned(l) || !glibWarned(other) {
		t.Errorf("broadway: frame timings warned %v, other %v", glibWarned(l), glibWarned(other))
	}
	t.Setenv("GDK_BACKEND", "wayland")
	if !glibWarned(l) {
		t.Error("wayland: the frame timings warning passed")
	}
}
