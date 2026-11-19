package tui

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/health"
	"github.com/Enzord758/archmaid/internal/held"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// The System health screen (#295): the eleven read-only checks of
// internal/health, as the GUI's Overview shows them. The look rows come
// first, then System, Packages and Hardware; a check that could not tell is
// "?", and an ignored one is greyed. The checks run off the UI loop (a run
// can take the 15 s deadline when the news, AUR and mirror caches are
// cold), with a spinner. For a look row the keys copy its command to the
// terminal's clipboard (OSC 52; the command is printed too), print its link
// (archmaid never starts a browser: it may run as root), and ignore it or
// undo that, through health.IgnoreStore, the only thing this screen writes.

// idHealth is the menu entry's ID. It is the TUI's own: not an engine
// action, and not in actions.Menu.
const idHealth = "health"

// healthStale is how old a report may be when the screen opens before it
// runs the checks again.
const healthStale = 5 * time.Minute

// Seams. The binary's are the real checks and the real ignore list; every
// test fakes them (TestMain), so no test reaches the machine's tools.
var (
	healthRun   = realHealthRun
	healthStore = health.DefaultIgnoreStore()
	// osc52Out is where the copy escape goes: the terminal.
	osc52Out io.Writer = os.Stdout
)

func realHealthRun(ctx context.Context) health.Report {
	d := health.DefaultDeps()
	d.IgnoreLoad = healthStore.Load
	return health.Run(ctx, d)
}

// menu is the main menu as the TUI shows it: the engine's actions with the
// TUI's own entries ahead of "Edit topgrade config".
func menu() []actions.Action {
	own := tuiEntries()
	var out []actions.Action
	for _, a := range actions.Menu {
		if a.ID == actions.IDEditConfig {
			out = append(out, own...)
		}
		out = append(out, a)
	}
	return out
}

// tuiEntries are the menu entries the TUI adds.
func tuiEntries() []actions.Action {
	return []actions.Action{{ID: idHealth, Icon: "󰗶", Label: "System health"}, {ID: idHeld, Icon: "󰔟", Label: "Held updates"}}
}

// healthState is the screen's state.
type healthState struct {
	rep    health.Report
	have   bool // rep is a finished run
	busy   bool // a run is in flight
	seq    int  // the run whose result is wanted
	cancel context.CancelFunc
	cur    int // the selected row
	note   string
	view   viewport.Model
}

type healthDoneMsg struct {
	seq int
	rep health.Report
}

type healthIgnoredMsg struct {
	undo bool
	n    int
	err  error
}

// startHealth runs the checks off the loop. A run already in flight is
// dropped: only the newest one's result is taken.
func (m model) startHealth() (model, tea.Cmd) {
	if m.health.cancel != nil {
		m.health.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.health.cancel = cancel
	m.health.busy = true
	m.health.seq++
	seq := m.health.seq
	return m, func() tea.Msg { return healthDoneMsg{seq: seq, rep: healthRun(ctx)} }
}

// openHealth is the menu entry: the screen, with a run unless a recent
// report is there.
func (m model) openHealth() (tea.Model, tea.Cmd) {
	m.screen = screenHealth
	m.health.note = ""
	var cmd tea.Cmd
	if !m.health.busy && (!m.health.have || time.Since(m.health.rep.At) > healthStale) {
		m, cmd = m.startHealth()
	}
	m.healthLayout()
	return m, cmd
}

// healthDone takes a run's result, unless a newer run or leaving the screen
// made it unwanted.
func (m model) healthDone(msg healthDoneMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.health.seq {
		return m, nil
	}
	if m.health.cancel != nil {
		m.health.cancel()
		m.health.cancel = nil
	}
	m.health.rep, m.health.have, m.health.busy = msg.rep, true, false
	if n := len(healthRows(msg.rep)); m.health.cur >= n {
		m.health.cur = max(n-1, 0)
	}
	m.healthLayout()
	return m, nil
}

// healthIgnored is the end of an ignore or undo: the checks run again, so
// the report is the list's.
func (m model) healthIgnored(msg healthIgnoredMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.health.note = "Couldn't change the ignore list: " + sanitize.Field(msg.err.Error(), 200)
		if errors.Is(msg.err, health.ErrIgnoreFull) {
			m.health.note = "The ignore list is full (" + fmt.Sprint(health.MaxIgnored) + " items): undo some first."
		}
		m.healthLayout()
		return m, nil
	}
	verb := "Ignored"
	if msg.undo {
		verb = "No longer ignoring"
	}
	m.health.note = fmt.Sprintf("%s %d item%s.", verb, msg.n, plural(msg.n))
	m, cmd := m.startHealth()
	m.healthLayout()
	return m, cmd
}

