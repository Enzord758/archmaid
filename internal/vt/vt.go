// Package vt is the line model a step's output goes through: a small,
// bounded terminal that keeps what a real one would show of the output,
// line by line, instead of every byte that was written. A progress bar
// that redraws its line with "\r", a spinner, or a block of bars that moves
// the cursor up and rewrites them (pacman's parallel downloads, ollama) end
// as their final lines, not as every frame glued together.
//
// It understands "\r", "\n" (as CR LF: the pty's ONLCR makes every newline
// one, and a pipe's writers mean one), "\b", "\t", erasing (EL, ED 0/1,
// ECH, DCH, ICH), relative cursor moves (CUU, CUD, CUF, CUB, CNL, CPL,
// CHA/HPA, RI, IND, NEL), save/restore cursor and SGR, which it keeps per
// cell and writes back as canonical SGR. Every other escape (OSC, DCS and
// the other strings, private CSI such as "?25l" or "?1049h", absolute
// positioning, mouse) is dropped, as are C1 controls, hidden format
// characters (sanitize.Hidden) and SGR conceal. A string (OSC, DCS, SOS,
// PM, APC) its child never terminates ends at a newline, or after
// maxString bytes, and what follows is output again: one unterminated
// "ESC ]" doesn't swallow the rest of a step. Invalid UTF-8 becomes
// U+FFFD.
//
// Everything is bounded, whatever the stream: the cursor reaches only the
// last MaxRows lines (one Screen per child, so never an earlier step's
// output), a line holds at most MaxCols cells, a rendered line at most about
// MaxLineBytes, at most MaxStyles distinct styles live at once, and escape
// sequences are parsed without being stored past maxCSI bytes.
//
// A Screen reports its changes as rewrites (Flush): the last n lines it
// delivered are replaced by the lines it returns, of which there are at
// least n. Lines never disappear. When to flush (coalescing) is the
// caller's: internal/proc flushes complete lines at once and redraws at
// most every few tens of milliseconds.
package vt

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"

	"github.com/Enzord758/archmaid/internal/sanitize"
)

const (
	// MaxRows is how many of the newest lines the cursor can reach and
	// rewrite. Older lines are final.
	MaxRows = 64
	// MaxCols is how many cells one line holds; text past it is dropped.
	MaxCols = 8192
	// MaxLineBytes bounds a rendered line: past it, its styles stop (the
	// text, at most MaxCols cells, still follows).
	MaxLineBytes = 64 << 10
	// MaxStyles is how many distinct styles a Screen keeps at once; past
	// it (after one compaction per MaxRows new lines), new styles draw as
	// the default.
	MaxStyles = 1024
	// maxCSI is the longest control sequence kept for parsing; a longer
	// one is consumed and ignored.
	maxCSI = 256
	// maxString is the longest string (OSC, DCS, SOS, PM, APC) dropped
	// as one: past it, an unterminated string has ended and its bytes are
	// output (review #135).
	maxString = 4096
	// maxParam caps one numeric parameter.
	maxParam = 1 << 16
	// tabWidth is the distance between tab stops.
	tabWidth = 8
)

// cont marks the second cell of a wide rune.
const cont rune = -1

// cell is one column: a rune (0 = blank, never written or erased) and its
// style's id in the Screen's table.
type cell struct {
	r rune
	s uint16
}

// Attribute bits of a style.
const (
	attrBold uint8 = 1 << iota
	attrDim
	attrItalic
	attrUnderline
	attrBlink
	attrReverse
	attrStrike
)

// color is a colour: 0 the default, 1<<24|i the indexed colour i (0–255),
// 2<<24|rgb a 24-bit one.
type color uint32

const (
	colorIndexed color = 1 << 24
	colorRGB     color = 2 << 24
)

// style is what SGR sets.
type style struct {
	attrs  uint8
	fg, bg color
}

