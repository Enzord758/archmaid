package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/sanitize"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Enzord758/archmaid/internal/proc"
)

// ── AUR audit ────────────────────────────────────────────────────────────────
//
// A read-only check of the foreign packages (installed, but not from a sync
// repo — i.e. AUR or locally built) against the AUR's RPC, looking for the
// signals that matter for the maintainer-takeover pattern: a package that was
// orphaned then adopted, a maintainer who changed since the last audit, a
// co-maintainer added since then (a co-maintainer can push to the package's
// git just like the maintainer), an unmaintained package, a very recent push,
// or a package that vanished from the AUR entirely. It never touches the
// system; the only thing it writes is the maintainer snapshot below (skipped
// in a dry run).
//
// The RPC exposes the *current* maintainer and co-maintainers only, not their
// history, so a change is detected by diffing against a snapshot saved by the
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
	// aurMaxResponse caps one RPC response body, far above what a real
	// 100-package reply needs.
	aurMaxResponse = 8 << 20
	// aurFieldMax caps an AUR-supplied name or maintainer for display.
	aurFieldMax = 64
)

// auditDeadline bounds a whole audit (the pacman query and every RPC chunk
// together) rather than each request on its own, so a dead network can't hold
// an audit for longer than this. A var so tests can shrink it.
var auditDeadline = 20 * time.Second

// aurInfo is the subset of an RPC "info" result the audit reads.
type aurInfo struct {
	Name         string
	Maintainer   *string // null when orphaned
	LastModified int64
	OutOfDate    *int64 // null unless flagged out of date
	NumVotes     int
	// CoMaintainers can push to the package like its maintainer. aurweb leaves
	// the key out when there are none (like Depends), so absent reads as none.
	CoMaintainers []string
}

// auditFinding is one package's audit result. high marks the signals worth
// stopping for (a maintainer change/adoption, an added co-maintainer);
// everything else is a glance. maintainerChanged and coAdded say which of
// those it was, for the gate's confirm.
type auditFinding struct {
	name  string
	high  bool
	flags []string

	maintainerChanged bool
	coAdded           []string // raw names, sorted
}

// line renders the finding. name stays raw in the struct (it keys the
// snapshot); it is only sanitized here, for display.
func (f auditFinding) line(width int) string {
	return fmt.Sprintf("%-*s  %s", width, sanitize.Field(f.name, aurFieldMax), strings.Join(f.flags, " · "))
}

// foreignPackages lists installed packages that aren't in any sync repo
// (pacman -Qqm). Empty when there are none (pacman exits 1), or when ctx ends
// first (performAudit checks for that).
func foreignPackages(ctx context.Context) []string {
	out, _ := proc.Query(ctx, "pacman", "-Qqm")
	return nonEmptyLines(string(out))
}

// fetchAURInfo looks the packages up on the AUR RPC (POST, chunked so a large
// package list never hits URL limits) and returns the ones it knows about,
// keyed by name. Packages the AUR doesn't know are simply absent, and so is
// any result for a name that wasn't asked for.
//
// ctx bounds the whole lookup (performAudit sets the deadline) and cancelling
// it abandons a request mid-flight. The transport is private to the call and
// its idle connections are closed on return, so no connection goroutine
// outlives it.
func fetchAURInfo(ctx context.Context, pkgs []string) (map[string]aurInfo, error) {
	return fetchAURInfoAt(ctx, aurRPCURL, pkgs)
}

// fetchAURInfoAt is fetchAURInfo against an explicit endpoint, for callers
// that captured it before going off the model's goroutine (see auditTarget).
func fetchAURInfoAt(ctx context.Context, rpcURL string, pkgs []string) (map[string]aurInfo, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	found := make(map[string]aurInfo, len(pkgs))
	for start := 0; start < len(pkgs); start += auditChunk {
		end := min(start+auditChunk, len(pkgs))
		form := url.Values{}
		requested := make(map[string]bool, end-start)
		for _, p := range pkgs[start:end] {
			form.Add("arg[]", p)
			requested[p] = true
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, strings.NewReader(form.Encode()))
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
		// Capped: a response past aurMaxResponse is cut off and fails to
		// decode, rather than growing without bound in memory.
		err = json.NewDecoder(io.LimitReader(resp.Body, aurMaxResponse)).Decode(&body)
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
			if requested[r.Name] { // ignore anything this chunk didn't ask about
				found[r.Name] = r
			}
		}
	}
	return found, nil
}

