package main

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// overlay composites fg centered on top of bg, preserving the parts of bg that
// fg doesn't cover. Both are multi-line strings; the result is ANSI-aware, so
// styled/wide background content keeps its width and colours around the popup.
func overlay(bg, fg string) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")

	bgH, fgH := len(bgLines), len(fgLines)
	bgW, fgW := maxWidth(bgLines), maxWidth(fgLines)

	startY := max0((bgH - fgH) / 2)
	startX := max0((bgW - fgW) / 2)

	outH := bgH
	if startY+fgH > outH {
		outH = startY + fgH
	}

	out := make([]string, outH)
	for y := 0; y < outH; y++ {
		var bgLine string
		if y < bgH {
			bgLine = bgLines[y]
		}
		if y >= startY && y < startY+fgH {
			out[y] = compositeLine(bgLine, fgLines[y-startY], startX, fgW)
		} else {
			out[y] = bgLine
		}
	}
	return strings.Join(out, "\n")
}

// compositeLine places fgLine (a cell fgW wide) onto bgLine at column startX,
// keeping bg to the left and right of it.
func compositeLine(bgLine, fgLine string, startX, fgW int) string {
	left := ansi.Truncate(bgLine, startX, "")
	if w := ansi.StringWidth(left); w < startX {
		left += strings.Repeat(" ", startX-w) // pad short bg lines out to startX
	}
	if w := ansi.StringWidth(fgLine); w < fgW {
		fgLine += strings.Repeat(" ", fgW-w) // pad the popup cell to a uniform width
	}
	right := ansi.TruncateLeft(bgLine, startX+fgW, "")
	return left + fgLine + right
}

func maxWidth(lines []string) int {
	w := 0
	for _, l := range lines {
		if x := ansi.StringWidth(l); x > w {
			w = x
		}
	}
	return w
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
