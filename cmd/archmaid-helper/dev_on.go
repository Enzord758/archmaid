//go:build devhelper

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// devBuild marks a development binary (`go build -tags devhelper`): it runs
// from any path as a normal user — handy for a GUI walking skeleton calling
// check-files or plan-orphans — and refuses to run as root at all, so it can
// never stand in for the real helper. Never package this build.
const devBuild = true

func devLockPath() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("archmaid-helper-dev-%d.lock", os.Getuid()))
}
