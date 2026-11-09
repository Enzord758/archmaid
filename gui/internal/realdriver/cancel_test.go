package realdriver

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/gui/internal/polkitagent/agenttest"
	"github.com/Enzord758/archmaid/gui/internal/runview"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/helperclient"
	"github.com/Enzord758/archmaid/internal/helperclient/helpertest"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/steps"
)

// cancelShims puts pkexec, su, systemctl and pkttyagent shims first on
// PATH (each ahead of testenv's trap) and returns their dir. pkexec hands
// its request to the pkttyagent shim (agenttest.Shim) when one is
// registered and exits 127 unless the agent says ok, as polkitd and
// pkexec do; with no agent of ours it stands for the desktop's agent,
// whose dialog the user cancelled (archmaid can't see it): 127. su takes
// only the shim's password. systemctl answers from internal/actions'
// fixture machine (enabled-but-inactive units), so Disable unused services
// finds something to disable.
func cancelShims(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	pkexec := `#!/bin/sh
printf '%s\n' "$*" >> ` + q(dir+"/pkexec.log") + `
[ "$1" = ` + helperclient.DisableInternalAgent + ` ] && [ "$2" = ` + helperclient.HelperPath + ` ] || exit 126
verb=$3
agent=$(cat ` + q(dir+"/agent.pid") + ` 2>/dev/null)
if [ -n "$agent" ] && kill -0 "$agent" 2>/dev/null; then
	rm -f ` + q(dir+"/result") + `
	printf 'org.archmaid.helper.%s' "$verb" > ` + q(dir+"/request.tmp") + `
	mv ` + q(dir+"/request.tmp") + ` ` + q(dir+"/request") + `
	i=0
	while [ ! -f ` + q(dir+"/result") + ` ] && [ $i -lt 1500 ] && kill -0 "$agent" 2>/dev/null; do sleep 0.02; i=$((i+1)); done
	[ "$(cat ` + q(dir+"/result") + ` 2>/dev/null)" = ok ] || exit 127
else
	printf '%s\n' "$verb" >> ` + q(dir+"/desktop.log") + `
	exit 127
fi
printf '{"v":1,"t":"start","verb":"%s","version":"0.1.0"}\n' "$verb"
printf '{"v":1,"t":"result","ok":true,"code":0,"child_exit":0,"skipped":[]}\n'
`
	fix, err := filepath.Abs("../../../internal/actions/testdata")
	if err != nil {
		t.Fatal(err)
	}
	systemctl := `#!/bin/sh
case "$1" in
list-units) cat ` + q(filepath.Join(fix, "systemctl_list_units.txt")) + ` ;;
is-enabled)
	shift; [ "$1" = -- ] && shift
	for u; do
		awk -v u="$u" '$1==u {print $2; f=1} END {exit !f}' ` + q(filepath.Join(fix, "systemctl_list_unit_files.txt")) + ` ||
			echo "Failed to get unit file state for $u: No such file or directory" >&2
	done
	exit 1 ;;
*) exit 1 ;;
esac
`
	shims := map[string]string{"pkexec": pkexec, "su": "#!/bin/sh\nread pw\n[ \"$pw\" = right ]\n",
		"systemctl": systemctl, "pkttyagent": agenttest.Shim}
	for name, body := range shims {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	stand := filepath.Join(dir, "archmaid-helper")
	if err := os.WriteFile(stand, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	helpertest.UseHelperFile(t, stand)
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AGENT_DIR", dir)
	t.Setenv("AGENT_USER", me.Username)
	t.Setenv("AGENT_WANT", "right")
	return dir
}

// TestServicesPolkitCancel runs Disable unused services on the real
// Driver, with archmaid's own polkit agent (the pkttyagent shim) asking
// for its one helper call, and ends that call four ways. Only a Cancel in
// archmaid's own dialog is the user's: the run is cancelled with nothing
// refused, in the Finished event (the Run page and its toast), in
// last-run.json (the Overview and the DMS widget, which read it) and in
// the Overview's summary of it. The others are polkit's refusals and stay
// "not authorised": three wrong passwords at our dialog (su refuses each,
// so polkit never sees one, and the third gives up on the agent's
// request), and a cancel at the desktop's dialog when archmaid has no
// agent (pkexec's 127 is all archmaid sees).
func TestServicesPolkitCancel(t *testing.T) {
	for _, c := range []struct {
		name, agent string
		answer      func(d engine.Driver, np engine.NeedsPassword)
		outcome     engine.Outcome
		refused     int
		finished    string // what the Run page's status line says
	}{
		{name: "own dialog cancelled", answer: func(d engine.Driver, np engine.NeedsPassword) {
			d.Send(engine.CancelPrompt{Prompt: np.ID})
		}, outcome: engine.OutcomeCancelled, refused: 0,
			finished: "Finished: cancelled"},
		{name: "three wrong passwords", answer: func(d engine.Driver, np engine.NeedsPassword) {
			d.Send(engine.SubmitPassword{Prompt: np.ID, Password: "wrong"})
		}, outcome: engine.OutcomeCompleted, refused: 1,
			finished: "Finished: not authorised"},
		{name: "desktop dialog cancelled", agent: "-", outcome: engine.OutcomeCompleted, refused: 1,
			finished: "Finished: not authorised"},
	} {
		for _, seeded := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/previous record %v", c.name, seeded), func(t *testing.T) {
				dir := cancelShims(t)
				cfg := fakes(t, Config{NoAskpass: true, TTYAgent: c.agent})
				// The run before this one, a real one: nothing that ends without
				// a step having run may replace it (#276).
				var seed []byte
				if seeded {
					os.MkdirAll(cfg.StateDir, 0o700)
					seed = []byte(`{"v":1,"action":"fullcleanup","started":"2026-11-09T10:00:00Z","finished":"2026-11-09T10:05:00Z","outcome":"ok","steps_failed":0,"steps_skipped":0,"steps_refused":0,"dry_run":false}` + "\n")
					if err := os.WriteFile(filepath.Join(cfg.StateDir, logstore.LastRunFile), seed, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				w := Start(cfg)
				defer w.Close()
				d := w.New(w.Options())
				defer d.Close()
				d.Send(engine.Start{Action: actions.IDServices})
				var evs []engine.Event
				var fin engine.Finished
				asked := 0
				timeout := time.After(30 * time.Second)
			loop:
				for {
					select {
					case ev, ok := <-d.Events():
						if !ok {
							t.Fatal("Events closed early")
						}
						evs = append(evs, ev)
						switch ev := ev.(type) {
						case engine.NeedsSelection:
							d.Send(engine.Select{Prompt: ev.ID, Picked: ev.Items})
						case engine.NeedsPassword:
							if !ev.Polkit || c.answer == nil {
								t.Fatalf("unexpected prompt %+v", ev)
							}
							asked++
							c.answer(d, ev)
						case engine.Finished:
							fin = ev
							break loop
						}
					case <-timeout:
						t.Fatalf("no Finished; events %#v", evs)
					}
				}
				if c.answer != nil && asked == 0 {
					t.Error("archmaid's dialog never asked")
				}
				var done []engine.StepDone
				for _, ev := range evs {
					if sd, ok := ev.(engine.StepDone); ok {
						done = append(done, sd)
					}
				}
				want := steps.AuthRefused
				if c.refused == 0 {
					want = steps.Skipped
				}
				if len(done) != 1 || done[0].Status != want {
					t.Errorf("StepDone %+v, want one with status %d", done, want)
				}
				if fin.Outcome != c.outcome || fin.Refused != c.refused || fin.Errs != 0 {
					t.Errorf("Finished = %+v, want %v with %d refused", fin, c.outcome, c.refused)
				}
				// Nothing ran, so the run is no "last run": the previous record
				// stays byte for byte (or none appears), and the cancelled
				// attempt leaves no run folder behind either.
				got, rerr := os.ReadFile(filepath.Join(w.cfg.StateDir, logstore.LastRunFile))
				if seeded {
					if rerr != nil || string(got) != string(seed) {
						t.Errorf("last-run.json = %q (%v), want the previous record unchanged", got, rerr)
					}
					rec, err := logstore.New(logstore.Options{Dir: w.cfg.StateDir}).ReadLastRun()
					if err != nil || rec.Action != "fullcleanup" || rec.Outcome != logstore.OutcomeOK {
						t.Errorf("ReadLastRun = %+v, %v", rec, err)
					}
					if s := overview.Summarize(rec, rec.Finished); len(s.Issues) != 0 {
						t.Errorf("Overview issues %q, want the old ok run", s.Issues)
					}
				} else if !errors.Is(rerr, os.ErrNotExist) {
					t.Errorf("last-run.json = %q (%v), want none: nothing ran", got, rerr)
				}
				// ... and no real run folder: what a run in which nothing ran
				// logs is only stub entries (CHECK, SKIP), which the store does
				// not count towards KeepRuns (logstore, stubLog).
				ents, _ := os.ReadDir(w.cfg.StateDir)
				for _, e := range ents {
					if !e.IsDir() {
						continue
					}
					logs, _ := os.ReadDir(filepath.Join(w.cfg.StateDir, e.Name()))
					for _, l := range logs {
						text := readFile(t, filepath.Join(w.cfg.StateDir, e.Name(), l.Name()))
						if regexp.MustCompile(`\] (OK|FAIL|DRYRUN|ABORT): `).MatchString(text) {
							t.Errorf("run folder %s holds %s with a step's entry: %s", e.Name(), l.Name(), text)
						}
					}
				}
				m := runview.NewModel()
				m.Apply(evs)
				if status, _ := m.FinishedText(fin); !strings.HasPrefix(status, c.finished) {
					t.Errorf("Run page %q, want %q", status, c.finished)
				}
				// pkexec ran once, for the two picked units' one call; the
				// desktop case reached only the stand-in for the desktop's agent.
				if got := readFile(t, filepath.Join(dir, "pkexec.log")); strings.Count(got, "disable-unit") != 1 {
					t.Errorf("pkexec calls %q", got)
				}
				if _, err := os.Stat(filepath.Join(dir, "desktop.log")); (err == nil) != (c.agent == "-") {
					t.Errorf("the desktop's agent asked: %v, want %v", err == nil, c.agent == "-")
				}
			})
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
