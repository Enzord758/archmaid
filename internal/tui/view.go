package tui

import (
	"fmt"
	"strings"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/steps"
	"github.com/charmbracelet/lipgloss"
)

// ── view ─────────────────────────────────────────────────────────────────────

// View renders the active screen. Popup screens (password, confirm, abort,
// notice) are composited as a floating box over their own background,
// recursively — a popup can itself be showing over another popup (e.g. an
// askpass prompt interrupting the abort-confirm dialog over a running step).
func (m model) View() string {
	return m.renderScreen(m.screen)
}

// renderScreen renders s as it appears standalone: a popup composited over
// its own background (rendered recursively, since that background may
// itself be a popup), or viewOf(s) directly for a non-popup screen.
func (m model) renderScreen(s screen) string {
	bg, view, ok := m.popupLayer(s)
	if !ok {
		return m.viewOf(s)
	}
	return overlay(m.renderScreen(bg), view)
}

// popupLayer is the single place that knows which screens are popups: the
// screen each composites over, and its rendered content. Keeping this list
// in one spot (rather than duplicated across View, background bookkeeping,
// and layout sizing) means a screen added here is automatically handled
// everywhere a popup needs to be recognised.
//
// screenAbortConfirm's background is always screenRunning by construction —
// the only place that opens it sets that — so it's hardcoded here rather
// than read from m.bg. The other three vary, so they use the shared m.bg
// field; see openAskpass in adapter.go for why a screen already
// showing as one of those three must not overwrite m.bg with itself (m.bg
// is a single field, not a per-layer stack — self-assignment there would
// recurse forever).
func (m model) popupLayer(s screen) (bg screen, view string, ok bool) {
	switch s {
	case screenPassword:
		return m.bg, m.viewPassword(), true
	case screenConfirm:
		return m.bg, m.viewConfirm(), true
	case screenAbortConfirm:
		return screenRunning, m.viewAbortConfirm(), true
	case screenNotice:
		return m.bg, m.viewNotice(), true
	}
	return 0, "", false
}

// baseScreen resolves s down through any popup layers to the underlying
// non-popup screen it's ultimately shown over — e.g. screenPassword over
// screenAbortConfirm over screenRunning resolves to screenRunning.
func (m model) baseScreen(s screen) screen {
	if bg, _, ok := m.popupLayer(s); ok {
		return m.baseScreen(bg)
	}
	return s
}

// viewOf renders a full (non-popup) screen.
func (m model) viewOf(s screen) string {
	switch s {
	case screenMenu:
		return m.viewMenu()
	case screenRunning:
		return m.viewRunning()
	case screenSummary:
		return m.viewSummary()
	case screenLogPicker:
		return m.viewLogPicker()
	case screenLog:
		return m.viewLog()
	case screenAudit:
		return m.viewAudit()
	case screenDiscovering:
		return m.viewDiscovering()
	case screenServices:
		return m.viewServices()
	}
	return m.viewMenu() // safe fallback; unreachable via renderScreen/baseScreen
}

func (m model) header() string {
	line := titleStyle.Render("archmaid — Arch maintenance")
	if dryRun {
		line += "  " + stepFailStyle.Render("[dry-run]")
	}
	return line + "\n" + statusStyle.Render(m.status) + "\n"
}

func (m model) viewMenu() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	for i, it := range actions.Menu {
		cursor := "  "
		style := itemStyle
		if i == m.cursor {
			cursor = cursorStyle.Render("➤ ")
			style = selectedStyle
		}
		b.WriteString(cursor + style.Render(it.Icon+"  "+it.Label) + "\n")
	}
	b.WriteString("\n" + hintStyle.Render("↑/↓ move · enter select · q quit"))
	return b.String()
}

func (m model) viewPassword() string {
	head := stepRunStyle.Render("🔒 sudo access required")
	if m.eng.askpassOpen() {
		head += "\n\n" + m.viewAskpassFrom()
	}
	if m.authInFlight {
		body := head + "\n\n" +
			m.spinner.View() + " " + stepRunStyle.Render("authenticating…")
		return popupBox.Render(body)
	}
	body := head + "\n\n" + m.password.View()
	if m.pwErr != "" {
		body += "\n\n" + stepFailStyle.Render(m.pwErr)
	}
	body += "\n\n" + hintStyle.Render("enter submit · esc cancel")
	return popupBox.Render(body)
}

// viewAskpassFrom says which request a mid-run password prompt answers: the
// running step, the program that ran sudo (its real binary, from /proc, as
// the Driver names it) and sudo's own prompt — quoted, since whoever runs
// sudo chooses it (sudo -p).
func (m model) viewAskpassFrom() string {
	f := m.askpassFrom
	lines := []string{
		statusStyle.Render("step    ") + f.step,
		statusStyle.Render("caller  ") + f.caller,
	}
	if f.prompt != "" {
		lines = append(lines, statusStyle.Render("prompt  ")+fmt.Sprintf("%q", f.prompt))
	}
	return strings.Join(lines, "\n")
}

// yesNoButtons renders the two-button row for a confirm popup.
func yesNoButtons(yes bool) string {
	y, n := "  Yes  ", "  No  "
	if yes {
		y = selectedStyle.Render("▸ Yes ")
	} else {
		n = selectedStyle.Render("▸ No ")
	}
	return y + "   " + n
}

func (m model) viewConfirm() string {
	body := stepFailStyle.Render("⚠ "+m.confirmMsg) + "\n\n" +
		yesNoButtons(m.confirmYes) + "\n\n" +
		hintStyle.Render("y/n or ←/→ then enter")
	return popupBox.Render(body)
}

