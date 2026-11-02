package pkgdb

import "strings"

// Satisfied reports whether an installed package meets the dependency raw
// ("glibc>=2.38", "libcap.so=2-64", "sh"): one with that name and a version
// that satisfies the constraint, or one that provides the name (a soname or a
// virtual) at a satisfying version. As pacman does, an unversioned provide
// does not meet a versioned dependency.
func (db *DB) Satisfied(raw string) bool {
	name, op, ver := splitConstraint(raw)
	if name == "" {
		return false
	}
	s := db.cur.Load()
	if l, ok := s.local[name]; ok && meets(l.Version, op, ver) {
		return true
	}
	for _, l := range s.local {
		for _, pr := range l.Provides {
			if pr.Name != name {
				continue
			}
			_, _, pv := splitConstraint(pr.Raw)
			if op == "" || (pv != "" && meets(pv, op, ver)) {
				return true
			}
		}
	}
	return false
}

// splitConstraint cuts "foo>=1.2" into "foo", ">=", "1.2".
func splitConstraint(raw string) (name, op, ver string) {
	raw = strings.TrimSpace(raw)
	i := strings.IndexAny(raw, "<>=")
	if i < 0 {
		return raw, "", ""
	}
	j := i
	for j < len(raw) && strings.ContainsRune("<>=", rune(raw[j])) {
		j++
	}
	return strings.TrimSpace(raw[:i]), raw[i:j], strings.TrimSpace(raw[j:])
}

// meets reports whether have satisfies "op want" (an empty op always does).
func meets(have, op, want string) bool {
	if op == "" {
		return true
	}
	c := Vercmp(have, want)
	switch op {
	case "=", "==":
		return c == 0
	case ">=":
		return c >= 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	case "<":
		return c < 0
	}
	return false
}
