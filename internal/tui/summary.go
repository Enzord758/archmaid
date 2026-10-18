package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Enzord758/archmaid/internal/report"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// ── the run report on the summary screen ─────────────────────────────────────
//
// Between the checklist and the outcome line, the Driver's engine.RunReport
// (internal/report) is drawn as a block (the owner's variant C of #187):
//
//	Summary  ·  took 14 m 52 s  ·  9 steps: 8 done, 1 skipped, 0 failed
//	────────────────────────────────────────────────────────────────────
//	Packages    47 upgraded (6 AUR)
//	            linux-cachyos, systemd, nvidia-utils, mesa +43 more
//	Installed   3 · cachyos-gaming-meta, lib32-vulkan-icd-loader, zenity
//	Removed     2 · python-pyxdg-old, gtk2
//	Freed       3.1 GiB
//	            Package cache (keeps 3)            1.83 GiB
//	            ...
//	            Package cache still holds 4.2 GiB (312 packages).
//	            "Clear caches" in the menu frees it.
//
//	Needs your attention
//	⚠ Restart recommended: linux-cachyos, systemd upgraded (running ...)
//	Audit       2 findings (1 high): evil-bin, meh
//
// Installed and Removed only when not zero, zero Freed rows dimmed, the
// audit line only with findings, "✔ Nothing needs your attention." in place
// of the list when there is nothing. It wraps and trims to the terminal's
// width (80 when unknown), and a taller-than-the-screen result is shortened
// in two steps (viewSummary).

// cacheMenuLabel is the menu's name for the step that empties the package
// cache; the "still holds" line points at it. It must read as the menu entry
// does (internal/actions): the wipe is part of Clear caches (owner decision 2026-10-18).
const cacheMenuLabel = "Clear caches"

const (
	summaryIndent  = 12 // the label column: "Downgraded " and a space
	summaryMaxRule = 98 // the rule and the wrap stop here on a wide terminal
	areaLabelWidth = 30
	areaValueWidth = 9
)

// summaryOpts says how much of the block to draw.
type summaryOpts struct {
	width     int  // terminal columns; 0 is 80
	trimZeros bool // leave out the Freed rows that freed nothing
}

// summaryBlock draws r, or "" when it holds nothing worth a block. The
// result ends in a newline.
func summaryBlock(r *report.Report, o summaryOpts) string {
	if r == nil || !r.HasData() {
		return ""
	}
	w := o.width
	if w <= 0 {
		w = 80
	}
	w = min(w, summaryMaxRule)
	pad := strings.Repeat(" ", summaryIndent)
	var b strings.Builder
	line := func(label, text string) {
		b.WriteString(hintStyle.Render(fmt.Sprintf("%-*s", summaryIndent, label)) + text + "\n")
	}
	cont := func(text string) { b.WriteString(pad + text + "\n") }

	took, tally := "  ·  took "+duration(r.Elapsed), stepTally(r.Steps)
	if utf8.RuneCountInString("Summary"+took+"  ·  "+tally) <= w {
		b.WriteString(titleStyle.Render("Summary") + hintStyle.Render(took+"  ·  "+tally) + "\n")
	} else { // a narrow terminal: the tally on a line of its own
		b.WriteString(titleStyle.Render("Summary") + hintStyle.Render(took) + "\n" + hintStyle.Render(tally) + "\n")
	}
	b.WriteString(hintStyle.Render(strings.Repeat("─", w)) + "\n")

	if p := r.Packages; p != nil {
		head := fmt.Sprintf("%d upgraded", len(p.Upgraded))
		if p.AURKnown && len(p.Upgraded) > 0 {
			head += fmt.Sprintf(" (%d AUR)", p.AURUpgraded)
		}
		line("Packages", itemStyle.Render(head))
		if len(p.Upgraded) > 0 {
			cont(itemStyle.Render(nameList(topNames(p.Upgraded, r), 4, w-summaryIndent)))
		}
		if n := len(p.Installed); n > 0 {
			line("Installed", itemStyle.Render(fmt.Sprintf("%d · %s", n, nameList(pkgNames(p.Installed), 3, w-summaryIndent-4))))
		}
		if n := len(p.Removed); n > 0 {
			line("Removed", itemStyle.Render(fmt.Sprintf("%d · %s", n, nameList(pkgNames(p.Removed), 3, w-summaryIndent-4))))
		}
		if n := len(p.Downgraded); n > 0 {
			line("Downgraded", itemStyle.Render(fmt.Sprintf("%d · %s", n, nameList(pkgNames(p.Downgraded), 3, w-summaryIndent-4))))
		}
	} else if o := r.Orphans; o != nil {
		line("Removed", itemStyle.Render(fmt.Sprintf("%d orphans · %s", len(o.Names), nameList(safeNames(o.Names), 3, w-summaryIndent-14))))
	}

	if len(r.Freed) > 0 {
		total := report.HumanBytes(r.FreedBytes)
		if r.FreedPartial {
			total = "at least " + total
		}
		line("Freed", itemStyle.Render(total))
		for _, a := range r.Freed {
			if o.trimZeros && a.Known && a.Bytes == 0 {
				continue
			}
			val, style := report.HumanBytes(a.Bytes), itemStyle
			switch {
			case !a.Known:
				val, style = "unknown", stepSkipStyle
			case a.Bytes == 0:
				style = stepSkipStyle // dimmed: the step ran, nothing was there
			}
			lw := min(areaLabelWidth, max(w-summaryIndent-areaValueWidth-1, 8))
			cont(style.Render(fmt.Sprintf("%-*s %*s", lw, sanitize.Clip(a.Label, lw), areaValueWidth, val)))
		}
	}
	if c := r.Cache; c != nil {
		if c.Bytes == 0 && c.Packages == 0 {
			cont(reportInfoStyle.Render("The package cache is empty."))
		} else {
			text := fmt.Sprintf("Package cache still holds %s (%s). %q in the menu frees it.",
				report.HumanBytes(c.Bytes), pluralize(c.Packages, "package"), cacheMenuLabel)
			// The two sentences on their own lines when they fit, else wrapped.
			first, second, _ := strings.Cut(text, ". ")
			for _, part := range []string{first + ".", second} {
				for _, l := range wrap(part, w-summaryIndent) {
					cont(reportInfoStyle.Render(l))
				}
			}
		}
	}

	b.WriteString("\n")
	if len(r.Attention) == 0 {
		b.WriteString(stepOKStyle.Render("✔ Nothing needs your attention.") + "\n")
	} else {
		b.WriteString(noteStyle.Bold(true).Render("Needs your attention") + "\n")
		for _, it := range r.Attention {
			for i, l := range wrap(it.Text, w-2) {
				mark := "  "
				if i == 0 {
					mark = "⚠ "
				}
				b.WriteString(noteStyle.Render(mark+l) + "\n")
			}
		}
	}
	if a := r.Audit; a != nil && a.Findings > 0 {
		text := pluralize(a.Findings, "finding")
		if a.High > 0 {
			text += fmt.Sprintf(" (%d high)", a.High)
		}
		if left := w - summaryIndent - utf8.RuneCountInString(text) - 2; len(a.Names) > 0 && left >= 12 {
			text += ": " + nameList(safeNames(a.Names), 3, left)
		}
		line("Audit", itemStyle.Render(text))
	}
	for _, n := range r.Notes {
		for _, l := range wrap("Note: "+n, w) {
			b.WriteString(hintStyle.Render(l) + "\n")
		}
	}
	return b.String()
}

