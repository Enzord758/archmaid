package actions

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// servicesExcluded is systemd-internal/oneshot noise that is never offered
// for disabling: a unit whose name contains one of these is skipped.
var servicesExcluded = []string{"systemd-", "getty@", "emergency", "rescue", "initrd", "plymouth", "shadow",
	"pkgstats", "man-db", "mkinitcpio", "modprobe@", "audit", "dirmngr", "gpg-agent", "keyboxd", "dm-event",
	"lm_sensors", "nmb", "archlinux-keyring-wkd-sync"}

// listUnitsArgs is the listing discoverServices runs.
var listUnitsArgs = []string{"list-units", "--type=service", "--all", "--state=inactive", "--plain", "--no-legend"}

// skippedUnit is a listed unit the discovery doesn't check, and the rule
// that skipped it ("not loaded (masked)", "an instance or template",
// "excluded: systemd-").
type skippedUnit struct{ unit, rule string }

// parseInactiveServices picks the unit names out of
// `systemctl list-units --type=service --all --state=inactive --plain --no-legend`
// (columns UNIT LOAD ACTIVE SUB DESCRIPTION). Units that aren't loaded
// (not-found, masked, …) can't be enabled and are dropped; they print with a
// leading "●" marker that must not be mistaken for the name. Templates and
// instances ("name@.service", "name@arg.service") are dropped too: the
// unit-file listing this replaces only ever matched plain units. The result is
// filtered through servicesExcluded and sorted.
func parseInactiveServices(out string) []string {
	units, _ := splitInactiveServices(out)
	return units
}

// splitInactiveServices is parseInactiveServices with what it dropped:
// every listed .service unit is either checked (units, sorted, once each)
// or skipped with its rule (sorted by unit, once each).
func splitInactiveServices(out string) (units []string, skipped []skippedUnit) {
	for _, line := range nonEmptyLines(out) {
		f := strings.Fields(strings.TrimLeft(line, "●* \t"))
		if len(f) < 2 || !strings.HasSuffix(f[0], ".service") {
			continue
		}
		switch rule := excludedBy(f[0]); {
		case f[1] != "loaded":
			skipped = append(skipped, skippedUnit{f[0], "not loaded (" + f[1] + ")"})
		case strings.Contains(f[0], "@"):
			skipped = append(skipped, skippedUnit{f[0], "an instance or template"})
		case rule != "":
			skipped = append(skipped, skippedUnit{f[0], "excluded: " + rule})
		default:
			units = append(units, f[0])
		}
	}
	sort.Strings(units)
	slices.SortStableFunc(skipped, func(a, b skippedUnit) int { return strings.Compare(a.unit, b.unit) })
	skipped = slices.CompactFunc(skipped, func(a, b skippedUnit) bool { return a.unit == b.unit })
	return slices.Compact(units), skipped
}

// excludedBy is the servicesExcluded entry unit matches, or "".
func excludedBy(unit string) string {
	for _, p := range servicesExcluded {
		if strings.Contains(unit, p) {
			return p
		}
	}
	return ""
}

// enabledServices keeps the units `systemctl is-enabled` reports as exactly
// "enabled" (not enabled-runtime, static, masked, …).
func enabledServices(ctx context.Context, units []string) []string {
	states, _ := unitStates(ctx, units)
	var enabled []string
	for i, u := range units {
		if states[i] == "enabled" {
			enabled = append(enabled, u)
		}
	}
	return enabled
}

// unitStates is each unit's `systemctl is-enabled` state, in order, and
// the commands with their raw output, for the record. One batched call
// prints one state per unit; if the line count doesn't match (a unit
// systemctl couldn't resolve prints nothing on stdout) it falls back to one
// call each. A unit with no answer has state "".
func unitStates(ctx context.Context, units []string) (states []string, raw string) {
	if len(units) == 0 {
		return nil, ""
	}
	out, _ := proc.Query(ctx, "systemctl", append([]string{"is-enabled", "--"}, units...)...)
	raw = "$ systemctl is-enabled -- " + strings.Join(units, " ") + "\n" + string(out)
	states = nonEmptyLines(string(out))
	if len(states) != len(units) {
		states = states[:0]
		for _, u := range units {
			o, _ := proc.Query(ctx, "systemctl", "is-enabled", "--", u)
			raw += "$ systemctl is-enabled -- " + u + "\n" + string(o)
			states = append(states, strings.TrimSpace(string(o)))
		}
	}
	return states, raw
}

