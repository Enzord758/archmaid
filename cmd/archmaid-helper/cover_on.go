//go:build archmaidcover

package main

// A cover build for the test VM (scripts/cover-build.sh with
// COVER_HELPER_DIR): the helper's counters go to a directory baked in at
// build time when pkexec dropped GOCOVERDIR. Never in a packaged build.
import _ "github.com/Enzord758/archmaid/cmd/archmaid-helper/internal/coverdir"
