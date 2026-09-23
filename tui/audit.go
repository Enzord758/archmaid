package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ── AUR audit ────────────────────────────────────────────────────────────────
//
// A read-only check of the foreign packages (installed, but not from a sync
// repo — i.e. AUR or locally built) against the AUR's RPC, looking for the
// signals that matter for the maintainer-takeover pattern: a package that was
// orphaned then adopted, a maintainer who changed since the last audit, an
// unmaintained package, a very recent push, or a package that vanished from
// the AUR entirely. It never touches the system; the only thing it writes is
// the maintainer snapshot below (skipped in a dry run).
//
// The RPC exposes the *current* maintainer only, not adoption history, so
// "maintainer changed" is detected by diffing against a snapshot saved by the
// previous audit. The first audit therefore just establishes the baseline.

// aurRPCURL is the AUR RPC v5 "info" endpoint. A var so tests can point it at
// an httptest server.
var aurRPCURL = "https://aur.archlinux.org/rpc/v5/info"

const (
	// auditCooldown is how recent a push has to be to get flagged. Matches the
	// 72-hour hold in the recommended ~/.config/yay/init.lua hook, so the audit
	// flags exactly the packages yay would hold back.
	auditCooldown = 72 * time.Hour
	auditChunk    = 100 // packages per RPC request
	auditTimeout  = 15 * time.Second
)

// aurInfo is the subset of an RPC "info" result the audit reads.
type aurInfo struct {
	Name         string
	Maintainer   *string // null when orphaned
	LastModified int64
	OutOfDate    *int64 // null unless flagged out of date
	NumVotes     int
}

// auditFinding is one package's audit result. high marks the signals worth
// stopping for (a maintainer change/adoption); everything else is a glance.
type auditFinding struct {
	name  string
	high  bool
	flags []string
}

func (f auditFinding) line(width int) string {
	return fmt.Sprintf("%-*s  %s", width, f.name, strings.Join(f.flags, " · "))
}

// foreignPackages lists installed packages that aren't in any sync repo
// (pacman -Qqm). Empty when there are none (pacman exits 1).
func foreignPackages() []string {
	out, _ := exec.Command("pacman", "-Qqm").Output()
	return nonEmptyLines(string(out))
}

// fetchAURInfo looks the packages up on the AUR RPC (POST, chunked so a large
// package list never hits URL limits) and returns the ones it knows about,
// keyed by name. Packages the AUR doesn't know are simply absent.
func fetchAURInfo(pkgs []string) (map[string]aurInfo, error) {
	client := &http.Client{Timeout: auditTimeout}
	found := make(map[string]aurInfo, len(pkgs))
	for start := 0; start < len(pkgs); start += auditChunk {
		end := min(start+auditChunk, len(pkgs))
		form := url.Values{}
		for _, p := range pkgs[start:end] {
			form.Add("arg[]", p)
		}
		req, err := http.NewRequest(http.MethodPost, aurRPCURL, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", "archmaid")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		var body struct {
			Type    string
			Error   string
			Results []aurInfo
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("AUR returned HTTP %d", resp.StatusCode)
		}
		if err != nil {
			return nil, fmt.Errorf("unreadable AUR response: %w", err)
		}
		if body.Type == "error" {
			return nil, fmt.Errorf("AUR error: %s", body.Error)
		}
		for _, r := range body.Results {
			found[r.Name] = r
		}
	}
	return found, nil
}

// classify turns one package's AUR data (nil = the AUR doesn't know it) plus
// its previous-audit maintainer into a finding. prev/hadPrev come from the
// snapshot; hadPrev is false for a package audited for the first time.
func classify(name string, ai *aurInfo, prev string, hadPrev bool, now time.Time) auditFinding {
	f := auditFinding{name: name}
	if ai == nil {
		f.flags = append(f.flags, "not on the AUR (removed, renamed, or a local build)")
		return f
	}
	cur := ""
	if ai.Maintainer != nil {
		cur = *ai.Maintainer
	}
	switch {
	case hadPrev && prev == "" && cur != "":
		f.high = true
		f.flags = append(f.flags, fmt.Sprintf("adopted by %s (was orphaned)", cur))
	case hadPrev && prev != "" && cur == "":
		f.flags = append(f.flags, fmt.Sprintf("orphaned (was %s)", prev))
	case hadPrev && prev != cur:
		f.high = true
		f.flags = append(f.flags, fmt.Sprintf("maintainer changed: %s → %s", prev, cur))
	case cur == "":
		f.flags = append(f.flags, "orphaned")
	}
	if age := now.Sub(time.Unix(ai.LastModified, 0)); age < auditCooldown {
		f.flags = append(f.flags, "modified "+ageString(age)+" ago")
	}
	if ai.OutOfDate != nil {
		f.flags = append(f.flags, "flagged out of date")
	}
	if ai.NumVotes == 0 {
		f.flags = append(f.flags, "0 votes")
	}
	return f
}

