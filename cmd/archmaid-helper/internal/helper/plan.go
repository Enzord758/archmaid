package helper

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Plan is what `pacman -Rns` would do to the current orphans: Targets are the
// orphans themselves (pacman -Qdtq), Items everything -s cascades into, as
// "name version" (pacman -Rs --print). Hash commits to both, so a confirm
// given for one plan can't be spent on another.
type Plan struct {
	Targets []string
	Items   []string
	Hash    string
}

// versionPattern is what pacman prints for %v: [epoch:]pkgver-pkgrel.
var versionPattern = regexp.MustCompile(`^[A-Za-z0-9._:+~-]{1,128}$`)

// PlanHash is the canonical hash of a plan. Inputs are sorted copies, so the
// hash doesn't depend on pacman's output order.
func PlanHash(targets, items []string) string {
	t, it := slices.Sorted(slices.Values(targets)), slices.Sorted(slices.Values(items))
	h := sha256.New()
	fmt.Fprint(h, "archmaid orphan plan v1\n")
	for _, s := range t {
		fmt.Fprintf(h, "target %s\n", s)
	}
	for _, s := range it {
		fmt.Fprintf(h, "remove %s\n", s)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// ComputePlan asks pacman for the orphan plan. It is read-only and needs no
// root: -Qdtq is a query and --print never touches the database lock.
func ComputePlan(r Runner) (Plan, error) {
	out, errOut, exit, err := r.Query(orphansArgv())
	if err != nil {
		return Plan{}, fmt.Errorf("pacman -Qdtq: %w", err)
	}
	targets := nonEmptyLines(out)
	if exit != 0 {
		// pacman exits 1 with no output when there are no orphans.
		if len(targets) == 0 && exit == 1 && strings.TrimSpace(errOut) == "" {
			return Plan{Hash: PlanHash(nil, nil)}, nil
		}
		return Plan{}, fmt.Errorf("pacman -Qdtq exited %d: %s", exit, firstLine(errOut))
	}
	for _, t := range targets {
		if !namePattern.MatchString(t) {
			return Plan{}, fmt.Errorf("unexpected package name from pacman -Qdtq: %q", t)
		}
	}
	targets = slices.Compact(slices.Sorted(slices.Values(targets)))
	if len(targets) == 0 {
		return Plan{Hash: PlanHash(nil, nil)}, nil
	}

	argv := cascadeArgv(targets)
	out, errOut, exit, err = r.Query(argv)
	if err != nil {
		return Plan{}, fmt.Errorf("pacman -Rs --print: %w", err)
	}
	if exit != 0 {
		return Plan{}, fmt.Errorf("pacman -Rs --print exited %d: %s", exit, firstLine(errOut))
	}
	var items []string
	for _, l := range nonEmptyLines(out) {
		name, ver, ok := strings.Cut(l, " ")
		if !ok || !namePattern.MatchString(name) || !versionPattern.MatchString(ver) {
			return Plan{}, fmt.Errorf("unexpected line from pacman -Rs --print: %q", l)
		}
		items = append(items, l)
	}
	items = slices.Compact(slices.Sorted(slices.Values(items)))
	return Plan{Targets: targets, Items: items, Hash: PlanHash(targets, items)}, nil
}

func nonEmptyLines(s string) []string {
	var out []string
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func firstLine(s string) string {
	if l := nonEmptyLines(s); len(l) > 0 {
		return l[0]
	}
	return "(no output)"
}
