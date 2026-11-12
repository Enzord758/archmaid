package overview

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Enzord758/archmaid/internal/audit"
)

// The sheet's audit section lists audit-state.json's "high": the high
// findings that are not accepted yet (#286). Thirteen audited packages with
// one high finding have one entry; the accepted ones and the low and medium
// ones are never in the file; after the user accepts, the file says high []
// and the section is gone. So "Open audit" correctly stays hidden when the
// 13 findings are all low or medium, or accepted.
func TestReadHeldListsOnlyHighUnaccepted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, audit.StateFile)
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{
  "v": 1,
  "checked": "2026-11-12T08:00:00+01:00",
  "packages": 13,
  "high": [
    {"pkg": "dux-search-bin", "reason": "co-maintainer bob added"}
  ],
  "snapshot_present": true,
  "snapshot_readable": true
}
`)
	h := ReadHeld(dir)
	if len(h.Audit) != 1 || h.Audit[0].Pkg != "dux-search-bin" || h.Audit[0].Reason != "co-maintainer bob added" {
		t.Fatalf("one high unaccepted finding among 13 packages: %+v", h.Audit)
	}
	write(`{"v":1,"checked":"2026-11-12T08:05:00+01:00","packages":13,"high":[],"snapshot_present":true,"snapshot_readable":true}`)
	if h := ReadHeld(dir); len(h.Audit) != 0 {
		t.Fatalf("after accepting, high is [] but the audit holds %+v", h.Audit)
	}
	// A corrupt snapshot is a high finding too (it holds the update).
	write(`{"v":1,"checked":"2026-11-12T08:10:00+01:00","packages":13,"high":[{"pkg":"maintainer snapshot","reason":"snapshot unreadable"}],"snapshot_present":true,"snapshot_readable":false}`)
	if h := ReadHeld(dir); len(h.Audit) != 1 || h.Audit[0].Reason != "snapshot unreadable" {
		t.Fatalf("unreadable snapshot: %+v", h.Audit)
	}
	write(`not json`)
	if h := ReadHeld(dir); len(h.Audit) != 0 {
		t.Fatalf("junk: %+v", h.Audit)
	}
}
