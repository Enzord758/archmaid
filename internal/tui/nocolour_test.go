package tui

import (
	"strings"
	"testing"

	tt "github.com/Enzord758/archmaid/internal/testtranscript"
)

// alwaysBoldTopgrade is a topgrade shim whose line has bold SGR whatever
// its environment says, as yay's ":: Searching AUR for updates..." does
// inside topgrade (Dev 5's VM round b8).
var alwaysBoldTopgrade = Shims{
	"sudo":     sudoRules(true),
	"topgrade": {{Prefix: "-y", Script: `printf ':: \033[1mSearching AUR for updates...\033[0m\n'`}},
}

// TestNoColourStripsUserStepSGR: NO_COLOR means no SGR anywhere. A user
// step that colours regardless of the environment shows its bold in the
// pane without NO_COLOR, and plain text with it, on the TUI's model and
// through the binary's real Driver alike.
func TestNoColourStripsUserStepSGR(t *testing.T) {
	const bold, plain = ":: \x1b[1mSearching AUR for updates...\x1b[0m\n", ":: Searching AUR for updates...\n"
	for _, real := range []bool{false, true} {
		name := "tui"
		if real {
			name = "real-driver"
		}
		for _, noColour := range []string{"", "1"} {
			t.Run(name+"/NO_COLOR="+noColour, func(t *testing.T) {
				transcriptEnv(t, false)
				t.Setenv("NO_COLOR", noColour)
				shimLog := installShims(t, alwaysBoldTopgrade)
				gate := Answer{"confirm", "Run a system update with topgrade", "yes"}
				var pane string
				if real {
					var tg *realDriverTarget
					r := record(t, func(t *testing.T, shimLog string) tt.Target {
						tg = newRealDriverTarget(t, shimLog)
						return tg
					}, shimLog, "Run topgrade", gate)
					tg.finish(t)
					for _, l := range strings.Split(r.Transcript(), "\n") {
						if s, ok := strings.CutPrefix(l, "out   2 "); ok {
							pane += s + "\n"
						}
					}
				} else {
					pane = run(t, shimLog, "Run topgrade", gate).m.tail.String()
				}
				want := bold
				if noColour != "" {
					want = plain
				}
				if pane != want {
					t.Errorf("pane %q, want %q", pane, want)
				}
			})
		}
	}
}
