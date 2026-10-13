package tui

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLogsStayPlainWhateverNoColour (QA b8 tail): the pane's colour follows
// NO_COLOR, the logs never hold an escape: with NO_COLOR empty (the pane
// shows the bold) and set (it does not; "0" included), every file under the
// state dir is plain text.
func TestLogsStayPlainWhateverNoColour(t *testing.T) {
	for _, noColour := range []string{"", "1", "0"} {
		t.Run("NO_COLOR="+noColour, func(t *testing.T) {
			transcriptEnv(t, false)
			t.Setenv("NO_COLOR", noColour)
			shimLog := installShims(t, alwaysBoldTopgrade)
			pane := run(t, shimLog, "Run topgrade", Answer{"confirm", "Run a system update with topgrade", "yes"}).m.tail.String()
			if bold := strings.Contains(pane, "\x1b[1m"); bold != (noColour == "") {
				t.Errorf("pane %q: bold = %v with NO_COLOR=%q", pane, bold, noColour)
			}
			files := 0
			err := filepath.WalkDir(logs().Root(), func(p string, e fs.DirEntry, err error) error {
				if err != nil || e.IsDir() {
					return err
				}
				b, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				files++
				if strings.Contains(string(b), "\x1b") || strings.Contains(string(b), `\u001b`) {
					t.Errorf("%s holds an escape: %q", p, b)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if files < 2 {
				t.Errorf("only %d files under %s", files, logs().Root())
			}
		})
	}
}
