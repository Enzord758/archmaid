package proc

import (
	"io"
	"time"

	"github.com/Enzord758/archmaid/internal/vt"
)

// DefaultRedraw is how often, at most, a step's already delivered lines are
// sent again when the child redraws them (Options.Redraw's default).
const DefaultRedraw = 50 * time.Millisecond

// flushBacklog is how many lines may wait behind a held-back redraw before
// they all go at once.
const flushBacklog = 2 * vt.MaxRows

// readChunks reads r into chunks until it ends, then closes chunks: an
// EOF (a pipe), EIO (a pty whose every slave fd is closed), the read
// deadline WaitDelay sets on a pty, or any other error all end the output.
func readChunks(r io.Reader, chunks chan<- []byte) {
	defer close(chunks)
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunks <- append([]byte(nil), buf[:n]...)
		}
		if err != nil {
			return
		}
	}
}

// emitLines runs the child's raw output through a vt.Screen and sends its
// lines as Msgs, in order, until chunks closes. Complete new lines go at
// once; an unfinished line (no newline yet) after every; a redraw of lines
// already sent (Msg.Replace) at most once per every, carrying every line
// that changed, unless flushBacklog lines are waiting; and whatever is left
// when the output ends. So a progress bar costs at most one Msg run per
// every, however fast it repaints.
//
// send returns false once nobody reads any more (an abort): from then on
// the output is still read to its end, and discarded, so the child never
// blocks on a write.
func emitLines(chunks <-chan []byte, send func(Msg) bool, every time.Duration) {
	if every <= 0 {
		every = DefaultRedraw
	}
	scr := vt.New()
	sending := true
	var lastRedraw time.Time
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	armed := false
	arm := func(d time.Duration) {
		if !armed {
			timer.Reset(max(d, time.Millisecond))
			armed = true
		}
	}
	flush := func(complete bool, now time.Time) {
		replace, lines := scr.Flush(complete)
		if replace > 0 {
			lastRedraw = now
		}
		for i, l := range lines {
			if !sending {
				return
			}
			m := Msg{Line: l}
			if i == 0 {
				m.Replace = replace
			}
			sending = send(m)
		}
	}
	decide := func(now time.Time) {
		if scr.Rewrite() {
			if wait := lastRedraw.Add(every).Sub(now); wait <= 0 || scr.Backlog() >= flushBacklog {
				flush(false, now)
			} else {
				arm(wait)
			}
			return
		}
		if scr.Ready() > 0 {
			flush(true, now)
		}
		if scr.Partial() {
			arm(every)
		}
	}
	for {
		select {
		case b, ok := <-chunks:
			if !ok {
				timer.Stop()
				if sending {
					flush(false, time.Now())
				}
				return
			}
			if sending {
				scr.Write(b)
				decide(time.Now())
			}
		case now := <-timer.C:
			armed = false
			if !sending || len(chunks) > 0 {
				// more output is already waiting: it decides (and arms
				// again) once read, so a line cut at a read's end isn't
				// sent half while its rest is right there
				continue
			}
			if wait := lastRedraw.Add(every).Sub(now); scr.Rewrite() && wait > 0 {
				arm(wait)
				continue
			}
			flush(false, now)
		}
	}
}