// visibleBlank reports whether a blank cell in this style still shows
// (a background, reverse video, an underline or a strike line).
func (st style) visibleBlank() bool {
	return st.bg != 0 || st.attrs&(attrReverse|attrUnderline|attrStrike) != 0
}

// pen is a style as the cells keep it: what it means (st) and the SGR text
// that set it, from the default style on (raw: "" for the default). The
// text is the child's own, so a line keeps the sequences it was written
// with ("\x1b[1m\x1b[34m" stays that, not a canonical "\x1b[0;1;34m"),
// unless it grew past maxRaw, when it is st's canonical SGR.
type pen struct {
	st  style
	raw string
}

// maxRaw caps a pen's SGR text.
const maxRaw = 256

// resets reports whether the SGR text seq starts by resetting every
// attribute (its first parameter is 0 or empty).
func resets(seq string) bool {
	return strings.HasPrefix(seq, "\x1b[0;") || strings.HasPrefix(seq, "\x1b[0m") ||
		strings.HasPrefix(seq, "\x1b[;") || strings.HasPrefix(seq, "\x1b[m")
}

// canonical is st as one SGR sequence that first resets.
func (st style) canonical() string {
	var b strings.Builder
	st.sgr(&b)
	return b.String()
}

// sgr writes st as one SGR sequence that first resets.
func (st style) sgr(b *strings.Builder) {
	b.WriteString("\x1b[0")
	for i, code := range [...]string{"1", "2", "3", "4", "5", "7", "9"} {
		if st.attrs&(1<<i) != 0 {
			b.WriteByte(';')
			b.WriteString(code)
		}
	}
	writeColor(b, st.fg, 30, 90, "38")
	writeColor(b, st.bg, 40, 100, "48")
	b.WriteByte('m')
}

func writeColor(b *strings.Builder, c color, base, bright int, ext string) {
	switch {
	case c == 0:
	case c&colorRGB == colorRGB:
		v := uint32(c) & 0xffffff
		b.WriteString(";" + ext + ";2;" + strconv.Itoa(int(v>>16)) + ";" + strconv.Itoa(int(v>>8&0xff)) + ";" + strconv.Itoa(int(v&0xff)))
	default:
		i := int(c & 0xff)
		switch {
		case i < 8:
			b.WriteString(";" + strconv.Itoa(base+i))
		case i < 16:
			b.WriteString(";" + strconv.Itoa(bright+i-8))
		default:
			b.WriteString(";" + ext + ";5;" + strconv.Itoa(i))
		}
	}
}

// parser states.
const (
	stGround = iota
	stEsc
	stEscInter
	stCSI
	stString
)

// Screen is one child's output as a terminal shows it. The zero value is
// not ready: use New. It is not safe for concurrent use.
type Screen struct {
	win  [][]cell // the reachable lines, oldest first
	base int      // index of win[0] in the whole output
	r, c int      // cursor: row in win (len(win) = the line after the last), column

	cur      style
	curRaw   string // the SGR text that set cur (pen.raw)
	curID    uint16
	curValid bool
	styles   []pen
	ids      map[string]uint16 // by pen.raw
	// noCompact: no style compaction until base reaches it (MaxStyles).
	noCompact int

	savedRow, savedCol int
	saved              bool

	// sent lines have been delivered (Flush); dirty is the lowest of
	// them changed since (sent when none). pend are the rendered lines
	// [dirty, base): lines to deliver that have left win.
	sent, dirty int
	pend        []string

	// parser
	state    int
	csi      []byte
	csiLong  bool
	osc      bool // the string is an OSC (BEL ends it)
	strC2    bool // the string's last byte was 0xC2 (a UTF-8 C1 ST may follow)
	strN     int  // the string's bytes so far (maxString)
	partial  [utf8.UTFMax]byte
	npartial int

	width *runewidth.Condition
}

// New returns an empty Screen.
func New() *Screen {
	return &Screen{
		styles: []pen{{}},
		ids:    map[string]uint16{"": 0},
		csi:    make([]byte, 0, 32),
		width:  &runewidth.Condition{EastAsianWidth: false},
	}
}

