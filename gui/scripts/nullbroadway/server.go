// Command nullbroadway is a Broadway display server that shows nothing: the
// headless display of the GUI's display tests (gui/scripts/headless.sh).
//
//	nullbroadway -socket $XDG_RUNTIME_DIR/broadway1.socket -width 1280 -height 800
//
// GTK's Broadway backend (GDK_BACKEND=broadway) talks to its display server
// over a Unix socket, in the binary protocol of gtk's
// gdk/broadway/broadway-protocol.h. gtk4-broadwayd is that server, but it
// does not do for the tests: with no browser connected it answers each
// frame's roundtrip locally, and GDK then draws one frame a second; with a
// browser (or a stand-in) connected, gtk 4.24's broadwayd aborts on the
// app's frames (broadway-output.c append_node_ops asserts "node ==
// reused_node" with the cairo renderer, "!reused_node->consumed" with
// Broadway's own). This server keeps broadwayd's no-browser behaviour —
// surfaces, configure notifies, focus, pointer grabs, the screen size —
// answers every roundtrip as a browser would (so frames run at once), and
// drops the render nodes and textures instead of forwarding them: there is
// no web server and nothing to diff. It listens on the one Unix socket and
// nothing else.
//
// A request it doesn't know (a newer gtk's protocol) ends it with an
// error, so the GUI fails at once instead of drawing wrong.
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Requests (BroadwayRequestType).
const (
	reqNewSurface      = 0
	reqFlush           = 1
	reqSync            = 2
	reqQueryMouse      = 3
	reqDestroySurface  = 4
	reqShowSurface     = 5
	reqHideSurface     = 6
	reqSetTransientFor = 7
	reqMoveResize      = 8
	reqGrabPointer     = 9
	reqUngrabPointer   = 10
	reqFocusSurface    = 11
	reqSetShowKeyboard = 12
	reqUploadTexture   = 13
	reqReleaseTexture  = 14
	reqSetNodes        = 15
	reqRoundtrip       = 16
	reqSetModalHint    = 17
)

// Replies (BroadwayReplyType).
const (
	replyEvent         = 0
	replySync          = 1
	replyQueryMouse    = 2
	replyNewSurface    = 3
	replyGrabPointer   = 4
	replyUngrabPointer = 5
)

// Events (BroadwayEventType).
const (
	evConfigureNotify   = 11
	evScreenSizeChanged = 12
	evFocus             = 13
	evRoundtripNotify   = 14
)

// Sizes of the protocol's C structs on a 64-bit ABI (broadwayd sends
// sizeof the event struct after the reply header, whose event union is
// 8-aligned: BroadwayInputBaseMsg holds a guint64).
const (
	reqHeader      = 12 // size, serial, type
	replyHeader    = 12 // size, in_reply_to, type
	eventOffset    = 16 // offsetof (BroadwayReplyEvent, msg)
	eventBase      = 16 // type, serial, time (guint64)
	maxRequest     = 64 << 20
	grabSuccess    = 0 // GDK_GRAB_SUCCESS
	alreadyGrabbed = 1 // GDK_GRAB_ALREADY_GRABBED
)

var ne = binary.NativeEndian

type surface struct {
	id, owner      uint32
	x, y           int32
	width, height  uint32
	visible, modal bool
	transientFor   uint32
}

type client struct {
	id     uint32
	conn   *net.UnixConn
	wmu    sync.Mutex
	serial atomic.Uint32 // the last request's
	fds    []int         // passed with upload requests, not yet used
}

// server is the display: its state is broadwayd's (broadway-server.c) for
// a server with no browser output.
type server struct {
	width, height uint32
	start         time.Time

	mu       sync.Mutex
	clients  map[uint32]*client
	surfaces map[uint32]*surface
	nextID   uint32 // surface ids; the root is 0
	nextCl   uint32
	focused  uint32
	grab     uint32 // the grabbing surface, 0 for none
	grabTime uint32
}

