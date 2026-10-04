package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
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

// baselineState is what loadBaseline found on disk.
type baselineState int

const (
	baselineMissing baselineState = iota // no snapshot yet: the first audit
	baselineOK
	// baselineCorrupt: a snapshot exists but can't be read or parsed. It is
	// never treated as missing: that would silently re-baseline and wave
	// through exactly the maintainer change the snapshot exists to catch.
	baselineCorrupt
)

// loadBaseline reads the previous audit's name → maintainer map ("" =
// orphaned). Only a file that doesn't exist reads as baselineMissing; any
// other read error, invalid JSON, or a JSON null is baselineCorrupt.
func loadBaseline() (map[string]string, baselineState) {
	b, err := os.ReadFile(baselinePath())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, baselineMissing
	case err != nil:
		return nil, baselineCorrupt
	}
	var m map[string]string
	if json.Unmarshal(b, &m) != nil || m == nil {
		return nil, baselineCorrupt
	}
	return m, baselineOK
}

// snapshotFinding is the finding a corrupt snapshot turns into. Without a
// readable baseline no maintainer change can be detected, so the audit fails
// closed: high severity, gating the update like a takeover would.
func snapshotFinding() auditFinding {
	return auditFinding{
		name: "maintainer snapshot", high: true,
		flags: []string{"unreadable: maintainer changes can't be checked, re-verify maintainers by hand"},
	}
}

// nextBaseline is the snapshot to save: each audited package's current
// maintainer, keeping the old value for any package the AUR didn't report
// this time (a transient removal must not erase its history).
func nextBaseline(pkgs []string, info map[string]aurInfo, prev map[string]string) map[string]string {
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
	return next
}

// saveBaseline records the audited packages' maintainers (see nextBaseline
// and writeBaseline).
func saveBaseline(pkgs []string, info map[string]aurInfo, prev map[string]string) error {
	return writeBaseline(nextBaseline(pkgs, info, prev))
}

// errSnapshotLocked: another archmaid holds the snapshot lock, so this save
// is skipped rather than racing it.
var errSnapshotLocked = errors.New("another archmaid is saving the AUR snapshot")

// writeBaseline replaces the snapshot atomically, under an exclusive lock: a
// 0600 temp file in the same directory is written, fsynced and renamed over
// the snapshot, then the directory is fsynced, so a crash leaves the old
// snapshot or the new one, never a torn file. A corrupt snapshot on disk is
// first renamed to aur-maintainers.json.corrupt-<time>, kept for inspection
// rather than overwritten. A held lock returns errSnapshotLocked unwritten.
func writeBaseline(next map[string]string) error {
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(baselinePath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	unlock, err := lockBaseline(dir)
	if err != nil {
		return err
	}
	defer unlock()

	if _, state := loadBaseline(); state == baselineCorrupt {
		aside := baselinePath() + ".corrupt-" + time.Now().Format("20060102-150405")
		if err := os.Rename(baselinePath(), aside); err != nil {
			return err
		}
	}

	tmp, err := os.CreateTemp(dir, "aur-maintainers.json.tmp-*") // mode 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // fails harmlessly once renamed
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), baselinePath()); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync() // make the rename itself durable
}

// lockBaseline takes an exclusive, non-blocking flock on aur-maintainers.lock
// (left on disk: deleting a lock file races its next user) and returns the
// unlock func, or errSnapshotLocked if another process holds it.
func lockBaseline(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, "aur-maintainers.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errSnapshotLocked
		}
		return nil, err
	}
	return func() { f.Close() }, nil // closing the fd releases the flock
}

// ── the action ───────────────────────────────────────────────────────────────

// auditResult is one completed audit.
type auditResult struct {
	total    int // foreign packages checked
	findings []auditFinding
	baseline baselineState // the snapshot this audit was diffed against

	// what the next snapshot is built from (see saveSnapshot)
	pkgs []string
	info map[string]aurInfo
	prev map[string]string
}

// saveSnapshot advances the maintainer snapshot to what this audit saw.
// performAudit never saves: the caller decides when, because saving is what
// acknowledges a maintainer change. Saved too early (before the user has
// answered the gate), a declined change would read as settled next run.
//
// With holdHigh, packages with a high finding keep their previous maintainer,
// so they're flagged again until acknowledged, and a corrupt snapshot is left
// alone entirely (replacing it is itself the acknowledgement).
func (r auditResult) saveSnapshot(holdHigh bool) error {
	if holdHigh && r.baseline == baselineCorrupt {
		return nil
	}
	next := nextBaseline(r.pkgs, r.info, r.prev)
	if holdHigh {
		for _, f := range r.findings {
			if !f.high {
				continue
			}
			if p, ok := r.prev[f.name]; ok {
				next[f.name] = p
			} else {
				delete(next, f.name)
			}
		}
	}
	return writeBaseline(next)
}

