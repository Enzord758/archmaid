package dms

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// listPkg and listFile are the strict shape of updates-cache.json v3.
type listPkg struct {
	Name string
	From string
	To   string
	Src  string
	Held *bool
	Age  *string
}

type listFile struct {
	V        int
	Count    int
	Repo     int
	Aur      *int
	AurHeld  *int `json:"aur_held"`
	Checked  string
	Packages *[]listPkg
}

// parseList parses the file the way a strict consumer would: unknown fields
// and trailing data are errors.
func parseList(t *testing.T, raw string) listFile {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var f listFile
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("invalid or unexpected JSON: %v\n%.600s", err, raw)
	}
	if dec.More() {
		t.Fatalf("trailing data after the object: %.300s", raw)
	}
	if f.V != 3 {
		t.Fatalf("v = %d, want 3", f.V)
	}
	return f
}

func bptr(b bool) *bool     { return &b }
func sptr(s string) *string { return &s }

func TestUpdatesList(t *testing.T) {
	repoOut := "linux 7.3.1-1 -> 7.3.2-1\nlib32-x 1:2.0-1 -> 1:2.1~rc1-2\ngtk4+ 4.0 -> 4.1\n"
	aurOut := yayListing + "noage 1 -> 2\n"
	t.Run("normal: repo, eligible and held AUR, age present or absent", func(t *testing.T) {
		f := parseList(t, updatesWith(t, repoOut, &yayShim{out: aurOut}))
		want := []listPkg{
			{"linux", "7.3.1-1", "7.3.2-1", "repo", nil, nil},
			{"lib32-x", "1:2.0-1", "1:2.1~rc1-2", "repo", nil, nil},
			{"gtk4+", "4.0", "4.1", "repo", nil, nil},
			{"bonsai", "2.1.1-10", "2.2.1-1", "aur", bptr(true), sptr("1d9h")},
			{"curseforge", "1.321.1_39714-1", "1.322.0_40357-1", "aur", bptr(false), sptr("3d17h")},
			{"archon-appimage", "9.5.8-1", "9.6.140-22", "aur", bptr(true), sptr("6h54m")},
			{"raiderio-client", "5.0.9-1", "5.0.11-1", "aur", bptr(false), sptr("3d")},
			{"noage", "1", "2", "aur", bptr(true), nil},
		}
		if f.Packages == nil || !reflect.DeepEqual(*f.Packages, want) {
			t.Fatalf("packages = %+v", f.Packages)
		}
		if f.Count != 5 || f.Repo != 3 || *f.Aur != 2 || *f.AurHeld != 3 {
			t.Fatalf("counts changed: %+v", f)
		}
	})
	t.Run("colours are stripped, then the line is listed", func(t *testing.T) {
		f := parseList(t, updatesWith(t, "\x1b[1mred\x1b[0m 1 -> 2\n", &yayShim{out: "\x1b[1mfoo\x1b[0m 1 -> 2 \x1b[36m[5d]\x1b[0m\n"}))
		if f.Packages == nil || len(*f.Packages) != 2 || (*f.Packages)[0].Name != "red" || (*f.Packages)[1].Name != "foo" || *(*f.Packages)[1].Age != "5d" {
			t.Fatalf("packages = %+v", f.Packages)
		}
	})
	t.Run("yay missing or failing: repo entries only", func(t *testing.T) {
		for _, y := range []*yayShim{nil, {rc: 2}, {err: "boom\n", rc: 1}, {out: aurOut, rc: 1}} {
			f := parseList(t, updatesWith(t, repoOut, y))
			if f.Aur != nil || f.Packages == nil || len(*f.Packages) != 3 {
				t.Fatalf("yay %+v: %+v", y, f)
			}
			for _, p := range *f.Packages {
				if p.Src != "repo" {
					t.Fatalf("AUR entry without a count: %+v", p)
				}
			}
		}
	})
	t.Run("0 updates: an empty list", func(t *testing.T) {
		raw := updatesWith(t, "", &yayShim{rc: 1})
		if f := parseList(t, raw); f.Packages == nil || len(*f.Packages) != 0 || !strings.Contains(raw, `"packages":[]}`) {
			t.Fatalf("raw = %s", raw)
		}
	})
	t.Run("the list failing keeps the counts and drops the list", func(t *testing.T) {
		raw, err := updatesRunTools(t, repoOut, 0, nil, "", "awk")
		if err != nil {
			t.Fatal(err)
		}
		f := parseList(t, raw)
		if f.Packages != nil || f.Count != 3 || f.Repo != 3 || f.Aur != nil {
			t.Fatalf("raw = %s", raw)
		}
	})
	t.Run("cap: 250 lines give 200 entries, counts exact", func(t *testing.T) {
		var rb, ab strings.Builder
		for i := 0; i < 250; i++ {
			fmt.Fprintf(&rb, "r%d 1 -> 2\n", i)
		}
		f := parseList(t, updatesWith(t, rb.String(), nil))
		if f.Packages == nil || len(*f.Packages) != 200 || f.Count != 250 || f.Repo != 250 || (*f.Packages)[199].Name != "r199" {
			t.Fatalf("repo only: %+v", f)
		}
		rb.Reset()
		for i := 0; i < 150; i++ {
			fmt.Fprintf(&rb, "r%d 1 -> 2\n", i)
		}
		for i := 0; i < 100; i++ {
			fmt.Fprintf(&ab, "a%d 1 -> 2 [4d]\n", i)
		}
		raw := updatesWith(t, rb.String(), &yayShim{out: ab.String()})
		f = parseList(t, raw)
		if f.Packages == nil || len(*f.Packages) != 200 || f.Count != 250 || f.Repo != 150 || *f.Aur != 100 {
			t.Fatalf("mixed: %+v", f)
		}
		if p := (*f.Packages)[199]; p.Name != "a49" || p.Src != "aur" {
			t.Fatalf("cut from the wrong end: %+v", p)
		}
		if len(raw) > 64<<10 {
			t.Fatalf("file is %d bytes", len(raw))
		}
	})
}

