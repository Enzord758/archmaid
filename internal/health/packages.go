package health

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/audit"
	"github.com/Enzord758/archmaid/internal/aursearch"
	"github.com/Enzord758/archmaid/internal/logstore"
	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/sanitize"
)

// Foreign is an installed package that is in no sync database (pacman -Qm).
type Foreign struct{ Name, Version string }

// AURPkg is what the AUR check needs of an RPC answer.
type AURPkg struct {
	Name      string
	OutOfDate bool
}

// AUR check limits.
const (
	// AURTimeout bounds the whole check (its own request budget: news runs in
	// parallel with its own 10 s, both inside Run's 15 s).
	AURTimeout = 10 * time.Second
	// MaxForeign caps how many names are asked about (aursearch splits them
	// into requests of 150): a machine with more is sampled by name order.
	MaxForeign = 300
	aurShown   = 4
)

// localForeign is the real Foreign: the local database through internal/pkgdb
// (the sync databases decide what is foreign, as pacman -Qm does), at pacman's
// default paths.
func localForeign() ([]Foreign, error) {
	return foreignAt(pkgdb.Paths{Sync: "/var/lib/pacman/sync", Local: "/var/lib/pacman/local", Conf: "/etc/pacman.conf"})
}

func foreignAt(p pkgdb.Paths) ([]Foreign, error) {
	db, err := pkgdb.Open(p)
	if err != nil {
		return nil, err
	}
	var out []Foreign
	for _, p := range db.Installed(pkgdb.ViewForeign) {
		out = append(out, Foreign{Name: p.Name, Version: p.InstalledVersion})
	}
	return out, nil
}

// aurInfo is the real AURInfo: internal/aursearch's client (its User-Agent,
// 8 s per request, 8 MiB cap, malformed entries dropped, unasked names
// ignored).
func aurInfo(ctx context.Context, names []string) ([]AURPkg, error) {
	return aurInfoAt(ctx, aursearch.DefaultBaseURL, names)
}

func aurInfoAt(ctx context.Context, base string, names []string) ([]AURPkg, error) {
	pk, err := aursearch.New(aursearch.Config{BaseURL: base}).Info(ctx, names)
	if err != nil {
		return nil, err
	}
	out := make([]AURPkg, 0, len(pk))
	for _, p := range pk {
		a := AURPkg{Name: p.Name}
		if p.AUR != nil && !p.AUR.OutOfDate.IsZero() {
			a.OutOfDate = true
		}
		out = append(out, a)
	}
	return out, nil
}

// aurCandidates are the foreign packages worth asking the AUR about: legal
// names only, and not "-debug" packages (makepkg builds those next to an AUR
// package; they are never on the AUR themselves). Sorted, deduplicated, at most
// MaxForeign.
func aurCandidates(f []Foreign) []string {
	seen := map[string]bool{}
	var names []string
	for _, p := range f {
		if !pkgdb.ValidName(p.Name) || strings.HasSuffix(p.Name, "-debug") || seen[p.Name] {
			continue
		}
		seen[p.Name] = true
		names = append(names, p.Name)
	}
	sort.Strings(names)
	if len(names) > MaxForeign {
		names = names[:MaxForeign]
	}
	return names
}

func checkAUR(ctx context.Context, d Deps) Check {
	if d.Foreign == nil || d.AURInfo == nil {
		return unknown(IDAUR, TitleAUR, "can't read installed packages")
	}
	foreign, err := d.Foreign()
	if err != nil {
		return unknown(IDAUR, TitleAUR, "can't read installed packages")
	}
	names := aurCandidates(foreign)
	if len(names) == 0 {
		return Check{State: OK, Detail: "No AUR packages"}
	}
	ctx, cancel := context.WithTimeout(ctx, AURTimeout)
	defer cancel()
	found, err := d.AURInfo(ctx, names)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return unknown(IDAUR, TitleAUR, "AUR check timed out")
		}
		return unknown(IDAUR, TitleAUR, "Can't reach the AUR")
	}
	known := map[string]AURPkg{}
	for _, p := range found {
		known[p.Name] = p
	}
	// A package the AUR doesn't know is "gone" only if the audit's snapshot
	// says it was in the AUR before. Without that it is a local build (or from
	// a repo this machine doesn't have): never an alarm. With no readable
	// snapshot there are no "gone" verdicts at all.
	var seen map[string]bool
	haveSnap := false
	if d.AURSeen != nil {
		seen, haveSnap = d.AURSeen()
	}
	// What is held on purpose is not an alarm: an out-of-date package whose
	// pending update waits in the cooldown (the maintainer already published
	// the fix), and one the AUR audit holds (it has its own report).
	var held HeldSet
	if d.Held != nil {
		held = d.Held()
	}
	var gone, flagged []string
	inAUR, other, cool, audited := 0, 0, 0, 0
	for _, n := range names {
		if held.Cooldown[n] {
			cool++
		}
		if held.Audit[n] {
			audited++
		}
		p, ok := known[n]
		switch {
		case !ok && haveSnap && seen[n]:
			gone = append(gone, n)
		case !ok:
			other++
		case p.OutOfDate && !held.Cooldown[n] && !held.Audit[n]:
			flagged = append(flagged, n)
			inAUR++
		default:
			inAUR++
		}
	}
	heldNote := ""
	if cool > 0 {
		heldNote += " · " + plural(cool, "update", "updates") + " held (cooldown)"
	}
	if audited > 0 {
		heldNote += fmt.Sprintf(" · %d held by the AUR audit", audited)
	}
	if len(gone) == 0 && len(flagged) == 0 {
		return Check{State: OK, Detail: sanitize.Field(aurOKDetail(inAUR, other, haveSnap)+heldNote, DetailMax)}
	}
	var parts []string
	if len(gone) > 0 {
		parts = append(parts, "not in the AUR anymore: "+nameList(gone))
	}
	if len(flagged) > 0 {
		parts = append(parts, "flagged out of date: "+nameList(flagged))
	}
	return Check{State: Look, Detail: sanitize.Field(strings.Join(parts, " · ")+heldNote, DetailMax)}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// HeldSet is what is held back on purpose, by package name.
