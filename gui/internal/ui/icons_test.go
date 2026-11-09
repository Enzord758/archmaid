package ui

import (
	"bytes"
	"testing"
)

// The embedded gresource (compiled by `go generate`) holds the SVG that is
// checked in, at the path the icon theme looks under; so a changed SVG
// without a recompile fails here.
func TestIconResourceHoldsTheSVG(t *testing.T) {
	res, err := loadIconResource()
	if err != nil {
		t.Fatalf("the embedded gresource does not load: %v", err)
	}
	got, err := res.LookupData(iconsResourcePath+"/scalable/status/archmaid-health-symbolic.svg", 0)
	if err != nil {
		t.Fatalf("the pulse icon is not in the resource: %v", err)
	}
	if !bytes.Equal(got.Data(), healthIconSVG) {
		t.Fatal("icons/icons.gresource is out of date: run go generate ./internal/ui")
	}
}