// Total is how many lines the output has so far (the cursor's line counts
// once something was written on it, or a newline passed it).
func (s *Screen) Total() int { return s.base + len(s.win) }

// Rewrite reports whether a line already delivered has changed since.
func (s *Screen) Rewrite() bool { return s.dirty < s.sent }

// Backlog is how many lines a full Flush would deliver.
func (s *Screen) Backlog() int { return s.Total() - s.dirty }

// completeEnd is the end of the complete lines: every line but the
// cursor's when the cursor is on the last one (it may still grow).
func (s *Screen) completeEnd() int {
	n := s.Total()
	if len(s.win) > 0 && s.r == len(s.win)-1 {
		n--
	}
	return n
}

// Ready is how many complete lines wait for delivery, none of them a
// rewrite (0 while Rewrite holds: deliver those with a full Flush).
func (s *Screen) Ready() int {
	if s.Rewrite() {
		return 0
	}
	return max(s.completeEnd()-s.sent, 0)
}

// Partial reports whether the cursor's line is unfinished and not yet
// delivered.
func (s *Screen) Partial() bool { return s.completeEnd() < s.Total() && s.Total()-1 >= s.sent }

// Flush delivers what changed: the last replace lines delivered before
// are to be replaced by lines (len(lines) >= replace). complete delivers
// only the complete lines (Ready), and only when no rewrite is pending;
// otherwise everything goes, the cursor's unfinished line included.
func (s *Screen) Flush(complete bool) (replace int, lines []string) {
	end := s.Total()
	if complete && !s.Rewrite() {
		end = max(s.completeEnd(), s.sent)
	}
	if end <= s.dirty {
		return 0, nil
	}
	replace = s.sent - s.dirty
	lines = make([]string, 0, end-s.dirty)
	lines = append(lines, s.pend...)
	for i := max(s.dirty, s.base) - s.base; i < end-s.base; i++ {
		lines = append(lines, s.render(s.win[i]))
	}
	clear(s.pend)
	s.pend = s.pend[:0]
	s.sent, s.dirty = end, end
	return replace, lines
}

// touch records that line abs (an index in the whole output) changed.
func (s *Screen) touch(abs int) {
	if abs < s.dirty {
		s.dirty = abs
	}
}

// Write feeds output bytes. It never fails.
func (s *Screen) Write(p []byte) (int, error) {
	n := len(p)
	if s.npartial > 0 {
		p = s.finishRune(p)
	}
	for i := 0; i < len(p); {
		b := p[i]
		switch s.state {
		case stGround:
			if b >= 0x20 && b < 0x7f {
				j := i + 1
				for j < len(p) && p[j] >= 0x20 && p[j] < 0x7f {
					j++
				}
				s.putASCII(p[i:j])
				i = j
				continue
			}
			if b >= 0x80 {
				if !utf8.FullRune(p[i:]) {
					s.npartial = copy(s.partial[:], p[i:])
					return n, nil
				}
				r, size := utf8.DecodeRune(p[i:])
				s.putRune(r, size)
				i += size
				continue
			}
			s.control(b)
		case stEsc:
			s.escByte(b)
		case stEscInter:
			switch {
			case b < 0x20:
				s.controlInSeq(b)
			case b <= 0x2f:
			default: // the final byte (or anything else) ends it
				s.state = stGround
			}
		case stCSI:
			s.csiByte(b)
		case stString:
			s.stringByte(b)
		}
		i++
	}
	return n, nil
}

