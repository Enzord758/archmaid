package runview

// Per-step reads of the view, for P8's timeline (one list per step): the
// widgets keep a step's lines in a list of their own (Change.Steps), so
// they find a line's colour by its place in its step, and rebuild a step's
// list (expanding it) from the view, not from what they held.

// StepLine is line i (0 = its oldest still in view) of step's output in
// the view as of the last Apply, with its colour spans; false when there
// is no such line. The step's "── title" header line isn't counted, as in
// Change.Steps.
func (m *Model) StepLine(step string, i int) (Line, bool) {
	if i < 0 {
		return Line{}, false
	}
	seq := m.viewLo
	for k := m.runHead; k < len(m.runs); k++ {
		r := m.runs[k]
		if !r.head && r.step == step {
			if i < r.n {
				return m.seqLine(seq + i)
			}
			i -= r.n
		}
		seq += r.n
	}
	return Line{}, false
}

// StepText is step's output lines in the view, oldest first, as plain
// text (the header line not included).
func (m *Model) StepText(step string) []string {
	var out []string
	seq := m.viewLo
	for k := m.runHead; k < len(m.runs); k++ {
		r := m.runs[k]
		if !r.head && r.step == step {
			for j := 0; j < r.n; j++ {
				out = append(out, m.out.buf[(seq+j)%len(m.out.buf)].text)
			}
		}
		seq += r.n
	}
	return out
}

// seqLine is the line with sequence number seq, if the ring still holds
// it and the view shows it.
func (m *Model) seqLine(seq int) (Line, bool) {
	if seq < m.viewLo || seq >= m.viewHi || seq < m.out.total-len(m.out.buf) {
		return Line{}, false
	}
	e := m.out.buf[seq%len(m.out.buf)]
	if e.raw == e.text {
		return Line{Text: e.text}, true
	}
	return ParseSGR(e.raw), true
}
