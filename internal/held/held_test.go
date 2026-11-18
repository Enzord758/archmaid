package held

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Enzord758/archmaid/internal/audit"
)

func TestParseAge(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"2d4h", 52 * time.Hour, true},
		{"6h54m", 6*time.Hour + 54*time.Minute, true},
		{"3d", 72 * time.Hour, true},
		{"30s", 30 * time.Second, true},
		{"", 0, false},
		{"2 days", 0, false},
		{"d4", 0, false},
		{"-3h", 0, false},
		{"99999999999999999999h", 0, false},
	} {
		got, ok := ParseAge(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ParseAge(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestFreeIn(t *testing.T) {
	h := func(age string) Pkg {
		d, ok := ParseAge(age)
		return Pkg{Name: "x", Age: age, AgeKnown: ok, Since: d}
	}
	for _, c := range []struct{ age, want string }{
		{"2d4h", "20 h"},         // 72 - 52
		{"1d9h", "1 d 15 h"},     // 72 - 33
		{"6h", "2 d 18 h"},       // 72 - 6
		{"1d", "2 d"},            // whole days
		{"2d23h30m", "30 min"},   // under an hour: minutes
		{"2d23h59m30s", "1 min"}, // never "0 min"
		{"2d22h30m", "2 h"},      // 73 - ... rounded up: 1 h 30 min shows 2 h
		{"3d", "soon"},           // the cooldown has run out
		{"5d", "soon"},
	} {
		if got := h(c.age).FreeIn(); got != c.want {
			t.Errorf("age %s: free in %q, want %q", c.age, got, c.want)
		}
	}
	none := Pkg{Name: "x"}
	if none.FreeIn() != "soon" || none.Tag() != "held" || none.Detail() != " →  · free soon" {
		t.Errorf("no age: %q %q %q", none.FreeIn(), none.Tag(), none.Detail())
	}
	row := Pkg{Name: "spotify", From: "1.2.74-1", To: "1.2.76-1", Age: "2d4h", AgeKnown: true, Since: 52 * time.Hour}
	if row.Tag() != "held 2d4h" || row.Detail() != "1.2.74-1 → 1.2.76-1 · free in 20 h" {
		t.Errorf("row: %q, %q", row.Tag(), row.Detail())
	}
}

const heldCache = `{"v":3,"count":3,"repo":0,"aur":1,"aur_held":4,"checked":"2026-11-02T10:00:00+01:00","packages":[
 {"name":"linux","from":"7.3.1-1","to":"7.3.2-1","src":"repo"},
 {"name":"zen-browser-bin","from":"1.17.2-1","to":"1.17.3-1","src":"aur","held":false,"age":"3d4h"},
 {"name":"spotify","from":"1.2.74-1","to":"1.2.76-1","src":"aur","held":true,"age":"2d4h"},
 {"name":"noage","from":"1-1","to":"2-1","src":"aur","held":true},
 {"name":"bad name; rm -rf","from":"1-1","to":"2-1","src":"aur","held":true,"age":"1h"},
 {"name":"evil","from":"1-1","to":"<b>2</b>","src":"aur","held":true,"age":"1h"},
 {"name":"weirdage","from":"1-1","to":"2-1","src":"aur","held":true,"age":"a while"}]}`

func TestParseHeld(t *testing.T) {
	got := ParseHeld([]byte(heldCache))
	var names []string
	for _, h := range got {
		names = append(names, h.Name)
	}
	// Only AUR entries that are held, with a package's name and versions.
	if len(got) != 3 || names[0] != "spotify" || names[1] != "noage" || names[2] != "weirdage" {
		t.Fatalf("held entries %v", names)
	}
	if !got[0].AgeKnown || got[0].Age != "2d4h" || got[1].AgeKnown || got[2].AgeKnown || got[2].Age != "" {
		t.Errorf("ages: %+v", got)
	}
	if ParseHeld([]byte(`not json`)) != nil || ParseHeld([]byte(`{"v":3,"count":0}`)) != nil {
		t.Error("a file without a list has held entries")
	}
}

func TestRead(t *testing.T) {
	dir := t.TempDir()
	if h := Read(dir); len(h.Cooldown) != 0 || len(h.Audit) != 0 {
		t.Fatalf("empty folder: %+v", h)
	}
	if err := os.WriteFile(filepath.Join(dir, "updates-cache.json"), []byte(heldCache), 0o600); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", 300)
	st := audit.State{V: audit.StateVersion, High: []audit.StateFinding{{Pkg: "dux-search-bin", Reason: "new co-maintainer · not accepted yet"}, {Pkg: "", Reason: "x"}, {Pkg: "long", Reason: long}}}
	if err := audit.WriteState(filepath.Join(dir, audit.StateFile), st); err != nil {
		t.Fatal(err)
	}
	h := Read(dir)
	if len(h.Cooldown) != 3 || len(h.Audit) != 2 || h.Audit[0].Pkg != "dux-search-bin" || h.Audit[0].Reason != "new co-maintainer · not accepted yet" {
		t.Fatalf("held %+v", h)
	}
	if r := []rune(h.Audit[1].Reason); len(r) != auditReasonMax || r[len(r)-1] != '…' {
		t.Errorf("a long reason is clipped to %d runes: got %d", auditReasonMax, len(r))
	}
}
