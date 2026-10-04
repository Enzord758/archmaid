package main

import (
	"os"
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	const red, reset = "\x1b[31m", "\x1b[0m"
	cases := []struct {
		name, in, keep, plain string
	}{
		{"plain text untouched", "hello\tworld\nnext", "hello\tworld\nnext", "hello\tworld\nnext"},
		{"utf-8 untouched", "Pakke-cache · 5,0 G ✔", "Pakke-cache · 5,0 G ✔", "Pakke-cache · 5,0 G ✔"},
		{"SGR kept or stripped", red + "error" + reset, red + "error" + reset, "error"},
		{"SGR with colon params", "\x1b[38:5:196mx\x1b[m", "\x1b[38:5:196mx\x1b[m", "x"},
		{"cursor movement dropped", "a\x1b[2Ab\x1b[Kc", "abc", "abc"},
		{"private CSI dropped", "\x1b[?25lhidden cursor\x1b[?25h", "hidden cursor", "hidden cursor"},
		{"OSC title (BEL) dropped", "\x1b]0;pwned\x07ok", "ok", "ok"},
		{"OSC 52 clipboard (ST) dropped", "\x1b]52;c;ZXZpbA==\x1b\\ok", "ok", "ok"},
		{"OSC hyperlink dropped, text kept", "\x1b]8;;https://evil\x1b\\link\x1b]8;;\x1b\\", "link", "link"},
		{"unterminated OSC eats the rest", "ok\x1b]0;never ends", "ok", "ok"},
		{"DCS dropped", "\x1bPq#0;2;0;0;0\x1b\\ok", "ok", "ok"},
		{"charset and two-byte escapes dropped", "\x1b(Ba\x1b7b\x1bc", "ab", "ab"},
		{"trailing lone ESC dropped", "ok\x1b", "ok", "ok"},
		{"C0 controls dropped", "a\x00b\x07c\x08d\x7fe", "abcde", "abcde"},
		{"C1 controls dropped", "a\u009b31mb\u009dc", "a31mbc", "a31mbc"},
		{"invalid utf-8 replaced", "a\xffb", "a\uFFFDb", "a\uFFFDb"},
		{"bidi overrides and isolates dropped", "a\u202Eb\u202Ac\u2066d\u2069", "abcd", "abcd"},
		{"zero-width and BOM dropped", "\uFEFFa\u200Bb\u200Dc\u200Fd", "abcd", "abcd"},
		{"tag characters dropped", "a\U000e0041b", "ab", "ab"},
		{"progress redraw keeps last", "50%\r75%\r100%", "100%", "100%"},
		{"trailing CR keeps line", "done\r", "done", "done"},
		{"CRLF is a newline", "one\r\ntwo\r\n", "one\ntwo\n", "one\ntwo\n"},
		{"CR only rewrites its own line", "keep\nold\rnew", "keep\nnew", "keep\nnew"},
		{"SGR after CR starts the new text", "50%\r" + red + "100%", red + "100%", "100%"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Sanitize(c.in, SanitizeKeepSGR); got != c.keep {
				t.Errorf("KeepSGR(%q) = %q, want %q", c.in, got, c.keep)
			}
			if got := Sanitize(c.in, SanitizePlain); got != c.plain {
				t.Errorf("Plain(%q) = %q, want %q", c.in, got, c.plain)
			}
		})
	}
}

// TestLogsAreSanitized: what a step streamed (colours, a progress bar, an
// OSC 52 clipboard write) lands in the log as plain text, and an old log that
// still holds raw escapes is shown plain by View log.
func TestLogsAreSanitized(t *testing.T) {
	useLogDir(t, t.TempDir())

	m := fixedModel()
	m.screen = screenRunning
	m.queue = []stepState{{step: step{title: "up", logLabel: "Topgrade completed",
		category: logCategoryTopgrade, alwaysLog: true}, status: statusRunning}}
	m.current = 0
	for _, line := range []string{"\x1b[1m:: \x1b[0mupgrading", "10%\r100%", "\x1b]52;c;ZXZpbA==\x07done"} {
		next, _ := m.handleStream(streamBatchMsg{lines: []string{line}})
		m = next.(model)
	}
	if got := m.buf.String(); got != "\x1b[1m:: \x1b[0mupgrading\n100%\ndone\n" {
		t.Fatalf("output pane = %q: colours kept, everything else stripped", got)
	}
	m.handleStream(streamBatchMsg{done: true})

	data, err := os.ReadFile(logPath(logCategoryTopgrade))
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	if strings.ContainsAny(log, "\x1b\r") {
		t.Fatalf("log still holds escapes or CRs: %q", log)
	}
	if !strings.Contains(log, ":: upgrading\n100%\ndone\n") {
		t.Fatalf("log lost the text: %q", log)
	}

	old := "[2026-09-28 00:58:00] OK: Topgrade completed\n\x1b[32mok\x1b[0m\x1b]0;title\x07\n"
	if err := os.WriteFile(logPath(logCategoryCleanup), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readLog(logCategoryCleanup); strings.ContainsAny(got, "\x1b\x07") || !strings.Contains(got, "ok\n") {
		t.Fatalf("View log shows %q, want the old log as plain text", got)
	}
}

var benchOutput = strings.Repeat("\x1b[1m:: \x1b[0m(123/456) checking package integrity of some-package-1.2.3-1 [#####-----] 55%\n", 5000)

// BenchmarkSanitizePlain5k: stripping a 5k-line coloured topgrade log, as
// appendLog does once per step. Measured ~1.6 ms with x/ansi.Strip.
func BenchmarkSanitizePlain5k(b *testing.B) {
	b.SetBytes(int64(len(benchOutput)))
	for i := 0; i < b.N; i++ {
		_ = Sanitize(benchOutput, SanitizePlain)
	}
}

// BenchmarkSanitizeKeepSGR5kLines: the per-line streaming path over 5k lines.
func BenchmarkSanitizeKeepSGR5kLines(b *testing.B) {
	lines := strings.Split(strings.TrimSuffix(benchOutput, "\n"), "\n")
	b.SetBytes(int64(len(benchOutput)))
	for i := 0; i < b.N; i++ {
		for _, l := range lines {
			_ = Sanitize(l, SanitizeKeepSGR)
		}
	}
}

// BenchmarkSanitizeClean5k: already-clean text takes the no-copy fast path.
func BenchmarkSanitizeClean5k(b *testing.B) {
	s := strings.Repeat("(123/456) checking package integrity of some-package-1.2.3-1\n", 5000)
	b.SetBytes(int64(len(s)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Sanitize(s, SanitizePlain)
	}
}
