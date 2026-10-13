package proc

// Msg is one message on a child's output channel (see Runner.Start).
type Msg struct {
	Line string // a line of combined stdout+stderr (ignored when Done)
	// Replace is how many of the lines sent before this one it and the
	// lines after it replace: the child redrew them (a progress bar's
	// carriage return, a cursor moved up). The receiver drops the last
	// Replace lines it got, then takes Line. Lines never disappear: at
	// least Replace lines follow, this one included. 0 appends.
	Replace int
	Done    bool  // true for the final message
	Err     error // non-nil if the command failed (only meaningful when Done)
}

// BatchMax is the batch cap the TUI pumps with, and the size of a step's
// channel: even a flood reaches the screen in steady steps.
const BatchMax = 256

// Batch is what Next takes off a channel: lines in order, ending with the
// done message if that was among them. Nothing follows a done on the
// channel, so a batch never spans two children.
type Batch struct {
	// Replace is how many lines of the batches before this one Lines
	// replace (Msg.Replace, merged); 0 appends.
	Replace int
	Lines   []string // raw output lines, in order (may be empty when Done)
	Done    bool     // the child's done message ended the batch
	Err     error    // the child's exit error (only meaningful when Done)
}

// Next blocks for the next message on ch, then takes whatever else is already
// buffered behind it — up to max lines, stopping at the done — and returns it
// all as one Batch. Delivering a batch instead of one message per line is
// what keeps a fast producer (topgrade, pacman -Qk), which emits thousands of
// lines, from costing the UI a re-render per line. Call it again after every
// batch without Done to keep pumping. A closed quit (abort) releases a Next
// that is still waiting for the first message, with ok false; one that finds
// a line ready as quit closes may still return it, so the caller must be able
// to tell a late batch apart (the TUI stamps each with its launch).
func Next(ch <-chan Msg, quit <-chan struct{}, max int) (b Batch, ok bool) {
	var msg Msg
	select {
	case msg = <-ch:
	case <-quit:
		return Batch{}, false
	}
	for {
		if msg.Done {
			b.Done, b.Err = true, msg.Err
			return b, true
		}
		if r := msg.Replace; r > 0 {
			// a redraw of lines in this batch drops them here; one of
			// lines before it becomes the batch's own Replace
			if r <= len(b.Lines) {
				clear(b.Lines[len(b.Lines)-r:])
				b.Lines = b.Lines[:len(b.Lines)-r]
			} else {
				b.Replace += r - len(b.Lines)
				b.Lines = b.Lines[:0]
			}
		}
		b.Lines = append(b.Lines, msg.Line)
		if len(b.Lines) >= max {
			return b, true
		}
		select {
		case msg = <-ch:
		default: // nothing more buffered: deliver what we have
			return b, true
		}
	}
}