type HeldSet struct {
	// Cooldown: foreign packages with a pending AUR update held by the
	// cooldown (updates-cache.json v3: "packages" entries with src "aur" and
	// held true; with paru nothing is held, it prints no age tags).
	Cooldown map[string]bool
	// Audit: packages the AUR audit holds, its high findings not accepted yet
	// (audit-state.json).
	Audit map[string]bool
}

// maxHeldFile caps updates-cache.json as read here; maxHeldNames the names.
const (
	maxHeldFile  = 1 << 20
	maxHeldNames = 200
)

// readHeld is the real Held: both files of the state dir, read only; a
// missing or unreadable one holds nothing.
func readHeld() HeldSet {
	dir := logstore.DefaultDir()
	if dir == "" {
		return HeldSet{}
	}
	return heldIn(dir)
}

func heldIn(dir string) HeldSet {
	var h HeldSet
	if f, err := os.Open(filepath.Join(dir, "updates-cache.json")); err == nil {
		if b, err := readCapped(f, maxHeldFile); err == nil {
			h.Cooldown = parseCooldownHeld(b)
		}
		f.Close()
	}
	if st, err := audit.ReadState(filepath.Join(dir, audit.StateFile)); err == nil {
		for i, f := range st.High {
			if i >= maxHeldNames {
				break
			}
			if pkgdb.ValidName(f.Pkg) {
				if h.Audit == nil {
					h.Audit = map[string]bool{}
				}
				h.Audit[f.Pkg] = true
			}
		}
	}
	return h
}

