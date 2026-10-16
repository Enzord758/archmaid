package main

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/testenv"
)

// envMain makes the test binary nullbroadway itself (headless_test.go runs
// headless.sh with it as HEADLESS_SERVER, so no cold build in a scratch
// GOCACHE).
const envMain = "ARCHMAID_NULLBROADWAY_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(envMain) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(testenv.Run(m))
}

// gdk is a test client speaking GDK's side of the protocol.
type gdk struct {
	t      *testing.T
	c      *net.UnixConn
	serial uint32
	buf    []byte
}

// start runs a server on a socket in a temp dir; errs gets what would
// have ended it.
func start(t *testing.T, w, h uint32) (path string, errs chan error) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "broadway1.socket")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	errs = make(chan error, 4)
	old := fatal
	fatal = func(err error) { errs <- err }
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := newServer(w, h).serve(l); err != nil {
			errs <- err
		}
	}()
	t.Cleanup(func() {
		l.Close()
		<-done
		fatal = old
	})
	return path, errs
}

func dial(t *testing.T, path string) *gdk {
	t.Helper()
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &gdk{t: t, c: c}
}

// send writes a request of typ with fields; fd, if >= 0, rides along.
func (g *gdk) send(typ uint32, fd int, fields ...uint32) uint32 {
	g.t.Helper()
	g.serial++
	b := make([]byte, reqHeader+4*len(fields))
	ne.PutUint32(b, uint32(len(b)))
	ne.PutUint32(b[4:], g.serial)
	ne.PutUint32(b[8:], typ)
	for i, f := range fields {
		ne.PutUint32(b[reqHeader+4*i:], f)
	}
	var oob []byte
	if fd >= 0 {
		oob = syscall.UnixRights(fd)
	}
	if _, _, err := g.c.WriteMsgUnix(b, oob, nil); err != nil {
		g.t.Fatal(err)
	}
	return g.serial
}

// record is one reply or event as read.
type record struct {
	typ, inReplyTo uint32
	body           []byte // after the reply header
}

// event is an event record's type and fields.
func (r record) event() (typ uint32, fields []uint32) {
	m := r.body[eventOffset-replyHeader:]
	typ = ne.Uint32(m)
	for i := eventBase; i+4 <= len(m); i += 4 {
		fields = append(fields, ne.Uint32(m[i:]))
	}
	return typ, fields
}

func (g *gdk) read() record {
	g.t.Helper()
	g.c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for len(g.buf) < 4 || uint32(len(g.buf)) < ne.Uint32(g.buf) {
		chunk := make([]byte, 4096)
		n, err := g.c.Read(chunk)
		if err != nil {
			g.t.Fatalf("read: %v", err)
		}
		g.buf = append(g.buf, chunk[:n]...)
	}
	size := ne.Uint32(g.buf)
	r := record{inReplyTo: ne.Uint32(g.buf[4:]), typ: ne.Uint32(g.buf[8:]), body: bytes.Clone(g.buf[replyHeader:size])}
	g.buf = g.buf[size:]
	return r
}

// next reads until an event of typ (skipping others), or a reply to serial.
func (g *gdk) nextEvent(typ uint32) []uint32 {
	g.t.Helper()
	for {
		r := g.read()
		if r.typ != replyEvent {
			g.t.Fatalf("reply type %d while waiting for event %d", r.typ, typ)
		}
		if et, f := r.event(); et == typ {
			return f
		}
	}
}

func (g *gdk) reply(serial, typ uint32) []uint32 {
	g.t.Helper()
	for {
		r := g.read()
		if r.typ == replyEvent {
			continue
		}
		if r.inReplyTo != serial || r.typ != typ {
			g.t.Fatalf("reply %d to %d, want %d to %d", r.typ, r.inReplyTo, typ, serial)
		}
		var f []uint32
		for i := 0; i+4 <= len(r.body); i += 4 {
			f = append(f, ne.Uint32(r.body[i:]))
		}
		return f
	}
}

func eq(t *testing.T, what string, got, want []uint32) {
	t.Helper()
	if len(got) < len(want) {
		t.Fatalf("%s: %v, want %v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: %v, want %v", what, got, want)
		}
	}
}

// The screen first, a surface with its configure notify, a move/resize
// with another, and every roundtrip answered as a browser does (local 0),
// so GDK never throttles to a frame a second.
func TestScreenSurfaceRoundtrip(t *testing.T) {
	path, errs := start(t, 1600, 1000)
	g := dial(t, path)
	eq(t, "screen", g.nextEvent(evScreenSizeChanged), []uint32{1600, 1000, 1})

	s := g.send(reqNewSurface, -1, uint32(10), uint32(20), 300, 200)
	eq(t, "configure", g.nextEvent(evConfigureNotify), []uint32{1, 10, 20, 300, 200})
	eq(t, "new surface", g.reply(s, replyNewSurface), []uint32{1})

	g.send(reqMoveResize, -1, 1, 0, 99, 99, 1460, 930) // no move: x, y stay
	eq(t, "resized", g.nextEvent(evConfigureNotify), []uint32{1, 10, 20, 1460, 930})
	g.send(reqMoveResize, -1, 1, 1, uint32(0xffffffff), 5, 1460, 930) // x = -1
	eq(t, "moved", g.nextEvent(evConfigureNotify), []uint32{1, 0xffffffff, 5, 1460, 930})

	for tag := uint32(100); tag < 103; tag++ {
		g.send(reqRoundtrip, -1, 1, tag)
		eq(t, "roundtrip", g.nextEvent(evRoundtripNotify), []uint32{1, tag, 0})
	}

	s = g.send(reqSync, -1)
	g.reply(s, replySync)
	s = g.send(reqQueryMouse, -1)
	eq(t, "query mouse", g.reply(s, replyQueryMouse), []uint32{0, 0, 0, 0})
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
}

