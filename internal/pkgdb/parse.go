package pkgdb

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/sanitize"
)

// Caps on untrusted database text. A sync DB is signed by pacman's keyring,
// but a mirror, a custom repo or a damaged file can hold anything, and what is
// read here ends up on screen.
const (
	maxEntryBytes = 1 << 20 // one desc file
	maxDescRunes  = 1024
	maxFieldRunes = 256 // URL, packager, a list item
	maxVerRunes   = 64
	maxListItems  = 512
)

// ValidName reports whether s is a legal pacman package name: what the
// databases may hold, and the only thing the install and remove paths may be
// handed later.
func ValidName(s string) bool {
	if s == "" || len(s) > 255 || s[0] == '-' || s[0] == '.' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '@' || c == '.' || c == '_' || c == '+' || c == '-':
		default:
			return false
		}
	}
	return true
}

// parseDesc parses one desc file ("%KEY%\nvalue\n...\n\n"). ok is false when
// the entry has no valid %NAME% or no %VERSION%: such an entry is skipped.
func parseDesc(data []byte) (p Package, ok bool) {
	var key string
	var vals []string
	flush := func() {
		if key != "" {
			p.set(key, vals)
		}
		key, vals = "", nil
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), maxEntryBytes)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		switch {
		case len(line) > 2 && line[0] == '%' && line[len(line)-1] == '%':
			flush()
			key = line[1 : len(line)-1]
		case line == "":
			flush()
		case key != "":
			vals = append(vals, line)
		}
	}
	flush()
	if !ValidName(p.Name) || p.Version == "" {
		return Package{}, false
	}
	return p, true
}

func (p *Package) set(key string, vals []string) {
	first := ""
	if len(vals) > 0 {
		first = vals[0]
	}
	switch key {
	case "NAME":
		p.Name = first // validated by parseDesc, never sanitized into validity
	case "VERSION":
		p.Version = sanitize.Field(first, maxVerRunes)
	case "DESC":
		p.Description = sanitize.Field(strings.Join(vals, " "), maxDescRunes)
	case "URL":
		p.URL = sanitize.Field(first, maxFieldRunes)
	case "PACKAGER":
		p.Packager = sanitize.Field(first, maxFieldRunes)
	case "CSIZE":
		p.DownloadSize = num(first)
	case "ISIZE", "SIZE":
		p.InstalledSize = num(first)
	case "BUILDDATE":
		p.BuildDate = unix(first)
	case "INSTALLDATE":
		p.InstallDate = unix(first)
	case "REASON":
		p.Reason = ReasonExplicit
		if num(first) == 1 {
			p.Reason = ReasonDep
		}
	case "GROUPS":
		p.Groups = list(vals)
	case "LICENSE":
		p.Licenses = list(vals)
	case "DEPENDS":
		p.Depends = Deps(vals, false)
	case "OPTDEPENDS":
		p.OptDepends = Deps(vals, true)
	case "CONFLICTS":
		p.Conflicts = Deps(vals, false)
	case "REPLACES":
		p.Replaces = Deps(vals, false)
	case "PROVIDES":
		p.Provides = Deps(vals, false)
	}
}

func num(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func unix(s string) time.Time {
	n := num(s)
	if n == 0 || n > 1<<40 {
		return time.Time{}
	}
	return time.Unix(n, 0).UTC()
}

func list(vals []string) []string {
	if len(vals) > maxListItems {
		vals = vals[:maxListItems]
	}
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if v = sanitize.Field(v, maxFieldRunes); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Deps parses dependency-style lines ("glibc>=2.38", and for optional
// dependencies "tk: gui support") into Deps: every text sanitized, capped at
// maxListItems entries. Empty entries are dropped.
func Deps(lines []string, optional bool) []Dep {
	if len(lines) > maxListItems {
		lines = lines[:maxListItems]
	}
	out := make([]Dep, 0, len(lines))
	for _, l := range lines {
		d := Dep{}
		if optional {
			if i := strings.Index(l, ": "); i >= 0 {
				l, d.Desc = l[:i], sanitize.Field(l[i+2:], maxFieldRunes)
			}
		}
		d.Raw = sanitize.Field(strings.TrimSpace(l), maxFieldRunes)
		d.Name = depName(d.Raw)
		if d.Name != "" {
			out = append(out, d)
		}
	}
	return out
}

// depName strips the version constraint: "glibc>=2.38" -> "glibc".
func depName(d string) string {
	if i := strings.IndexAny(d, "<>="); i >= 0 {
		d = d[:i]
	}
	return strings.TrimSpace(d)
}
