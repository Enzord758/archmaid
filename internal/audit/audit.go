// Package audit is archmaid's AUR audit: a read-only check of the foreign
// packages (installed, but not from a sync repo, i.e. AUR or locally built)
// against the AUR's RPC, looking for the signals that matter for the
// maintainer-takeover pattern: a package that was orphaned then adopted, a
// maintainer who changed since the last audit, a co-maintainer added since
// then (a co-maintainer can push to the package's git just like the
// maintainer), an unmaintained package, a very recent push, or a package that
// vanished from the AUR entirely. It never touches the system; the only thing
// it writes is the maintainer snapshot, and only when a caller runs
// Result.Commit (never in a dry run).
//
// The RPC exposes the *current* maintainer and co-maintainers only, not their
// history, so a change is detected by diffing against a snapshot saved by the
// previous audit. The first audit therefore just establishes the baseline.
//
// It is UI-free: Result.Text is plain text, and the TUI renders its own
// report from Result's fields. Every setting an audit reads is in Config,
// which the caller fills in before the audit goes off on its own goroutine.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/sanitize"
)

// DefaultRPCURL is the AUR RPC v5 "info" endpoint.
const DefaultRPCURL = "https://aur.archlinux.org/rpc/v5/info"

// DefaultDeadline bounds a whole audit when Config.Deadline is zero.
const DefaultDeadline = 20 * time.Second

const (
	// Cooldown is how recent a push has to be to get flagged. Matches the
	// 72-hour hold in the recommended ~/.config/yay/init.lua hook, so the
	// audit flags exactly the packages yay would hold back.
	Cooldown = 72 * time.Hour
	// FieldMax caps an AUR-supplied name or maintainer for display.
	FieldMax = 64

	chunk = 100 // packages per RPC request
	// maxResponse caps one RPC response body, far above what a real
	// 100-package reply needs.
	maxResponse = 8 << 20
)

// ErrNoForeign means there was nothing to audit.
var ErrNoForeign = errors.New("no foreign packages installed")

// Config is every setting an audit reads. Fill it in where the settings live
// (in the TUI, the model's goroutine) before the audit goes off on its own:
// the audit and its Result read only this, never global state.
type Config struct {
	// RPCURL is the AUR RPC "info" endpoint (DefaultRPCURL in production).
	// Required: an empty one fails the audit rather than defaulting to the
	// real AUR.
	RPCURL string
	// SnapshotPath is the maintainer snapshot: read by Run, and the file
	// Result.Commit writes. Its directory also holds the lock file.
	SnapshotPath string
	// StatePath is audit-state.json (StateFile): the unacknowledged high
	// findings, written when an audit completes and again when a commit
	// accepts them. Empty disables it; a dry run never writes it.
	StatePath string
	// Deadline bounds the whole audit: the Foreign query and every RPC chunk
	// together, not each request on its own. Zero means DefaultDeadline.
	Deadline time.Duration
	// DryRun audits against the snapshot but never advances it: Commit
	// returns nil.
	DryRun bool
	// Foreign lists the installed foreign packages (pacman -Qqm). It must
	// honour ctx. An empty list is ErrNoForeign; an error fails the audit.
	Foreign func(ctx context.Context) ([]string, error)
	// Log, if set, records an entry in the audit log: the menu audit's
	// summary (Result.LogSummary), a failed save and an accept (Commit).
	Log func(kind, label, body string)
}

func (c Config) deadline() time.Duration {
	if c.Deadline <= 0 {
		return DefaultDeadline
	}
	return c.Deadline
}

func (c Config) log(kind, label, body string) {
	if c.Log != nil {
		c.Log(kind, label, body)
	}
}

// Info is the subset of an RPC "info" result the audit reads.
type Info struct {
	Name         string
	Maintainer   *string // null when orphaned
	LastModified int64
	OutOfDate    *int64 // null unless flagged out of date
	NumVotes     int
	// CoMaintainers can push to the package like its maintainer. aurweb leaves
	// the key out when there are none (like Depends), so absent reads as none.
	CoMaintainers []string
}

// Finding is one package's audit result. High marks the signals worth
// stopping for (a maintainer change/adoption, an added co-maintainer);
// everything else is a glance. MaintainerChanged and CoAdded say which of
// those it was, for the gate's confirm.
//
// Name and CoAdded are raw AUR values (they key the snapshot); Flags and Line
// are already sanitized for display.
type Finding struct {
	Name  string
	High  bool
	Flags []string

	MaintainerChanged bool
	CoAdded           []string // raw names, sorted

	// Reason is the short, sanitized why-it's-high ("maintainer alice →
	// mallory", "co-maintainer bob added") for audit-state.json; "" unless High.
	Reason string
}

// Line renders the finding, its name padded to width (see NameWidth).
func (f Finding) Line(width int) string {
	return fmt.Sprintf("%-*s  %s", width, sanitize.Field(f.Name, FieldMax), strings.Join(f.Flags, " · "))
}