// ── rows ─────────────────────────────────────────────────────────────────────

// healthRow is one check as listed, under a section header that the first
// row of a section carries.
type healthRow struct {
	chk     health.Check
	section string // "" for a row that continues its section
}

const healthAttention = "Needs your attention"

var healthGroupNames = []struct{ id, name string }{
	{health.GroupSystem, "System"}, {health.GroupPackages, "Packages"}, {health.GroupHardware, "Hardware"},
}

// healthRows lists the report as the screen shows it: the look rows first,
// then a section per group, where a check that could not tell comes before
// one that is fine and an ignored one comes last.
func healthRows(rep health.Report) []healthRow {
	var out []healthRow
	add := func(section string, cs []health.Check) {
		for i, c := range cs {
			r := healthRow{chk: c}
			if i == 0 {
				r.section = section
			}
			out = append(out, r)
		}
	}
	var look []health.Check
	byGroup := map[string][]health.Check{}
	for _, state := range []health.State{health.Look, health.Unknown, health.OK, health.Ignored} {
		for _, c := range rep.Checks {
			if c.State != state {
				continue
			}
			if state == health.Look {
				look = append(look, c)
			} else {
				byGroup[c.Group] = append(byGroup[c.Group], c)
			}
		}
	}
	add(healthAttention, look)
	seen := map[string]bool{}
	for _, g := range healthGroupNames {
		seen[g.id] = true
		add(g.name, byGroup[g.id])
	}
	var other []health.Check
	for _, c := range rep.Checks {
		if c.State != health.Look && !seen[c.Group] {
			other = append(other, c)
		}
	}
	add("Other", other)
	return out
}

// healthSummary says how many checks need attention.
func healthSummary(rep health.Report) string {
	switch n := rep.Attention(); n {
	case 0:
		return "Nothing needs attention"
	case 1:
		return "1 thing needs attention"
	default:
		return fmt.Sprintf("%d things need attention", n)
	}
}

// ── keys ─────────────────────────────────────────────────────────────────────

func (m model) healthKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := healthRows(m.health.rep)
	var sel *health.Check
	if m.health.cur < len(rows) {
		sel = &rows[m.health.cur].chk
	}
	switch msg.String() {
	case "esc", "q":
		if m.health.cancel != nil { // a run nobody waits for any more
			m.health.cancel()
			m.health.cancel = nil
		}
		m.health.seq++
		m.health.busy = false
		m.screen = screenMenu
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		if m.health.cur > 0 {
			m.health.cur--
			m.health.note = ""
		}
	case "down", "j":
		if m.health.cur < len(rows)-1 {
			m.health.cur++
			m.health.note = ""
		}
	case "r":
		m.health.note = ""
		if !m.health.busy {
			var cmd tea.Cmd
			m, cmd = m.startHealth()
			m.healthLayout()
			return m, cmd
		}
	case "c":
		if sel != nil && sel.Copy != "" {
			cmd := sanitize.Field(sel.Copy, 600)
			m.health.note = "Command, copied to your terminal's clipboard if it takes OSC 52:\n" + cmd
			m.healthLayout()
			return m, copyOSC52(cmd)
		}
		m.health.note = "This check has no command to copy."
	case "o":
		if sel != nil && sel.Link != "" {
			m.health.note = "Open this link in your browser (archmaid doesn't start one):\n" + sanitize.Field(sel.Link, 300)
		} else {
			m.health.note = "This check has no link."
		}
	case "i":
		if sel != nil && sel.State == health.Look {
			return m.healthChange(sel.Items, false)
		}
		m.health.note = "Only a check that needs attention can be ignored."
	case "u":
		if sel != nil && len(sel.IgnoredItems) > 0 {
			return m.healthChange(sel.IgnoredItems, true)
		}
		m.health.note = "Nothing is ignored on this check."
	default:
		var cmd tea.Cmd
		m.health.view, cmd = m.health.view.Update(msg)
		return m, cmd
	}
	m.healthLayout()
	return m, nil
}

