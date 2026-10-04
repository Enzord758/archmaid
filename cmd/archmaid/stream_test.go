package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// pumpAll runs a step's pump to its done the way the runtime would, starting
// with first and re-pumping m's channel, and returns every batch it
// delivered. It never touches the model, so the batches can be fed later.
func pumpAll(t *testing.T, m model, first tea.Cmd) []streamBatchMsg {
	t.Helper()
	var out []streamBatchMsg
	for cmd := first; ; cmd = waitForStream(m.stream, m.quit, m.stepSeq) {
		b, ok := cmd().(streamBatchMsg)
		if !ok {
			t.Fatalf("pump delivered %#v, want a streamBatchMsg", b)
		}
		out = append(out, b)
		if b.done {
			return out
		}
	}
}

// streamingModel is a model sized like a terminal with one step running and
// its channel ready to be filled; feed it with the returned pump.
func streamingModel(s step, chanCap int) (model, tea.Cmd) {
	m := update(fixedModel(), tea.WindowSizeMsg{Width: 120, Height: 40})
	m.screen = screenRunning
	m.queue = []stepState{{step: s, status: statusRunning}}
	m.current = 0
	m.resizeOutput()
	m.stepSeq = 1
	m.stream = make(chan streamMsg, chanCap)
	m.quit = make(chan struct{})
	return m, waitForStream(m.stream, m.quit, m.stepSeq)
}

func numbered(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	return lines
}

// TestStreamBatchesBufferedLines: the pump takes everything already buffered
// in one batch, at most streamBatchMax lines, in order, and ends the batch at
// the done message (which nothing follows). Every batch carries the seq.
func TestStreamBatchesBufferedLines(t *testing.T) {
	const n = 2*streamBatchMax + 10
	ch := make(chan streamMsg, n+1)
	for _, l := range numbered(n) {
		ch <- streamMsg{line: l}
	}
	ch <- streamMsg{done: true, err: os.ErrClosed}

	var got []string
	var sizes []int
	for {
		b := waitForStream(ch, nil, 7)().(streamBatchMsg)
		if b.seq != 7 {
			t.Fatalf("batch seq = %d, want 7", b.seq)
		}
		got = append(got, b.lines...)
		sizes = append(sizes, len(b.lines))
		if b.done {
			if b.err != os.ErrClosed {
				t.Fatalf("done err = %v, want the step's error", b.err)
			}
			break
		}
	}
	if fmt.Sprint(sizes) != fmt.Sprint([]int{streamBatchMax, streamBatchMax, 10}) {
		t.Fatalf("batch sizes = %v, want %d, %d, then 10 with the done", sizes, streamBatchMax, streamBatchMax)
	}
	if strings.Join(got, ",") != strings.Join(numbered(n), ",") {
		t.Fatal("lines out of order or lost across batches")
	}
	if len(ch) != 0 {
		t.Fatalf("%d messages left after the done", len(ch))
	}
}

// TestStreamBatchDoneAlone: a done with nothing before it is a batch of its
// own, and a pump that finds nothing buffered after a line delivers just that.
func TestStreamBatchDoneAlone(t *testing.T) {
	ch := make(chan streamMsg, 4)
	ch <- streamMsg{line: "only"}
	if b := waitForStream(ch, nil, 1)().(streamBatchMsg); b.done || len(b.lines) != 1 || b.lines[0] != "only" {
		t.Fatalf("got %#v, want just the one line", b)
	}
	ch <- streamMsg{done: true}
	if b := waitForStream(ch, nil, 1)().(streamBatchMsg); !b.done || len(b.lines) != 0 {
		t.Fatalf("got %#v, want a bare done", b)
	}
}

// TestStreamPumpReleasedByAbort: a pump waiting on an empty channel returns
// nil once quit closes, so an abort leaves no goroutine behind.
func TestStreamPumpReleasedByAbort(t *testing.T) {
	quit := make(chan struct{})
	close(quit)
	if msg := waitForStream(make(chan streamMsg), quit, 1)(); msg != nil {
		t.Fatalf("aborted pump returned %#v, want nil", msg)
	}
}

