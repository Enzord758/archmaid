package tui

import (
	"fmt"
	"strings"

	"github.com/Enzord758/archmaid/internal/audit"
)

// renderAudit builds the styled report shown on the audit screen
// (frontend.Glue.Render).
func renderAudit(res audit.Result) string {
	var b strings.Builder
	b.WriteString(stepRunStyle.Render(fmt.Sprintf("%d foreign package(s) checked against the AUR", res.Total)) + "\n\n")

	high := res.High()
	b.WriteString(fmt.Sprintf("%s  %s  %s\n\n",
		stepFailStyle.Render(fmt.Sprintf("✖ %d look closely", high)),
		stepSkipStyle.Render(fmt.Sprintf("⚠ %d glance", len(res.Findings)-high)),
		stepOKStyle.Render(fmt.Sprintf("✔ %d clean", res.Clean())),
	))

	width := audit.NameWidth(res.Findings)
	for _, f := range res.Findings {
		glyph, style := "⚠", stepSkipStyle
		if f.High {
			glyph, style = "✖", stepFailStyle
		}
		b.WriteString(style.Render(glyph) + " " + itemStyle.Render(f.Line(width)) + "\n")
	}
	if len(res.Findings) == 0 {
		b.WriteString(stepOKStyle.Render("Nothing flagged.") + "\n")
	}

	b.WriteString("\n")
	if note := res.Note(); note != "" {
		b.WriteString(statusStyle.Render(note) + "\n")
	}
	if high > 0 && !res.DryRun() {
		b.WriteString(statusStyle.Render("Flagged maintainers stay flagged on every audit until you've checked them and press a to accept.") + "\n")
	}
	b.WriteString(statusStyle.Render("Recent = pushed within 72h, the same window as the yay cooldown hook."))
	return b.String()
}
