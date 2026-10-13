package ui

import (
	"sync/atomic"

	"github.com/diamondburned/gotk4/pkg/pango"
)

// outputCols is the Run view's width in monospace columns: the width of
// the pty every step's child writes to (realdriver.Config.TermCols, read
// as each child starts), so a tool's full-width rules and bars fit the
// page. The main thread writes it whenever the view's width changes; 0
// until the view has been laid out (the child then gets 80).
var outputCols atomic.Int32

// TermCols is realdriver.Config.TermCols: the Run view's width in columns.
func TermCols() int { return int(outputCols.Load()) }

// lineInset is an output line's horizontal padding (.am-line in
// style.css: 0 14px 0 48px), which the text never uses.
const lineInset = 48 + 14

// minCols is the narrowest pty a child gets, however narrow the window.
const minCols = 20

// measureCols reads the Run view's width in columns of its monospace font:
// its width less the lines' padding, over the font's digit width, less one
// column, so a line exactly as wide as the pty never wraps. Main thread.
func (w *Window) measureCols() {
	if w.closed || w.tl == nil {
		return
	}
	width := w.tl.view.Width()
	cw := w.tl.view.PangoContext().Metrics(nil, nil).ApproximateDigitWidth()
	if width <= lineInset || cw <= 0 {
		return
	}
	cols := (width-lineInset)*pango.SCALE/cw - 1
	outputCols.Store(int32(max(cols, minCols)))
}