// finishRune completes a rune split across Writes with p's first bytes and
// returns the rest of p.
func (s *Screen) finishRune(p []byte) []byte {
	for s.npartial > 0 {
		var buf [utf8.UTFMax]byte
		k := copy(buf[:], s.partial[:s.npartial])
		k += copy(buf[k:], p)
		if !utf8.FullRune(buf[:k]) {
			// still short: p was 1–2 bytes; keep them
			copy(s.partial[s.npartial:], p)
			s.npartial += len(p)
			return nil
		}
		r, size := utf8.DecodeRune(buf[:k])
		if size <= s.npartial {
			// invalid: the first held byte is U+FFFD, retry the rest
			s.putRune(utf8.RuneError, 1)
			copy(s.partial[:], s.partial[1:s.npartial])
			s.npartial--
			continue
		}
		p = p[size-s.npartial:]
		s.npartial = 0
		s.putRune(r, size)
	}
	return p
}

// control is a C0 control byte (or DEL) in the ground state.
func (s *Screen) control(b byte) {
	switch b {
	case 0x1b:
		s.state = stEsc
	case '\n', 0x0b, 0x0c:
		s.newline(true)
	case '\r':
		s.c = 0
	case '\b':
		if s.c > 0 {
			s.c--
		}
	case '\t':
		if s.c < MaxCols-1 {
			s.c = min((s.c/tabWidth+1)*tabWidth, MaxCols-1)
		}
	}
	// anything else (BEL, NUL, DEL, …): dropped
}

// controlInSeq is a C0 byte inside an escape sequence: ESC starts a new
// one, CAN and SUB cancel it, the rest act as in the ground state.
func (s *Screen) controlInSeq(b byte) {
	switch b {
	case 0x1b:
		s.state = stEsc
	case 0x18, 0x1a:
		s.state = stGround
	default:
		s.control(b)
	}
}

func (s *Screen) escByte(b byte) {
	s.state = stGround
	switch {
	case b == '[':
		s.state, s.csi, s.csiLong = stCSI, s.csi[:0], false
	case b == ']' || b == 'P' || b == 'X' || b == '^' || b == '_':
		s.state, s.osc, s.strC2, s.strN = stString, b == ']', false, 0
	case b < 0x20:
		s.controlInSeq(b)
	case b >= 0x20 && b <= 0x2f:
		s.state = stEscInter
	case b == '7':
		s.save()
	case b == '8':
		s.restore()
	case b == 'M': // reverse index: up one, no scrolling
		s.r = max(s.r-1, 0)
	case b == 'D': // index: down one, same column
		s.newline(false)
	case b == 'E': // next line
		s.newline(true)
	}
	// any other final: dropped
}

func (s *Screen) csiByte(b byte) {
	switch {
	case b < 0x20:
		s.controlInSeq(b)
	case b <= 0x3f: // a parameter or intermediate byte
		if len(s.csi) < maxCSI {
			s.csi = append(s.csi, b)
		} else {
			s.csiLong = true
		}
	case b <= 0x7e:
		s.state = stGround
		if !s.csiLong {
			s.dispatch(b)
		}
	default: // DEL or a high byte: the sequence is broken
		s.state = stGround
	}
}

func (s *Screen) stringByte(b byte) {
	switch {
	case b == 0x07 && s.osc:
		s.state = stGround
	case b == 0x1b: // ST (ESC \) or a new escape: the string ends either way
		s.state = stEsc
	case b == 0x18 || b == 0x1a:
		s.state = stGround
	case b == 0x9c && s.strC2: // C1 ST, UTF-8 encoded
		s.state = stGround
	case b == '\n': // never terminated: it ends with its line
		s.state = stGround
		s.control(b)
	default:
		s.strN++
		if s.strN >= maxString { // never terminated: what follows is output
			s.state = stGround
		}
	}
	s.strC2 = b == 0xc2
}

