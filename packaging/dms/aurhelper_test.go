package dms

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// The updates script picks its AUR helper as archmaid does (internal/aurhelper):
// topgrade's [linux] arch_package_manager when it is paru or yay and installed,
// else the first installed of paru, yay. These run the real script with fake
// helpers; the real checkupdates, paru and yay can never be reached (PATH holds
// only the shims).

// paruListing: `paru -Qua` lines have no age tag, "latest-commit" stands for a
// -git package's new version, and an ignored package has a suffix.
const paruListing = "bonsai 2.1.1-10 -> 2.2.1-1\n" +
	"hyprland-git 0.50.0.r1-1 -> latest-commit\n" +
	"ignored-thing 1-1 -> 2-1 [ignored]\n" +
	"raiderio-client 5.0.9-1 -> 5.0.11-1\n"

func TestUpdatesScriptAURHelperChoice(t *testing.T) {
	type upd struct {
		Aur      *int
		AurHeld  *int `json:"aur_held"`
		Count    int
		Packages []struct {
			Name string
			Src  string
			Held *bool
			Age  string
		}
	}
	yayOut := &yayShim{out: yayListing}
	paruOut := &yayShim{out: paruListing}
	three := "a 1-1 -> 1-2\nb 1 -> 2\nc 3 -> 4\n"
	conf := func(v string) string {
		return "[misc]\nassume_yes = true\n[linux]\narch_package_manager = \"" + v + "\"\n"
	}
	for _, c := range []struct {
		name    string
		helpers map[string]*yayShim
		conf    string
		ran     []string
		aur     int // -1 = null
		held    int
	}{
		{"only paru", map[string]*yayShim{"paru": paruOut}, "", []string{"paru"}, 3, 0},
		{"only yay", map[string]*yayShim{"yay": yayOut}, "", []string{"yay"}, 2, 2},
		{"both installed: paru first, as topgrade", map[string]*yayShim{"paru": paruOut, "yay": yayOut}, "", []string{"paru"}, 3, 0},
		{"the topgrade config says yay", map[string]*yayShim{"paru": paruOut, "yay": yayOut}, conf("yay"), []string{"yay"}, 2, 2},
		{"the topgrade config says paru", map[string]*yayShim{"paru": paruOut, "yay": yayOut}, conf("paru"), []string{"paru"}, 3, 0},
		{"the config says paru, only yay installed", map[string]*yayShim{"yay": yayOut}, conf("paru"), []string{"yay"}, 2, 2},
		{"the config names an unsupported helper", map[string]*yayShim{"paru": paruOut, "yay": yayOut}, conf("pikaur"), []string{"paru"}, 3, 0},
		{"the key outside [linux] is ignored", map[string]*yayShim{"paru": paruOut, "yay": yayOut}, "[misc]\narch_package_manager = \"yay\"\n", []string{"paru"}, 3, 0},
		{"a commented key is ignored", map[string]*yayShim{"paru": paruOut, "yay": yayOut}, "[linux]\n# arch_package_manager = \"yay\"\n", []string{"paru"}, 3, 0},
		{"a hostile value is ignored", map[string]*yayShim{"paru": paruOut, "yay": yayOut}, conf("yay; touch /tmp/x"), []string{"paru"}, 3, 0},
		{"neither installed", nil, "", nil, -1, 0},
		{"paru: no updates", map[string]*yayShim{"paru": {rc: 1}}, "", []string{"paru"}, 0, 0},
		{"paru fails with a message", map[string]*yayShim{"paru": {err: "error: AUR rpc down\n", rc: 1}}, "", []string{"paru"}, -1, 0},
		{"paru timed out", map[string]*yayShim{"paru": {rc: 124}}, "", []string{"paru"}, -1, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw, ran, err := updatesRunHelpers(t, three, 0, c.helpers, c.conf, "", "")
			if err != nil {
				t.Fatalf("script: %v", err)
			}
			if !slices.Equal(ran, c.ran) {
				t.Errorf("ran %v, want %v", ran, c.ran)
			}
			var u upd
			if err := json.Unmarshal([]byte(raw), &u); err != nil {
				t.Fatalf("%q: %v", raw, err)
			}
			if c.aur < 0 {
				if u.Aur != nil || u.AurHeld != nil || !strings.Contains(raw, `"aur":null,"aur_held":null`) {
					t.Errorf("want nulls: %s", raw)
				}
				return
			}
			if u.Aur == nil || *u.Aur != c.aur || *u.AurHeld != c.held || u.Count != 3+c.aur {
				t.Errorf("aur %v held %v count %d, want %d and %d: %s", u.Aur, u.AurHeld, u.Count, c.aur, c.held, raw)
			}
		})
	}
}

// paru's listing in the package list: every AUR entry is eligible (held false,
// no age), the ignored one is neither listed nor counted, "latest-commit" is a
// version like any other.
func TestUpdatesScriptParuPackages(t *testing.T) {
	raw, _, err := updatesRunHelpers(t, "a 1-1 -> 1-2\n", 0, map[string]*yayShim{"paru": {out: paruListing}}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	var u struct {
		Count, Repo int
		Aur, Held   *int
		AurHeld     *int `json:"aur_held"`
		Packages    []struct {
			Name, From, To, Src string
			Held                *bool
			Age                 *string
		}
	}
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatalf("%q: %v", raw, err)
	}
	if u.Repo != 1 || u.Aur == nil || *u.Aur != 3 || *u.AurHeld != 0 || u.Count != 4 || len(u.Packages) != 4 {
		t.Fatalf("counts and list: %s", raw)
	}
	var names []string
	for _, p := range u.Packages[1:] {
		names = append(names, p.Name)
		if p.Src != "aur" || p.Held == nil || *p.Held || p.Age != nil {
			t.Errorf("entry %+v: want an eligible AUR update with no age", p)
		}
	}
	if !slices.Equal(names, []string{"bonsai", "hyprland-git", "raiderio-client"}) {
		t.Errorf("AUR entries %v (the ignored one must not be there)", names)
	}
	if got := u.Packages[2]; got.To != "latest-commit" {
		t.Errorf("devel entry %+v", got)
	}
}

// yay's behaviour is unchanged by the helper choice: its cooldown split and age
// tags still drive the counts and the list.
func TestUpdatesScriptYayStillSplitsByAge(t *testing.T) {
	raw, ran, err := updatesRunHelpers(t, "", 2, map[string]*yayShim{"yay": {out: yayListing}}, "", "", "")
	if err != nil || !slices.Equal(ran, []string{"yay"}) {
		t.Fatalf("%v %v", ran, err)
	}
	if !strings.Contains(raw, `"aur":2,"aur_held":2`) || !strings.Contains(raw, `"held":true,"age":"1d9h"`) {
		t.Errorf("yay split lost: %s", raw)
	}
}
