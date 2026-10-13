package realdriver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/steps"
)

// ptyTopgrade is a topgrade shim that reports what its streams are and
// redraws its output as terminal tools do: an ollama-like spinner, a "\r"
// progress bar and a line rewritten with the cursor moved up. The pauses
// let each frame reach the Run page before the next replaces it.
const ptyTopgrade = `#!/bin/sh
[ -t 0 ] && echo stdin=tty || echo stdin=notty
[ -t 1 ] && echo stdout=tty || echo stdout=notty
[ -t 2 ] && echo stderr=tty >&2 || echo stderr=notty >&2
( : </dev/tty ) 2>/dev/null && echo devtty=open || echo devtty=fails
echo "tty_nr=$(sed 's/.*) //' /proc/$$/stat | cut -d' ' -f5)"
echo "size=$(stty size <&2) TERM=$TERM"
printf 'pulling manifest \342\240\213'; sleep 0.15
printf '\rpulling manifest \342\240\231'; sleep 0.15
printf '\rpulling manifest done\033[K\n'
printf '[#   ]  25%%'; sleep 0.15; printf '\r[##  ]  50%%'; sleep 0.15; printf '\r\033[32m[####] 100%%\033[0m\n'
printf 'one\ntwo\n'; sleep 0.15; printf '\033[1Atwo, final\033[K\n'
`

// TestRunPagePty (#129): Run topgrade on the GUI's real Driver, its
// Events applied to the Run page's model. The step's child writes to a pty
// as wide as the Run view (Config.TermCols), never its controlling
// terminal: stdout and stderr are terminals, stdin isn't, /dev/tty can't
// be opened and tty_nr is 0, so sudo under it still finds no terminal. Its
// redraws end as the final lines on the page and in topgrade.log. Fails on
// ecb0903: the child wrote into a pipe (stdout=notty, no size) and every
// frame stayed on the page.
func TestRunPagePty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "topgrade"), []byte(ptyTopgrade), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NO_COLOR", "")
	cfg := fakes(t, Config{NoAskpass: true, TermCols: func() int { return 97 }})
	w := Start(cfg)
	defer w.Close()
	d := w.New(w.Options())
	defer d.Close()

	m := runview.NewModel()
	frames := 0
	d.Send(engine.Start{Action: actions.IDTopgrade})
	timeout := time.After(20 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-d.Events():
			if !ok {
				t.Fatal("Events closed early")
			}
			m.Apply([]engine.Event{ev})
			switch ev := ev.(type) {
			case engine.OutputChunk:
				if ev.Replace > 0 {
					frames++
				}
			case engine.NeedsConfirm:
				d.Send(engine.Confirm{Prompt: ev.ID, Yes: true})
			case engine.Finished:
				if ev.Outcome != engine.OutcomeCompleted {
					t.Fatalf("outcome %v", ev.Outcome)
				}
				break loop
			}
		case <-timeout:
			t.Fatal("no Finished")
		}
	}
	want := []string{"stdin=notty", "stdout=tty", "stderr=tty", "devtty=fails", "tty_nr=0",
		"size=50 97 TERM=xterm-256color", "pulling manifest done", "[####] 100%", "one", "two, final"}
	if got := m.StepText(steps.IDTopgrade); !slices.Equal(got, want) {
		t.Fatalf("the Run page shows\n%q\nwant\n%q", got, want)
	}
	if frames == 0 {
		t.Error("no chunk redrew a line: the frames went out as new lines")
	}
	if l, _ := m.StepLine(steps.IDTopgrade, 7); len(l.Spans) != 1 {
		t.Errorf("the bar lost its colour: %+v", l)
	}
	logs, err := filepath.Glob(filepath.Join(cfg.StateDir, "*", "topgrade.log"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("topgrade.log: %v %v", logs, err)
	}
	b, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	if log := string(b); !strings.Contains(log, strings.Join(want[6:], "\n")+"\n") ||
		strings.Contains(log, "25%") || strings.ContainsAny(log, "\x1b\r") {
		t.Errorf("topgrade.log is not the final plain lines:\n%s", log)
	}
}