func newServer(width, height uint32) *server {
	return &server{
		width: width, height: height, start: time.Now(),
		clients: map[uint32]*client{}, surfaces: map[uint32]*surface{}, nextID: 1,
	}
}

func (s *server) now() uint64 { return uint64(time.Since(s.start).Milliseconds()) }

// serve accepts GDK clients until l closes.
func (s *server) serve(l *net.UnixListener) error {
	for {
		c, err := l.AcceptUnix()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		cl := s.connect(c)
		go func() {
			err := s.handle(cl)
			s.disconnect(cl)
			if err != nil {
				fatal(fmt.Errorf("client %d: %w", cl.id, err))
			}
		}()
	}
}

// fatal ends the server: a protocol it can't follow must not limp on.
var fatal = func(err error) { panic(err) }

func (s *server) connect(c *net.UnixConn) *client {
	s.mu.Lock()
	s.nextCl++
	cl := &client{id: s.nextCl, conn: c}
	s.clients[cl.id] = cl
	s.mu.Unlock()
	// As broadwayd's incoming_client: the screen, first thing.
	s.event(cl, evScreenSizeChanged, u32(s.width), u32(s.height), u32(1))
	return cl
}

func (s *server) disconnect(cl *client) {
	s.mu.Lock()
	delete(s.clients, cl.id)
	var gone []uint32
	for id, sf := range s.surfaces {
		if sf.owner == cl.id {
			gone = append(gone, id)
		}
	}
	s.mu.Unlock()
	for _, id := range gone {
		s.destroy(id)
	}
	for _, fd := range cl.fds {
		syscall.Close(fd)
	}
	cl.conn.Close()
}

// handle reads one client's requests until it hangs up (nil).
func (s *server) handle(cl *client) error {
	base := make([]byte, 0, 1<<16)
	buf := base
	chunk := make([]byte, 1<<16)
	oob := make([]byte, syscall.CmsgSpace(64*4))
	for {
		n, oobn, _, _, err := cl.conn.ReadMsgUnix(chunk, oob)
		n, oobn = max(n, 0), max(oobn, 0) // -1 with an error
		if oobn > 0 {
			fds, ferr := unixRights(oob[:oobn])
			cl.fds = append(cl.fds, fds...)
			if ferr != nil {
				return ferr
			}
		}
		buf = append(buf, chunk[:n]...)
		for len(buf) >= 4 {
			size := ne.Uint32(buf)
			if size < reqHeader || size > maxRequest {
				return fmt.Errorf("request of %d bytes", size)
			}
			if uint32(len(buf)) < size {
				break
			}
			if err := s.request(cl, buf[:size]); err != nil {
				return err
			}
			buf = buf[size:]
		}
		base = append(base[:0], buf...) // the partial request, to the front
		buf = base
		if err != nil {
			// Gone (EOF, or reset by a client killed with replies
			// unread): a disconnect, not a protocol error.
			return nil
		}
	}
}

func unixRights(oob []byte) ([]int, error) {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, err
	}
	var fds []int
	for _, m := range msgs {
		got, err := syscall.ParseUnixRights(&m)
		if err != nil {
			return fds, err
		}
		fds = append(fds, got...)
	}
	return fds, nil
}

// args reads a request's 32-bit fields after its header.
type args struct {
	b   []byte
	pos int
	err error
}

func (a *args) u32() uint32 {
	if a.pos+4 > len(a.b) {
		if a.err == nil {
			a.err = fmt.Errorf("request of %d bytes is short", len(a.b))
		}
		return 0
	}
	v := ne.Uint32(a.b[a.pos:])
	a.pos += 4
	return v
}

func (a *args) i32() int32 { return int32(a.u32()) }

