package tui

import (
	"os"
	"strings"
	"testing"

	tt "github.com/Enzord758/archmaid/internal/testtranscript"
)

// redrawTopgrade is a topgrade shim that redraws its output as terminal
// tools do (#129): an ollama-like spinner on one line, a "\r" progress bar,
// and a line rewritten with the cursor moved up (ESC[1A). The pauses let
// each frame reach the pane before the next replaces it.
var redrawTopgrade = Shims{
	"sudo": sudoRules(true),
	"topgrade": {{Prefix: "-y", Script: `printf 'pulling manifest \342\240\213'; sleep 0.15
printf '\rpulling manifest \342\240\231'; sleep 0.15
printf '\rpulling manifest done\033[K\n'
printf '[#   ]  25%%'; sleep 0.15; printf '\r[##  ]  50%%'; sleep 0.15; printf '\r\033[32m[####] 100%%\033[0m\n'
printf 'one\ntwo\n'; sleep 0.15; printf '\033[1Atwo, final\033[K\n'`}},
}

// redrawFinal is what is left of redrawTopgrade's output, in the pane and
// (plain) in topgrade.log: only each line's final text.
var redrawFinal = []string{"pulling manifest done", "\x1b[32m[####] 100%\x1b[0m", "one", "two, final"}

// TestRedrawsEndAsFinalLines (#129): Run topgrade on the TUI with a
// topgrade that redraws. On the TUI's model and through the binary's real
// Driver alike the pane ends with the final lines, not every frame, and
// topgrade.log has only the final text. Fails on ecb0903, where every
// frame stayed (the spinner's frames glued into one line, the bar's too,
// and "two" kept beside "two, final").
func TestRedrawsEndAsFinalLines(t *testing.T) {
	plainFinal := []string{"pulling manifest done", "[####] 100%", "one", "two, final"}
	for _, real := range []bool{false, true} {
		name := "tui"
		if real {
			name = "real-driver"
		}
		t.Run(name, func(t *testing.T) {
			transcriptEnv(t, false)
			t.Setenv("NO_COLOR", "")
			shimLog := installShims(t, redrawTopgrade, "sleep")
			gate := Answer{"confirm", "Run a system update with topgrade", "yes"}
			var pane []string
			if real {
				var tg *realDriverTarget
				record(t, func(t *testing.T, shimLog string) tt.Target {
					tg = newRealDriverTarget(t, shimLog)
					return tg
				}, shimLog, "Run topgrade", gate)
				tg.finish(t)
				pane = tg.panes[1] // the topgrade step is the second
			} else {
				pane = run(t, shimLog, "Run topgrade", gate).m.tail.lines
			}
			if strings.Join(pane, "\n") != strings.Join(redrawFinal, "\n") {
				t.Errorf("pane %q, want %q", pane, redrawFinal)
			}
			b, err := os.ReadFile(logPath(logCategoryTopgrade))
			if err != nil {
				t.Fatal(err)
			}
			log := string(b)
			if !strings.Contains(log, strings.Join(plainFinal, "\n")+"\n") {
				t.Errorf("topgrade.log lacks the final lines %q:\n%s", plainFinal, log)
			}
			for _, frame := range []string{"\u280b", "\u2819", "25%", "50%", "two\n"} {
				if strings.Contains(log, frame) {
					t.Errorf("topgrade.log keeps the frame %q:\n%s", frame, log)
				}
			}
			if strings.ContainsAny(log, "\x1b\r") {
				t.Errorf("topgrade.log holds an escape: %q", log)
			}
		})
	}
}

// TestPaneRedraw: the pane's tail applies OutputChunk.Replace within what
// it got since its last gap; what it can't remove is gone already, so the
// new lines all go in.
func TestPaneRedraw(t *testing.T) {
	tl := newLineTail(5)
	tl.write(0, false, []string{"a", "b", "c"})
	tl.write(1, false, []string{"C", "d"})
	if got := strings.Join(tl.lines, ","); got != "a,b,C,d" {
		t.Fatalf("after a redraw of 1: %s", got)
	}
	tl.write(2, true, []string{"x", "y", "z"}) // a gap (Dropped) follows the removal
	if got := strings.Join(tl.lines, ","); got != "a,b,x,y,z" {
		t.Fatalf("after a redraw with a gap: %s", got)
	}
	tl.write(9, false, []string{"1", "2", "3"}) // reaches past the gap: only x, y, z go
	if got := strings.Join(tl.lines, ","); got != "a,b,1,2,3" {
		t.Fatalf("a redraw reaching past the gap: %s", got)
	}
	tl.write(0, false, []string{"4", "5", "6", "7"}) // the cap of 5 drops the oldest
	tl.write(9, false, []string{"8"})                // and limits a redraw to what it holds
	if got := strings.Join(tl.lines, ","); got != "8" {
		t.Fatalf("a redraw past the cap: %s", got)
	}
	tl.reset()
	tl.write(3, false, []string{"p", "q", "r", "s"}) // nothing to replace after a reset
	if got := strings.Join(tl.lines, ","); got != "p,q,r,s" {
		t.Fatalf("after a reset: %s", got)
	}
}