// healthChange ignores or un-ignores items, off the loop.
func (m model) healthChange(items []health.Item, undo bool) (tea.Model, tea.Cmd) {
	if dryRun {
		m.health.note = "Dry run: the ignore list is left as it is."
		m.healthLayout()
		return m, nil
	}
	store := healthStore
	keys := make([]string, 0, len(items))
	for _, it := range items {
		keys = append(keys, it.Key)
	}
	return m, func() tea.Msg {
		n := 0
		for _, k := range keys {
			var err error
			if undo {
				err = store.Unignore(k)
			} else {
				err = store.Ignore(k)
			}
			if err != nil {
				return healthIgnoredMsg{undo: undo, n: n, err: err}
			}
			n++
		}
		return healthIgnoredMsg{undo: undo, n: n}
	}
}

// copyOSC52 asks the terminal to put s on its clipboard.
func copyOSC52(s string) tea.Cmd {
	return func() tea.Msg {
		fmt.Fprintf(osc52Out, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(s)))
		return nil
	}
}

// ── view ─────────────────────────────────────────────────────────────────────

// healthWidth is the width the screen draws to.
func (m model) healthWidth() int {
	if m.termWidth > 0 {
		return max(m.termWidth-2, 30)
	}
	return 78
}

func (m model) healthStateStyle(c health.Check) (string, lipgloss.Style) {
	switch c.State {
	case health.OK:
		return "✔", stepOKStyle
	case health.Look:
		return "⚠", noteStyle
	case health.Ignored:
		return "–", stepSkipStyle
	}
	return "?", stepSkipStyle
}

// healthHint is the key line for the selected row.
func (m model) healthHint() string {
	h := "↑/↓ move"
	rows := healthRows(m.health.rep)
	if m.health.cur < len(rows) {
		c := rows[m.health.cur].chk
		if c.Copy != "" {
			h += " · c copy command"
		}
		if c.Link != "" {
			h += " · o link"
		}
		if c.State == health.Look {
			h += " · i ignore"
		}
		if len(c.IgnoredItems) > 0 {
			h += " · u undo ignore"
		}
	}
	return h + " · r re-run · esc back"
}

// healthBody is the rows as text, and the first and last line of the
// selected one.
func (m model) healthBody() (string, int, int) {
	rows := healthRows(m.health.rep)
	w := m.healthWidth()
	var lines []string
	first, last := 0, 0
	for i, r := range rows {
		if r.section != "" {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, titleStyle.Render(r.section))
		}
		glyph, style := m.healthStateStyle(r.chk)
		cursor := "  "
		title := itemStyle
		if r.chk.State == health.Ignored {
			title = stepSkipStyle
		}
		if i == m.health.cur {
			cursor = cursorStyle.Render("➤ ")
			title = selectedStyle
			first = len(lines)
		}
		name := sanitize.Field(r.chk.Title, 80)
		if r.chk.State == health.Ignored {
			name += " (ignored)"
		}
		lines = append(lines, cursor+style.Render(glyph)+" "+title.Render(name))
		detail := statusStyle
		if r.chk.State == health.Look {
			detail = itemStyle
		}
		if d := sanitize.Field(r.chk.Detail, health.DetailMax); d != "" {
			lines = append(lines, strings.Split(detail.Width(w).PaddingLeft(5).Render(d), "\n")...)
		}
		if len(r.chk.IgnoredItems) > 0 && r.chk.State == health.Look {
			var names []string
			for _, it := range r.chk.IgnoredItems {
				names = append(names, sanitize.Field(it.Label, 80))
			}
			lines = append(lines, strings.Split(stepSkipStyle.Width(w).PaddingLeft(5).Render("ignored: "+strings.Join(names, ", ")), "\n")...)
		}
		if i == m.health.cur {
			last = len(lines) - 1
		}
	}
	return strings.Join(lines, "\n"), first, last
}

