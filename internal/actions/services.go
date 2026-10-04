package actions

import (
	"context"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/steps"
)

// servicesExclude is systemd-internal/oneshot noise that is never offered for
// disabling.
var servicesExclude = regexp.MustCompile(`systemd-|getty@|emergency|rescue|initrd|plymouth|shadow|pkgstats|man-db|` +
	`mkinitcpio|modprobe@|audit|dirmngr|gpg-agent|keyboxd|dm-event|lm_sensors|nmb|archlinux-keyring-wkd-sync`)

// parseInactiveServices picks the unit names out of
// `systemctl list-units --type=service --all --state=inactive --plain --no-legend`
// (columns UNIT LOAD ACTIVE SUB DESCRIPTION). Units that aren't loaded
// (not-found, masked, …) can't be enabled and are dropped; they print with a
// leading "●" marker that must not be mistaken for the name. Templates and
// instances ("name@.service", "name@arg.service") are dropped too: the
// unit-file listing this replaces only ever matched plain units. The result is
// filtered through servicesExclude and sorted.
func parseInactiveServices(out string) []string {
	var units []string
	for _, line := range nonEmptyLines(out) {
		f := strings.Fields(strings.TrimLeft(line, "●* \t"))
		if len(f) < 2 || f[1] != "loaded" || !strings.HasSuffix(f[0], ".service") ||
			strings.Contains(f[0], "@") || servicesExclude.MatchString(f[0]) {
			continue
		}
		units = append(units, f[0])
	}
	sort.Strings(units)
	return slices.Compact(units)
}

// enabledServices keeps the units `systemctl is-enabled` reports as exactly
// "enabled" (not enabled-runtime, static, masked, …). One batched call prints
// one state per unit; if the line count doesn't match (a unit systemctl
// couldn't resolve prints nothing on stdout) it falls back to one call each.
func enabledServices(ctx context.Context, units []string) []string {
	if len(units) == 0 {
		return nil
	}
	out, _ := proc.Query(ctx, "systemctl", append([]string{"is-enabled", "--"}, units...)...)
	states := nonEmptyLines(string(out))
	if len(states) != len(units) {
		states = states[:0]
		for _, u := range units {
			o, _ := proc.Query(ctx, "systemctl", "is-enabled", "--", u)
			states = append(states, strings.TrimSpace(string(o)))
		}
	}
	var enabled []string
	for i, u := range units {
		if states[i] == "enabled" {
			enabled = append(enabled, u)
		}
	}
	return enabled
}

// discoverServices finds services that are enabled but not running, for the
// user to pick from. Listing loaded units is ~3 ms where `list-unit-files`
// costs ~430 ms, so it asks for the inactive units first and checks only
// those with one is-enabled call.
func discoverServices(ctx context.Context, _ Env) Discovery {
	out, _ := proc.Query(ctx, "systemctl", "list-units", "--type=service", "--all",
		"--state=inactive", "--plain", "--no-legend")
	svcs := enabledServices(ctx, parseInactiveServices(string(out)))
	if len(svcs) == 0 {
		return Notice{"No enabled-but-inactive services found."}
	}
	return NeedsSelection{Items: svcs, Steps: DisableUnits}
}

// DisableUnits is the services pick list's queue: one step that disables and
// stops the picked units; nothing picked is nothing to run.
func DisableUnits(units []string) []steps.Step {
	if len(units) == 0 {
		return nil
	}
	return []steps.Step{{
		ID: steps.IDDisableUnits, Verb: steps.VerbDisableUnit,
		Title: "Disabling services…", LogLabel: "Services disabled",
		Argv:      append([]string{"sudo", "systemctl", "disable", "--now", "--"}, units...),
		NeedsPriv: true, AlwaysLog: true,
	}}
}
