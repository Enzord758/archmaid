package audit

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Enzord758/archmaid/internal/sanitize"
)

// Result is one completed audit. Total, Findings and Baseline are what a
// report shows; the rest is what Commit builds the next snapshot from.
type Result struct {
	Total    int       // foreign packages checked
	Findings []Finding // flagged packages only, high first
	Baseline Baseline  // the snapshot this audit was diffed against

	pkgs []string
	info map[string]Info
	prev map[string]Entry

	// cfg is what the audit ran with, set even when it failed: Commit writes
	// the snapshot it read and logs where it logged.
	cfg Config
}

// DryRun reports whether the audit ran as a dry run (its snapshot is never
// advanced).
func (r Result) DryRun() bool { return r.cfg.DryRun }

// High is how many findings are high severity.
func (r Result) High() int {
	n := 0
	for _, f := range r.Findings {
		if f.High {
			n++
		}
	}
	return n
}

// Clean is how many packages had nothing flagged. A corrupt snapshot's
// finding isn't a package, so it doesn't count against them.
func (r Result) Clean() int {
	n := r.Total - len(r.Findings)
	if r.Baseline == BaselineCorrupt {
		n++
	}
	return n
}

// save advances the maintainer snapshot to what this audit saw.
//
// With holdHigh, packages with a high finding keep their whole previous entry
// (maintainer and co-maintainers), so they're flagged again until
// acknowledged, and a corrupt snapshot is left alone entirely (replacing it
// is itself the acknowledgement).
func (r Result) save(holdHigh bool) error {
	if holdHigh && r.Baseline == BaselineCorrupt {
		return nil
	}
	next := nextSnapshot(r.pkgs, r.info, r.prev)
	if holdHigh {
		for _, f := range r.Findings {
			if !f.High {
				continue
			}
			if p, ok := r.prev[f.Name]; ok {
				next[f.Name] = p
			} else {
				delete(next, f.Name)
			}
		}
	}
	return WriteSnapshot(r.cfg.SnapshotPath, next)
}

// Commit is the deferred snapshot save for the audit's caller to run, nil in
// a dry run (which never writes the snapshot). Run never saves: saving is
// what acknowledges a maintainer change, so the caller decides when. Saved
// too early (before the user has answered the gate), a declined change would
// read as settled next run.
//
// It writes the snapshot the audit read (Config.SnapshotPath). With holdHigh
// the high findings keep their previous entries (see save). A failed save is
// logged (WARN) and returned — the changes then stay flagged next time — and,
// with accepting, a successful one records that the user accepted flagged
// maintainer changes (ACCEPT).
func (r Result) Commit(holdHigh, accepting bool) func() error {
	if r.cfg.DryRun {
		return nil
	}
	return func() error {
		if err := r.save(holdHigh); err != nil {
			r.cfg.log("WARN", "AUR snapshot not saved: "+err.Error(), "")
			return err
		}
		if accepting {
			r.cfg.log("ACCEPT", "AUR maintainer changes accepted; snapshot updated", "")
		}
		return nil
	}
}

// GateReason is the sentence a gate's confirm opens with, or "" when nothing
// should gate the update. A corrupt snapshot gates (fail closed: with no
// readable snapshot, a takeover would go unseen); otherwise it names the
// high-severity packages and what changed:
//
//	victim changed maintainer since the last audit.
//	Since the last audit: foo: co-maintainer bob added.
//	Since the last audit: victim changed maintainer; foo: co-maintainer bob added.
func (r Result) GateReason() string {
	if r.Baseline == BaselineCorrupt {
		return "The AUR maintainer snapshot is unreadable, so maintainer changes can't be checked."
	}
	return gateReason(r.Findings)
}

