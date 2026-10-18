package actions

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// wipeDryArgv is the dry run the wipe's confirm is built from: what
// `paccache -rk0` would remove, listed and counted, removing nothing. The
// cache is world-readable, so it runs as the user, with no root and no
// password. --nocolor keeps the summary line free of escapes.
var wipeDryArgv = []string{"--nocolor", "-dk0"}

// wipeSummary is paccache's last line of a dry run:
// "==> finished dry run: 5 candidates (disk space saved: 101.35 MiB)".
var wipeSummary = regexp.MustCompile(`finished dry run: (\d+) candidates? \(disk space saved: ([0-9][0-9.,]* [A-Za-z]+)\)`)

// wipeNone is paccache's line for an empty cache.
const wipeNone = "no candidate packages found for pruning"

// discoverWipeCache is "Delete entire package cache": it asks paccache what
// removing every cached package would free (a read-only dry run as the user)
// and offers the removal with that in its confirm, default No. An empty
// cache is a notice; a dry run that fails or can't be read is a failure
// notice, never a confirm without its numbers. The removal is the root
// helper's paccache-wipe verb (sudo paccache -rk0 in the TUI); it takes no
// argument.
func discoverWipeCache(ctx context.Context, env Env) Discovery {
	cmd := "$ paccache " + strings.Join(wipeDryArgv, " ")
	fail := func(text, log string) Discovery {
		return Notice{Text: text, Err: sanitize.OneLine(text),
			Details: []string{"Tried: paccache " + strings.Join(wipeDryArgv, " ")}, Log: cmd + "\n" + log}
	}
	if _, err := env.lookPath("paccache"); err != nil {
		return fail("Couldn't measure the package cache: paccache is not installed (pacman-contrib).", "error: "+err.Error()+"\n")
	}
	out, err := queryWipe(ctx)
	if err != nil {
		return fail("Couldn't measure the package cache: "+clipErr(err), string(out)+"error: "+err.Error()+"\n")
	}
	m := wipeSummary.FindStringSubmatch(string(out))
	switch {
	case m == nil && strings.Contains(string(out), wipeNone):
		return Notice{Text: "The package cache is empty: nothing to delete.",
			Details: []string{"paccache -dk0: " + wipeNone}, Log: cmd + "\n" + string(out)}
	case m == nil:
		return fail("Couldn't read paccache's dry-run summary.", string(out))
	}
	n, _ := strconv.Atoi(m[1])
	size := m[2]
	return Pending{Steps: []steps.Step{{
		ID: steps.IDPaccacheWipe, Verb: steps.VerbPaccacheWipe,
		Title: "Deleting entire package cache…", LogLabel: "Entire package cache removed",
		Argv:      []string{"sudo", "paccache", "-rk0"},
		NeedsPriv: true, Optional: true, AlwaysLog: true,
		Confirm: WipeConfirm(n, size),
	}}, Checked: Checked{
		Summary: fmt.Sprintf("Measured the package cache: %d %s, %s.", n, plural(n, "package file", "package files"), size),
		Log:     cmd + "\n" + string(out),
	}}
}

// WipeConfirm is the wipe's confirm: a title, then what it frees and what
// it costs. The size is paccache's own text; it was matched against a strict
// pattern before it got here.
func WipeConfirm(n int, size string) string {
	// One sentence per line: the TUI's popup doesn't wrap its text.
	return fmt.Sprintf("Delete the ENTIRE package cache?\nThis frees %s (%d %s).\n"+
		"You can't reinstall or downgrade these packages without internet.",
		size, n, plural(n, "package file", "package files"))
}

func queryWipe(ctx context.Context) ([]byte, error) {
	return proc.Query(ctx, "paccache", wipeDryArgv...)
}