// Run audits the foreign packages against the AUR and the maintainer
// snapshot without advancing it (see Result.Commit). Any error other than
// ErrNoForeign is the lookup failing. A corrupt snapshot becomes a
// high-severity finding of its own (see snapshotFinding).
//
// The whole audit runs under one deadline, cfg.Deadline, on top of ctx; once
// ctx is cancelled (an abort) it returns promptly with ctx's error. The
// Result carries cfg even when it fails, so Commit and the report use the
// settings the audit ran with.
func Run(ctx context.Context, cfg Config, now time.Time) (Result, error) {
	failed := Result{cfg: cfg}
	switch {
	case cfg.RPCURL == "":
		return failed, errors.New("audit: no AUR RPC URL configured")
	case cfg.SnapshotPath == "":
		return failed, errors.New("audit: no snapshot path configured")
	case cfg.Foreign == nil:
		return failed, errors.New("audit: no foreign-package lister configured")
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.deadline())
	defer cancel()
	pkgs, err := cfg.Foreign(ctx)
	if cerr := ctx.Err(); cerr != nil {
		return failed, cerr // cut short: an empty list proves nothing
	}
	if err != nil {
		return failed, err
	}
	if len(pkgs) == 0 {
		// A completed audit with nothing to flag: any earlier highs are moot.
		_, state := LoadSnapshot(cfg.SnapshotPath)
		failed.Baseline, failed.checked = state, now
		_ = failed.writeState(failed.state())
		return failed, ErrNoForeign
	}
	info, err := fetch(ctx, cfg.RPCURL, pkgs)
	if err != nil {
		return failed, err
	}
	prev, state := LoadSnapshot(cfg.SnapshotPath)
	findings := classifyAll(pkgs, info, prev, state == BaselineOK, now)
	if state == BaselineCorrupt {
		findings = append([]Finding{snapshotFinding()}, findings...)
	}
	// The one place a Result becomes committable (ok): the audit completed.
	res := Result{Total: len(pkgs), Findings: findings, Baseline: state, ok: true, pkgs: pkgs, info: info, prev: prev, cfg: cfg, checked: now}
	// Record the highs that are still unacknowledged. An audit that failed
	// (unreachable AUR, cancelled) returned above and leaves the file alone.
	_ = res.writeState(res.state())
	return res, nil
}

// fetch looks the packages up on the AUR RPC (POST, chunked so a large
// package list never hits URL limits) and returns the ones it knows about,
// keyed by name. Packages the AUR doesn't know are simply absent, and so is
// any result for a name that wasn't asked for.
//
// ctx bounds the whole lookup (Run sets the deadline) and cancelling it
// abandons a request mid-flight. The transport is private to the call and
// its idle connections are closed on return, so no connection goroutine
// outlives it.
func fetch(ctx context.Context, rpcURL string, pkgs []string) (map[string]Info, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	found := make(map[string]Info, len(pkgs))
	for start := 0; start < len(pkgs); start += chunk {
		end := min(start+chunk, len(pkgs))
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
			Results []Info
		}
		// Capped: a response past maxResponse is cut off and fails to
		// decode, rather than growing without bound in memory.
		err = json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(&body)
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
// Co-maintainers are only compared when prev recorded them (prev.HasCo): an
// entry saved before they were tracked is a baseline for that field, the same
// way a package's first audit is for all of them.
func classify(name string, ai *Info, prev Entry, hadPrev bool, now time.Time) Finding {
	f := Finding{Name: name}
	var reasons []string
	if ai == nil {
		f.Flags = append(f.Flags, "not on the AUR (removed, renamed, or a local build)")
		return f
	}
	cur := ""
	if ai.Maintainer != nil {
		cur = *ai.Maintainer
	}
	// Compare the raw values; only what's displayed is sanitized.
	switch {
	case hadPrev && prev.Maintainer == "" && cur != "":
		f.High, f.MaintainerChanged = true, true
		f.Flags = append(f.Flags, fmt.Sprintf("adopted by %s (was orphaned)", sanitize.Field(cur, FieldMax)))
		reasons = append(reasons, "adopted by "+sanitize.Field(cur, FieldMax))
	case hadPrev && prev.Maintainer != "" && cur == "":
		f.Flags = append(f.Flags, fmt.Sprintf("orphaned (was %s)", sanitize.Field(prev.Maintainer, FieldMax)))
	case hadPrev && prev.Maintainer != cur:
		f.High, f.MaintainerChanged = true, true
		f.Flags = append(f.Flags, fmt.Sprintf("maintainer changed: %s → %s", sanitize.Field(prev.Maintainer, FieldMax), sanitize.Field(cur, FieldMax)))
		reasons = append(reasons, fmt.Sprintf("maintainer %s → %s", sanitize.Field(prev.Maintainer, FieldMax), sanitize.Field(cur, FieldMax)))
	case cur == "":
		f.Flags = append(f.Flags, "orphaned")
	}
	if hadPrev && prev.HasCo {
		added, removed := diffNames(prev.CoMaintainers, ai.CoMaintainers)
		if len(added) > 0 {
			f.High, f.CoAdded = true, added
			f.Flags = append(f.Flags, coFlag(added, "added"))
			reasons = append(reasons, coFlag(added, "added"))
		}
		if len(removed) > 0 {
			f.Flags = append(f.Flags, coFlag(removed, "removed"))
		}
	}
	if age := now.Sub(time.Unix(ai.LastModified, 0)); age < Cooldown {
		f.Flags = append(f.Flags, "modified "+ageString(age)+" ago")
	}
	if ai.OutOfDate != nil {
		f.Flags = append(f.Flags, "flagged out of date")
	}
	if ai.NumVotes == 0 {
		f.Flags = append(f.Flags, "0 votes")
	}
	f.Reason = strings.Join(reasons, "; ")
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
		shown = append(shown, sanitize.Field(n, FieldMax))
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
func classifyAll(pkgs []string, info map[string]Info, prev map[string]Entry, hadBaseline bool, now time.Time) []Finding {
	var out []Finding
	for _, name := range pkgs {
		var ai *Info
		if v, ok := info[name]; ok {
			ai = &v
		}
		if ai == nil && strings.HasSuffix(name, "-debug") {
			continue
		}
		p, had := prev[name]
		f := classify(name, ai, p, hadBaseline && had, now)
		if len(f.Flags) > 0 {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].High && !out[j].High })
	return out
}

// ShortErr trims a failed audit's error to something that fits a notice
// popup. deadline is the one the audit ran under (its Config's).
func ShortErr(err error, deadline time.Duration) string {
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