// dispatch runs a complete CSI sequence with final byte f; s.csi holds its
// parameters and intermediates.
func (s *Screen) dispatch(f byte) {
	for _, b := range s.csi {
		if b < 0x30 || (b >= 0x3c && b <= 0x3f) {
			return // intermediates or a private marker: not ours
		}
	}
	if f == 'm' {
		s.sgr(s.csi)
		return
	}
	n := s.param(0, 1)
	switch f {
	case 'A':
		s.r = max(s.r-n, 0)
	case 'B':
		s.r = min(s.r+n, len(s.win))
	case 'C':
		s.c = min(s.c+n, MaxCols-1)
	case 'D':
		s.c = max(min(s.c, MaxCols-1)-n, 0)
	case 'E':
		s.r, s.c = min(s.r+n, len(s.win)), 0
	case 'F':
		s.r, s.c = max(s.r-n, 0), 0
	case 'G', '`':
		s.c = min(n, MaxCols) - 1
	case 'K':
		s.eraseLine(s.param(0, 0))
	case 'J':
		s.eraseDisplay(s.param(0, 0))
	case 'X':
		s.eraseChars(n)
	case 'P':
		s.deleteChars(n)
	case '@':
		s.insertChars(n)
	case 's':
		s.save()
	case 'u':
		s.restore()
	}
}

// param is the i-th numeric parameter of s.csi; def when absent or 0 with
// def 1 (cursor counts), capped at maxParam.
func (s *Screen) param(i, def int) int {
	k := 0
	for _, f := range strings.Split(string(s.csi), ";") {
		if k == i {
			main, _, _ := strings.Cut(f, ":")
			v := atoi(main)
			if v == 0 {
				return def
			}
			return v
		}
		k++
	}
	return def
}

// atoi parses a parameter, saturating at maxParam; "" is 0.
func atoi(s string) int {
	v := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
		v = min(v*10+int(s[i]-'0'), maxParam)
	}
	return v
}

func (s *Screen) save() {
	s.savedRow, s.savedCol, s.saved = s.base+s.r, s.c, true
}

func (s *Screen) restore() {
	if !s.saved {
		s.r, s.c = 0, 0
		return
	}
	s.r = min(max(s.savedRow-s.base, 0), len(s.win))
	s.c = s.savedCol
}

// newline moves to the next line (at column 0 when cr), making the line
// it leaves a line of the output if it was the one after the last.
func (s *Screen) newline(cr bool) {
	if s.r == len(s.win) {
		s.win = append(s.win, nil)
		s.bound()
	}
	s.r++
	if cr {
		s.c = 0
	}
}

// bound keeps win at MaxRows lines: the oldest leave it, final, and go to
// pend when they are still to be delivered.
func (s *Screen) bound() {
	for len(s.win) > MaxRows {
		if s.base >= s.dirty {
			s.pend = append(s.pend, s.render(s.win[0]))
		}
		s.win[0] = nil
		s.win = s.win[1:]
		s.base++
		s.r--
	}
	if cap(s.win) > 4*MaxRows && len(s.win) <= MaxRows {
		s.win = append(make([][]cell, 0, 2*MaxRows), s.win...)
	}
}

// row is the cursor's line, created when the cursor is past the last one.
func (s *Screen) row() []cell {
	if s.r == len(s.win) {
		s.win = append(s.win, nil)
		s.bound()
	}
	return s.win[s.r]
}

// styleID is the current style's id.
func (s *Screen) styleID() uint16 {
	if !s.curValid {
		s.curID, s.curValid = s.intern(pen{s.cur, s.curRaw}), true
	}
	return s.curID
}

func (s *Screen) intern(p pen) uint16 {
	if id, ok := s.ids[p.raw]; ok {
		return id
	}
	if len(s.styles) >= MaxStyles {
		if s.base < s.noCompact {
			return 0
		}
		s.compact()
		s.noCompact = s.base + MaxRows
		if len(s.styles) >= MaxStyles {
			return 0
		}
	}
	id := uint16(len(s.styles))
	s.styles = append(s.styles, p)
	s.ids[p.raw] = id
	return id
}

// compact keeps only the styles win still uses.
func (s *Screen) compact() {
	remap := map[uint16]uint16{0: 0}
	styles := []pen{{}}
	ids := map[string]uint16{"": 0}
	for _, row := range s.win {
		for i, c := range row {
			id, ok := remap[c.s]
			if !ok {
				id = uint16(len(styles))
				p := s.styles[c.s]
				styles = append(styles, p)
				ids[p.raw] = id
				remap[c.s] = id
			}
			row[i].s = id
		}
	}
	s.styles, s.ids, s.curValid = styles, ids, false
}

