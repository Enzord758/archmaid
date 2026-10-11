package actions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/downloads"
)

// skipUnlessUser skips a test that relies on file permissions, which root
// ignores.
func skipUnlessUser(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("file permissions don't bind root")
	}
}

// TestDiscoverDownloadsScanFails: a folder that resolves but can't be opened
// is a failed look (Err set, what was tried), never "nothing old".
func TestDiscoverDownloadsScanFails(t *testing.T) {
	skipUnlessUser(t)
	home := t.TempDir()
	dir := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	env := Env{Downloads: func() downloads.Options { return downloads.Options{Flag: dir, Home: home} }}
	n, ok := discoverDownloads(context.Background(), env).(Notice)
	if !ok || n.Err == "" || !strings.Contains(n.Text, "Couldn't open ") || len(n.Details) != 1 ||
		!strings.HasPrefix(n.Details[0], "Tried: the downloads folder") {
		t.Fatalf("got %#v", n)
	}
}

// TestDownloadsCheckedRecordEdges: the record's rarer lines. An entry old by
// its own ctime that is not in the plan says it was kept; a folder that can't
// be listed says no entries; entries whose age can't be read say so; and a
// record longer than maxDetails is cut and counted.
func TestDownloadsCheckedRecordEdges(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "held"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(40 * 24 * time.Hour)
	ck := downloadsChecked(downloads.Plan{Dir: dir, Shown: "~/D"}, later)
	if want := "held: 40 days old, kept: something inside changed within 30 days, or it is on another filesystem"; len(ck.Details) != 1 || ck.Details[0] != want {
		t.Errorf("kept entry: %q, want %q", ck.Details, want)
	}

	ck = downloadsChecked(downloads.Plan{Dir: filepath.Join(dir, "gone"), Shown: "~/gone"}, later)
	if ck.Summary != "Checked ~/gone: no entries older than 30 days." || len(ck.Details) != 0 {
		t.Errorf("unlistable folder: %#v", ck)
	}

	many := t.TempDir()
	for i := range maxDetails + 3 {
		if err := os.WriteFile(filepath.Join(many, fmt.Sprintf("f%04d", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ck = downloadsChecked(downloads.Plan{Dir: many, Shown: "~/many"}, time.Now())
	if len(ck.Details) != maxDetails+1 || ck.Details[maxDetails] != "… and 3 more" {
		t.Errorf("%d detail lines, last %q; want %d, ending in \"… and 3 more\"", len(ck.Details), ck.Details[len(ck.Details)-1], maxDetails+1)
	}
}

// TestDownloadsCheckedAgeUnknown: a folder that lists but whose entries can't
// be stat'ed (read without search permission) reports each as age unknown,
// kept, and doesn't invent a newest/oldest.
func TestDownloadsCheckedAgeUnknown(t *testing.T) {
	skipUnlessUser(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	ck := downloadsChecked(downloads.Plan{Dir: dir, Shown: "~/D"}, time.Now())
	if len(ck.Details) != 1 || ck.Details[0] != "a: age unknown, kept" {
		t.Errorf("details %q, want the one entry as age unknown", ck.Details)
	}
}

// TestSplitInactiveServicesSkipsMalformedLines: a line without a second
// column, or not naming a .service, is neither checked nor reported skipped.
func TestSplitInactiveServicesSkipsMalformedLines(t *testing.T) {
	out := "lonely.service\n" +
		"sshd.socket loaded inactive dead SSH\n" +
		"  \n" +
		"cups.service loaded inactive dead CUPS\n"
	units, skipped := splitInactiveServices(out)
	if !reflect.DeepEqual(units, []string{"cups.service"}) || len(skipped) != 0 {
		t.Errorf("units %v, skipped %v; want only cups.service", units, skipped)
	}
}

// TestSkipRulesCapsAtThree: the skip summary names the three commonest rules
// and then an ellipsis.
func TestSkipRulesCapsAtThree(t *testing.T) {
	sk := []skippedUnit{
		{"a.service", "excluded: systemd-"}, {"b.service", "excluded: systemd-"},
		{"c.service", "not loaded (masked)"}, {"d.service", "not loaded (not-found)"},
		{"e@1.service", "an instance or template"},
		{"f.service", "excluded: fwupd"},
	}
	if got, want := skipRules(sk), "systemd-*, not loaded, instances, …"; got != want {
		t.Errorf("skipRules = %q, want %q", got, want)
	}
}
