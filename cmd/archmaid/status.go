package main

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/sysstat"
)

// ── system status ────────────────────────────────────────────────────────────

type statusMsg string

// statusNowMsg is a one-off refresh. Unlike statusMsg it never reschedules, so
// there is only ever the one tick loop that Init starts.
type statusNowMsg string

// refreshStatus recomputes the disk/ram/cache line every few seconds so the
// header stays live (the bit gum couldn't do without redrawing the world).
// Only Init may start this loop; anything else wants refreshStatusNow.
func refreshStatus() tea.Cmd {
	p := sysstat.DefaultPaths() // a value taken here, on the model goroutine
	return tickFn(4*time.Second, func(time.Time) tea.Msg {
		return statusMsg(sysstat.Read(p).Line())
	})
}

// refreshStatusNow updates the line once, immediately — e.g. after a run so
// the freed space shows without waiting for the next tick.
func refreshStatusNow() tea.Cmd {
	p := sysstat.DefaultPaths() // captured now: the Cmd runs off the model goroutine
	return func() tea.Msg { return statusNowMsg(sysstat.Read(p).Line()) }
}

// tickFn is tea.Tick, behind a seam so the transcript harness can drop the
// timers (status refresh, sudo keep-alive) instead of sleeping on them.
var tickFn = tea.Tick

// systemStatus renders the header line from the real system paths.
func systemStatus() string {
	return sysstat.Read(sysstat.DefaultPaths()).Line()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