// blank is an erased cell: the current background shows (as xterm's bce).
func (s *Screen) blank() cell {
	if s.cur.bg == 0 {
		return cell{}
	}
	st := style{bg: s.cur.bg}
	return cell{s: s.intern(pen{st, st.canonical()})}
}

func (s *Screen) putASCII(text []byte) {
	if s.c >= MaxCols {
		return
	}
	row := s.row()
	id := s.styleID()
	text = text[:min(len(text), MaxCols-s.c)]
	end := s.c + len(text)
	if end > len(row) {
		row = grow(row, end)
	}
	s.unsplit(row, s.c)
	s.unsplit(row, end)
	for k, b := range text {
		row[s.c+k] = cell{r: rune(b), s: id}
	}
	s.win[s.r] = row
	s.c = end
	s.touch(s.base + s.r)
}

// putRune writes r, decoded from size bytes of input.
func (s *Screen) putRune(r rune, size int) {
	switch {
	case r == utf8.RuneError && size == 1:
		// invalid UTF-8: U+FFFD
	case r >= 0x80 && r <= 0x9f, sanitize.Hidden(r):
		return
	}
	w := s.width.RuneWidth(r)
	if w < 1 {
		w = 1
	}
	if s.c+w > MaxCols {
		s.c = MaxCols
		return
	}
	row := s.row()
	id := s.styleID()
	if s.c+w > len(row) {
		row = grow(row, s.c+w)
	}
	s.unsplit(row, s.c)
	s.unsplit(row, s.c+w)
	row[s.c] = cell{r: r, s: id}
	if w == 2 {
		row[s.c+1] = cell{r: cont, s: id}
	}
	s.win[s.r] = row
	s.c += w
	s.touch(s.base + s.r)
}

// grow extends row with blank cells to n.
func grow(row []cell, n int) []cell {
	if n <= cap(row) {
		k := len(row)
		row = row[:n]
		clear(row[k:])
		return row
	}
	out := make([]cell, n, max(n, 2*cap(row), 16))
	copy(out, row)
	return out
}

// unsplit keeps a wide rune whole across a write or erase boundary at i:
// if i falls on its second cell, both halves become blank.
func (s *Screen) unsplit(row []cell, i int) {
	if i > 0 && i < len(row) && row[i].r == cont {
		row[i-1], row[i] = cell{}, cell{}
	}
}

func (s *Screen) eraseLine(mode int) {
	if s.r >= len(s.win) {
		return
	}
	row := s.win[s.r]
	switch mode {
	case 0:
		s.eraseRange(row, s.c, len(row))
	case 1:
		s.eraseRange(row, 0, min(s.c+1, len(row)))
	case 2:
		s.eraseRange(row, 0, len(row))
	default:
		return
	}
	s.touch(s.base + s.r)
}

// eraseRange blanks row[from:to]; a blank tail without a background is cut
// off instead (a line's trailing blanks don't show).
func (s *Screen) eraseRange(row []cell, from, to int) {
	if from >= to {
		return
	}
	s.unsplit(row, from)
	s.unsplit(row, to)
	b := s.blank()
	if to == len(row) && b == (cell{}) {
		clear(row[from:])
		s.win[s.r] = row[:from]
		return
	}
	for i := from; i < to; i++ {
		row[i] = b
	}
}

func (s *Screen) eraseDisplay(mode int) {
	switch mode {
	case 0:
		s.eraseLine(0)
		for i := s.r + 1; i < len(s.win); i++ {
			s.win[i] = s.win[i][:0]
			s.touch(s.base + i)
		}
	case 1:
		s.eraseLine(1)
		for i := 0; i < min(s.r, len(s.win)); i++ {
			s.win[i] = s.win[i][:0]
			s.touch(s.base + i)
		}
	}
	// 2 and 3 (clear the screen, the scrollback) would blank real output: ignored
}

