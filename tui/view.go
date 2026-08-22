package main

import (
	"fmt"
	"strings"
)

// ── view ─────────────────────────────────────────────────────────────────────

// View renders the active screen. Popup screens (password, confirm, abort,
// notice) are composited as a floating box over their recorded background.
func (m model) View() string {
	switch m.screen {
	case screenPassword:
		return overlay(m.viewOf(m.bg), m.viewPassword())
	case screenConfirm:
		return overlay(m.viewOf(m.bg), m.viewConfirm())
	case screenAbortConfirm:
		return overlay(m.viewOf(m.bg), m.viewAbortConfirm())
	case screenNotice:
		return overlay(m.viewOf(m.bg), m.viewNotice())
	}
	return m.viewOf(m.screen)
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
	case screenLog:
		return m.viewLog()
	case screenDiscovering:
		return m.viewDiscovering()
	case screenServices:
		return m.viewServices()
	}
	return m.viewMenu() // safe fallback for a popup background
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
	for i, it := range menu {
		cursor := "  "
		style := itemStyle
		if i == m.cursor {
			cursor = cursorStyle.Render("➤ ")
			style = selectedStyle
		}
		b.WriteString(cursor + style.Render(it.label) + "\n")
	}
	b.WriteString("\n" + hintStyle.Render("↑/↓ move · enter select · q quit"))
	return b.String()
}

func (m model) viewPassword() string {
	if m.authInFlight {
		body := stepRunStyle.Render("🔒 sudo access required") + "\n\n" +
			m.spinner.View() + " " + stepRunStyle.Render("authenticating…")
		return popupBox.Render(body)
	}
	body := stepRunStyle.Render("🔒 sudo access required") + "\n\n" + m.password.View()
	if m.pwErr != "" {
		body += "\n\n" + stepFailStyle.Render(m.pwErr)
	}
	body += "\n\n" + hintStyle.Render("enter submit · esc cancel")
	return popupBox.Render(body)
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
		title = m.queue[m.current].step.title
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
		case statusPending:
			glyph, text = stepPendingStyle.Render("○"), stepPendingStyle.Render(st.step.title)
		case statusRunning:
			glyph, text = m.spinner.View(), stepRunStyle.Render(st.step.title)
		case statusOK:
			glyph, text = stepOKStyle.Render("✔"), itemStyle.Render(st.step.logLabel)
		case statusFailed:
			glyph, text = stepFailStyle.Render("✖"), stepFailStyle.Render(st.step.title+" failed")
		case statusSkipped:
			glyph, text = stepSkipStyle.Render("–"), stepSkipStyle.Render(st.step.title+" (skipped)")
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

func (m model) viewLog() string {
	return m.header() + "\n" +
		outputPaneStyle.Width(m.output.Width).Render(m.output.View()) + "\n" +
		hintStyle.Render("↑/↓ scroll · q back")
}

func (m model) viewNotice() string {
	body := itemStyle.Render(m.notice) + "\n\n" + hintStyle.Render("any key to return")
	return popupBox.Render(body)
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
