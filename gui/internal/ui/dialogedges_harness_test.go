package ui

// The decision dialogs' response contract at its edges (task #96, QA of
// the wide dialogs, task #95): every prompt gets exactly one answer, or
// none when the engine withdrew it, whatever the user does and in
// whatever order. TestGUICovDialogClose holds the plain paths (Escape, a
// button, the window's close); this holds what lies between them: Escape
// twice, a click while the dialog is already closing (closed comes at
// the start of libadwaita's close animation), a prompt
// withdrawn while open, while closing or before it was ever drawn, the
// password AlertDialog's own order (closed, then the response; the entry
// read before it is wiped), the window's close mid-run and idle, and the
// quit question with a confirm over it.
//
// What is checked is both what the Driver got (Window.answer drops a
// second answer for an ID it has forgotten) and, for the wide dialogs,
// what the dialog itself responded: amDialog.respond's once-guard is the
// contract there, and the window's map would hide its loss.

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/internal/engine"
)

func init() { covScenarios["cov-dialog-edges"] = covDialogEdges }

// TestGUICovDialogEdges: exactly one answer per prompt at the response
// contract's edges; none for a withdrawn prompt.
func TestGUICovDialogEdges(t *testing.T) {
	rep := covChild(t, "cov-dialog-edges")
	if rep != nil {
		for _, l := range rep.Narrow {
			t.Log(l)
		}
	}
}

// underPointer says whether a pointer at b's centre reaches b (or its
// label): whether a real click there would be b's. Once a dialog is
// closing it is not (libadwaita takes the closing sheet off the pick), so
// the clicks below during a close are what only code could do; the
// once-guard holds them anyway.
func underPointer(w *Window, b *gtk.Button) bool {
	bb, ok := boundsIn(b, w.win)
	if !ok {
		return false
	}
	want := coreglib.InternObject(b).Native()
	for p := w.win.Pick(bb.x+bb.w/2, bb.y+bb.h/2, gtk.PickDefault); p != nil; p = gtk.BaseWidget(p).Parent() {
		if coreglib.InternObject(p).Native() == want {
			return true
		}
	}
	return false
}