// topNames puts the packages the attention items name (the kernel, systemd,
// the driver) first, then the rest in the order pacman upgraded them.
func topNames(ps []report.Pkg, r *report.Report) []string {
	var first, rest []string
	named := map[string]bool{}
	for _, it := range r.Attention {
		if it.Kind == report.KindRestart {
			for _, n := range it.Detail {
				named[n] = true
			}
		}
	}
	for _, p := range ps {
		if named[p.Name] {
			first = append(first, safeName(p.Name))
		} else {
			rest = append(rest, safeName(p.Name))
		}
	}
	return append(first, rest...)
}

func pkgNames(ps []report.Pkg) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = safeName(p.Name)
	}
	return out
}

func safeNames(ns []string) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = safeName(n)
	}
	return out
}

// safeName is a name from outside archmaid, safe on one line.
func safeName(n string) string { return sanitize.QuoteName(n, 40) }

// nameList joins as many of names (at most max) as fit in width, then "+N
// more". At least one name always shows, trimmed if it must be.
func nameList(names []string, max, width int) string {
	if len(names) == 0 {
		return ""
	}
	width = max0(width)
	shown := 0
	for shown < len(names) && shown < max {
		next := strings.Join(names[:shown+1], ", ")
		rest := len(names) - (shown + 1)
		if rest > 0 {
			next += fmt.Sprintf(" +%d more", rest)
		}
		if shown > 0 && utf8.RuneCountInString(next) > width {
			break
		}
		shown++
	}
	out := strings.Join(names[:shown], ", ")
	if rest := len(names) - shown; rest > 0 {
		out += fmt.Sprintf(" +%d more", rest)
	}
	if width >= 4 && utf8.RuneCountInString(out) > width {
		out = sanitize.Clip(out, width)
	}
	return out
}

// wrap breaks text at spaces to lines of at most width runes; a word longer
// than that is cut with "…".
func wrap(text string, width int) []string {
	width = max(width, 10)
	var lines []string
	cur := ""
	for _, word := range strings.Fields(text) {
		if utf8.RuneCountInString(word) > width {
			word = sanitize.Clip(word, width)
		}
		switch {
		case cur == "":
			cur = word
		case utf8.RuneCountInString(cur)+1+utf8.RuneCountInString(word) <= width:
			cur += " " + word
		default:
			lines = append(lines, cur)
			cur = word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func pluralize(n int, what string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, what)
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// duration writes a run's length as "52 s", "6 m 08 s" or "1 h 02 m".
func duration(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	switch {
	case s >= 3600:
		return fmt.Sprintf("%d h %02d m", s/3600, s%3600/60)
	case s >= 60:
		return fmt.Sprintf("%d m %02d s", s/60, s%60)
	}
	return fmt.Sprintf("%d s", s)
}

// stepTally is "9 steps: 8 done, 1 skipped, 0 failed".
func stepTally(t report.StepTally) string {
	return fmt.Sprintf("%s: %d done, %d skipped, %d failed", pluralize(t.Total(), "step"), t.Done, t.Skipped, t.Failed)
}
