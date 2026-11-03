package runview

import (
	"fmt"
	"strconv"
	"strings"
)

// Output lines arrive with SGR colour kept and every other escape stripped
// (engine.OutputChunk; internal/sanitize, KeepSGR). ParseSGR turns such a
// line into plain text plus styled spans, which the view draws as Pango
// attributes with the dank16 palette (palette.go).
//
// Only SGR (ESC [ params m, params digits, ';' and ':') is interpreted.
// Anything else that starts with ESC can't be in a line (sanitize drops it,
// and ansi_test.go asserts that for hostile input); should one get
// through anyway it is dropped, never acted on: ESC and, for a CSI, every
// byte up to its final byte.

// ColorKind says how a Color is given.
type ColorKind uint8

const (
	ColorDefault ColorKind = iota // the theme's own colour
	ColorIndex                    // a palette index, 0–255 (0–15 are dank16)
	ColorRGB                      // 24-bit
)

// Color is a text or background colour as SGR names it.
type Color struct {
	Kind    ColorKind
	Index   uint8 // ColorIndex
	R, G, B uint8 // ColorRGB
}

// Style is the SGR state of a run of text. Blink, conceal (sanitize drops
// it) and fonts are not kept.
type Style struct {
	FG, BG                                        Color
	Bold, Dim, Italic, Underline, Reverse, Strike bool
}

// Span is a styled run of a Line's Text: bytes [Start, End).
type Span struct {
	Start, End int
	Style      Style
}

// Line is an output line as the view shows it: Text without escapes, and
// Spans for its runs that aren't in the default style, in order and not
// overlapping.
type Line struct {
	Text  string
	Spans []Span
}

// ParseSGR splits s into its text and styled spans. A line without ESC is
// returned as is, without allocating.
func ParseSGR(s string) Line {
	if strings.IndexByte(s, 0x1b) < 0 {
		return Line{Text: s}
	}
	var (
		text  = make([]byte, 0, len(s))
		spans []Span
		st    Style
	)
	open := func() { // start a span at the current text end, if styled
		if st != (Style{}) {
			spans = append(spans, Span{Start: len(text), End: len(text), Style: st})
		}
	}
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			j := i + 1
			for j < len(s) && s[j] != 0x1b {
				j++
			}
			text = append(text, s[i:j]...)
			if n := len(spans); n > 0 && spans[n-1].Style == st && st != (Style{}) {
				spans[n-1].End = len(text)
			}
			i = j
			continue
		}
		params, end, ok := sgrAt(s, i)
		i = end
		if !ok {
			continue // not SGR: dropped, never interpreted
		}
		before := st
		applySGR(&st, params)
		if st != before {
			// Close an empty span left by a run of escapes, then open.
			if n := len(spans); n > 0 && spans[n-1].Start == spans[n-1].End {
				spans = spans[:n-1]
			}
			open()
		}
	}
	if n := len(spans); n > 0 && spans[n-1].Start == spans[n-1].End {
		spans = spans[:n-1]
	}
	// Merge neighbours with the same style (a reset and the same colour
	// again, as pacman writes it).
	out := spans[:0]
	for _, sp := range spans {
		if n := len(out); n > 0 && out[n-1].End == sp.Start && out[n-1].Style == sp.Style {
			out[n-1].End = sp.End
			continue
		}
		out = append(out, sp)
	}
	if len(out) == 0 {
		out = nil
	}
	return Line{Text: string(text), Spans: out}
}

// StripSGR is ParseSGR(s).Text, without building the spans: what the
// list shows, computed for every line as it arrives.
func StripSGR(s string) string {
	i := strings.IndexByte(s, 0x1b)
	if i < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i >= 0 {
		b.WriteString(s[:i])
		_, end, _ := sgrAt(s, i)
		s = s[end:]
		i = strings.IndexByte(s, 0x1b)
	}
	b.WriteString(s)
	return b.String()
}

// sgrAt reads the escape at s[i] == ESC: its end, and its parameters if
// it is a well-formed SGR. Any other CSI runs to its final byte (or the
// end of s); any other escape is the ESC alone plus the byte after it.
func sgrAt(s string, i int) (params string, end int, ok bool) {
	if i+1 >= len(s) {
		return "", len(s), false
	}
	if s[i+1] != '[' {
		return "", i + 2, false
	}
	j := i + 2
	sgr := true
	for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
		if c := s[j]; !(c >= '0' && c <= '9') && c != ';' && c != ':' {
			sgr = false
		}
		j++
	}
	if j == len(s) {
		return "", j, false
	}
	return s[i+2 : j], j + 1, sgr && s[j] == 'm'
}