// discoverServices finds services that are enabled but not running, for the
// user to pick from. Listing loaded units is ~3 ms where `list-unit-files`
// costs ~430 ms, so it asks for the inactive units first and checks only
// those with one is-enabled call. A listing that fails is said as such (with
// systemctl's error), never as "nothing found"; is-enabled exits non-zero
// whenever a unit isn't enabled, so its status says nothing.
//
// Either way it says what it checked (Checked, or the Notice's fields):
// each stopped unit with its is-enabled state, each skipped one with the
// rule that skipped it, and the commands with their raw output for the log.
func discoverServices(ctx context.Context, _ Env) Discovery {
	listCmd := "$ systemctl " + strings.Join(listUnitsArgs, " ")
	out, err := proc.Query(ctx, "systemctl", listUnitsArgs...)
	if err != nil {
		return Notice{Text: "Couldn't list services: " + clipErr(err), Err: sanitize.OneLine(err.Error()),
			Details: []string{"Tried: systemctl " + strings.Join(listUnitsArgs, " ")},
			Log:     listCmd + "\n" + string(out) + "error: " + err.Error() + "\n"}
	}
	units, skipped := splitInactiveServices(string(out))
	states, raw := unitStates(ctx, units)
	var enabled, details []string
	for i, u := range units {
		state := states[i]
		if state == "" {
			state = "unknown"
		}
		if state == "enabled" {
			enabled = append(enabled, u)
		}
		details = append(details, u+": "+state)
	}
	for _, sk := range skipped {
		details = append(details, sk.unit+": skipped, "+sk.rule)
	}
	log := listCmd + "\n" + string(out) + raw
	found := "none are enabled"
	if len(enabled) > 0 {
		found = fmt.Sprintf("%d %s enabled", len(enabled), plural(len(enabled), "is", "are"))
	}
	summary := fmt.Sprintf("Checked %d stopped %s: %s.", len(units), plural(len(units), "service", "services"), found)
	if len(units) == 0 {
		summary = "No enabled-but-inactive services found. No stopped services to check."
	}
	if len(skipped) > 0 {
		summary += fmt.Sprintf(" Skipped %d (%s).", len(skipped), skipRules(skipped))
	}
	if len(enabled) == 0 {
		return Notice{Text: summary, Details: details, Log: log}
	}
	return NeedsSelection{Items: enabled, Steps: DisableUnits, Checked: Checked{Summary: summary, Details: details, Log: log}}
}

// skipRules names the rules that skipped units, most units first, at most
// three, then "…": "systemd-*, not loaded, instances".
func skipRules(skipped []skippedUnit) string {
	count := map[string]int{}
	var rules []string
	for _, sk := range skipped {
		var r string
		switch {
		case strings.HasPrefix(sk.rule, "excluded: "):
			r = strings.TrimPrefix(sk.rule, "excluded: ") + "*"
		case strings.HasPrefix(sk.rule, "not loaded"):
			r = "not loaded"
		default:
			r = "instances"
		}
		if count[r] == 0 {
			rules = append(rules, r)
		}
		count[r]++
	}
	slices.SortStableFunc(rules, func(a, b string) int { return count[b] - count[a] })
	if len(rules) > 3 {
		rules = append(rules[:3], "…")
	}
	return strings.Join(rules, ", ")
}

// plural is one or many by n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// DisableUnits is the services pick list's queue: one step that disables and
// stops the picked units; nothing picked is nothing to run. Its Items are
// the units: the root helper's disable-unit verb takes them from there.
func DisableUnits(units []string) []steps.Step {
	if len(units) == 0 {
		return nil
	}
	return []steps.Step{{
		ID: steps.IDDisableUnits, Verb: steps.VerbDisableUnit,
		Title: "Disabling services…", LogLabel: "Services disabled",
		Argv:      append([]string{"sudo", "systemctl", "disable", "--now", "--"}, units...),
		Items:     slices.Clone(units),
		NeedsPriv: true, AlwaysLog: true,
	}}
}
