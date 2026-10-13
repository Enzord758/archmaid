package ui

// TestGUIMinWidth's real-narrow measures the real plan; this one measures
// the states a user can reach that carry long text: a failed run with a
// long error in a row, the refused and locked-out rows, a discovery
// result, every kind of dialog (gate with a plan, orphans, downloads,
// audit gate, the three password dialogs, the services pick, the audit
// report, the quit question), Settings with long values, the Logs page
// with long lines and the last-run cell with a long issue. Each at the
// window's width (the parent sets it): the content's measured minimum
// must fit it, and GTK must log nothing.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/downloads"
	"github.com/Enzord758/archmaid/internal/engine"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

func init() { covScenarios["narrow-states"] = narrowStates }

// Long text in the shapes users meet: a package name that is one word, a
// path, a unit name, a sentence.
var (
	longWord = strings.Repeat("a", 90)
	longPkg  = "texlive-latex-extra-documentation-with-an-unreasonably-long-package-name-for-testing"
	longPath = "/home/someone/.local/state/archmaid/runs/2026-10-12_09-30-00_full-cleanup/" + strings.Repeat("deeply-nested-folder/", 6) + "file.log"
	longUnit = "systemd-journald-audit-and-other-things-with-a-long-instance-name@" + strings.Repeat("0123456789", 6) + ".service"
	longText = "The step failed because the thing it needs is not there and the explanation of why is long enough to wrap over several lines in a narrow window"
)

// realDownloads are names as a real Downloads folder holds them, sorted as
// the scan sorts them: installers, archives with versions, a screenshot
// with spaces, a long video title and a folder; more than the confirm's
// text lists (it counts the rest).
var realDownloads = []string{
	"Firefox Setup 131.0.3.exe",
	"Invoice_2026-08_Order-1234567890_Nordisk-Fjernvarme-og-Energiselskab-ApS.pdf",
	"NVIDIA-Linux-x86_64-615.71.run",
	"Screenshot from 2026-08-14 21-33-07.png",
	"archlinux-2026.09.01-x86_64.iso",
	"jetbrains-toolbox-2.5.2.35332.tar.gz",
	"linux-firmware-20260910.r1234.gabcdef0123456789-1-any.pkg.tar.zst",
	"proton-ge-custom-GE-Proton10-17",
	"steamcmd_linux.tar.gz",
	"texlive-2026-full-installation-image-with-all-collections-and-documentation-x86_64.iso",
	"ubuntu-24.04.3-desktop-amd64.iso",
	"y2mate.com - Some_Really_Long_Video_Title_With_Underscores_That_Never_Ends_1080p.mp4",
}