// classify turns one package's AUR data (nil = the AUR doesn't know it) plus
// its previous-audit snapshot entry into a finding. prev/hadPrev come from the
// snapshot; hadPrev is false for a package audited for the first time.
// Co-maintainers are only compared when prev recorded them (prev.hasCo): an
// entry saved before they were tracked is a baseline for that field, the same
// way a package's first audit is for all of them.
func classify(name string, ai *aurInfo, prev snapEntry, hadPrev bool, now time.Time) auditFinding {
	f := auditFinding{name: name}
	if ai == nil {
		f.flags = append(f.flags, "not on the AUR (removed, renamed, or a local build)")
		return f
	}
	cur := ""
	if ai.Maintainer != nil {
		cur = *ai.Maintainer
	}
	// Compare the raw values; only what's displayed is sanitized.
	switch {
	case hadPrev && prev.maintainer == "" && cur != "":
		f.high, f.maintainerChanged = true, true
		f.flags = append(f.flags, fmt.Sprintf("adopted by %s (was orphaned)", sanitize.Field(cur, aurFieldMax)))
	case hadPrev && prev.maintainer != "" && cur == "":
		f.flags = append(f.flags, fmt.Sprintf("orphaned (was %s)", sanitize.Field(prev.maintainer, aurFieldMax)))
	case hadPrev && prev.maintainer != cur:
		f.high, f.maintainerChanged = true, true
		f.flags = append(f.flags, fmt.Sprintf("maintainer changed: %s → %s", sanitize.Field(prev.maintainer, aurFieldMax), sanitize.Field(cur, aurFieldMax)))
	case cur == "":
		f.flags = append(f.flags, "orphaned")
	}
	if hadPrev && prev.hasCo {
		added, removed := diffNames(prev.coMaintainers, ai.CoMaintainers)
		if len(added) > 0 {
			f.high, f.coAdded = true, added
			f.flags = append(f.flags, coFlag(added, "added"))
		}
		if len(removed) > 0 {
			f.flags = append(f.flags, coFlag(removed, "removed"))
		}
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

// diffNames returns the names in cur but not in prev (added) and in prev but
// not in cur (removed), each sorted and without duplicates. Raw values are
// compared.
func diffNames(prev, cur []string) (added, removed []string) {
	p, c := nameSet(prev), nameSet(cur)
	for n := range c {
		if !p[n] {
			added = append(added, n)
		}
	}
	for n := range p {
		if !c[n] {
			removed = append(removed, n)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func nameSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// coFlag renders a co-maintainer change: "co-maintainer bob added",
// "co-maintainers bob, carol removed".
func coFlag(names []string, what string) string {
	noun := "co-maintainer"
	if len(names) > 1 {
		noun += "s"
	}
	return noun + " " + nameList(names, 3) + " " + what
}

// nameList joins up to limit sanitized names, then "+N more", so a package with
// a long co-maintainer list can't flood a report line or a popup.
func nameList(names []string, limit int) string {
	shown := make([]string, 0, min(len(names), limit))
	for _, n := range names[:min(len(names), limit)] {
		shown = append(shown, sanitize.Field(n, aurFieldMax))
	}
	s := strings.Join(shown, ", ")
	if len(names) > limit {
		s += fmt.Sprintf(" +%d more", len(names)-limit)
	}
	return s
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
func classifyAll(pkgs []string, info map[string]aurInfo, prev map[string]snapEntry, hadBaseline bool, now time.Time) []auditFinding {
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

// snapEntry is one package in the snapshot: its maintainer ("" = orphaned)
// and its co-maintainers (sorted, raw).
//
// On disk it is {"maintainer": "alice", "comaintainers": ["bob"]}. Snapshots
// written before co-maintainers were tracked hold a bare "alice" per package
// instead; that still loads, as an entry with hasCo false, and so does an
// object without "comaintainers". hasCo false means "co-maintainers never
// recorded": classify then treats them as a baseline (no alarm, like a first
// audit), and the next save records them. Without that, upgrading archmaid
// would gate every package that has a co-maintainer. An entry saved with
// hasCo false (a package the AUR didn't report, so its old entry is kept)
// leaves "comaintainers" out, so it reads back the same way.
type snapEntry struct {
	maintainer    string
	coMaintainers []string
	hasCo         bool
}

type snapEntryJSON struct {
	Maintainer    *string   `json:"maintainer"`
	CoMaintainers *[]string `json:"comaintainers,omitempty"`
}

func (e snapEntry) MarshalJSON() ([]byte, error) {
	j := snapEntryJSON{Maintainer: &e.maintainer}
	if e.hasCo {
		co := append([]string{}, e.coMaintainers...) // [] rather than null
		j.CoMaintainers = &co
	}
	return json.Marshal(j)
}

// UnmarshalJSON accepts the current object and the legacy bare string. Any
// other shape — null, a number, an object without "maintainer" — is an error,
// which makes the whole snapshot baselineCorrupt.
func (e *snapEntry) UnmarshalJSON(b []byte) error {
	var legacy string
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &legacy); err != nil {
			return err
		}
		*e = snapEntry{maintainer: legacy}
		return nil
	}
	if len(b) == 0 || b[0] != '{' {
		return fmt.Errorf("snapshot entry is neither a maintainer nor an object: %.20s", b)
	}
	var j snapEntryJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	if j.Maintainer == nil {
		return errors.New("snapshot entry without a maintainer")
	}
	*e = snapEntry{maintainer: *j.Maintainer}
	if j.CoMaintainers != nil {
		e.coMaintainers, e.hasCo = sortedNames(*j.CoMaintainers), true
	}
	return nil
}

// entryFor is the snapshot entry for what the AUR reports now.
func entryFor(ai aurInfo) snapEntry {
	e := snapEntry{coMaintainers: sortedNames(ai.CoMaintainers), hasCo: true}
	if ai.Maintainer != nil {
		e.maintainer = *ai.Maintainer
	}
	return e
}

// sortedNames is names sorted, without duplicates (nil for none).
func sortedNames(names []string) []string {
	var out []string
	for n := range nameSet(names) {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// loadBaseline reads the previous audit's name → snapshot entry map (see
// snapEntry for the format, old and new). Only a file that doesn't exist
// reads as baselineMissing; any other read error, invalid JSON, a JSON null
// or an entry of the wrong shape is baselineCorrupt.
func loadBaseline() (map[string]snapEntry, baselineState) {
	return loadBaselineFrom(baselinePath())
}

// loadBaselineFrom is loadBaseline for an explicit snapshot path.
func loadBaselineFrom(path string) (map[string]snapEntry, baselineState) {
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, baselineMissing
	case err != nil:
		return nil, baselineCorrupt
	}
	var m map[string]snapEntry
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
// maintainer and co-maintainers, keeping the old entry for any package the
// AUR didn't report this time (a transient removal must not erase its
// history). This is also where an entry from an old-format snapshot gets its
// co-maintainers recorded.
func nextBaseline(pkgs []string, info map[string]aurInfo, prev map[string]snapEntry) map[string]snapEntry {
	next := make(map[string]snapEntry, len(pkgs))
	for _, name := range pkgs {
		if ai, ok := info[name]; ok {
			next[name] = entryFor(ai)
		} else if p, ok := prev[name]; ok {
			next[name] = p
		}
	}
	return next
}

// saveBaseline records the audited packages' maintainers (see nextBaseline
// and writeBaseline).
func saveBaseline(pkgs []string, info map[string]aurInfo, prev map[string]snapEntry) error {
	return writeBaseline(baselinePath(), nextBaseline(pkgs, info, prev))
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
// path is the snapshot file: the one the audit read (auditTarget.snapshot).
func writeBaseline(path string, next map[string]snapEntry) error {
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	unlock, err := lockBaseline(dir)
	if err != nil {
		return err
	}
	defer unlock()

	if _, state := loadBaselineFrom(path); state == baselineCorrupt {
		aside := path + ".corrupt-" + time.Now().Format("20060102-150405")
		if err := os.Rename(path, aside); err != nil {
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
	if err := os.Rename(tmp.Name(), path); err != nil {
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
	prev map[string]snapEntry

	// target is what the audit ran against, set even when it failed: the
	// snapshot it read is the one saveSnapshot writes, and its log and
	// dry-run settings are the ones the report and the commit use.
	target auditTarget
}

// saveSnapshot advances the maintainer snapshot to what this audit saw.
// performAudit never saves: the caller decides when, because saving is what
// acknowledges a maintainer change. Saved too early (before the user has
// answered the gate), a declined change would read as settled next run.
//
// With holdHigh, packages with a high finding keep their whole previous entry
// (maintainer and co-maintainers), so they're flagged again until
// acknowledged, and a corrupt snapshot is left
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
	return writeBaseline(r.target.snapshot, next)
}

// snapshotCommit is the deferred save for an audit's caller to run (nil in a
// dry run, which never writes the snapshot). It logs a failed save — the
// changes then stay flagged next time — and, with accepting, records that the
// user accepted flagged maintainer changes.
func snapshotCommit(res auditResult, holdHigh, accepting bool) func() error {
	if res.target.dryRun {
		return nil
	}
	return func() error {
		if err := res.saveSnapshot(holdHigh); err != nil {
			res.target.log("WARN", "AUR snapshot not saved: "+err.Error(), "")
			return err
		}
		if accepting {
			res.target.log("ACCEPT", "AUR maintainer changes accepted; snapshot updated", "")
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
//
// The whole audit runs under one deadline, the target's (auditDeadline), on
// top of ctx; once ctx is cancelled (an abort) it returns promptly with ctx's
// error.
func performAudit(ctx context.Context, now time.Time) (auditResult, error) {
	return performAuditFor(ctx, now, currentAuditTarget())
}

// auditTarget is every setting an audit reads: the AUR endpoint, the
// snapshot file (read, and later written: see saveSnapshot), the deadline,
// whether this is a dry run and where its log entries go. It is read from
// the package vars on the model's goroutine, before the audit goes off on
// its own (the menu action's Cmd, the step's run, a prefetch), so the audit
// never reads those vars while something else (a test, mostly) changes them.
type auditTarget struct {
	rpcURL   string
	snapshot string
	deadline time.Duration
	dryRun   bool
	logs     *logstore.Store // where its log entries go
}

func currentAuditTarget() auditTarget {
	return auditTarget{
		rpcURL: aurRPCURL, snapshot: baselinePath(), deadline: auditDeadline,
		dryRun: dryRun, logs: logs(),
	}
}

// log appends an entry to the audit log the target captured (a Store never
// changes once made, so this is safe off the model's goroutine). Like
// appendLog, a failed write is dropped.
func (t auditTarget) log(kind, label, body string) {
	_ = t.logs.Append(logCategoryAudit, kind, label, body)
}

// performAuditFor is performAudit against an explicit target.
func performAuditFor(ctx context.Context, now time.Time, t auditTarget) (auditResult, error) {
	failed := auditResult{target: t}
	ctx, cancel := context.WithTimeout(ctx, t.deadline)
	defer cancel()
	pkgs := foreignPackages(ctx)
	if err := ctx.Err(); err != nil {
		return failed, err // cut short: an empty list proves nothing
	}
	if len(pkgs) == 0 {
		return failed, errNoForeign
	}
	info, err := fetchAURInfoAt(ctx, t.rpcURL, pkgs)
	if err != nil {
		return failed, err
	}
	prev, state := loadBaselineFrom(t.snapshot)
	findings := classifyAll(pkgs, info, prev, state == baselineOK, now)
	if state == baselineCorrupt {
		findings = append([]auditFinding{snapshotFinding()}, findings...)
	}
	return auditResult{total: len(pkgs), findings: findings, baseline: state, pkgs: pkgs, info: info, prev: prev, target: t}, nil
}

// discoverAURAudit runs the audit off the UI thread and returns it as a
// discoveryMsg: a notice for "nothing to audit" / "couldn't reach the AUR", or
// the rendered report for the audit screen. The target is captured here, on
// the model's goroutine, not in the Cmd.
func discoverAURAudit() tea.Cmd {
	t := currentAuditTarget()
	return func() tea.Msg { return runAuditFor(time.Now(), t) }
}

// runAudit is the menu action's audit against the current settings.
func runAudit(now time.Time) discoveryMsg {
	return runAuditFor(now, currentAuditTarget())
}

func runAuditFor(now time.Time, t auditTarget) discoveryMsg {
	res, err := performAuditFor(context.Background(), now, t)
	switch {
	case errors.Is(err, errNoForeign):
		return discoveryMsg{kind: "audit", notice: "No foreign (AUR or locally built) packages installed."}
	case err != nil:
		return discoveryMsg{kind: "audit", notice: "Couldn't reach the AUR: " + shortErrWithin(err, t.deadline)}
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

// auditStep is the first step of Full Cleanup and of Run topgrade: the audit,
// run in-process. It sits directly in front of topgrade so that a
// high-severity finding (an adoption, a maintainer change or an added
// co-maintainer) can put a confirm in front of the update — the one step that would build a taken-over AUR
// package. Declining skips only the update; in Full Cleanup the rest of the
// cleanup still runs. The audit can't *hold back* a single package; that's the
// yay cooldown hook's job (see HANDOVER.md).
//
// alwaysLog: the step's own log entry is the audit record, so unlike the menu
// action it doesn't call logAudit. It never needs sudo. An abort cancels the
// step's ctx, which ends the lookup at once; the late result is then dropped by
// stepSeq like any other.
//
// aurAudit marks it for activate, which starts the lookup as an auditFetch
// the moment the action is chosen, so it runs while the password prompt is
// open; launch then awaits that instead of calling run.
//
// Without a prefetch, run audits the target captured here, when the queue is
// built on the model's goroutine (the moment a prefetch would capture it),
// not when run executes in its Cmd.
func auditStep() step {
	t := currentAuditTarget()
	return step{
		title: "Auditing AUR packages…", logLabel: "AUR audit",
		category: logCategoryAudit, alwaysLog: true,
		run: func(ctx context.Context) stepResult {
			return auditStepResult(performAuditFor(ctx, time.Now(), t))
		},
		aurAudit: true,
	}
}

// runAuditStep is the audit step's run against the current settings.
func runAuditStep(ctx context.Context) stepResult {
	return auditStepResult(performAudit(ctx, time.Now()))
}

// auditFetch is an audit started ahead of its step: performAudit on its own
// goroutine, its result published by closing done. The model owns it
// (model.auditFetch) and cancels it when the run is abandoned before or
// during the step: esc on the password prompt, a declined gate, an abort.
// It only reads; committing the snapshot is still the step's result's job
// (see auditStepResult), so nothing is saved before the gate is answered.
type auditFetch struct {
	cancel context.CancelFunc
	target auditTarget   // read by startAuditFetch, not on the fetch's goroutine
	done   chan struct{} // closed once res and err are set
	res    auditResult
	err    error
}

func startAuditFetch() *auditFetch {
	ctx, cancel := context.WithCancel(context.Background())
	f := &auditFetch{cancel: cancel, target: currentAuditTarget(), done: make(chan struct{})}
	go func() {
		defer close(f.done)
		f.res, f.err = performAuditFor(ctx, time.Now(), f.target)
	}()
	return f
}

// run is the audit step's run when the lookup was prefetched: it waits for
// the fetch rather than starting another. An abort cancels both ctx and the
// fetch, so it returns at once either way.
func (f *auditFetch) run(ctx context.Context) stepResult {
	select {
	case <-f.done:
		return auditStepResult(f.res, f.err)
	case <-ctx.Done():
		f.cancel()
		return auditStepResult(auditResult{target: f.target}, ctx.Err())
	}
}

// auditStepResult turns an audit into the step's result: the plain-text
// report, a gate on topgrade for a high finding, and the snapshot commit.
func auditStepResult(res auditResult, err error) stepResult {
	switch {
	case errors.Is(err, errNoForeign):
		return stepResult{output: "No foreign (AUR or locally built) packages installed — nothing to audit."}
	case err != nil:
		return stepResult{output: "Couldn't reach the AUR: " + shortErrWithin(err, res.target.deadline), err: err}
	}
	r := stepResult{output: auditText(res)}
	if res.baseline == baselineCorrupt {
		// Fail closed: with no readable snapshot, a takeover would go unseen.
		r.confirmNext = "The AUR maintainer snapshot is unreadable, so maintainer changes can't be checked. Update anyway?"
	} else if why := gateReason(res.findings); why != "" {
		// Name the packages: the popup covers the report that explains it.
		r.confirmNext = why + " Update anyway?"
	}
	// Run by handleStepResult right away when nothing gates, otherwise only if
	// the gated update is accepted: declining leaves the snapshot as it was,
	// so the same change gates the next run too.
	r.commit = snapshotCommit(res, false, r.confirmNext != "")
	return r
}

// gateReason is the sentence the gate's confirm opens with, naming the
// high-severity packages and what changed ("" if nothing gates):
//
//	victim changed maintainer since the last audit.
//	Since the last audit: foo: co-maintainer bob added.
//	Since the last audit: victim changed maintainer; foo: co-maintainer bob added.
//
// With more than one package gaining co-maintainers it names just the
// packages ("a and b added co-maintainers"), to stay popup-sized.
func gateReason(findings []auditFinding) string {
	var changed, coPkgs []string
	var co auditFinding
	for _, f := range findings {
		if f.maintainerChanged {
			changed = append(changed, f.name)
		}
		if len(f.coAdded) > 0 {
			coPkgs, co = append(coPkgs, f.name), f
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
		parts = append(parts, sanitize.Field(co.name, aurFieldMax)+": "+coFlag(co.coAdded, "added"))
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
		names = append(names, sanitize.Field(n, aurFieldMax))
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
	return shortErrWithin(err, auditDeadline)
}

// shortErrWithin is shortErr for an audit that ran under deadline (its
// target's), rather than whatever auditDeadline holds by now.
func shortErrWithin(err error, deadline time.Duration) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("no answer within %s", deadline)
	}
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
	res.target.log("AUDIT", label, findingLines(res.findings))
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
	if note := baselineNote(res.baseline, res.target.dryRun); note != "" {
		b.WriteString("\n" + note + "\n")
	}
	if high > 0 && !res.target.dryRun {
		b.WriteString("\nThe flagged maintainers are only recorded if you accept the update; declining keeps them flagged for the next audit.\n")
	}
	return b.String()
}

// baselineNote explains the snapshot state, or "" when there's nothing to say.
func baselineNote(state baselineState, dryRun bool) string {
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
		w = max(w, utf8.RuneCountInString(sanitize.Field(f.name, aurFieldMax)))
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
	if note := baselineNote(res.baseline, res.target.dryRun); note != "" {
		b.WriteString(statusStyle.Render(note) + "\n")
	}
	if high > 0 && !res.target.dryRun {
		b.WriteString(statusStyle.Render("Flagged maintainers stay flagged on every audit until you've checked them and press a to accept.") + "\n")
	}
	b.WriteString(statusStyle.Render("Recent = pushed within 72h, the same window as the yay cooldown hook."))
	return b.String()
}
