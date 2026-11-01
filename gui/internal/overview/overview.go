// Package overview is the Overview page's state, in plain Go: the menu
// (internal/actions.Menu as the GUI shows it), the status header and the
// last run, read from last-run.json. Nothing here imports gotk4 or writes
// anything.
package overview

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/plural"
	"github.com/Enzord758/archmaid/internal/actions"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/sysstat"
)

// Kind is what choosing a menu entry does.
type Kind int

const (
	// KindRun starts the action: Start(ID).
	KindRun Kind = iota
	// KindLogs opens the Logs page (the TUI's "View log").
	KindLogs
)

// Entry is one menu row.
type Entry struct {
	ID, Label string
	// Icon is a symbolic icon name from the Adwaita theme: the menu's own
	// icons are Nerd Font glyphs, which a GTK window can't count on.
	Icon string
	// Busy is what the row says while the action's discovery runs ("" for
	// an action without one).
	Busy string
	Kind Kind
}

// icons maps the menu's actions to Adwaita symbolic icons.
var icons = map[string]string{
	actions.IDFullCleanup: "edit-clear-all-symbolic",
	actions.IDClearCaches: "user-trash-symbolic",
	actions.IDRepair:      "emblem-system-symbolic",
	actions.IDDownloads:   "folder-download-symbolic",
	actions.IDTopgrade:    "software-update-available-symbolic",
	actions.IDServices:    "system-run-symbolic",
	actions.IDAURAudit:    "security-medium-symbolic",
	actions.IDViewLog:     "document-open-recent-symbolic",
}

// Entries is the menu as the GUI shows it, in actions.Menu's order. Exit
// (the window closes) and Edit topgrade config (it opens $EDITOR in the
// terminal) are the TUI's; View log opens the Logs page.
func Entries() []Entry {
	var out []Entry
	for _, a := range actions.Menu {
		e := Entry{ID: a.ID, Label: a.Label, Icon: icons[a.ID], Busy: a.Busy}
		if e.Icon == "" {
			e.Icon = "system-run-symbolic"
		}
		switch a.ID {
		case actions.IDExit, actions.IDEditConfig:
			continue
		case actions.IDViewLog:
			e.Kind = KindLogs
		}
		out = append(out, e)
	}
	return out
}

// Label is the menu label of action id, or id itself for one the menu
// doesn't know.
func Label(id string) string {
	if a, ok := actions.ByID(id); ok {
		return a.Label
	}
	if l := actions.LegacyLabel(id); l != "" {
		return l // "Clear user cache" in an old last-run.json
	}
	return id
}

// StatusLine is the header's status: disk free, RAM available, the cache
// size; "" before the first StatusUpdated.
func StatusLine(s sysstat.Status) string {
	if s == (sysstat.Status{}) {
		return ""
	}
	return fmt.Sprintf("Free %s · RAM available %s · Cache %s", s.Free, s.RAM, s.Cache)
}

// StaleDays is how old the last run may be before the overview warns: the
// DMS widget's rule (packaging/dms/archmaid/state.js, STALE_DAYS).
const StaleDays = 14

// Level is how the last run reads.
type Level int

const (
	LevelNone  Level = iota // no record (or an unreadable one)
	LevelOK                 // it went well and is recent
	LevelWarn               // incomplete, aborted, cancelled, or stale
	LevelError              // a step failed
)

// LastRun is the last run as the overview shows it.
type LastRun struct {
	Level Level
	// Title is "Full Cleanup (Recommended) · 3 h ago"; Issues are the
	// warnings and errors, errors first ("Last run failed (2 steps)",
	// "No cleanup for 20 days").
	Title  string
	Issues []string
	Record *logstore.RunRecord
}

// ReadLastRun reads last-run.json from stateDir ("" is logstore's default,
// ~/.local/state/archmaid) and summarizes it at now. It only reads:
// logstore.New creates nothing, and nothing here writes or prunes.
func ReadLastRun(stateDir string, now time.Time) LastRun {
	r, err := logstore.New(logstore.Options{Dir: stateDir}).ReadLastRun()
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return LastRun{Title: "No run recorded yet"}
	case err != nil:
		return LastRun{Title: "The last run's record can't be read", Issues: []string{err.Error()}}
	}
	return Summarize(r, now)
}

// NotAuthorised: the run ended cancelled because every step was refused
// (the engine records OutcomeCancelled whenever no step started). The
// Overview, the Run page and the DMS widget call it "not authorised", not
// "cancelled".
func NotAuthorised(r logstore.RunRecord) bool {
	return r.Outcome == logstore.OutcomeCancelled && r.StepsRefused > 0
}

// Summarize is the DMS widget's summarize for the last run alone: the
// outcome's issue, then the staleness warning past StaleDays.
func Summarize(r logstore.RunRecord, now time.Time) LastRun {
	out := LastRun{Level: LevelOK, Record: &r, Title: Label(r.Action) + " · " + Ago(r.Finished, now)}
	if r.DryRun {
		out.Title += " · dry run"
	}
	var errs, warns []string
	switch r.Outcome {
	case logstore.OutcomeFailed:
		s := "Last run failed"
		if r.StepsFailed > 0 {
			s += " (" + plural.N(r.StepsFailed, "step", "steps") + ")"
		}
		s += plural.AlsoRefused(r.StepsRefused)
		errs = append(errs, s)
	case logstore.OutcomeIncomplete:
		s := "Last run incomplete: root steps refused"
		if r.StepsRefused > 0 {
			s += fmt.Sprintf(" (%d)", r.StepsRefused)
		}
		warns = append(warns, s)
	case logstore.OutcomeAborted:
		warns = append(warns, "Last run aborted"+plural.AlsoRefused(r.StepsRefused))
	case logstore.OutcomeCancelled:
		if NotAuthorised(r) {
			warns = append(warns, "Last run not authorised · nothing ran ("+plural.Refused(r.StepsRefused)+")")
		} else {
			warns = append(warns, "Last run cancelled, nothing ran")
		}
	}
	if age := ageDays(r.Finished, now); age > StaleDays {
		warns = append(warns, "No cleanup for "+plural.N(int(math.Floor(age)), "day", "days"))
	}
	switch {
	case len(errs) > 0:
		out.Level = LevelError
	case len(warns) > 0:
		out.Level = LevelWarn
	}
	out.Issues = append(errs, warns...)
	return out
}

func ageDays(then, now time.Time) float64 {
	return max(0, now.Sub(then).Hours()/24)
}

// Ago is the Overview's "how long ago": "just now", "5 min ago", "3 h ago",
// "12 days ago". It was the DMS widget's ago; since #223 the widget says
// "1 h ago" from 60 min, where this still counts minutes up to 90.
func Ago(then, now time.Time) string {
	s := max(0, now.Sub(then).Seconds())
	switch {
	case s < 90:
		return "just now"
	case s < 5400:
		return fmt.Sprintf("%d min ago", int(math.Round(s/60)))
	case s < 129600:
		return fmt.Sprintf("%d h ago", int(math.Round(s/3600)))
	}
	return plural.N(int(math.Floor(s/86400)), "day", "days") + " ago"
}
