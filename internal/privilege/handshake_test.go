package privilege

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// The handshake's failure paths (askpass.go, deliver and ReadAnswer): the
// password goes out only after the relay said it is hardened, and the relay
// takes one only after the server said the request is verified. Every way
// the other side can go wrong leaves the password unwritten.

// writeFailConn is a connection whose writes all fail (the relay is gone)
// and whose reads answer the handshake as a relay would, so a deliver
// that went on past a failed write would get as far as the password. It
// records every write attempted.
type writeFailConn struct {
	net.Conn // nil: only the methods below are used
	mu       sync.Mutex
	writes   []string
	closed   bool
	acks     *strings.Reader
}

func (c *writeFailConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes = append(c.writes, string(p))
	return 0, io.ErrClosedPipe
}
func (c *writeFailConn) Read(p []byte) (int, error)      { return c.acks.Read(p) }
func (c *writeFailConn) SetReadDeadline(time.Time) error { return nil }
func (c *writeFailConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

// TestDeliverRelayGone: when the verified line can't be written (the
// relay went away), deliver stops there: the password is never written,
// even to a connection that would then acknowledge, and it hangs up.
func TestDeliverRelayGone(t *testing.T) {
	dir := shimAuth(t, 0, 0, "")
	c := &writeFailConn{acks: strings.NewReader(ackLine)}
	req := newRequest(c, "[sudo] password for u: ", helperPeer())
	if _, err := verifyAgainst(t, req, fakeTree()); err != nil {
		t.Fatal(err)
	}
	// The answer still counts: that sudo's own step reports its failure.
	if detail, err := req.Answer("secret"); err != nil || detail != "" {
		t.Fatalf("Answer = %q, %v", detail, err)
	}
	requireSuOnly(t, dir, "secret")
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.writes) != 1 || c.writes[0] != verifiedLine {
		t.Fatalf("writes %q, want only the verified line attempted", c.writes)
	}
	if !c.closed {
		t.Fatal("the connection was not hung up")
	}
}

// TestDeliverWrongAck: a relay that answers the verified line with
// anything but the acknowledgement (here: not hardened) gets no password
// and is hung up on.
func TestDeliverWrongAck(t *testing.T) {
	for name, reply := range map[string]string{"wrong line": "no\n", "cut short": "o"} {
		t.Run(name, func(t *testing.T) {
			shimAuth(t, 0, 0, "")
			req, helper := pipeRequest(t)
			got := make(chan string, 1)
			go func() {
				line := make([]byte, len(verifiedLine))
				if _, err := io.ReadFull(helper, line); err != nil || string(line) != verifiedLine {
					got <- "handshake: " + string(line)
					return
				}
				if _, err := io.WriteString(helper, reply); err != nil {
					got <- "write: " + err.Error()
					return
				}
				if name == "cut short" {
					helper.Close() // EOF in the middle of the ack
					got <- ""
					return
				}
				rest, _ := io.ReadAll(helper) // until the server hangs up
				got <- string(rest)
			}()
			if _, err := req.Answer("secret"); err != nil {
				t.Fatalf("Answer: %v", err)
			}
			if rest := recv(t, got, "the relay's side"); rest != "" {
				t.Fatalf("after a %s the relay got %q, want nothing", name, rest)
			}
		})
	}
}

// relayConn is the relay's end in ReadAnswer's tests: what the server sent,
// and what the relay wrote back.
type relayConn struct {
	in  io.Reader
	out bytes.Buffer
}

func (c *relayConn) Read(p []byte) (int, error)  { return c.in.Read(p) }
func (c *relayConn) Write(p []byte) (int, error) { return c.out.Write(p) }

// TestReadAnswerRefusesWithoutHandshake: the relay hardens itself and
// acknowledges only after the server's verified line. A server that hangs
// up first, or opens with anything else (an old UI, or not the UI at all),
// gets no acknowledgement and hands over no password; harden never runs.
func TestReadAnswerRefusesWithoutHandshake(t *testing.T) {
	for name, sent := range map[string]string{
		"hung up":        "",
		"cut short":      "verif",
		"no handshake":   "secret\n",
		"other line":     "verified \n",
		"password first": "secret\nverified\nok\n",
	} {
		t.Run(name, func(t *testing.T) {
			c := &relayConn{in: strings.NewReader(sent)}
			hardened := false
			pw, err := ReadAnswer(c, func() error { hardened = true; return nil })
			if err == nil || pw != "" {
				t.Fatalf("ReadAnswer = %q, %v; want an error and no password", pw, err)
			}
			if name != "hung up" && name != "cut short" && !errors.Is(err, errHandshake) {
				t.Errorf("err = %v, want errHandshake", err)
			}
			if hardened || c.out.Len() != 0 {
				t.Fatalf("hardened %v, wrote %q: want neither before the handshake", hardened, c.out.String())
			}
		})
	}
}

// TestReadAnswerHardenFails: a relay that can't make itself non-dumpable
// doesn't acknowledge, so the server never sends it the password; it reads
// nothing past the verified line.
func TestReadAnswerHardenFails(t *testing.T) {
	c := &relayConn{in: strings.NewReader(verifiedLine + "secret\n")}
	boom := errors.New("prctl failed")
	pw, err := ReadAnswer(c, func() error { return boom })
	if !errors.Is(err, boom) || pw != "" {
		t.Fatalf("ReadAnswer = %q, %v; want the harden error and no password", pw, err)
	}
	if c.out.Len() != 0 {
		t.Fatalf("the relay acknowledged (%q) without being hardened", c.out.String())
	}
}

// ackFailConn is a relay's end whose writes fail: the server is gone.
type ackFailConn struct{ in io.Reader }

func (c ackFailConn) Read(p []byte) (int, error)  { return c.in.Read(p) }
func (c ackFailConn) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }

// TestReadAnswerAckFails: a relay whose acknowledgement can't be sent
// takes no password, even one that is already waiting to be read.
func TestReadAnswerAckFails(t *testing.T) {
	pw, err := ReadAnswer(ackFailConn{strings.NewReader(verifiedLine + "secret\n")}, nil)
	if !errors.Is(err, io.ErrClosedPipe) || pw != "" {
		t.Fatalf("ReadAnswer = %q, %v; want the write error and no password", pw, err)
	}
}