// narrowStates walks the states one after another, each measured once its
// frames have been drawn.
func narrowStates(c *covEnv) {
	w := c.w
	c.quiet()
	var steps_ []func()
	add := func(f ...func()) { steps_ = append(steps_, f...) }
	// tagsAt checks the drawn rows' tags (checkTags).
	tagsAt := func(m string) func() {
		return func() { c.ok(checkTags(c, m) > 0, "%s: no step header with tags drawn", m) }
	}
	measure := func(m string) func() {
		return func() {
			c.rep.Narrow = append(c.rep.Narrow, narrowMeasure(w, m))
			shoot(w, fmt.Sprintf("narrow-states-%d", w.win.Width()), m)
		}
	}
	// ask feeds a prompt, measures its dialog, then answers it with resp.
	// dry and kept are what the next dialogs are built with (a dry run's
	// banner, the kept-password note): set when the prompt is fed, as the
	// steps run, not when they are listed.
	var dry, kept bool
	// dialogChecks are a dialog's own checks, by its moment's name.
	dialogChecks := map[string]func(labels []string){}
	ask := func(name string, id engine.PromptID, ev engine.Event, resp string) {
		dry, kept := dry, kept
		if np, ok := ev.(engine.NeedsPassword); ok {
			np.Keeps = kept // the Driver's word that the password is kept
			ev = np
		}
		var wasDry, wasKept bool
		add(func() {
			wasDry, wasKept = w.drvDry, w.opts.KeepsPassword
			w.drvDry, w.opts.KeepsPassword = dry, kept
			c.feed(ev)
		}, func() {
			measure(name)()
			w.drvDry, w.opts.KeepsPassword = wasDry, wasKept
			d := w.dialogs[id]
			if d == nil {
				c.fail("%s: no dialog for prompt %d", name, id)
				return
			}
			if cf, ok := ev.(engine.NeedsConfirm); ok && len(cf.Plan) > 0 {
				// The gate's rail: its tags under the titles in the
				// bottom sheet, as every narrow row has them.
				c.ok(checkRailTags(c, d, name) > 0, "%s: no rail step with tags drawn", name)
			}
			labels := labelsUnder(&d.Widget)
			c.ok(slices.Contains(labels, dryBannerText) == dry, "%s: the dry run's banner shown %v, want %v", name, !dry, dry)
			c.ok(slices.Contains(labels, KeptNote) == kept, "%s: the kept-password note shown %v, want %v", name, !kept, kept)
			if f := dialogChecks[name]; f != nil {
				f(labels)
			}
			c.press(d, resp)
		})
	}
	now := time.Now()
	plan := func(n int) []engine.PlanStep {
		titles := []string{
			"Updating " + longText,
			"Removing " + longPkg,
			longWord,
			"Cleaning " + longPath,
			"Disabling " + longUnit,
			"Short",
		}
		var p []engine.PlanStep
		for i := range n {
			p = append(p, engine.PlanStep{ID: fmt.Sprintf("s%d", i+1), Title: titles[i%len(titles)], NeedsPriv: i%2 == 0, Confirm: i%3 == 0, Category: logstore.Cleanup})
		}
		return p
	}

	// wrapNatural can't loop or blow up: an empty label, spaces, a newline,
	// one very long word and a long sentence, alone and in a tagLine, asked
	// for their size at widths from their minimum to 2000 px: every answer finite, the
	// height never negative, the minimum width at most the natural one, and
	// all of it quick.
	add(func() {
		start := time.Now()
		for _, text := range []string{"", " ", "   ", "\n", "\n\n", longWord, strings.Repeat("a", 1000), longPkg + " " + longPath, longText, "ä" + strings.Repeat("🙂", 300)} {
			l, l2 := gtk.NewLabel(text), gtk.NewLabel(text)
			wrapNatural(l)
			wrapNatural(l2)
			beside := newTagLine(l, 8, gtk.NewLabel("root"))
			under := newTagLine(l2, 8, gtk.NewLabel("root"))
			under.setUnder(true)
			for _, wd := range []gtk.Widgetter{l, beside.box, under.box} {
				b := gtk.BaseWidget(wd)
				minW, natW, _, _ := b.Measure(gtk.OrientationHorizontal, -1)
				c.ok(minW >= 0 && minW <= natW && natW < 1<<20, "wrapNatural(%.20q): width min %d nat %d", text, minW, natW)
				for _, forW := range []int{minW, minW + 1, 100, 360, 2000} {
					forW = max(forW, minW) // asking for less than the minimum is a GTK warning
					minH, natH, _, _ := b.Measure(gtk.OrientationVertical, forW)
					c.ok(minH >= 0 && natH >= minH && natH < 1<<24, "wrapNatural(%.20q) at %d px: height min %d nat %d", text, forW, minH, natH)
				}
			}
		}
		c.ok(time.Since(start) < 2*time.Second, "measuring the wrapNatural labels took %v", time.Since(start))
	})

	// The Run page: a failed run whose rows carry long text, a refused
	// one, a skipped one, then the run's end.
	add(func() {
		w.current = actions.IDFullCleanup
		w.pickPage("run")
		c.feed(
			engine.PhaseChanged{Phase: engine.PhaseRunning},
			engine.QueueStarted{Action: actions.IDFullCleanup, Started: now, Steps: plan(6)},
			engine.StepStarted{ID: "s1", At: now},
			engine.OutputChunk{ID: "s1", Lines: []string{longText, longWord + longWord, longPath, "\x1b[31m" + longPkg + "\x1b[0m"}},
		)
	}, measure("run-going"), tagsAt("run-going"), func() {
		c.feed(
			engine.StepDone{ID: "s1", Index: 0, Status: steps.Failed, Elapsed: 3 * time.Second, Err: longText + " " + longPath},
			engine.StepStarted{ID: "s2", Index: 1, At: now},
			engine.StepDone{ID: "s2", Index: 1, Status: steps.Failed, Elapsed: time.Second, Err: longWord},
			engine.StepDone{ID: "s3", Index: 2, Status: steps.AuthRefused, Err: "no polkit agent is running, so the password can't be asked"},
			engine.StepDone{ID: "s4", Index: 3, Status: steps.AuthRefused, Err: longText},
			engine.StepDone{ID: "s5", Index: 4, Status: steps.Skipped},
			engine.Finished{Action: actions.IDFullCleanup, Outcome: engine.OutcomeCompleted, Elapsed: 5 * time.Second, Errs: 2, Refused: 2, NotRun: 1},
			engine.PhaseChanged{Phase: engine.PhaseIdle},
		)
	}, measure("run-failed"), tagsAt("run-failed"))

	// Waiting for authorisation with one more failure to lock, then the
	// locked-out account's refused row.
	add(func() {
		c.feed(
			engine.PhaseChanged{Phase: engine.PhaseRunning},
			engine.QueueStarted{Action: actions.IDFullCleanup, Started: now, Steps: plan(4)},
			engine.FaillockState{Failures: 2, Deny: 3},
			engine.AuthWaiting{Kind: engine.WaitPolkit, Step: "s1"},
			engine.StepStarted{ID: "s1", At: now},
		)
	}, measure("run-waiting"), func() {
		c.feed(
			engine.AuthResolved{Granted: false},
			engine.StepDone{ID: "s1", Index: 0, Status: steps.AuthRefused},
			engine.FaillockState{Failures: 3, Deny: 3, Locked: true, Unlock: now.Add(10 * time.Minute)},
			engine.StepDone{ID: "s2", Index: 1, Status: steps.AuthRefused},
			engine.Finished{Action: actions.IDFullCleanup, Outcome: engine.OutcomeCompleted, Refused: 2, NotRun: 2},
			engine.PhaseChanged{Phase: engine.PhaseIdle},
		)
	}, measure("run-locked"), tagsAt("run-locked"))

	// Dev 5's VM row: one root step refused with a state line over three
	// lines (its tag stretched to their height).
	add(func() {
		c.feed(
			engine.PhaseChanged{Phase: engine.PhaseRunning},
			engine.QueueStarted{Action: actions.IDFullCleanup, Started: now, Steps: []engine.PlanStep{
				{ID: "o1", Title: "Finding orphaned packages…"}, {ID: "o2", Title: "Removing orphaned packages…", NeedsPriv: true}}},
			engine.StepStarted{ID: "o1", At: now},
			engine.StepDone{ID: "o1", Status: steps.OK},
			engine.StepDone{ID: "o2", Index: 1, Status: steps.AuthRefused, Err: "no polkit agent is running, so the password can't be asked"},
			engine.Finished{Action: actions.IDFullCleanup, Outcome: engine.OutcomeCompleted, Refused: 1},
			engine.PhaseChanged{Phase: engine.PhaseIdle},
		)
	}, measure("run-refused"), tagsAt("run-refused"))

	// reset puts a short, clean run on the Run page, so the next state is
	// measured without the last one's rows.
	reset := func() {
		add(func() {
			c.feed(
				engine.PhaseChanged{Phase: engine.PhaseRunning},
				engine.QueueStarted{Action: actions.IDFullCleanup, Started: now, Steps: []engine.PlanStep{{ID: "r1", Title: "Short"}}},
				engine.StepStarted{ID: "r1", At: now},
				engine.StepDone{ID: "r1", Status: steps.OK},
				engine.Finished{Action: actions.IDFullCleanup, Outcome: engine.OutcomeCompleted},
				engine.PhaseChanged{Phase: engine.PhaseIdle},
			)
		})
	}

	// A long busy line and a long notice.
	reset()
	add(func() {
		c.feed(engine.PhaseChanged{Phase: engine.PhaseDiscovering, Busy: "Finding " + longText})
	}, measure("busy"), func() {
		c.feed(engine.Notice{Text: longText + " " + longPath}, engine.PhaseChanged{Phase: engine.PhaseIdle})
	}, measure("notice"))

	// A discovery result, found nothing and failed.
	reset()
	add(func() {
		w.current = actions.IDServices
		d := engine.DiscoveryResult{Action: actions.IDServices, Outcome: engine.DiscoveryNothing,
			Summary: "Checked 12 stopped services: " + longText, Category: logstore.Services, Started: now, Elapsed: time.Second,
			Details: []string{longUnit + ": skipped, excluded: initrd", longWord, "bluetooth.service: skipped, " + longText, longPath}}
		c.feed(d, engine.Notice{Text: d.Summary}, engine.Finished{Action: actions.IDServices, Outcome: engine.OutcomeNotice})
	}, measure("discovery-nothing"), func() {
		d := engine.DiscoveryResult{Action: actions.IDServices, Outcome: engine.DiscoveryFailed,
			Summary: "Couldn't list the services", Err: "systemctl list-unit-files: " + longText + " " + longPath, Category: logstore.Services,
			Started: now, Elapsed: time.Second, Details: []string{longUnit}}
		c.feed(d, engine.Notice{Text: d.Summary}, engine.Finished{Action: actions.IDServices, Outcome: engine.OutcomeNotice})
	}, measure("discovery-failed"))

	reset()
	// The dialogs: each kind with long text, a dry run's banner on the
	// first and the kept-password note on the passwords.
	dry = true
	ask("confirm-gate", 101, engine.NeedsConfirm{ID: 101, Kind: engine.ConfirmGate, Title: "Proceed with " + longText + "?", Body: longText + " " + longPath,
		Plan: plan(6), YesLabel: "Proceed"}, "no")
	dry = false
	ask("confirm-step", 102, engine.NeedsConfirm{ID: 102, Kind: engine.ConfirmStep, Step: "s1", Title: "Clear all of " + longPath + "?", Msg: "Clear?",
		Destructive: true, YesLabel: "Clear everything under that folder"}, "no")
	ask("confirm-orphans", 103, engine.NeedsConfirm{ID: 103, Kind: engine.ConfirmOrphans, Title: "Remove 3 orphans?", Destructive: true,
		Direct: []string{longPkg, longWord, "short"}, Cascade: []string{longPkg, longUnit, "other"}}, "no")
	ask("confirm-downloads", 104, engine.NeedsConfirm{ID: 104, Kind: engine.ConfirmDownloads, Title: "Delete 2 old downloads?", Destructive: true,
		Items: []string{longPath, longWord + ".iso"}, YesLabel: "Delete"}, "no")
	// The downloads confirm as the engine builds it from a real scan's
	// plan (downloads.Plan.Confirm, split as the engine splits it), with
	// a dry run's banner: names as a Downloads folder holds them.
	dry = true
	dl := downloads.Plan{Shown: "~/Downloads", Names: realDownloads}
	dlTitle, dlBody, _ := strings.Cut(dl.Confirm(), "\n")
	var dlItems []string
	for _, n := range dl.Names {
		dlItems = append(dlItems, sanitize.QuoteName(n, 200))
	}
	dialogChecks["confirm-downloads-real"] = func(labels []string) {
		noNamesTwice(c, "confirm-downloads-real", labels, dlItems)
		c.ok(slices.Contains(labels, dlTitle), "confirm-downloads-real: the heading %q isn't shown: %q", dlTitle, labels)
	}
	ask("confirm-downloads-real", 111, engine.NeedsConfirm{ID: 111, Kind: engine.ConfirmDownloads, Step: "downloads", Title: dlTitle,
		Body: strings.TrimLeft(dlBody, "\n"), Msg: dl.Confirm(), Items: dlItems, Destructive: true, YesLabel: "Delete"}, "no")
	dry = false
	ask("confirm-audit-gate", 105, engine.NeedsConfirm{ID: 105, Kind: engine.ConfirmAuditGate, Title: "The audit flagged " + longPkg,
		Body: "Maintainer " + longWord + " → " + longWord, YesLabel: "Update anyway"}, "no")
	kept = true
	ask("password-upfront", 106, engine.NeedsPassword{ID: 106, Kind: engine.PasswordUpFront, Gate: "Proceed with " + longText + "?",
		Error: "Incorrect password — " + longText, AttemptsLeft: 2}, "cancel")
	ask("password-askpass", 107, engine.NeedsPassword{ID: 107, Kind: engine.PasswordAskpass, Requester: "/usr/bin/" + longWord + " (pid 12345)",
		Prompt: "[sudo] password for " + longWord + ": " + longText, Error: "wrong password"}, "cancel")
	ask("password-polkit", 108, engine.NeedsPassword{ID: 108, Kind: engine.PasswordAskpass, Polkit: true, Action: "org.archmaid.helper." + longWord}, "cancel")
	kept = false
	ask("selection", 109, engine.NeedsSelection{ID: 109, Title: "Enabled but not running — " + longText,
		Items: []string{longUnit, "cups.service", longWord, longPath}}, "cancel")
	ask("audit-report", 110, engine.OutputReport{ID: 110, Text: "x", Audit: &engine.AuditReport{Total: 7, CanAccept: true, Findings: []engine.AuditFinding{
		{Name: longPkg, High: true, Flags: []string{"new maintainer", "install script " + longWord}, Reason: "maintainer " + longWord + " → " + longWord},
		{Name: longWord, Flags: []string{longText}},
	}}}, "close")
	add(func() {
		w.busy = true // as mid-run: the question is asked
		w.closeRequest()
	}, func() {
		measure("quit-question")()
		if w.quitDialog != nil {
			c.press(w.quitDialog, "cancel")
		}
		w.busy = false
	})

	// Settings with long values and every error visible.
	add(func() {
		w.pickPage("settings")
		p := w.set
		p.downloads.SetText(longPath)
		p.showDownloadsError("Not a usable folder: " + longPath + " " + longText)
		p.showProblem("Not saved (it applies until you close archmaid): open " + longPath + ": " + longWord)
		p.showConfigError("Couldn't create it: " + longPath + ": " + longWord)
	}, measure("settings-long"), func() {
		w.set.showDownloadsError(longWord + longWord)
		w.set.showProblem(longWord + longWord)
		w.set.showConfigError(longWord + longWord)
	}, measure("settings-one-word"), func() {
		w.set.showDownloadsError("")
		w.set.showProblem("")
		w.set.showConfigError("")
	})

	// The last-run cell's longest issue, on the Overview.
	add(func() {
		w.pickPage("overview")
		w.lastTitle.SetText("The last run's record can't be read")
		w.lastIssues.SetText("read " + longPath + ": " + longWord)
		w.lastIssues.SetVisible(true)
	}, measure("overview-last-run"), func() {
		// The VM's last-run title over two lines and more beside the
		// FAILED and CANCELLED chips: a chip keeps its own height.
		w.lastTitle.SetText("Full Cleanup (Recommended) · just now")
		w.lastTag.SetText("CANCELLED")
		w.lastTag.SetVisible(true)
	}, measure("overview-last-run-chip"), func() {
		titleH, tagH := w.lastTitle.Height(), w.lastTag.Height()
		// Its natural height counts its padding and border; Height doesn't,
		// so a chip at its own height is a few px under it.
		_, natH, _, _ := w.lastTag.Measure(gtk.OrientationVertical, -1)
		if n := len(c.rep.Narrow); n > 0 { // for the log
			c.rep.Narrow[n-1] += fmt.Sprintf(" chip=%d (own %d) title=%d", tagH, natH, titleH)
		}
		c.ok(titleH > natH || w.win.Width() > 360, "the last-run title is %d px high, the chip %d px: the title didn't wrap, nothing to check", titleH, natH)
		c.ok(tagH <= natH, "the last-run chip is drawn %d px high beside a %d px title; its own height is %d px", tagH, titleH, natH)
	}, w.refreshLastRun)

	// The Logs page: every category with long lines, then the error row.
	add(func() { w.pickPage("logs"); w.refreshLogs() }, measure("logs"))
	for _, ci := range logstore.Categories {
		add(func() {
			if len(w.logs.runList) == 0 {
				c.fail("the Logs page lists no runs (state %q)", w.opts.StateDir)
				return
			}
			w.showRun(w.logs.runList[0], ci.ID)
		}, measure("logs-"+string(ci.ID)))
	}
	add(func() {
		w.logs.runs.RemoveAll()
		w.logs.runs.Append(placeholder("The logs can't be read", "open "+longPath+": permission denied"))
	}, measure("logs-unreadable"))

	c.seq(200, steps_...)
}