// TestScrollUpNotYankedDuringStream: the pane follows new output only while
// the user is at the bottom. Scrolled up, new batches leave them where they
// are — even once the tail starts dropping old lines, the line they were
// reading stays on top — and scrolling back down resumes following.
func TestScrollUpNotYankedDuringStream(t *testing.T) {
	m, _ := streamingModel(step{title: "x", logLabel: "x"}, 1)
	feed := func(lines []string) {
		t.Helper()
		next, cmd := m.Update(streamBatchMsg{seq: m.stepSeq, lines: lines})
		m = next.(model)
		if cmd == nil {
			t.Fatal("a batch without done must keep pumping")
		}
	}
	all := numbered(maxVisibleLines + 300)

	feed(all[:1800])
	if !m.output.AtBottom() {
		t.Fatalf("at the start the pane follows output: offset %d", m.output.YOffset)
	}

	for range 10 {
		m = update(m, key("up"))
	}
	if m.output.AtBottom() {
		t.Fatal("scrolling up should leave the bottom")
	}
	off := m.output.YOffset
	top := m.tail.lines[off]

	feed(all[1800:1900])
	if m.output.YOffset != off || m.output.AtBottom() {
		t.Fatalf("new output yanked the pane: offset %d → %d", off, m.output.YOffset)
	}

	// Push past the cap: the tail drops lines from the front, and the
	// offset moves with the text so the same line stays on top.
	feed(all[1900:])
	if n := len(m.tail.lines); n != maxVisibleLines {
		t.Fatalf("tail holds %d lines, want %d", n, maxVisibleLines)
	}
	if got := m.tail.lines[m.output.YOffset]; got != top {
		t.Fatalf("top line moved under the reader: %q, want %q", got, top)
	}

	m.output.GotoBottom()
	feed([]string{"fresh"})
	if !m.output.AtBottom() || !strings.Contains(m.output.View(), "fresh") {
		t.Fatalf("back at the bottom the pane should follow again:\n%s", m.output.View())
	}
}

// TestStreamAlwaysLogKeepsFullOutput: a 20k-line alwaysLog step logs every
// line in order, while the pane keeps only the newest maxVisibleLines.
func TestStreamAlwaysLogKeepsFullOutput(t *testing.T) {
	useLogDir(t, t.TempDir())
	const n = 20000
	m, pump := streamingModel(step{title: "Updating…", logLabel: "Updated",
		category: logCategoryTopgrade, alwaysLog: true}, n+1)
	lines := numbered(n)
	for _, l := range lines {
		m.stream <- streamMsg{line: l}
	}
	m.stream <- streamMsg{done: true}

	updates := 0
	for m.queue[0].status == statusRunning {
		next, cmd := m.Update(pump())
		m = next.(model)
		pump = cmd
		updates++
	}
	if m.queue[0].status != statusOK {
		t.Fatalf("step status = %d, want OK", m.queue[0].status)
	}
	if want := (n + streamBatchMax - 1) / streamBatchMax; updates != want {
		t.Fatalf("%d lines took %d updates, want %d batches", n, updates, want)
	}

	if len(m.tail.lines) != maxVisibleLines || m.tail.lines[0] != lines[n-maxVisibleLines] || m.tail.lines[maxVisibleLines-1] != lines[n-1] {
		t.Fatalf("pane should hold lines %d..%d, holds %d lines starting %q",
			n-maxVisibleLines, n-1, len(m.tail.lines), m.tail.lines[0])
	}

	data, err := os.ReadFile(logPath(logCategoryTopgrade))
	if err != nil {
		t.Fatalf("alwaysLog step not logged: %v", err)
	}
	log := string(data)
	want := "--- Updated output ---\n" + strings.Join(lines, "\n") + "\n--- end output ---\n"
	if !strings.Contains(log, want) {
		t.Fatalf("log should hold all %d lines in order; got %d bytes", n, len(log))
	}
}