// Hostile package and version text never breaks the JSON: such lines are left
// out of the list and still counted, and the good lines around them stay.
func TestUpdatesListHostile(t *testing.T) {
	bad := []string{
		`a"b 1-1 -> 2-1`,
		`a\b 1-1 -> 2-1`,
		`a 1" -> 2`,
		`a 1 -> 2\`,
		"tab\tname 1 -> 2",
		"a 1\t-> 2",
		"esc\x1b]0;title\x07x 1 -> 2",
		"esc\x1bx 1 -> 2",
		"a 1 -> 2\x1b",
		"ctl\rname 1 -> 2",
		"näme 1 -> 2",
		"a 1.0é -> 2",
		"日本 1 -> 2",
		"$(touch pwned) 1 -> 2",
		"a 1 -> $(id)",
		"`id` 1 -> 2",
		"a 1;rm -> 2",
		strings.Repeat("n", 5000) + " 1 -> 2",
		strings.Repeat("n", 65) + " 1 -> 2",
		"a " + strings.Repeat("1", 49) + " -> 2",
		"a 1 -> " + strings.Repeat("2", 49),
		" 1 -> 2",
		"a 1 -> 2 extra",
		"a b 1 -> 2",
		"a 1 2",
		"a 1 ->",
		"a 1 ->2",
		"a  1 -> 2",
		"{\"name\":\"x\"} 1 -> 2",
		"a 1 -> 2 [3d] [4d]",
		"a 1 -> 2 [soon]",
		"a 1 -> 2 [3d\"]",
		"a 1 -> 2 [3d]x",
		"onlyname",
	}
	good := "good-pkg_1.x@y+z 1:2.0-1 -> 1:2.1~rc1-1"
	for _, side := range []string{"repo", "aur"} {
		t.Run(side, func(t *testing.T) {
			suffix := ""
			if side == "aur" {
				suffix = " [4d]"
			}
			// A newline inside a field splits the line in two; both halves are dropped.
			lines := []string{good + suffix, "name 1\n-> 2" + suffix}
			for _, b := range bad {
				lines = append(lines, b+suffix)
			}
			lines = append(lines, good+suffix)
			out := strings.Join(lines, "\n") + "\n"
			n := 0
			for _, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
				if strings.TrimSpace(l) != "" {
					n++
				}
			}
			var raw string
			if side == "repo" {
				raw = updatesWith(t, out, nil)
			} else {
				raw = updatesWith(t, "", &yayShim{out: out})
			}
			f := parseList(t, raw)
			total := f.Repo
			if side == "aur" {
				total = *f.Aur + *f.AurHeld
			}
			if total != n {
				t.Fatalf("counted %d lines, want %d", total, n)
			}
			if f.Packages == nil || len(*f.Packages) != 2 {
				t.Fatalf("want only the two good lines listed: %+v\n%.800s", f.Packages, raw)
			}
			for _, p := range *f.Packages {
				if p.Name != "good-pkg_1.x@y+z" || p.From != "1:2.0-1" || p.To != "1:2.1~rc1-1" || p.Src != side {
					t.Fatalf("entry = %+v", p)
				}
			}
		})
	}
}