// request handles one request: broadwayd.c's client_handle_request.
func (s *server) request(cl *client, b []byte) error {
	serial, typ := ne.Uint32(b[4:]), ne.Uint32(b[8:])
	cl.serial.Store(serial)
	a := &args{b: b, pos: reqHeader}
	switch typ {
	case reqNewSurface:
		x, y, w, h := a.i32(), a.i32(), a.u32(), a.u32()
		if a.err != nil {
			return a.err
		}
		id := s.newSurface(cl, x, y, w, h)
		s.reply(cl, serial, replyNewSurface, u32(id))
	case reqFlush, reqSetShowKeyboard, reqReleaseTexture, reqSetNodes:
		// Nothing is shown: nodes and textures are dropped.
	case reqSync:
		s.reply(cl, serial, replySync)
	case reqQueryMouse:
		// No pointer ever moves: broadwayd's values before any input.
		s.reply(cl, serial, replyQueryMouse, u32(0), u32(0), u32(0), u32(0))
	case reqDestroySurface:
		id := a.u32()
		if a.err != nil {
			return a.err
		}
		s.destroy(id)
	case reqShowSurface, reqHideSurface:
		id := a.u32()
		if a.err != nil {
			return a.err
		}
		s.mu.Lock()
		if sf := s.surfaces[id]; sf != nil {
			sf.visible = typ == reqShowSurface
			if !sf.visible && s.grab == id {
				s.grab = 0
			}
		}
		s.mu.Unlock()
	case reqSetTransientFor:
		id, parent := a.u32(), a.u32()
		if a.err != nil {
			return a.err
		}
		s.mu.Lock()
		if sf := s.surfaces[id]; sf != nil {
			sf.transientFor = parent
		}
		s.mu.Unlock()
	case reqMoveResize:
		id, withMove, x, y, w, h := a.u32(), a.u32(), a.i32(), a.i32(), a.u32(), a.u32()
		if a.err != nil {
			return a.err
		}
		s.moveResize(id, withMove != 0, x, y, w, h)
	case reqGrabPointer:
		id, _, t := a.u32(), a.u32(), a.u32() // owner_events: no pointer to route
		if a.err != nil {
			return a.err
		}
		s.reply(cl, serial, replyGrabPointer, u32(s.grabPointer(id, t)))
	case reqUngrabPointer:
		t := a.u32()
		if a.err != nil {
			return a.err
		}
		s.reply(cl, serial, replyUngrabPointer, u32(s.ungrabPointer(t, serial)))
	case reqFocusSurface:
		id := a.u32()
		if a.err != nil {
			return a.err
		}
		s.focus(id)
	case reqUploadTexture:
		a.u32() // id, offset, size: the data is in the fd
		a.u32()
		a.u32()
		if a.err != nil {
			return a.err
		}
		if len(cl.fds) == 0 {
			return errors.New("texture upload without its fd")
		}
		syscall.Close(cl.fds[0])
		cl.fds = cl.fds[1:]
	case reqRoundtrip:
		id, tag := a.u32(), a.u32()
		if a.err != nil {
			return a.err
		}
		// A browser's answer (local = 0): GDK then draws the next frame
		// at once instead of a second later.
		s.event(cl, evRoundtripNotify, u32(id), u32(tag), u32(0))
	case reqSetModalHint:
		id, modal := a.u32(), a.u32()
		if a.err != nil {
			return a.err
		}
		s.mu.Lock()
		if sf := s.surfaces[id]; sf != nil {
			sf.modal = modal != 0
		}
		s.mu.Unlock()
	default:
		return fmt.Errorf("unknown request type %d (%d bytes): not the protocol this server speaks", typ, len(b))
	}
	return nil
}

func (s *server) newSurface(cl *client, x, y int32, w, h uint32) uint32 {
	s.mu.Lock()
	sf := &surface{id: s.nextID, owner: cl.id, x: x, y: y, width: w, height: h}
	s.nextID++
	s.surfaces[sf.id] = sf
	s.mu.Unlock()
	s.configure(sf)
	return sf.id
}