// gateReason names the changed packages (see GateReason). With more than one
// package gaining co-maintainers it names just the packages ("a and b added
// co-maintainers"), to stay popup-sized.
func gateReason(findings []Finding) string {
	var changed, coPkgs []string
	var co Finding
	for _, f := range findings {
		if f.MaintainerChanged {
			changed = append(changed, f.Name)
		}
		if len(f.CoAdded) > 0 {
			coPkgs, co = append(coPkgs, f.Name), f
		}
	}
	if len(coPkgs) == 0 {
		if len(changed) == 0 {
			return ""
		}
		return packageNames(changed) + " changed maintainer since the last audit."
	}
	var parts []string
	if len(changed) > 0 {
		parts = append(parts, packageNames(changed)+" changed maintainer")
	}
	if len(coPkgs) == 1 {
		parts = append(parts, sanitize.Field(co.Name, FieldMax)+": "+coFlag(co.CoAdded, "added"))
	} else {
		parts = append(parts, packageNames(coPkgs)+" added co-maintainers")
	}
	return "Since the last audit: " + strings.Join(parts, "; ") + "."
}

// packageNames names packages for a confirm line, short enough for a popup:
// "a", "a and b", or "3 AUR packages (a, b, …)". "" if none.
func packageNames(raw []string) string {
	var names []string
	for _, n := range raw {
		names = append(names, sanitize.Field(n, FieldMax))
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	}
	return fmt.Sprintf("%d AUR packages (%s, %s, …)", len(names), names[0], names[1])
}

// LogSummary records the audit in the audit log (Config.Log) so a closed
// report stays reviewable: "AUR audit: N checked, M flagged" and the plain
// findings list. The audit step doesn't need it: its own log entry is the
// report (Text).
func (r Result) LogSummary() {
	label := fmt.Sprintf("AUR audit: %d checked, %d flagged", r.Total, r.Total-r.Clean())
	r.cfg.log("AUDIT", label, findingLines(r.Findings))
}

// findingLines is the plain-text findings list, one "[HIGH]"/"[warn]" line per
// package ("" when nothing was flagged).
func findingLines(findings []Finding) string {
	width := NameWidth(findings)
	var b strings.Builder
	for _, f := range findings {
		mark := "warn"
		if f.High {
			mark = "HIGH"
		}
		fmt.Fprintf(&b, "[%s] %s\n", mark, f.Line(width))
	}
	return b.String()
}

// Text is the plain-text report: a count line, the findings, and the notes on
// the snapshot. The audit step streams it into the output pane (and its log).
func (r Result) Text() string {
	high := r.High()
	var b strings.Builder
	fmt.Fprintf(&b, "%d foreign package(s) checked against the AUR: %d high, %d glance, %d clean\n\n",
		r.Total, high, len(r.Findings)-high, r.Clean())
	if len(r.Findings) == 0 {
		b.WriteString("Nothing flagged.\n")
	}
	b.WriteString(findingLines(r.Findings))
	if note := r.Note(); note != "" {
		b.WriteString("\n" + note + "\n")
	}
	if high > 0 && !r.cfg.DryRun {
		b.WriteString("\nThe flagged maintainers are only recorded if you accept the update; declining keeps them flagged for the next audit.\n")
	}
	return b.String()
}

// Note explains the snapshot state, or "" when there's nothing to say.
func (r Result) Note() string {
	state, dryRun := r.Baseline, r.cfg.DryRun
	switch {
	case state == BaselineCorrupt && dryRun:
		return "The maintainer snapshot is unreadable (dry run: left as is) — maintainer changes can't be detected until it's replaced."
	case state == BaselineCorrupt:
		return "The maintainer snapshot is unreadable, so maintainer changes couldn't be detected. Accepting saves a fresh baseline and keeps the old file as aur-maintainers.json.corrupt-<time>."
	case state == BaselineMissing && dryRun:
		return "No maintainer snapshot yet (dry run: not saved) — maintainer changes can't be detected until a real audit saves one."
	case state == BaselineMissing:
		return "First audit: maintainers saved as the baseline. Adoptions and maintainer changes are flagged from the next audit on."
	case dryRun:
		return "Dry run: compared against the saved snapshot; the snapshot was not updated."
	}
	return ""
}

// NameWidth is the column width for findings' names in a report (capped at
// 28): pass it to Finding.Line.
func NameWidth(findings []Finding) int {
	w := 0
	for _, f := range findings {
		w = max(w, utf8.RuneCountInString(sanitize.Field(f.Name, FieldMax)))
	}
	return min(w, 28)
}
