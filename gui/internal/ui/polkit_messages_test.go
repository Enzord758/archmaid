package ui

import (
	"encoding/xml"
	"os"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/engine"
)

// TestPolkitMessagesMatchThePolicy (B9): every action of the policy file has
// its message in the dialog's table, word for word, and the table has no
// action the policy lacks.
func TestPolkitMessagesMatchThePolicy(t *testing.T) {
	data, err := os.ReadFile("../../../packaging/polkit/org.archmaid.helper.policy")
	if err != nil {
		t.Fatal(err)
	}
	var pol struct {
		Actions []struct {
			ID      string `xml:"id,attr"`
			Message string `xml:"message"`
		} `xml:"action"`
	}
	if err := xml.Unmarshal(data, &pol); err != nil {
		t.Fatal(err)
	}
	if len(pol.Actions) < 10 {
		t.Fatalf("parsed %d actions", len(pol.Actions))
	}
	seen := map[string]bool{}
	for _, a := range pol.Actions {
		seen[a.ID] = true
		if got := polkitMessages[a.ID]; got != strings.TrimSpace(a.Message) {
			t.Errorf("%s: dialog table %q, policy %q", a.ID, got, a.Message)
		}
	}
	for id := range polkitMessages {
		if !seen[id] {
			t.Errorf("%s is in the table but not in the policy", id)
		}
	}
}

// TestPolkitDialogShowsTheMessage (B9): the new package actions read as their
// message, an unknown id as before.
func TestPolkitDialogShowsTheMessage(t *testing.T) {
	for _, c := range []struct{ action, caller, body string }{
		{"org.archmaid.helper.pkg-install", "Authentication is required to install packages (polkit action org.archmaid.helper.pkg-install).", ""},
		{"org.archmaid.helper.pkg-remove", "Authentication is required to remove packages (polkit action org.archmaid.helper.pkg-remove).", ""},
		{"org.archmaid.helper.full-cleanup", "Authentication is required to clean the package cache, remove orphaned packages, vacuum the journal, clean temporary files and check package files (polkit action org.archmaid.helper.full-cleanup).", ""},
		{"org.archmaid.helper.unknown-new", "Asked by polkit for org.archmaid.helper.unknown-new.", ""},
	} {
		_, caller, body := passwordText(engine.NeedsPassword{Kind: engine.PasswordAskpass, Polkit: true, Action: c.action})
		if caller != c.caller || body != c.body {
			t.Errorf("%s: caller %q body %q", c.action, caller, body)
		}
	}
}
