package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/logstore"
	tt "github.com/Enzord758/archmaid/internal/testtranscript"
)

// colourTopgrade is a topgrade shim that colours its lines only when
// CLICOLOR_FORCE asks, as topgrade's console crate does into a pipe.
var colourTopgrade = Shims{
	"sudo": sudoRules(true),
	"topgrade": {{Prefix: "-y", Script: `if [ "${CLICOLOR_FORCE:-0}" != 0 ]; then
	printf '\033[1m\033[34m== System update ==\033[0m\nSystem update: \033[1m\033[32mOK\033[0m\n'
else
	printf '== System update ==\nSystem update: OK\n'
fi`}},
}

const (
	colourPane = "\x1b[1m\x1b[34m== System update ==\x1b[0m\nSystem update: \x1b[1m\x1b[32mOK\x1b[0m\n"
	plainPane  = "== System update ==\nSystem update: OK\n"
)

// stamp is a log entry's timestamp, the only part of a log that differs
// between two runs.
var stamp = regexp.MustCompile(`(?m)^\[[0-9-]+ [0-9:]+\] `)

// TestTopgradeColourShownLogsPlain: Run topgrade on the TUI (sudo path),
// with a topgrade that colours only under CLICOLOR_FORCE. The step's
// child gets the switch (actions/colour.go), so the output pane shows the
// colour, on the TUI's model and through the binary's real Driver alike;
// with NO_COLOR it doesn't. Either way topgrade.log and last-run.json (what
// the DMS widget reads) are the same plain text: no escape reaches them.
func TestTopgradeColourShownLogsPlain(t *testing.T) {
	type result struct{ pane, log string }
	runOnce := func(t *testing.T, noColour string, real bool) result {
		t.Helper()
		transcriptEnv(t, false)
		t.Setenv("NO_COLOR", noColour)
		shimLog := installShims(t, colourTopgrade)
		gate := Answer{"confirm", "Run a system update with topgrade", "yes"}
		var pane string
		if real {
			var tg *realDriverTarget
			r := record(t, func(t *testing.T, shimLog string) tt.Target {
				tg = newRealDriverTarget(t, shimLog)
				return tg
			}, shimLog, "Run topgrade", gate)
			tg.finish(t)
			// The topgrade step is the second (the transcript counts from 1).
			for _, l := range strings.Split(r.Transcript(), "\n") {
				if s, ok := strings.CutPrefix(l, "out   2 "); ok {
					pane += s + "\n"
				}
			}
		} else {
			pane = run(t, shimLog, "Run topgrade", gate).m.tail.String()
		}
		b, err := os.ReadFile(logPath(logCategoryTopgrade))
		if err != nil {
			t.Fatal(err)
		}
		rec := readLastRun(t)
		if rec.Action != "topgrade" || rec.Outcome != "ok" || rec.StepsFailed != 0 || rec.DryRun {
			t.Errorf("last-run.json %+v, want topgrade ok", rec)
		}
		raw, err := os.ReadFile(filepath.Join(logs().Root(), logstore.LastRunFile))
		if err != nil || strings.ContainsAny(string(raw), "\x1b\x07") || strings.Contains(string(raw), `\u001b`) {
			t.Errorf("last-run.json holds an escape (err %v): %q", err, raw)
		}
		if strings.ContainsAny(string(b), "\x1b\x07\r") {
			t.Errorf("topgrade.log holds an escape: %q", b)
		}
		return result{pane, stamp.ReplaceAllString(string(b), "[T] ")}
	}

	for _, real := range []bool{false, true} {
		name := "tui"
		if real {
			name = "real-driver"
		}
		t.Run(name, func(t *testing.T) {
			var colour, plain result
			t.Run("colour", func(t *testing.T) { colour = runOnce(t, "", real) })
			t.Run("NO_COLOR", func(t *testing.T) { plain = runOnce(t, "1", real) })
			if colour.pane != colourPane {
				t.Errorf("pane with colour on: %q, want %q", colour.pane, colourPane)
			}
			if plain.pane != plainPane {
				t.Errorf("pane under NO_COLOR: %q, want %q", plain.pane, plainPane)
			}
			if colour.log != plain.log || !strings.Contains(colour.log, plainPane) {
				t.Errorf("topgrade.log differs with colour on:\n%s\nvs NO_COLOR:\n%s", colour.log, plain.log)
			}
		})
	}
}