// healthNoteView is the note under the list, wrapped.
func (m model) healthNoteView() string {
	if m.health.note == "" {
		return ""
	}
	return noteStyle.Width(m.healthWidth()).Render(m.health.note)
}

// healthLayout sizes the list to the terminal and what is under it, and
// scrolls it so the selected row shows. A no-op off the screen.
func (m *model) healthLayout() {
	if m.screen != screenHealth {
		return
	}
	body, first, last := m.healthBody()
	h := 24
	if m.termHeight > 0 {
		h = m.termHeight
	}
	// header (3 lines), the summary and its blank line, the note and its
	// blank line, the hint.
	chrome := 3 + 2 + 1
	if note := m.healthNoteView(); note != "" {
		chrome += lipgloss.Height(note) + 1
	}
	h = max(h-chrome, 3)
	v := m.health.view
	v.Width, v.Height = m.healthWidth(), h
	v.SetContent(body)
	if first < v.YOffset {
		v.SetYOffset(first)
	} else if last >= v.YOffset+h {
		v.SetYOffset(max(min(last-h+1, first), 0))
	}
	m.health.view = v
}

func (m model) viewHealth() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	switch {
	case !m.health.have && m.health.busy:
		b.WriteString(m.spinner.View() + " " + stepRunStyle.Render("Checking the system…"))
	case !m.health.have:
		b.WriteString(statusStyle.Render("No report yet · r to run the checks"))
	default:
		line := titleStyle.Render("System health") + "  " + healthSummary(m.health.rep)
		if !m.health.rep.At.IsZero() {
			line += statusStyle.Render(" · checked " + m.health.rep.At.Format("15:04"))
		}
		if m.health.busy {
			line += "  " + m.spinner.View() + " " + stepRunStyle.Render("checking…")
		}
		b.WriteString(line)
	}
	b.WriteString("\n\n" + m.health.view.View() + "\n")
	if note := m.healthNoteView(); note != "" {
		b.WriteString("\n" + note + "\n")
	}
	b.WriteString(hintStyle.Render(m.healthHint()))
	return b.String()
}

// ── the menu's lines ─────────────────────────────────────────────────────────

// healthAtStart says whether the TUI runs the checks and reads the held
// lists when it starts, for the menu's lines. A test that must not wait for
// them turns it off.
var healthAtStart = true

type heldLoadedMsg struct{ set held.Set }

// startupChecks is the run the menu's lines come from, off the loop: the
// checks (their result is taken as run 0, unless the screen has started a
// newer one meanwhile) and the held lists.
func (m model) startupChecks() tea.Cmd {
	if m.eng == nil || !healthAtStart {
		return nil
	}
	return tea.Batch(
		func() tea.Msg { return healthDoneMsg{seq: 0, rep: healthRun(context.Background())} },
		func() tea.Msg { return heldLoadedMsg{set: heldRead()} },
	)
}

// menuLabel is an entry's label as the menu shows it: System health says
// what needs attention, Held updates how many are held, when there are any.
func (m model) menuLabel(a actions.Action) string {
	switch a.ID {
	case idHealth:
		if !m.health.have {
			break
		}
		switch n := m.health.rep.Attention(); n {
		case 0:
		case 1:
			return a.Label + " · 1 thing needs attention"
		default:
			return fmt.Sprintf("%s · %d things need attention", a.Label, n)
		}
	case idHeld:
		if n := heldCount(m.held.set, heldNow()); n > 0 {
			return fmt.Sprintf("%s · %d held", a.Label, n)
		}
	}
	return a.Label
}
