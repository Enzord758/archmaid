//go:build !devhelper

package main

// devBuild is false in every real build. Build with `-tags devhelper` for a
// development binary that refuses root and skips the install-path check.
const devBuild = false

func devLockPath() string { return "" }