func (s *Screen) eraseChars(n int) {
	if s.r >= len(s.win) {
		return
	}
	row := s.win[s.r]
	s.eraseRange(row, min(s.c, len(row)), min(s.c+n, len(row)))
	s.touch(s.base + s.r)
}

// deleteChars and insertChars shift the rest of the line in place. A wide
// rune can be split only where cells go or come (the cursor, the end of
// what is deleted, the cut at MaxCols), so only there is it blanked
// (unsplit): the work per sequence is one move of the line, with no
// rune-width lookups (review #135: a scan of the whole line per "ESC[P"
// cost 25 µs per input byte).
func (s *Screen) deleteChars(n int) {
	if s.r >= len(s.win) || s.c >= len(s.win[s.r]) {
		return
	}
	row := s.win[s.r]
	end := min(s.c+n, len(row))
	s.unsplit(row, s.c)
	s.unsplit(row, end)
	s.win[s.r] = append(row[:s.c], row[end:]...)
	s.touch(s.base + s.r)
}

func (s *Screen) insertChars(n int) {
	if s.r >= len(s.win) || s.c >= len(s.win[s.r]) {
		return
	}
	row := s.win[s.r]
	n = min(n, MaxCols-s.c)
	keep := min(len(row), MaxCols-n) // cells past it are pushed off the line
	s.unsplit(row, s.c)
	s.unsplit(row, keep)
	row = grow(row[:keep], keep+n)
	copy(row[s.c+n:], row[s.c:keep])
	b := s.blank()
	for i := s.c; i < s.c+n; i++ {
		row[i] = b
	}
	s.win[s.r] = row
	s.touch(s.base + s.r)
}