// ageString renders a short duration for a "modified … ago" flag.
func ageString(d time.Duration) string {
	if d < time.Hour {
		return "<1h"
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

// classifyAll audits every package and returns only the flagged ones, worst
// first. A "-debug" package the AUR doesn't know is a locally built split
// companion (e.g. yay-debug, whatever repo its base came from), so it's
// skipped rather than reported as missing. A "-debug" package that *is* on the
// AUR is audited like any other.
func classifyAll(pkgs []string, info map[string]aurInfo, prev map[string]string, hadBaseline bool, now time.Time) []auditFinding {
	var out []auditFinding
	for _, name := range pkgs {
		var ai *aurInfo
		if v, ok := info[name]; ok {
			ai = &v
		}
		if ai == nil && strings.HasSuffix(name, "-debug") {
			continue
		}
		p, had := prev[name]
		f := classify(name, ai, p, hadBaseline && had, now)
		if len(f.flags) > 0 {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].high && !out[j].high })
	return out
}

// ── maintainer snapshot ──────────────────────────────────────────────────────

// baselinePath is where the previous audit's maintainers are kept: the state
// dir's root (not a run directory, so pruneLogs never touches it).
func baselinePath() string {
	return filepath.Join(resolveLogDir(), "aur-maintainers.json")
}

// loadBaseline reads the previous audit's name → maintainer map ("" =
// orphaned). ok is false when there's no usable snapshot (first audit).
func loadBaseline() (map[string]string, bool) {
	b, err := os.ReadFile(baselinePath())
	if err != nil {
		return nil, false
	}
	m := map[string]string{}
	if json.Unmarshal(b, &m) != nil {
		return nil, false
	}
	return m, true
}

// saveBaseline records each audited package's current maintainer, keeping the
// old value for any package the AUR didn't report this time (a transient
// removal must not erase its history). Best-effort: a failed write just means
// the next audit has nothing to diff against.
func saveBaseline(pkgs []string, info map[string]aurInfo, prev map[string]string) {
	next := make(map[string]string, len(pkgs))
	for _, name := range pkgs {
		if ai, ok := info[name]; ok {
			if ai.Maintainer != nil {
				next[name] = *ai.Maintainer
			} else {
				next[name] = ""
			}
		} else if p, ok := prev[name]; ok {
			next[name] = p
		}
	}
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(baselinePath()), 0o755)
	os.WriteFile(baselinePath(), b, 0o644)
}

// ── the action ───────────────────────────────────────────────────────────────

// auditResult is one completed audit.
type auditResult struct {
	total       int // foreign packages checked
	findings    []auditFinding
	hadBaseline bool
}

func (r auditResult) high() int {
	n := 0
	for _, f := range r.findings {
		if f.high {
			n++
		}
	}
	return n
}

// errNoForeign means there was nothing to audit.
var errNoForeign = errors.New("no foreign packages installed")

// performAudit runs the audit and advances the maintainer snapshot (except in
// a dry run, which reads it but never writes it). Shared by the menu action
// and Full Cleanup's audit step. Any error other than errNoForeign is the AUR
// lookup failing.
func performAudit(now time.Time) (auditResult, error) {
	pkgs := foreignPackages()
	if len(pkgs) == 0 {
		return auditResult{}, errNoForeign
	}
	info, err := fetchAURInfo(pkgs)
	if err != nil {
		return auditResult{}, err
	}
	prev, hadBaseline := loadBaseline()
	findings := classifyAll(pkgs, info, prev, hadBaseline, now)
	if !dryRun {
		saveBaseline(pkgs, info, prev)
	}
	return auditResult{total: len(pkgs), findings: findings, hadBaseline: hadBaseline}, nil
}

// discoverAURAudit runs the audit off the UI thread and returns it as a
// discoveryMsg: a notice for "nothing to audit" / "couldn't reach the AUR", or
// the rendered report for the audit screen.
func discoverAURAudit() tea.Cmd {
	return func() tea.Msg { return runAudit(time.Now()) }
}

func runAudit(now time.Time) discoveryMsg {
	res, err := performAudit(now)
	switch {
	case errors.Is(err, errNoForeign):
		return discoveryMsg{kind: "audit", notice: "No foreign (AUR or locally built) packages installed."}
	case err != nil:
		return discoveryMsg{kind: "audit", notice: "Couldn't reach the AUR: " + shortErr(err)}
	}
	logAudit(res)
	return discoveryMsg{kind: "audit", report: renderAudit(res)}
}

// auditStep is Full Cleanup's first step: the audit, run in-process. It sits
// directly in front of topgrade so that a high-severity finding (an adoption
// or maintainer change) can put a confirm in front of the update — the one
// step that would build a taken-over AUR package. Declining skips only the
// update; the rest of the cleanup still runs. The audit can't *hold back* a
// single package; that's the yay cooldown hook's job (see HANDOVER.md).
//
// alwaysLog: the step's own log entry is the audit record, so unlike the menu
// action it doesn't call logAudit. It never needs sudo.
func auditStep() step {
	return step{
		title: "Auditing AUR packages…", logLabel: "AUR audit",
		category: logCategoryAudit, alwaysLog: true,
		run: runAuditStep,
	}
}