// parseCooldownHeld reads the names of the held AUR updates in
// updates-cache.json's bytes: src "aur" and held true. Text another program
// wrote: legal package names only, at most maxHeldNames entries looked at.
func parseCooldownHeld(b []byte) map[string]bool {
	var raw struct {
		Packages []struct {
			Name string `json:"name"`
			Src  string `json:"src"`
			Held *bool  `json:"held"`
		} `json:"packages"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	var out map[string]bool
	for i, p := range raw.Packages {
		if i >= maxHeldNames {
			break
		}
		if p.Src == "aur" && p.Held != nil && *p.Held && pkgdb.ValidName(p.Name) {
			if out == nil {
				out = map[string]bool{}
			}
			out[p.Name] = true
		}
	}
	return out
}

// auditSeen is the real AURSeen: the names in the AUR audit's maintainer
// snapshot (internal/audit, <state dir>/aur-maintainers.json), read only, never
// written or advanced. Every entry in it came from an AUR answer, so "in the
// snapshot" means "was in the AUR before". A missing or corrupt snapshot gives
// false.
func auditSeen() (map[string]bool, bool) {
	dir := logstore.DefaultDir()
	if dir == "" {
		return nil, false
	}
	return seenIn(filepath.Join(dir, audit.SnapshotFile))
}

func seenIn(path string) (map[string]bool, bool) {
	m, state := audit.LoadSnapshot(path)
	if state != audit.BaselineOK {
		return nil, false
	}
	seen := make(map[string]bool, len(m))
	for n := range m {
		seen[n] = true
	}
	return seen, true
}

// aurOKDetail: "3 AUR packages fine · 2 local builds". other counts the
// foreign packages the AUR doesn't know; with a snapshot they are local builds
// (it would have named them otherwise), without one we can't say.
func aurOKDetail(inAUR, other int, haveSnap bool) string {
	plural := func(n int, one, many string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, one)
		}
		return fmt.Sprintf("%d %s", n, many)
	}
	var s string
	if inAUR == 0 {
		s = "No AUR packages found"
	} else {
		s = plural(inAUR, "AUR package", "AUR packages") + " fine"
	}
	switch {
	case other == 0:
	case haveSnap:
		s += " · " + plural(other, "local build", "local builds")
	default:
		s += " · " + fmt.Sprintf("%d not found on the AUR (no audit history to say if %s removed)", other, map[bool]string{true: "it was", false: "they were"}[other == 1])
	}
	return s
}

func nameList(n []string) string {
	shown := n
	if len(shown) > aurShown {
		shown = shown[:aurShown]
	}
	s := strings.Join(shown, ", ")
	if len(n) > len(shown) {
		s += fmt.Sprintf(" +%d more", len(n)-len(shown))
	}
	return s
}

// Mirror check.
const (
	// DefaultMirrorList is pacman's mirrorlist.
	DefaultMirrorList = "/etc/pacman.d/mirrorlist"
	// MirrorTimeout bounds the one GET; MirrorBodyMax caps its body.
	MirrorTimeout = 5 * time.Second
	MirrorBodyMax = 64
	// MirrorMaxAge: a mirror that last synced longer ago than this is behind.
	MirrorMaxAge = 24 * time.Hour
	// maxMirrorList caps how much of the mirrorlist is read.
	maxMirrorList = 1 << 20
)

func checkMirror(ctx context.Context, d Deps) Check {
	if d.MirrorGet == nil {
		return unknown(IDMirror, TitleMirror, "can't check the mirror")
	}
	// pacman uses the first server of the repo section in pacman.conf's order:
	// a Server line in [core] before its Include of the mirrorlist beats the
	// mirrorlist's first. pacman-conf says which (read only); when it is not
	// there or fails, the mirrorlist's first is the best guess.
	base, host, err, asked := confMirror(ctx, d)
	if !asked {
		if d.MirrorList == "" {
			return unknown(IDMirror, TitleMirror, "can't check the mirror")
		}
		base, host, err = firstMirror(d.MirrorList)
		if err != nil {
			return unknown(IDMirror, TitleMirror, "can't read the mirrorlist")
		}
	}
	if err != nil {
		return unknown(IDMirror, TitleMirror, "can't read the mirror setting")
	}
	// Only /lastsync is ever fetched from the user's mirror.
	body, err := d.MirrorGet(ctx, base+"/lastsync")
	if err != nil {
		return unknown(IDMirror, TitleMirror, sanitize.Field(host, 60)+" has no lastsync")
	}
	ts, ok := parseLastSync(string(body))
	if !ok {
		return unknown(IDMirror, TitleMirror, sanitize.Field(host, 60)+" has no usable lastsync")
	}
	age := d.now().Sub(ts)
	if age < -time.Hour {
		return unknown(IDMirror, TitleMirror, sanitize.Field(host, 60)+" lastsync is in the future")
	}
	age = max(age, 0)
	detail := sanitize.Field(host, 60) + " synced " + Ago(age) + " ago"
	if age > MirrorMaxAge {
		return Check{State: Look, Detail: detail}
	}
	return Check{State: OK, Detail: detail}
}

// confMirror asks `pacman-conf --repo core Server` for pacman's first server
// of [core] (the servers in pacman's order, $repo and $arch expanded) and
// returns its base: the line without its /core/os/<arch>. asked is false when
// pacman-conf could not answer (no Query, not installed, a failure, no server
// at all: the caller falls back to the mirrorlist); with asked true a line that
// is not a usable mirror is err.
func confMirror(ctx context.Context, d Deps) (base, host string, err error, asked bool) {
	if d.Query == nil {
		return "", "", nil, false
	}
	out, qerr := d.Query(ctx, "pacman-conf", "--repo", "core", "Server")
	if qerr != nil {
		return "", "", nil, false
	}
	first := ""
	for _, l := range strings.Split(limitBytes(out, 16<<10), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			first = l
			break
		}
	}
	if first == "" {
		return "", "", nil, false
	}
	i := strings.LastIndex(first, "/core/os/")
	if i <= 0 || len(first) > 500 {
		return "", "", errors.New("not a mirror URL"), true
	}
	base, host, err = mirrorBase(first[:i] + "/$repo")
	return base, host, err, true
}

// parseLastSync reads a mirror's lastsync file: a unix time in seconds, plain
// digits only (a leading minus, a fraction or anything else is refused), after
// 2000 and before year 2200.
func parseLastSync(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 12 {
		return time.Time{}, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return time.Time{}, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 946684800 || n > 7258118400 {
		return time.Time{}, false
	}
	return time.Unix(n, 0), true
}

// firstMirror reads the mirrorlist at path and returns the first active
// `Server = ...` line's base URL (everything before "/$repo") and its host.
// The URL must be http or https with a host, no user info, query or fragment.
func firstMirror(path string) (base, host string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), 64<<10)
	read := 0
	for sc.Scan() {
		line := sc.Text()
		if read += len(line) + 1; read > maxMirrorList {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != "Server" {
			continue
		}
		v = strings.TrimSpace(v)
		if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		return mirrorBase(v)
	}
	return "", "", errors.New("no active Server line")
}

func mirrorBase(v string) (base, host string, err error) {
	i := strings.Index(v, "/$repo")
	if i <= 0 || len(v) > 500 {
		return "", "", errors.New("not a mirror URL")
	}
	base = strings.TrimRight(v[:i], "/")
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(base, " \t$") {
		return "", "", errors.New("not a mirror URL")
	}
	return base, u.Host, nil
}
