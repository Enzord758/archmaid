//go:build !shots

package ui

import (
	"errors"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
)

// shotsBuilt: without -tags shots this test binary takes no screenshots
// (shot.go isn't built); the tests that need them skip.
const shotsBuilt = false

func savePNG(coreglib.Objector, string) error { return errors.New("built without -tags shots") }

func nodeText(coreglib.Objector) (string, error) { return "", errors.New("built without -tags shots") }
