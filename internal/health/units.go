package health

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Enzord758/archmaid/internal/sanitize"
)

// unitName is a systemd unit as `systemctl --failed --plain` prints it. The
// character set is what systemd allows in a name before escaping; anything
// else is shown (sanitized) but never put in a command line.
var unitName = regexp.MustCompile(`^[A-Za-z0-9:_.@\\-]{1,200}\.(service|socket|target|device|mount|automount|swap|timer|path|slice|scope)$`)

// safeInCommand: a unit name that needs no shell quoting. Names with a
// backslash (systemd's \x2d escapes) are quoted by shQuote.
var safeInCommand = regexp.MustCompile(`^[A-Za-z0-9:_.@-]+$`)

const (
	unitsShown = 4  // names in the Detail before "+N more"
	unitsCopy  = 20 // units in the Copy command at most
)

func checkFailedUnits(ctx context.Context, d Deps) Check {
	if d.Query == nil {
		return unknown(IDFailedUnits, TitleFailedUnits, "systemctl not available")
	}
	sys, sysErr := failedUnits(ctx, d, false)
	usr, usrErr := failedUnits(ctx, d, true)
	if sysErr != nil && usrErr != nil {
		// No manager answered (no systemd, no user bus): neutral.
		return unknown(IDFailedUnits, TitleFailedUnits, "can't ask systemd")
	}
	n := len(sys) + len(usr)
	if n == 0 {
		return Check{State: OK, Detail: "No failed services"}
	}
	var names []string
	for _, u := range sys {
		names = append(names, sanitize.Field(u, 80))
	}
	for _, u := range usr {
		names = append(names, sanitize.Field(u, 80)+" (user)")
	}
	shown := names
	if len(shown) > unitsShown {
		shown = shown[:unitsShown]
	}
	detail := fmt.Sprintf("%d failed: %s", n, strings.Join(shown, ", "))
	if n > len(shown) {
		detail += fmt.Sprintf(" +%d more", n-len(shown))
	}
	return Check{State: Look, Detail: sanitize.Field(detail, DetailMax), Copy: journalCommand(sys, usr)}
}

// failedUnits lists the failed units of the system manager, or of the user's
// with user set. systemctl prints the list on stdout even when it exits
// non-zero, so output is parsed whatever the error; an error with no unit
// listed is reported.
func failedUnits(ctx context.Context, d Deps, user bool) ([]string, error) {
	args := []string{"--failed", "--plain", "--no-legend", "--no-pager"}
	if user {
		args = append([]string{"--user"}, args...)
	}
	out, err := d.Query(ctx, "systemctl", args...)
	var units []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && (f[0] == "●" || f[0] == "*" || f[0] == "×") {
			f = f[1:]
		}
		if len(f) == 0 || !unitName.MatchString(f[0]) || seen[f[0]] {
			continue
		}
		seen[f[0]] = true
		units = append(units, f[0])
	}
	if len(units) == 0 && err != nil {
		return nil, err
	}
	return units, nil
}

// journalCommand is the command to read why the units failed: this boot's
// journal of each, system units first, user units with --user. Both kinds:
// the two commands joined with &&.
func journalCommand(sys, usr []string) string {
	var cmds []string
	if c := journalFor(sys, "journalctl -b"); c != "" {
		cmds = append(cmds, c)
	}
	if c := journalFor(usr, "journalctl --user -b"); c != "" {
		cmds = append(cmds, c)
	}
	return strings.Join(cmds, " && ")
}

func journalFor(units []string, base string) string {
	if len(units) == 0 {
		return ""
	}
	if len(units) > unitsCopy {
		units = units[:unitsCopy]
	}
	var b strings.Builder
	b.WriteString(base)
	for _, u := range units {
		b.WriteString(" -u " + shQuote(u))
	}
	return b.String()
}

// shQuote quotes s for a POSIX shell only when it needs it. The command is
// for a person to paste; archmaid never runs it.
func shQuote(s string) string {
	if safeInCommand.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
