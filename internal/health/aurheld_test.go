package health

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// What is held on purpose is not an alarm (#283). A package flagged out of date
// whose pending update waits in the cooldown (the maintainer already published
// the fix), and one the AUR audit holds, are not LOOK; "gone from the AUR (it
// was in the snapshot)" and "flagged out of date with no pending update" stay.
func TestAURHeldIsNotAnAlarm(t *testing.T) {
	foreign := func(n ...string) func() ([]Foreign, error) {
		return func() ([]Foreign, error) {
			var out []Foreign
			for _, x := range n {
				out = append(out, Foreign{Name: x, Version: "1"})
			}
			return out, nil
		}
	}
	info := func(pk ...AURPkg) func(context.Context, []string) ([]AURPkg, error) {
		return func(context.Context, []string) ([]AURPkg, error) { return pk, nil }
	}
	set := func(n ...string) map[string]bool {
		m := map[string]bool{}
		for _, x := range n {
			m[x] = true
		}
		return m
	}
	seen := func(n ...string) func() (map[string]bool, bool) {
		return func() (map[string]bool, bool) { return set(n...), true }
	}
	held := func(cool, aud []string) func() HeldSet {
		return func() HeldSet { return HeldSet{Cooldown: set(cool...), Audit: set(aud...)} }
	}
	for _, c := range []struct {
		name   string
		d      Deps
		state  State
		detail string
	}{
		{"out of date and held by the cooldown: OK",
			Deps{Foreign: foreign("a", "b"), AURInfo: info(AURPkg{Name: "a", OutOfDate: true}, AURPkg{Name: "b"}), AURSeen: seen("a", "b"), Held: held([]string{"a"}, nil)},
			OK, "2 AUR packages fine · 1 update held (cooldown)"},
		{"out of date with no pending update: LOOK",
			Deps{Foreign: foreign("a", "b"), AURInfo: info(AURPkg{Name: "a", OutOfDate: true}, AURPkg{Name: "b"}), AURSeen: seen("a", "b"), Held: held([]string{"b"}, nil)},
			Look, "flagged out of date: a · 1 update held (cooldown)"},
		{"out of date and nothing held: LOOK",
			Deps{Foreign: foreign("a"), AURInfo: info(AURPkg{Name: "a", OutOfDate: true}), AURSeen: seen("a")}, Look, "flagged out of date: a"},
		{"held by the AUR audit and out of date: not LOOK",
			Deps{Foreign: foreign("a"), AURInfo: info(AURPkg{Name: "a", OutOfDate: true}), AURSeen: seen("a"), Held: held(nil, []string{"a"})},
			OK, "1 AUR package fine · 1 held by the AUR audit"},
		{"held by the AUR audit, up to date: mentioned, not LOOK",
			Deps{Foreign: foreign("a", "b"), AURInfo: info(AURPkg{Name: "a"}, AURPkg{Name: "b"}), AURSeen: seen("a", "b"), Held: held(nil, []string{"b"})},
			OK, "2 AUR packages fine · 1 held by the AUR audit"},
		{"both kinds of hold",
			Deps{Foreign: foreign("a", "b", "c"), AURInfo: info(AURPkg{Name: "a", OutOfDate: true}, AURPkg{Name: "b"}, AURPkg{Name: "c"}), AURSeen: seen("a", "b", "c"), Held: held([]string{"a", "c"}, []string{"b"})},
			OK, "3 AUR packages fine · 2 updates held (cooldown) · 1 held by the AUR audit"},
		{"gone from the AUR stays LOOK, held or not",
			Deps{Foreign: foreign("a", "b"), AURInfo: info(AURPkg{Name: "a"}), AURSeen: seen("a", "b"), Held: held([]string{"a"}, nil)},
			Look, "not in the AUR anymore: b · 1 update held (cooldown)"},
		{"a held name that is not installed is not counted",
			Deps{Foreign: foreign("a"), AURInfo: info(AURPkg{Name: "a"}), AURSeen: seen("a"), Held: held([]string{"zzz"}, []string{"yyy"})},
			OK, "1 AUR package fine"},
		// paru: nothing is held by the cooldown (no age tags), so the rule never applies.
		{"no cooldown holds (paru): out of date is LOOK",
			Deps{Foreign: foreign("a"), AURInfo: info(AURPkg{Name: "a", OutOfDate: true}), AURSeen: seen("a"), Held: held(nil, nil)},
			Look, "flagged out of date: a"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := checkAUR(context.Background(), c.d)
			if got.State != c.state || got.Detail != c.detail {
				t.Errorf("= %v %q; want %v %q", got.State, got.Detail, c.state, c.detail)
			}
		})
	}
}

// The real Held reads updates-cache.json and audit-state.json, read only: held
// AUR entries by name, high findings by package, hostile text ignored.
func TestHeldIn(t *testing.T) {
	dir := t.TempDir()
	cache := `{"v":3,"packages":[` +
		`{"name":"spotify","from":"1","to":"2","src":"aur","held":true,"age":"2d4h"},` +
		`{"name":"zen-browser-bin","from":"1","to":"2","src":"aur","held":false,"age":"3d"},` +
		`{"name":"linux","from":"1","to":"2","src":"repo"},` +
		`{"name":"bad name; rm","from":"1","to":"2","src":"aur","held":true},` +
		`{"name":"nohold","from":"1","to":"2","src":"aur"}]}`
	state := `{"v":1,"high":[{"pkg":"evil-bin","reason":"maintainer changed"},{"pkg":"x y","reason":"r"},{"pkg":"","reason":"r"}]}`
	os.WriteFile(filepath.Join(dir, "updates-cache.json"), []byte(cache), 0o600)
	os.WriteFile(filepath.Join(dir, "audit-state.json"), []byte(state), 0o600)
	h := heldIn(dir)
	if len(h.Cooldown) != 1 || !h.Cooldown["spotify"] || len(h.Audit) != 1 || !h.Audit["evil-bin"] {
		t.Errorf("held = %+v", h)
	}
	for _, before := range [][]byte{{}, []byte("{"), []byte("null"), []byte(`{"packages":"x"}`)} {
		os.WriteFile(filepath.Join(dir, "updates-cache.json"), before, 0o600)
		os.WriteFile(filepath.Join(dir, "audit-state.json"), before, 0o600)
		if h := heldIn(dir); len(h.Cooldown) != 0 || len(h.Audit) != 0 {
			t.Errorf("%q: held = %+v", before, h)
		}
	}
	if h := heldIn(filepath.Join(dir, "none")); len(h.Cooldown) != 0 || len(h.Audit) != 0 {
		t.Errorf("missing files: %+v", h)
	}
	// The default reads the state dir of the (isolated) test HOME: nothing there.
	if h := readHeld(); len(h.Cooldown) != 0 || len(h.Audit) != 0 {
		t.Errorf("readHeld = %+v", h)
	}
	// Past the name cap nothing more is read.
	var many []byte
	many = append(many, `{"packages":[`...)
	for i := 0; i < maxHeldNames+5; i++ {
		if i > 0 {
			many = append(many, ',')
		}
		many = append(many, []byte(`{"name":"p`+string(rune('a'+i%26))+string(rune('a'+i/26))+`","from":"1","to":"2","src":"aur","held":true}`)...)
	}
	many = append(many, `]}`...)
	if got := parseCooldownHeld(many); len(got) != maxHeldNames {
		t.Errorf("%d names, want the cap %d", len(got), maxHeldNames)
	}
}