func covDialogEdges(c *covEnv) {
	w := c.w
	c.quiet()
	log := func(f string, a ...any) { c.rep.Narrow = append(c.rep.Narrow, fmt.Sprintf(f, a...)) }
	sentFor := func(id engine.PromptID) []string {
		var out []string
		for _, s := range c.sent() {
			if f := strings.Fields(s); len(f) > 1 && f[1] == strconv.Itoa(int(id)) {
				out = append(out, s)
			}
		}
		return out
	}
	// responses: what each dialog itself responded, in order, and its
	// closed signal, by prompt.
	responses := map[engine.PromptID]*[]string{}
	open := func(id engine.PromptID) *amDialog {
		d := w.dialogs[id]
		if d == nil {
			c.fail("prompt %d has no dialog", id)
			return nil
		}
		got := &[]string{}
		responses[id] = got
		d.ConnectResponse(func(r string) { *got = append(*got, r) })
		d.ConnectClosed(func() { *got = append(*got, "(closed)") })
		return d
	}
	// once: the Driver got exactly want for id (nothing when want is
	// ""), the window forgot the dialog, and no dialog is left on screen.
	once := func(id engine.PromptID, want string) {
		got := sentFor(id)
		if want == "" {
			c.ok(len(got) == 0, "prompt %d (withdrawn): sent %q, want nothing", id, got)
		} else {
			c.ok(slices.Equal(got, []string{want}), "prompt %d: sent %q, want exactly %q", id, got, want)
		}
		c.ok(w.dialogs[id] == nil, "prompt %d: its dialog is still held", id)
	}
	// responded: a wide dialog responded exactly want (its own signals,
	// the closed one dropped).
	responded := func(id engine.PromptID, want ...string) {
		var got []string
		if r := responses[id]; r != nil {
			for _, s := range *r {
				if s != "(closed)" {
					got = append(got, s)
				}
			}
		}
		c.ok(slices.Equal(got, want), "prompt %d's dialog responded %q, want %q", id, got, want)
	}
	gone := func(what string) {
		if d := w.win.VisibleDialog(); d != nil {
			c.fail("%s: a dialog is still on screen: %q", what, d.Title())
		}
	}
	confirm := func(id engine.PromptID, title string) func() {
		return func() { c.feed(engine.NeedsConfirm{ID: id, Title: title, YesLabel: "Go"}) }
	}
	password := func(id engine.PromptID) func() {
		return func() { c.feed(engine.NeedsPassword{ID: id, Kind: engine.PasswordUpFront, Gate: "Proceed?"}) }
	}
	var entry interface{ TextLength() uint }
	c.seq(150,
		// Escape twice in the same main-loop turn: one close response.
		confirm(501, "Escape twice?"),
		func() {
			if d := open(501); d != nil {
				d.Close()
				d.Close()
			}
		},
		func() { once(501, "confirm 501 false"); responded(501, "no"); gone("501") },

		// Escape twice, the second a turn later (a key repeat).
		confirm(502, "Escape, then Escape again?"),
		func() {
			if d := open(502); d != nil {
				d.Close()
				coreglib.IdleAdd(func() { d.Close() })
			}
		},
		func() { once(502, "confirm 502 false"); responded(502, "no"); gone("502") },

		// Escape, then a click on Go while the dialog is closing (its
		// closed signal came at once, the close animation still runs):
		// the close response was the answer, the click does nothing.
		confirm(503, "Escape, then a click?"),
		func() {
			d := open(503)
			if d == nil {
				return
			}
			b := findButton(&d.Widget, "Go")
			c.ok(underPointer(w, b), "503: Go is not under the pointer while open (the probe's control)")
			d.Close()
			log("503: after Escape, Go under the pointer: %v", underPointer(w, b))
			b.Emit("clicked")
			coreglib.TimeoutAdd(30, func() bool {
				log("503: 30 ms after Escape, Go under the pointer: %v", underPointer(w, b))
				b.Emit("clicked")
				return false
			})
		},
		func() { once(503, "confirm 503 false"); responded(503, "no"); gone("503") },

		// A click on Go, then Escape while it closes.
		confirm(504, "A click, then Escape?"),
		func() {
			if d := open(504); d != nil {
				c.press(d, "yes")
				d.Close()
			}
		},
		func() { once(504, "confirm 504 true"); responded(504, "yes"); gone("504") },

		// Withdrawn by the engine while open (the step exited, an
		// Abort): a confirm, a pick list and a report. The dialog gives
		// its close response; the window sends none of it.
		confirm(505, "Withdrawn while open?"),
		func() {
			open(505)
			c.feed(engine.PromptClosed{ID: 505})
		},
		func() {
			once(505, "")
			responded(505, "no")
			gone("505")
			c.feed(engine.NeedsSelection{ID: 506, Title: "Pick", Items: []string{"a.service", "b.service"}})
		},
		func() {
			open(506)
			c.feed(engine.PromptClosed{ID: 506})
		},
		func() {
			once(506, "")
			gone("506")
			c.feed(engine.OutputReport{ID: 507, Text: "x", Audit: &engine.AuditReport{Total: 1, CanAccept: true,
				Findings: []engine.AuditFinding{{Name: "valent", High: true, Flags: []string{"new maintainer"}}}}})
		},
		func() {
			open(507)
			c.feed(engine.PromptClosed{ID: 507})
		},
		func() { once(507, ""); gone("507") },

		// Escape, then the engine withdraws it (crossing in flight): the
		// one answer went before the withdrawal; the engine ignores it as
		// stale (engine.Session: an ID that is not the open prompt's).
		confirm(508, "Escape, then withdrawn?"),
		func() {
			if d := open(508); d != nil {
				d.Close()
				c.feed(engine.PromptClosed{ID: 508})
			}
		},
		func() { once(508, "confirm 508 false"); responded(508, "no"); gone("508") },

		// Opened and withdrawn in the same turn, never drawn.
		func() {
			c.feed(engine.NeedsConfirm{ID: 509, Title: "Never seen?"})
			c.feed(engine.PromptClosed{ID: 509})
		},
		func() { once(509, ""); gone("509") },

		// The password AlertDialog, unchanged: a click on Authenticate
		// closes it first and responds after (libadwaita's order), the
		// typed password is read at the response and the entry wiped.
		password(510),
		func() {
			d := open(510)
			if d == nil || w.pwEntry == nil {
				c.fail("510: no password entry")
				return
			}
			entry = w.pwEntry
			w.pwEntry.SetText("secret")
			c.press(d, "ok")
		},
		func() {
			once(510, "password 510 len 6")
			got := *responses[510]
			c.ok(slices.Equal(got, []string{"(closed)", "ok"}), "510's signals %q, want closed then the ok response (AdwAlertDialog's order, which the entry wipe relies on)", got)
			c.ok(entry.TextLength() == 0 && w.pwEntry == nil, "510: the entry holds %d characters, kept %v", entry.TextLength(), w.pwEntry != nil)
			gone("510")
		},

		// Escape twice, then a click on Authenticate during the close:
		// the cancel is the one answer; nothing typed is sent.
		password(511),
		func() {
			d := open(511)
			if d == nil || w.pwEntry == nil {
				c.fail("511: no password entry")
				return
			}
			entry = w.pwEntry
			w.pwEntry.SetText("secret")
			b := findButton(&d.Widget, "Authenticate")
			d.Close()
			d.Close()
			log("511: after Escape, Authenticate under the pointer: %v", underPointer(w, b))
			b.Emit("clicked")
		},
		func() {
			once(511, "cancel 511")
			log("511: the AdwAlertDialog's own signals %q (a second response is libadwaita's; Window.answer drops it)", *responses[511])
			c.ok(entry.TextLength() == 0 && w.pwEntry == nil, "511: the entry holds %d characters, kept %v", entry.TextLength(), w.pwEntry != nil)
			gone("511")
		},

		// Enter in the entry: one password, though the ForceClose after
		// it gives the close response too.
		password(512),
		func() {
			if open(512) == nil || w.pwEntry == nil {
				c.fail("512: no password entry")
				return
			}
			entry = w.pwEntry
			w.pwEntry.SetText("secret")
			w.pwEntry.Emit("entry-activated")
		},
		func() {
			once(512, "password 512 len 6")
			log("512: the AdwAlertDialog's own signals after Enter %q", *responses[512])
			c.ok(entry.TextLength() == 0 && w.pwEntry == nil, "512: the entry holds %d characters, kept %v", entry.TextLength(), w.pwEntry != nil)
			gone("512")
		},

		// Withdrawn with a password typed: nothing sent, the entry wiped
		// (from the idle after closed).
		password(513),
		func() {
			if open(513) == nil || w.pwEntry == nil {
				c.fail("513: no password entry")
				return
			}
			entry = w.pwEntry
			w.pwEntry.SetText("secret")
			c.feed(engine.PromptClosed{ID: 513})
		},
		func() {
			once(513, "")
			c.ok(entry.TextLength() == 0 && w.pwEntry == nil, "513 (withdrawn): the entry holds %d characters, kept %v", entry.TextLength(), w.pwEntry != nil)
			gone("513")
		},

		// The window's close with a wide confirm open, idle: libadwaita
		// closes the dialog (its close response) and the window stays.
		confirm(514, "The window closes, idle?"),
		func() {
			open(514)
			w.busy = false
			w.win.Close()
		},
		func() {
			once(514, "confirm 514 false")
			responded(514, "no")
			c.ok(!w.closed && w.quitDialog == nil, "514: closed %v, quit question %v; want the dialog closed, the window up", w.closed, w.quitDialog != nil)
			gone("514")
		},

		// The same mid-run: the dialog closes, no question yet; the next
		// close asks; Keep Running keeps the window.
		confirm(515, "The window closes, mid-run?"),
		func() {
			open(515)
			w.busy = true // as mid-run
			w.win.Close()
		},
		func() {
			once(515, "confirm 515 false")
			responded(515, "no")
			c.ok(!w.closed && w.quitDialog == nil, "515: closed %v, quit question %v; want the dialog closed, no question yet", w.closed, w.quitDialog != nil)
			w.win.Close()
		},
		func() {
			c.ok(w.quitDialog != nil, "515: a close mid-run with no dialog open asked nothing")
			if w.quitDialog != nil {
				c.press(w.quitDialog, "cancel")
			}
		},
		func() {
			c.ok(!w.closed && w.quitDialog == nil, "Keep Running: closed %v, question %v", w.closed, w.quitDialog != nil)
		},

		// The quit question is up when a confirm arrives (the engine went
		// on to the next step's confirm): the confirm opens over it.
		// Escape answers the confirm, not the question; the question
		// still stands.
		func() { w.win.Close() },
		func() {
			c.ok(w.quitDialog != nil, "516: no quit question")
			confirm(516, "Over the quit question?")()
		},
		func() {
			d := open(516)
			if v := w.win.VisibleDialog(); d == nil || v == nil || v.Title() != d.Title() {
				c.fail("516: the confirm is not the dialog on top")
			}
			if d != nil {
				d.Close()
			}
		},
		func() {
			once(516, "confirm 516 false")
			responded(516, "no")
			c.ok(!w.closed && w.quitDialog != nil, "516: closed %v, question %v; want the question still up", w.closed, w.quitDialog != nil)
			confirm(517, "Under Stop and Quit?")()
		},
		// Stop and Quit on the question under a confirm: the window closes,
		// the confirm is withdrawn with it (the Driver closes), nothing is
		// sent for it.
		func() {
			open(517)
			if w.quitDialog != nil {
				c.press(w.quitDialog, "quit")
			}
		},
		func() {
			c.ok(w.closed, "517: Stop and Quit didn't close the window")
			once(517, "")
			responded(517, "no")
		},
	)
}