// snapshotCommit is the deferred save for an audit's caller to run (nil in a
// dry run, which never writes the snapshot). It logs a failed save — the
// changes then stay flagged next time — and, with accepting, records that the
// user accepted flagged maintainer changes.
func snapshotCommit(res auditResult, holdHigh, accepting bool) func() error {
	if dryRun {
		return nil
	}
	return func() error {
		if err := res.saveSnapshot(holdHigh); err != nil {
			appendLog(logCategoryAudit, "WARN", "AUR snapshot not saved: "+err.Error(), "")
			return err
		}
		if accepting {
			appendLog(logCategoryAudit, "ACCEPT", "AUR maintainer changes accepted; snapshot updated", "")
		}
		return nil
	}
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

// clean is how many packages had nothing flagged. A corrupt snapshot's finding
// isn't a package, so it doesn't count against them.
func (r auditResult) clean() int {
	n := r.total - len(r.findings)
	if r.baseline == baselineCorrupt {
		n++
	}
	return n
}

// errNoForeign means there was nothing to audit.
var errNoForeign = errors.New("no foreign packages installed")

// performAudit runs the audit against the maintainer snapshot without
// advancing it (see saveSnapshot). Shared by the menu action and Full
// Cleanup's audit step. Any error other than errNoForeign is the AUR lookup
// failing. A corrupt snapshot becomes a high-severity finding of its own (see
// snapshotFinding).
func performAudit(now time.Time) (auditResult, error) {
	pkgs := foreignPackages()
	if len(pkgs) == 0 {
		return auditResult{}, errNoForeign
	}
	info, err := fetchAURInfo(pkgs)
	if err != nil {
		return auditResult{}, err
	}
	prev, state := loadBaseline()
	findings := classifyAll(pkgs, info, prev, state == baselineOK, now)
	if state == baselineCorrupt {
		findings = append([]auditFinding{snapshotFinding()}, findings...)
	}
	return auditResult{total: len(pkgs), findings: findings, baseline: state, pkgs: pkgs, info: info, prev: prev}, nil
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
	msg := discoveryMsg{kind: "audit", report: renderAudit(res)}
	// Advance the snapshot for everything but the high findings (with none,
	// that's all of it); those stay flagged until `a` on the audit screen
	// runs msg.commit to acknowledge them.
	if save := snapshotCommit(res, true, false); save != nil {
		save()
	}
	if res.high() > 0 {
		msg.commit = snapshotCommit(res, false, true)
	}
	return msg
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
	if res.baseline == baselineCorrupt {
		// Fail closed: with no readable snapshot, a takeover would go unseen.
		r.confirmNext = "The AUR maintainer snapshot is unreadable, so maintainer changes can't be checked. Update anyway?"
	} else if who := highNames(res.findings); who != "" {
		// Name the packages: the popup covers the report that explains it.
		r.confirmNext = who + " changed maintainer since the last audit. Update anyway?"
	}
	// Run by handleStepResult right away when nothing gates, otherwise only if
	// the gated update is accepted: declining leaves the snapshot as it was,
	// so the same change gates the next run too.
	r.commit = snapshotCommit(res, false, r.confirmNext != "")
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
	label := fmt.Sprintf("AUR audit: %d checked, %d flagged", res.total, res.total-res.clean())
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
		res.total, high, len(res.findings)-high, res.clean())
	if len(res.findings) == 0 {
		b.WriteString("Nothing flagged.\n")
	}
	b.WriteString(findingLines(res.findings))
	if note := baselineNote(res.baseline); note != "" {
		b.WriteString("\n" + note + "\n")
	}
	if high > 0 && !dryRun {
		b.WriteString("\nThe flagged maintainers are only recorded if you accept the update; declining keeps them flagged for the next audit.\n")
	}
	return b.String()
}

// baselineNote explains the snapshot state, or "" when there's nothing to say.
func baselineNote(state baselineState) string {
	switch {
	case state == baselineCorrupt && dryRun:
		return "The maintainer snapshot is unreadable (dry run: left as is) — maintainer changes can't be detected until it's replaced."
	case state == baselineCorrupt:
		return "The maintainer snapshot is unreadable, so maintainer changes couldn't be detected. Accepting saves a fresh baseline and keeps the old file as aur-maintainers.json.corrupt-<time>."
	case state == baselineMissing && dryRun:
		return "No maintainer snapshot yet (dry run: not saved) — maintainer changes can't be detected until a real audit saves one."
	case state == baselineMissing:
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
		stepOKStyle.Render(fmt.Sprintf("✔ %d clean", res.clean())),
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
	if note := baselineNote(res.baseline); note != "" {
		b.WriteString(statusStyle.Render(note) + "\n")
	}
	if high > 0 && !dryRun {
		b.WriteString(statusStyle.Render("Flagged maintainers stay flagged on every audit until you've checked them and press a to accept.") + "\n")
	}
	b.WriteString(statusStyle.Render("Recent = pushed within 72h, the same window as the yay cooldown hook."))
	return b.String()
}