func (m model) viewRunning() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	b.WriteString(m.checklist() + "\n")
	b.WriteString(outputPaneStyle.Width(m.output.Width).Render(m.output.View()) + "\n")
	b.WriteString(hintStyle.Render("↑/↓ scroll output · ctrl+c abort"))
	return b.String()
}

func (m model) viewAbortConfirm() string {
	title := ""
	if m.current >= 0 && m.current < len(m.queue) {
		title = m.queue[m.current].step.Title
	}
	body := stepFailStyle.Render("⚠ Abort the running step?") + "\n" +
		statusStyle.Render(title) + "\n\n" +
		yesNoButtons(m.confirmYes) + "\n\n" +
		hintStyle.Render("y abort · n resume · ←/→ then enter")
	return popupBox.Render(body)
}

func (m model) checklist() string {
	var b strings.Builder
	for _, st := range m.queue {
		var glyph, text string
		switch st.status {
		case steps.Pending:
			glyph, text = stepPendingStyle.Render("○"), stepPendingStyle.Render(st.step.Title)
		case steps.Running:
			glyph, text = m.spinner.View(), stepRunStyle.Render(st.step.Title)
		case steps.OK:
			glyph, text = stepOKStyle.Render("✔"), itemStyle.Render(st.step.LogLabel)
		case steps.Failed:
			glyph, text = stepFailStyle.Render("✖"), stepFailStyle.Render(st.step.Title+" failed")
		case steps.Skipped, steps.AuthRefused, steps.Aborted:
			glyph, text = stepSkipStyle.Render("–"), stepSkipStyle.Render(st.step.Title+" (skipped)")
		}
		b.WriteString(glyph + " " + text + "\n")
	}
	return b.String()
}

func (m model) viewSummary() string {
	var line string
	switch {
	case m.aborted:
		line = stepFailStyle.Render("⚠ Aborted — remaining steps skipped")
	case m.errs > 0:
		msg := fmt.Sprintf("⚠ Completed with %d issue(s)", m.errs)
		if m.skipped > 0 {
			msg += fmt.Sprintf(", %d skipped", m.skipped)
		}
		line = stepFailStyle.Render(msg)
	case m.skipped > 0:
		line = stepSkipStyle.Render(fmt.Sprintf("Completed — %d step(s) skipped", m.skipped))
	default:
		line = stepOKStyle.Render("✔ Cleanup completed successfully")
	}
	return m.header() + "\n" + m.checklist() + "\n" + line + "\n\n" +
		hintStyle.Render("l view log · enter back to menu")
}

func (m model) viewLogPicker() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	b.WriteString(stepRunStyle.Render("View log — choose a category:") + "\n\n")
	for i, c := range logCategoryLabels {
		cursor := "  "
		style := itemStyle
		if i == m.logCursor {
			cursor = cursorStyle.Render("➤ ")
			style = selectedStyle
		}
		b.WriteString(cursor + style.Render(c.Label) + "\n")
	}
	b.WriteString("\n" + hintStyle.Render("↑/↓ move · enter open · esc back"))
	return b.String()
}

func (m model) viewLog() string {
	return m.header() + "\n" +
		statusStyle.Render("Log: "+categoryLabel(m.logCat)) + "\n" +
		outputPaneStyle.Width(m.output.Width).Render(m.output.View()) + "\n" +
		hintStyle.Render("↑/↓ scroll · q back")
}

// viewAudit has the same chrome as viewLog (header + title line + boxed pager
// + hint), so resizeOutput's fixed row budget for the log viewer applies.
func (m model) viewAudit() string {
	hint := "↑/↓ scroll · q back"
	if m.eng.canAccept() {
		hint = "↑/↓ scroll · a accept maintainer changes · q back"
	}
	return m.header() + "\n" +
		statusStyle.Render("AUR audit") + "\n" +
		outputPaneStyle.Width(m.output.Width).Render(m.output.View()) + "\n" +
		hintStyle.Render(hint)
}

func (m model) viewNotice() string {
	hint := "any key to return"
	if m.eng != nil && m.eng.stopped {
		hint = "any key to quit; the details follow on the terminal"
	}
	text := itemStyle.Render(m.notice)
	if w := m.noticeWidth(); lipgloss.Width(m.notice) > w {
		text = itemStyle.Width(w).Render(m.notice) // a discovery's summary is long: wrap it
	}
	body := text + "\n\n"
	if m.noticeLog != "" {
		body += hintStyle.Render("Details: View log → "+m.noticeLog) + "\n"
	}
	return popupBox.Render(body + hintStyle.Render(hint))
}

// noticeWidth is how wide a notice's text may be: 64 columns, less on a
// narrow terminal (the popup's border and padding take 8).
func (m model) noticeWidth() int {
	w := 64
	if m.termWidth > 0 {
		w = min(w, m.termWidth-12)
	}
	return max(w, 20)
}

func (m model) viewDiscovering() string {
	return m.header() + "\n" +
		m.spinner.View() + " " + stepRunStyle.Render(m.discovering) + "\n"
}

func (m model) viewServices() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n")
	b.WriteString(stepRunStyle.Render("Enabled but not running — select services to disable:") + "\n\n")
	for i, svc := range m.services {
		cursor := "  "
		if i == m.serviceCursor {
			cursor = cursorStyle.Render("➤ ")
		}
		box := "[ ]"
		if m.serviceOn[i] {
			box = selectedStyle.Render("[x]")
		}
		b.WriteString(cursor + box + " " + itemStyle.Render(svc) + "\n")
	}
	b.WriteString("\n" + hintStyle.Render("↑/↓ move · space toggle · enter disable · esc cancel"))
	return b.String()
}
