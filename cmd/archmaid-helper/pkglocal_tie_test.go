package main

import (
	"encoding/xml"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/cmd/archmaid-helper/internal/helper"
	"github.com/Enzord758/archmaid/internal/aurbuild"
	"github.com/Enzord758/archmaid/internal/steps"
)

// TestLocalHashTiedToAurbuild: the helper's hash is aurbuild.LocalHash's, byte
// for byte, including when one name extends another.
func TestLocalHashTiedToAurbuild(t *testing.T) {
	sha := func(c string) string { return strings.Repeat(c, 64) }
	cases := [][]aurbuild.BuiltFile{
		{{Name: "a-1-1-any.pkg.tar.zst", Size: 7, Sha256: strings.Repeat("ab", 32)}},
		{{Name: "b-1-1-any.pkg.tar.zst", Size: 3, Sha256: sha("b")}, {Name: "a-1-1-any.pkg.tar.zst", Size: 2, Sha256: sha("a")}},
		{{Name: "a-1-1-any.pkg.tar.zst", Size: 1, Sha256: sha("1")}, {Name: "a-1-1-any.pkg.tar.zst2", Size: 9, Sha256: sha("2")}, {Name: "a-1-1-any.pkg.tar.xz", Size: 5, Sha256: sha("3")}},
	}
	for i, c := range cases {
		var lf []helper.LocalFile
		for _, f := range c {
			lf = append(lf, helper.LocalFile{Name: f.Name, Size: f.Size, Sha256: f.Sha256})
		}
		if got, want := helper.PkgLocalHash(lf), aurbuild.LocalHash(c); got != want {
			t.Errorf("case %d: helper %s, aurbuild %s", i, got, want)
		}
	}
}

// TestLocalReasonsTied: the helper's new refusal reasons are steps' strings,
// and the verb is one steps names.
func TestLocalReasonsTied(t *testing.T) {
	if helper.ReasonUnsafeFile != string(steps.RefuseUnsafeFile) || helper.ReasonBadPackage != string(steps.RefuseBadPackage) {
		t.Error("reason strings differ")
	}
	if !slices.Contains(helper.Verbs(), steps.VerbPkgInstallLocal) {
		t.Error("verb missing")
	}
}

// TestPkgInstallLocalPolicy: the AUR action says what it authorises.
func TestPkgInstallLocalPolicy(t *testing.T) {
	data, err := os.ReadFile(policyFile)
	if err != nil {
		t.Fatal(err)
	}
	var pc struct {
		Actions []struct {
			ID      string `xml:"id,attr"`
			Message string `xml:"message"`
		} `xml:"action"`
	}
	if err := xml.Unmarshal(data, &pc); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range pc.Actions {
		if a.ID == "org.archmaid.helper.pkg-install-local" {
			found = true
			if !strings.Contains(a.Message, "AUR") || !strings.Contains(a.Message, "root") {
				t.Errorf("message %q must say AUR packages and root", a.Message)
			}
		}
	}
	if !found {
		t.Error("no pkg-install-local action")
	}
}