// Focus goes to every client; a modal surface is focused on its configure
// notify, as broadwayd does; destroying the focused one focuses the root.
func TestFocus(t *testing.T) {
	path, _ := start(t, 1280, 800)
	a, b := dial(t, path), dial(t, path)
	a.nextEvent(evScreenSizeChanged)
	b.nextEvent(evScreenSizeChanged)

	s := a.send(reqNewSurface, -1, 0, 0, 100, 100)
	a.reply(s, replyNewSurface)
	a.send(reqFocusSurface, -1, 1)
	eq(t, "a: focus", a.nextEvent(evFocus), []uint32{1, 0})
	eq(t, "b: focus", b.nextEvent(evFocus), []uint32{1, 0})

	s = b.send(reqNewSurface, -1, 0, 0, 50, 50)
	b.reply(s, replyNewSurface)
	b.send(reqSetModalHint, -1, 2, 1)
	b.send(reqMoveResize, -1, 2, 0, 0, 0, 60, 60)
	eq(t, "modal focused", b.nextEvent(evFocus), []uint32{2, 1})
	eq(t, "a: modal focused", a.nextEvent(evFocus), []uint32{2, 1})

	b.send(reqDestroySurface, -1, 2)
	eq(t, "root focused", a.nextEvent(evFocus), []uint32{0, 2})
}

// A grab succeeds and an ungrab answers with its serial.
func TestGrab(t *testing.T) {
	path, _ := start(t, 1280, 800)
	g := dial(t, path)
	s := g.send(reqNewSurface, -1, 0, 0, 10, 10)
	g.reply(s, replyNewSurface)
	s = g.send(reqGrabPointer, -1, 1, 1, 0)
	eq(t, "grab", g.reply(s, replyGrabPointer), []uint32{grabSuccess})
	s = g.send(reqUngrabPointer, -1, 0)
	eq(t, "ungrab", g.reply(s, replyUngrabPointer), []uint32{s})
}

// Nodes and textures are dropped; a texture's fd is closed, not kept.
func TestTexturesAndNodesDropped(t *testing.T) {
	path, errs := start(t, 1280, 800)
	g := dial(t, path)
	f, err := os.CreateTemp(t.TempDir(), "texture")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	before := openFDs(t)
	for i := range 20 {
		g.send(reqUploadTexture, int(f.Fd()), uint32(i), 0, 4)
	}
	g.send(reqSetNodes, -1, 1, 7, 7, 7, 7)
	g.send(reqReleaseTexture, -1, 3)
	s := g.send(reqSync, -1)
	g.reply(s, replySync)
	if after := openFDs(t); after > before+1 {
		t.Errorf("open fds %d → %d: the texture fds are kept", before, after)
	}
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
}

func openFDs(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	return len(ents)
}

// A request it doesn't know (another gtk's protocol), a short one, an
// upload without its fd: each ends the server with an error naming it.
func TestProtocolErrors(t *testing.T) {
	for _, c := range []struct {
		name   string
		typ    uint32
		fields []uint32
		want   string
	}{
		{"unknown", 99, nil, "unknown request type 99"},
		{"short", reqMoveResize, []uint32{1}, "is short"},
		{"no fd", reqUploadTexture, []uint32{1, 0, 4}, "without its fd"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path, errs := start(t, 1280, 800)
			g := dial(t, path)
			g.send(c.typ, -1, c.fields...)
			select {
			case err := <-errs:
				if !strings.Contains(err.Error(), c.want) {
					t.Errorf("error %q, want %q", err, c.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no error")
			}
		})
	}
}

// A client that hangs up, or is gone with replies unread, is a disconnect:
// the server goes on serving the others.
func TestDisconnectIsNoError(t *testing.T) {
	path, errs := start(t, 1280, 800)
	a := dial(t, path)
	for range 50 {
		a.send(reqSync, -1)
	}
	a.c.Close()
	b := dial(t, path)
	b.nextEvent(evScreenSizeChanged)
	s := b.send(reqSync, -1)
	b.reply(s, replySync)
	select {
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestEventLayout(t *testing.T) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	c1, c2 := fileConn(t, fds[0]), fileConn(t, fds[1])
	cl := &client{conn: c1}
	cl.serial.Store(7)
	got := make([]byte, 56)
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(c2, got)
		done <- err
	}()
	newServer(1, 1).event(cl, evConfigureNotify, u32(1), u32(2), u32(3), u32(4), u32(5))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// 12 header + 4 pad, 16 base, 20 fields = 52, padded to 56.
	if size := ne.Uint32(got); size != 56 {
		t.Fatalf("size %d, want 56", size)
	}
	if typ := ne.Uint32(got[8:]); typ != replyEvent {
		t.Errorf("reply type %d, want %d", typ, replyEvent)
	}
	if et, serial := ne.Uint32(got[16:]), ne.Uint32(got[20:]); et != evConfigureNotify || serial != 7 {
		t.Errorf("event %d serial %d, want %d serial 7", et, serial, evConfigureNotify)
	}
	for i, want := range []uint32{1, 2, 3, 4, 5} {
		if f := ne.Uint32(got[32+4*i:]); f != want {
			t.Errorf("field %d at offset %d is %d, want %d", i, 32+4*i, f, want)
		}
	}
}

func fileConn(t *testing.T, fd int) *net.UnixConn {
	t.Helper()
	f := os.NewFile(uintptr(fd), "pair")
	c, err := net.FileConn(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c.(*net.UnixConn)
}
