package aurbuild

import (
	"bufio"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Srcinfo is a parsed .SRCINFO: the text makepkg --printsrcinfo wrote and
// the AUR committed. It is read as text; nothing here sources a PKGBUILD.
type Srcinfo struct {
	Base string
	// Version is "epoch:pkgver-pkgrel" ("pkgver-pkgrel" without an epoch).
	Version string
	// URL is the project's url= (global), for the source-host hint.
	URL string
	// Depends, Makedepends, Checkdepends are the global lists, raw
	// ("foo>=1"), every architecture's entries together.
	Depends, Makedepends, Checkdepends []string
	Sources                            []Source
	Packages                           []SrcPackage
}

// SrcPackage is one pkgname section. Depends is the section's own list
// when it sets one (Own), else the global list applies, as in makepkg.
type SrcPackage struct {
	Name    string
	Depends []string
	Own     bool
}

// Source is one source array entry.
type Source struct {
	URL      string
	Checksum bool // false: SKIP, or no checksum array covers it
}

// maxSrcinfo caps what ParseSrcinfo reads.
const maxSrcinfo = 1 << 20

// ErrSrcinfo wraps a .SRCINFO that cannot be used.
var ErrSrcinfo = errors.New("unusable .SRCINFO")

// ParseSrcinfo reads a .SRCINFO. Lines are "key = value", indented or not;
// '#' lines and blank lines are skipped. The global section starts with
// pkgbase; each pkgname starts a package section.
func ParseSrcinfo(text string) (Srcinfo, error) {
	if len(text) > maxSrcinfo {
		return Srcinfo{}, fmt.Errorf("%w: over %d bytes", ErrSrcinfo, maxSrcinfo)
	}
	var si Srcinfo
	var epoch, ver, rel string
	src := map[string][]string{}             // by architecture suffix ("" generic)
	sums := map[string]map[string][]string{} // arch -> algorithm -> values
	var cur *SrcPackage
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64<<10), maxSrcinfo)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return Srcinfo{}, fmt.Errorf("%w: line %q is not key = value", ErrSrcinfo, clip(line))
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "pkgbase":
			si.Base = v
			continue
		case "pkgname":
			si.Packages = append(si.Packages, SrcPackage{Name: v})
			cur = &si.Packages[len(si.Packages)-1]
			continue
		}
		field, arch, _ := strings.Cut(k, "_")
		if cur != nil {
			// inside a pkgname section only its own depends matter here
			if field == "depends" {
				cur.Depends = append(cur.Depends, v)
				cur.Own = true
			}
			continue
		}
		switch {
		case k == "epoch":
			epoch = v
		case k == "pkgver":
			ver = v
		case k == "pkgrel":
			rel = v
		case k == "url":
			si.URL = v
		case field == "depends":
			si.Depends = append(si.Depends, v)
		case field == "makedepends":
			si.Makedepends = append(si.Makedepends, v)
		case field == "checkdepends":
			si.Checkdepends = append(si.Checkdepends, v)
		case field == "source":
			src[arch] = append(src[arch], v)
		case strings.HasSuffix(field, "sums"):
			if sums[arch] == nil {
				sums[arch] = map[string][]string{}
			}
			sums[arch][field] = append(sums[arch][field], v)
		}
	}
	if err := sc.Err(); err != nil {
		return Srcinfo{}, fmt.Errorf("%w: %v", ErrSrcinfo, err)
	}
	if si.Base == "" || ver == "" || len(si.Packages) == 0 {
		return Srcinfo{}, fmt.Errorf("%w: no pkgbase, pkgver or pkgname", ErrSrcinfo)
	}
	si.Version = ver
	if rel != "" {
		si.Version += "-" + rel
	}
	if epoch != "" && epoch != "0" {
		si.Version = epoch + ":" + si.Version
	}
	archs := make([]string, 0, len(src))
	for a := range src {
		archs = append(archs, a)
	}
	sort.Strings(archs)
	for _, a := range archs {
		for i, u := range src[a] {
			s := Source{URL: u}
			for _, col := range sums[a] {
				if i < len(col) && col[i] != "SKIP" {
					s.Checksum = true
				}
			}
			si.Sources = append(si.Sources, s)
		}
	}
	return si, nil
}

// clip shortens s for an error message.
func clip(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

// DepName strips the version constraint of a dependency ("glibc>=2.38"
// -> "glibc").
func DepName(d string) string {
	if i := strings.IndexAny(d, "<>=: "); i >= 0 {
		d = d[:i]
	}
	return d
}

// EffectiveDepends is the runtime dependency list of pkgname: its own
// section's, else the global one.
func (si Srcinfo) EffectiveDepends(name string) []string {
	for _, p := range si.Packages {
		if p.Name == name && p.Own {
			return p.Depends
		}
	}
	return si.Depends
}

// HasPackage reports whether name is one of the base's pkgnames.
func (si Srcinfo) HasPackage(name string) bool {
	for _, p := range si.Packages {
		if p.Name == name {
			return true
		}
	}
	return false
}
