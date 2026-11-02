package main

import (
	"encoding/xml"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/cmd/archmaid-helper/internal/helper"
)

const policyFile = "../../packaging/polkit/org.archmaid.helper.policy"

type policyConfig struct {
	Actions []struct {
		ID            string `xml:"id,attr"`
		AllowAny      string `xml:"defaults>allow_any"`
		AllowInactive string `xml:"defaults>allow_inactive"`
		AllowActive   string `xml:"defaults>allow_active"`
		Annotate      []struct {
			Key   string `xml:"key,attr"`
			Value string `xml:",chardata"`
		} `xml:"annotate"`
	} `xml:"action"`
}

// TestPolicyMatchesVerbs keeps the polkit policy and the verb table in
// lockstep: one action per root verb, matched on the fixed path + argv1,
// never for inactive/remote sessions, keep vs admin as designed.
func TestPolicyMatchesVerbs(t *testing.T) {
	data, err := os.ReadFile(policyFile)
	if err != nil {
		t.Fatal(err)
	}
	var pc policyConfig
	if err := xml.Unmarshal(data, &pc); err != nil {
		t.Fatal(err)
	}
	// Each one is justified in README.md "What keep allows"; repair is admin.
	keep := []string{"check-files", "paccache", "vacuum-journal", "clean-tmp"}
	var want []string
	for _, v := range helper.Verbs() {
		if !strings.HasPrefix(v, "plan-") { // read-only, no root, no action
			want = append(want, v)
		}
	}
	var got []string
	for _, a := range pc.Actions {
		verb, ok := strings.CutPrefix(a.ID, "org.archmaid.helper.")
		if !ok {
			t.Errorf("unexpected action id %q", a.ID)
			continue
		}
		got = append(got, verb)
		ann := map[string]string{}
		for _, an := range a.Annotate {
			ann[an.Key] = an.Value
		}
		if p := ann["org.freedesktop.policykit.exec.path"]; p != helper.InstallPath {
			t.Errorf("%s: exec.path = %q, want %q", a.ID, p, helper.InstallPath)
		}
		if v := ann["org.freedesktop.policykit.exec.argv1"]; v != verb {
			t.Errorf("%s: exec.argv1 = %q, want %q", a.ID, v, verb)
		}
		if a.AllowAny != "no" || a.AllowInactive != "no" {
			t.Errorf("%s: allow_any/allow_inactive = %q/%q, want no/no", a.ID, a.AllowAny, a.AllowInactive)
		}
		wantActive := "auth_admin"
		if slices.Contains(keep, verb) {
			wantActive = "auth_admin_keep"
		}
		if a.AllowActive != wantActive {
			t.Errorf("%s: allow_active = %q, want %q", a.ID, a.AllowActive, wantActive)
		}
	}
	slices.Sort(got)
	// The package verbs run scriptlets as root: admin every time, never
	// keep (README.md "What keep allows"). Explicit, not just "not in keep".
	for _, v := range []string{"pkg-install", "pkg-remove"} {
		if slices.Contains(keep, v) || !slices.Contains(got, v) {
			t.Errorf("%s must have an admin (never keep) policy action", v)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("policy actions %q, want one per root verb %q", got, want)
	}
}