// configure sends the owner a configure notify (broadwayd's
// fake_configure_notify), then does what broadwayd does with one: a modal
// surface that isn't focused, with no grab on, is focused.
func (s *server) configure(sf *surface) {
	s.mu.Lock()
	owner := s.clients[sf.owner]
	x, y, w, h := sf.x, sf.y, sf.width, sf.height
	focus := sf.modal && s.focused != sf.id && s.grab == 0
	s.mu.Unlock()
	if owner != nil {
		s.event(owner, evConfigureNotify, u32(sf.id), u32(uint32(x)), u32(uint32(y)), u32(w), u32(h))
	}
	if focus {
		s.focus(sf.id)
	}
}

func (s *server) moveResize(id uint32, withMove bool, x, y int32, w, h uint32) {
	s.mu.Lock()
	sf := s.surfaces[id]
	if sf != nil {
		if withMove {
			sf.x, sf.y = x, y
		}
		sf.width, sf.height = w, h
	}
	s.mu.Unlock()
	if sf != nil {
		s.configure(sf)
	}
}

// focus moves the focus to id and tells every client (broadwayd's
// broadway_server_focus_surface).
func (s *server) focus(id uint32) {
	s.mu.Lock()
	old := s.focused
	if old == id {
		s.mu.Unlock()
		return
	}
	s.focused = id
	all := make([]*client, 0, len(s.clients))
	for _, c := range s.clients {
		all = append(all, c)
	}
	s.mu.Unlock()
	for _, c := range all {
		s.event(c, evFocus, u32(id), u32(old))
	}
}

func (s *server) destroy(id uint32) {
	s.mu.Lock()
	sf := s.surfaces[id]
	delete(s.surfaces, id)
	if s.grab == id {
		s.grab = 0
	}
	refocus := sf != nil && s.focused == id
	s.mu.Unlock()
	if refocus {
		s.focus(0) // the root, as broadwayd does
	}
}

func (s *server) grabPointer(id, t uint32) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.grab != 0 && t != 0 && s.grabTime > t {
		return alreadyGrabbed
	}
	if t == 0 {
		t = uint32(s.now())
	}
	s.grab, s.grabTime = id, t
	return grabSuccess
}

func (s *server) ungrabPointer(t, serial uint32) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.grab != 0 && t != 0 && s.grabTime > t {
		return 0
	}
	s.grab = 0
	return serial
}

// word is one 32-bit field of a reply or event.
type word [4]byte

func u32(v uint32) word {
	var w word
	ne.PutUint32(w[:], v)
	return w
}

func (s *server) reply(cl *client, inReplyTo, typ uint32, fields ...word) {
	b := make([]byte, replyHeader, replyHeader+4*len(fields))
	for _, f := range fields {
		b = append(b, f[:]...)
	}
	s.send(cl, b, inReplyTo, typ)
}

// event sends a BROADWAY_REPLY_EVENT: the reply header, padding to the
// 8-aligned event, the event's base (type, serial, time) and its fields,
// padded to the struct's 8-byte size.
func (s *server) event(cl *client, typ uint32, fields ...word) {
	n := eventOffset + eventBase + 4*len(fields)
	n = (n + 7) &^ 7
	b := make([]byte, n)
	ne.PutUint32(b[eventOffset:], typ)
	ne.PutUint32(b[eventOffset+4:], cl.serial.Load())
	ne.PutUint64(b[eventOffset+8:], s.now())
	for i, f := range fields {
		copy(b[eventOffset+eventBase+4*i:], f[:])
	}
	s.send(cl, b, 0, replyEvent)
}

func (s *server) send(cl *client, b []byte, inReplyTo, typ uint32) {
	ne.PutUint32(b, uint32(len(b)))
	ne.PutUint32(b[4:], inReplyTo)
	ne.PutUint32(b[8:], typ)
	cl.wmu.Lock()
	defer cl.wmu.Unlock()
	// A client that went away is dropped by its reader.
	_, _ = cl.conn.Write(b)
}