func runAuditStep() stepResult {
	res, err := performAudit(time.Now())
	switch {
	case errors.Is(err, errNoForeign):
		return stepResult{output: "No foreign (AUR or locally built) packages installed — nothing to audit."}
	case err != nil:
		return stepResult{output: "Couldn't reach the AUR: " + shortErr(err), err: err}
	}
	r := stepResult{output: auditText(res)}
	if who := highNames(res.findings); who != "" {
		// Name the packages: the popup covers the report that explains it.
		r.confirmNext = who + " changed maintainer since the last audit. Update anyway?"
	}
	return r
}

// highNames names the high-severity packages for a confirm line, short enough
// for a popup: "a", "a and b", or "3 AUR packages (a, b, …)". "" if none.
func highNames(findings []auditFinding) string {
	var names []string
	for _, f := range findings {
		if f.high {
			names = append(names, f.name)
		}
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

// shortErr trims a transport error to something that fits a notice popup.
func shortErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	s := err.Error()
	if len(s) > 70 {
		s = s[:67] + "…"
	}
	return s
}

// logAudit records a menu-run audit in the audit log category so a closed
// report stays reviewable from "View log". Plain text — no styling in logs.
func logAudit(res auditResult) {
	label := fmt.Sprintf("AUR audit: %d checked, %d flagged", res.total, len(res.findings))
	appendLog(logCategoryAudit, "AUDIT", label, findingLines(res.findings))
}

// findingLines is the plain-text findings list, one "[HIGH]"/"[warn]" line per
// package ("" when nothing was flagged).
func findingLines(findings []auditFinding) string {
	width := nameWidth(findings)
	var b strings.Builder
	for _, f := range findings {
		mark := "warn"
		if f.high {
			mark = "HIGH"
		}
		fmt.Fprintf(&b, "[%s] %s\n", mark, f.line(width))
	}
	return b.String()
}

// auditText is the plain-text report Full Cleanup's audit step streams into
// the output pane (and, via alwaysLog, into audit.log).
func auditText(res auditResult) string {
	high := res.high()
	var b strings.Builder
	fmt.Fprintf(&b, "%d foreign package(s) checked against the AUR: %d high, %d glance, %d clean\n\n",
		res.total, high, len(res.findings)-high, res.total-len(res.findings))
	if len(res.findings) == 0 {
		b.WriteString("Nothing flagged.\n")
	}
	b.WriteString(findingLines(res.findings))
	if note := baselineNote(res.hadBaseline); note != "" {
		b.WriteString("\n" + note + "\n")
	}
	return b.String()
}

// baselineNote explains the snapshot state, or "" when there's nothing to say.
func baselineNote(hadBaseline bool) string {
	switch {
	case !hadBaseline && dryRun:
		return "No maintainer snapshot yet (dry run: not saved) — maintainer changes can't be detected until a real audit saves one."
	case !hadBaseline:
		return "First audit: maintainers saved as the baseline. Adoptions and maintainer changes are flagged from the next audit on."
	case dryRun:
		return "Dry run: compared against the saved snapshot; the snapshot was not updated."
	}
	return ""
}

func nameWidth(findings []auditFinding) int {
	w := 0
	for _, f := range findings {
		w = max(w, len(f.name))
	}
	return min(w, 28)
}

// renderAudit builds the styled report shown on the audit screen.
func renderAudit(res auditResult) string {
	var b strings.Builder
	b.WriteString(stepRunStyle.Render(fmt.Sprintf("%d foreign package(s) checked against the AUR", res.total)) + "\n\n")

	high := res.high()
	b.WriteString(fmt.Sprintf("%s  %s  %s\n\n",
		stepFailStyle.Render(fmt.Sprintf("✖ %d look closely", high)),
		stepSkipStyle.Render(fmt.Sprintf("⚠ %d glance", len(res.findings)-high)),
		stepOKStyle.Render(fmt.Sprintf("✔ %d clean", res.total-len(res.findings))),
	))

	width := nameWidth(res.findings)
	for _, f := range res.findings {
		glyph, style := "⚠", stepSkipStyle
		if f.high {
			glyph, style = "✖", stepFailStyle
		}
		b.WriteString(style.Render(glyph) + " " + itemStyle.Render(f.line(width)) + "\n")
	}
	if len(res.findings) == 0 {
		b.WriteString(stepOKStyle.Render("Nothing flagged.") + "\n")
	}

	b.WriteString("\n")
	if note := baselineNote(res.hadBaseline); note != "" {
		b.WriteString(statusStyle.Render(note) + "\n")
	}
	b.WriteString(statusStyle.Render("Recent = pushed within 72h, the same window as the yay cooldown hook."))
	return b.String()
}