// render is row as text with canonical SGR: trailing blanks that don't
// show are left out, and the line ends in the default style.
func (s *Screen) render(row []cell) string {
	end := len(row)
	for end > 0 {
		c := row[end-1]
		if (c.r != 0 && c.r != ' ') || s.styles[c.s].st.visibleBlank() {
			break
		}
		end--
	}
	var b strings.Builder
	cur, capped := uint16(0), false
	for _, c := range row[:end] {
		if c.r == cont {
			continue
		}
		if c.s != cur && !capped {
			if b.Len() > MaxLineBytes {
				capped = true
				if cur != 0 {
					b.WriteString("\x1b[0m")
				}
			} else {
				s.switchPen(&b, cur, c.s)
				cur = c.s
			}
		}
		if c.r == 0 {
			b.WriteByte(' ')
		} else {
			b.WriteRune(c.r)
		}
	}
	if cur != 0 && !capped {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// switchPen writes the SGR that changes the pen from cur to next: a
// reset for the default; next's own text from the default, or when it
// resets first; only what next's text adds when it continues cur's (the
// child's own sequences, as it wrote them); else a reset and next's text.
func (s *Screen) switchPen(b *strings.Builder, cur, next uint16) {
	raw := s.styles[next].raw
	switch {
	case next == 0:
		b.WriteString("\x1b[0m")
	case cur == 0 || resets(raw):
		b.WriteString(raw)
	case strings.HasPrefix(raw, s.styles[cur].raw):
		b.WriteString(raw[len(s.styles[cur].raw):])
	default:
		b.WriteString("\x1b[0m")
		b.WriteString(raw)
	}
}

// sgr applies one SGR sequence's parameters.
func (s *Screen) sgr(params []byte) {
	fields := strings.Split(string(params), ";")
	st := s.cur
	for i := 0; i < len(fields); i++ {
		main, sub, colon := strings.Cut(fields[i], ":")
		n := atoi(main)
		switch {
		case n == 0:
			st = style{}
		case n == 1:
			st.attrs |= attrBold
		case n == 2:
			st.attrs |= attrDim
		case n == 3:
			st.attrs |= attrItalic
		case n == 4:
			if colon && sub == "0" {
				st.attrs &^= attrUnderline
			} else {
				st.attrs |= attrUnderline
			}
		case n == 5 || n == 6:
			st.attrs |= attrBlink
		case n == 7:
			st.attrs |= attrReverse
		case n == 9:
			st.attrs |= attrStrike
		case n == 21:
			st.attrs |= attrUnderline
		case n == 22:
			st.attrs &^= attrBold | attrDim
		case n == 23:
			st.attrs &^= attrItalic
		case n == 24:
			st.attrs &^= attrUnderline
		case n == 25:
			st.attrs &^= attrBlink
		case n == 27:
			st.attrs &^= attrReverse
		case n == 29:
			st.attrs &^= attrStrike
		case n >= 30 && n <= 37:
			st.fg = colorIndexed | color(n-30)
		case n == 39:
			st.fg = 0
		case n >= 40 && n <= 47:
			st.bg = colorIndexed | color(n-40)
		case n == 49:
			st.bg = 0
		case n >= 90 && n <= 97:
			st.fg = colorIndexed | color(n-90+8)
		case n >= 100 && n <= 107:
			st.bg = colorIndexed | color(n-100+8)
		case n == 38 || n == 48 || n == 58:
			var c color
			var ok bool
			if colon {
				c, ok = extColon(sub)
			} else {
				c, ok, i = extSemicolon(fields, i)
			}
			if ok && n == 38 {
				st.fg = c
			} else if ok && n == 48 {
				st.bg = c
			}
			// 58 (underline colour) is parsed past and not kept
		}
		// 8 (conceal) and anything unknown: ignored
	}
	raw := s.curRaw
	// conceal (8, 08…) never reaches a line, not even inside a sequence
	// that changes something else
	switch seq := sanitize.String("\x1b["+string(params)+"m", sanitize.KeepSGR); {
	case st == s.cur:
		// changes nothing: not kept
	case st == style{}:
		raw = ""
	case resets(seq):
		raw = seq
	default:
		raw += seq
	}
	if len(raw) > maxRaw {
		raw = st.canonical()
	}
	if st != s.cur || raw != s.curRaw {
		s.cur, s.curRaw, s.curValid = st, raw, false
	}
}

// extColon parses "5:n" or "2:[cs:]r:g:b", the colon form after 38/48.
func extColon(sub string) (color, bool) {
	p := strings.Split(sub, ":")
	switch {
	case len(p) >= 2 && p[0] == "5":
		return indexed(p[1])
	case len(p) >= 4 && p[0] == "2":
		rgb := p[1:]
		if len(rgb) >= 4 {
			rgb = rgb[len(rgb)-3:] // a colour-space id came first
		}
		return rgbColor(rgb[0], rgb[1], rgb[2])
	}
	return 0, false
}

// extSemicolon parses "5;n" or "2;r;g;b" after fields[i] (38/48/58) and
// returns the index of the last field it used.
func extSemicolon(fields []string, i int) (color, bool, int) {
	if i+1 >= len(fields) {
		return 0, false, i
	}
	switch fields[i+1] {
	case "5":
		if i+2 < len(fields) {
			c, ok := indexed(fields[i+2])
			return c, ok, i + 2
		}
		return 0, false, len(fields)
	case "2":
		if i+4 < len(fields) {
			c, ok := rgbColor(fields[i+2], fields[i+3], fields[i+4])
			return c, ok, i + 4
		}
		return 0, false, len(fields)
	}
	return 0, false, i + 1
}

func indexed(v string) (color, bool) {
	n := atoi(v)
	if n > 255 {
		return 0, false
	}
	return colorIndexed | color(n), true
}

func rgbColor(r, g, b string) (color, bool) {
	R, G, B := atoi(r), atoi(g), atoi(b)
	if R > 255 || G > 255 || B > 255 {
		return 0, false
	}
	return colorRGB | color(R<<16|G<<8|B), true
}