// killHolder SIGKILLs the background process a shim step reported with a
// "holder <pid>" line, so a test never leaves its `sleep 30` behind.
func killHolder(t *testing.T, line string) {
	t.Helper()
	pid, err := strconv.Atoi(strings.TrimPrefix(line, "holder "))
	if err != nil || pid <= 0 {
		t.Fatalf("no holder pid in %q", line)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
}

// waitGoroutines fails t unless the goroutine count falls back to base.
func waitGoroutines(t *testing.T, base int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base {
		t.Fatalf("goroutines: %d before, %d after — leaked %d", base, n, n-base)
	}
}

// TestStreamStepNotHeldByBackgroundChild: a step whose child starts something
// in the background that keeps the output pipe (a daemon, a helper topgrade
// leaves running) is done once the child exits — within stepWaitDelay, not
// when the background process lets go. Every line the child wrote comes
// before exactly one done, which carries the child's own exit status (a
// clean exit is a success, not exec's ErrWaitDelay), and nothing is left
// running behind it.
func TestStreamStepNotHeldByBackgroundChild(t *testing.T) {
	old := stepWaitDelay
	stepWaitDelay = 200 * time.Millisecond
	defer func() { stepWaitDelay = old }()

	for _, tc := range []struct {
		exit int
		want func(error) bool
	}{
		{0, func(err error) bool { return err == nil }},
		{3, func(err error) bool {
			var ee *exec.ExitError
			return errors.As(err, &ee) && ee.ExitCode() == 3
		}},
	} {
		t.Run(fmt.Sprintf("exit%d", tc.exit), func(t *testing.T) {
			base := runtime.NumGoroutine()
			ch := make(chan streamMsg, 16)
			script := fmt.Sprintf(`echo one; sleep 30 & echo "holder $!"; echo two; exit %d`, tc.exit)
			began := time.Now()
			if p, _ := runStepUntil(step{command: "bash", args: []string{"-c", script}}, ch, nil); p == nil {
				t.Fatal("step did not start")
			}

			var lines []string
			var done streamMsg
			timeout := time.After(5 * time.Second)
		read:
			for {
				select {
				case msg := <-ch:
					if msg.done {
						done = msg
						break read
					}
					lines = append(lines, msg.line)
					if strings.HasPrefix(msg.line, "holder ") {
						killHolder(t, msg.line)
					}
				case <-timeout:
					t.Fatalf("no done after 5s (lines so far %q): the background sleep held the step open", lines)
				}
			}
			elapsed := time.Since(began)

			if len(lines) != 3 || lines[0] != "one" || !strings.HasPrefix(lines[1], "holder ") || lines[2] != "two" {
				t.Fatalf("lines = %q, want one, holder <pid>, two", lines)
			}
			if !tc.want(done.err) {
				t.Fatalf("done err = %v, want the child's own exit %d", done.err, tc.exit)
			}
			// The sleep really held the pipe (else this proves nothing), and
			// only for about stepWaitDelay.
			if elapsed < stepWaitDelay || elapsed > stepWaitDelay+2*time.Second {
				t.Fatalf("done after %v, want about stepWaitDelay (%v)", elapsed, stepWaitDelay)
			}
			select {
			case msg := <-ch:
				t.Fatalf("message after the done: %#v", msg)
			case <-time.After(100 * time.Millisecond):
			}
			waitGoroutines(t, base, 2*time.Second)
		})
	}
}

// benchStream measures what streaming n lines of output costs the UI thread:
// every message the pump delivers goes through Update and is followed by a
// View, as Bubble Tea's renderer does. The whole output is already buffered on
// the step's channel when the pump starts — a fast producer (topgrade, pacman
// -Qk) that stays ahead of the UI, which is the case that used to stall it.
func benchStream(b *testing.B, n int) {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("(%5d/%d) \x1b[1mupgrading\x1b[0m package-%d  [###########------] 64%%", i+1, n, i)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		m, pump := streamingModel(step{title: "Streaming…", logLabel: "Streamed"}, n+1)
		for _, l := range lines {
			m.stream <- streamMsg{line: l}
		}
		m.stream <- streamMsg{done: true}
		b.StartTimer()

		for m.queue[0].status == statusRunning {
			next, cmd := m.Update(pump())
			m = next.(model)
			_ = m.View()
			pump = cmd
		}
	}
}

func BenchmarkStreamView5k(b *testing.B)  { benchStream(b, 5000) }
func BenchmarkStreamView20k(b *testing.B) { benchStream(b, 20000) }