// applySGR folds one SGR's parameters into st.
func applySGR(st *Style, params string) {
	if params == "" {
		*st = Style{}
		return
	}
	var tbuf, sbuf [16]string // no allocation for the usual short SGR
	toks := split(tbuf[:0], params, ';')
	for i := 0; i < len(toks); i++ {
		sub := split(sbuf[:0], toks[i], ':') // 38:5:n, 38:2::r:g:b, 4:3
		n, err := strconv.Atoi(sub[0])
		if sub[0] == "" {
			n, err = 0, nil
		}
		if err != nil {
			continue
		}
		switch {
		case n == 0:
			*st = Style{}
		case n == 1:
			st.Bold = true
		case n == 2:
			st.Dim = true
		case n == 3:
			st.Italic = true
		case n == 4:
			st.Underline = len(sub) < 2 || sub[1] != "0"
		case n == 7:
			st.Reverse = true
		case n == 9:
			st.Strike = true
		case n == 21:
			st.Underline = true // double underline: drawn single
		case n == 22:
			st.Bold, st.Dim = false, false
		case n == 23:
			st.Italic = false
		case n == 24:
			st.Underline = false
		case n == 27:
			st.Reverse = false
		case n == 29:
			st.Strike = false
		case n >= 30 && n <= 37:
			st.FG = Color{Kind: ColorIndex, Index: uint8(n - 30)}
		case n == 39:
			st.FG = Color{}
		case n >= 40 && n <= 47:
			st.BG = Color{Kind: ColorIndex, Index: uint8(n - 40)}
		case n == 49:
			st.BG = Color{}
		case n >= 90 && n <= 97:
			st.FG = Color{Kind: ColorIndex, Index: uint8(n - 90 + 8)}
		case n >= 100 && n <= 107:
			st.BG = Color{Kind: ColorIndex, Index: uint8(n - 100 + 8)}
		case n == 38 || n == 48:
			var c Color
			var ok bool
			if len(sub) > 1 {
				c, ok = extColor(sub[1:], true)
			} else {
				var used int
				c, used, ok = extColorSemi(toks[i+1:])
				i += used
			}
			if ok {
				if n == 38 {
					st.FG = c
				} else {
					st.BG = c
				}
			}
		}
	}
}

// split appends s's fields separated by sep to dst.
func split(dst []string, s string, sep byte) []string {
	for {
		i := strings.IndexByte(s, sep)
		if i < 0 {
			return append(dst, s)
		}
		dst = append(dst, s[:i])
		s = s[i+1:]
	}
}

// extColor reads a colon-form extended colour: 5:n, 2:r:g:b or
// 2:colourspace:r:g:b.
func extColor(sub []string, colon bool) (Color, bool) {
	// ParseUint with bitSize 8 range-checks before any conversion, so a value
	// over 255 (or negative, or not a number) is refused, never truncated
	// (CodeQL go/incorrect-integer-conversion).
	num := func(s string) (uint8, bool) {
		if s == "" {
			return 0, true
		}
		v, err := strconv.ParseUint(s, 10, 8)
		if err != nil {
			return 0, false
		}
		return uint8(v), true
	}
	switch {
	case len(sub) >= 2 && sub[0] == "5":
		v, ok := num(sub[1])
		return Color{Kind: ColorIndex, Index: v}, ok
	case len(sub) >= 4 && sub[0] == "2":
		rgb := sub[1:4]
		if colon && len(sub) >= 5 {
			rgb = sub[2:5] // with the colour-space id
		}
		r, ok1 := num(rgb[0])
		g, ok2 := num(rgb[1])
		b, ok3 := num(rgb[2])
		return Color{Kind: ColorRGB, R: r, G: g, B: b}, ok1 && ok2 && ok3
	}
	return Color{}, false
}

// extColorSemi reads a semicolon-form extended colour (5;n or 2;r;g;b)
// from toks, the parameters after 38/48, and says how many it used.
func extColorSemi(toks []string) (Color, int, bool) {
	if len(toks) == 0 {
		return Color{}, 0, false
	}
	switch toks[0] {
	case "5":
		if len(toks) < 2 {
			return Color{}, len(toks), false
		}
		c, ok := extColor(toks[:2], false)
		return c, 2, ok
	case "2":
		if len(toks) < 4 {
			return Color{}, len(toks), false
		}
		c, ok := extColor(toks[:4], false)
		return c, 4, ok
	}
	return Color{}, 1, false
}

// Attrs is the line's spans in Pango's attribute-list text format
// (pango_attr_list_from_string), coloured with p; "" for a plain line.
func (l Line) Attrs(p *Palette) string {
	if len(l.Spans) == 0 {
		return ""
	}
	var b strings.Builder
	for _, sp := range l.Spans {
		st := sp.Style
		fg, bg := st.FG, st.BG
		if st.Reverse {
			fg, bg = bg, fg
			if fg.Kind == ColorDefault {
				fg = Color{Kind: ColorIndex, Index: 0} // dank16's background
			}
			if bg.Kind == ColorDefault {
				bg = Color{Kind: ColorIndex, Index: 7} // dank16's foreground
			}
		}
		attr := func(name, value string) {
			fmt.Fprintf(&b, "%d %d %s %s\n", sp.Start, sp.End, name, value)
		}
		if fg.Kind != ColorDefault {
			attr("foreground", p.Color(fg).Hex())
		}
		if bg.Kind != ColorDefault {
			attr("background", p.Color(bg).Hex())
		}
		if st.Bold {
			attr("weight", "bold")
		}
		if st.Dim {
			attr("foreground-alpha", "40000") // of 65535
		}
		if st.Italic {
			attr("style", "italic")
		}
		if st.Underline {
			attr("underline", "single")
		}
		if st.Strike {
			attr("strikethrough", "true")
		}
	}
	return b.String()
}