// narrowStateLogs makes a state dir with a run holding every log category
// with long lines and long entry headers.
func narrowStateLogs(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	at := time.Now().Add(-time.Hour).Truncate(time.Minute)
	st := logstore.New(logstore.Options{Dir: dir, Now: func() time.Time { return at }})
	body := "―― 00:45:36 - System update " + longText + " ――\n" + longText + "\n" + longWord + longWord + "\n" + longPath + "\n -> " + longPkg + "\n"
	for _, ci := range logstore.Categories {
		if err := st.Append(ci.ID, "FAIL", ci.Label+" "+longText+" "+longPath, body); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(st.Path(ci.ID), at, at); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestGUIMinWidthStates: the long-text states (narrowStates) at 360 px and
// 480 px: nothing asks for more width than the window has.
func TestGUIMinWidthStates(t *testing.T) {
	needDisplay(t)
	for _, wd := range narrowWidths(t, 360, 480) {
		t.Run(strconv.Itoa(wd), func(t *testing.T) {
			_, env := installShims(t, shimOpts{})
			state := narrowStateLogs(t)
			var buf bytes.Buffer
			rep, _ := childCmd(t, "narrow-states", func(c *exec.Cmd) { c.Stderr = io.MultiWriter(os.Stderr, &buf) },
				append(env, fmt.Sprintf("%s=%d", envNarrowW, wd), "ARCHMAID_GUI_STATE="+state)...)
			controls := 0
			for _, l := range strings.Split(buf.String(), "\n") {
				switch {
				case !glibWarned(l):
				case strings.Contains(l, toastControl):
					controls++
				default:
					t.Errorf("GTK logged: %s", l)
				}
			}
			if controls == 0 {
				t.Errorf("the provoked GTK warning never reached the captured stderr (%d bytes)", buf.Len())
			}
			seen := map[string]bool{}
			for _, m := range rep.Narrow {
				var name string
				var width, content int
				if _, err := fmt.Sscanf(m, "%s width=%d content=%d", &name, &width, &content); err != nil {
					t.Errorf("measure %q: %v", m, err)
					continue
				}
				seen[name] = true
				if width != wd {
					t.Errorf("%s: the window is %d px wide, want %d", name, width, wd)
				}
				if content > width || strings.Contains(m, "too wide") {
					t.Errorf("%s: the content asks for %d px in a %d px window:\n%s", name, content, width, m)
				} else {
					t.Log(m)
				}
			}
			for _, want := range []string{"run-going", "run-failed", "run-waiting", "run-locked", "run-refused", "busy", "notice", "discovery-nothing", "discovery-failed",
				"confirm-gate", "confirm-step", "confirm-orphans", "confirm-downloads", "confirm-downloads-real", "confirm-audit-gate", "password-upfront", "password-askpass",
				"password-polkit", "selection", "audit-report", "quit-question", "settings-long", "settings-one-word", "overview-last-run", "overview-last-run-chip", "logs", "logs-unreadable"} {
				if !seen[want] {
					t.Errorf("never measured %s (%q)", want, rep.Narrow)
				}
			}
			for _, ci := range logstore.Categories {
				if !seen["logs-"+string(ci.ID)] {
					t.Errorf("never measured logs-%s", ci.ID)
				}
			}
		})
	}
}
