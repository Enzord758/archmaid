package health

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/aursearch"
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
	var gone, flagged []string
	for _, n := range names {
		p, ok := known[n]
		switch {
		case !ok:
			gone = append(gone, n)
		case p.OutOfDate:
			flagged = append(flagged, n)
		}
	}
	if len(gone) == 0 && len(flagged) == 0 {
		return Check{State: OK, Detail: fmt.Sprintf("%d AUR packages, all current on the AUR", len(names))}
	}
	var parts []string
	if len(gone) > 0 {
		parts = append(parts, "not in the AUR anymore: "+nameList(gone))
	}
	if len(flagged) > 0 {
		parts = append(parts, "flagged out of date: "+nameList(flagged))
	}
	return Check{State: Look, Detail: sanitize.Field(strings.Join(parts, " · "), DetailMax)}
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
	if d.MirrorGet == nil || d.MirrorList == "" {
		return unknown(IDMirror, TitleMirror, "can't check the mirror")
	}
	base, host, err := firstMirror(d.MirrorList)
	if err != nil {
		return unknown(IDMirror, TitleMirror, "can't read the mirrorlist")
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
