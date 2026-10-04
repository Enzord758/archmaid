// Package seam carries internal/helperclient's test hook to helpertest,
// without exporting it from helperclient itself. Being internal to
// internal/helperclient, only helperclient and its subpackages can import
// it: production code elsewhere can't move the install check. helperclient
// fills the hook in when it initialises.
package seam

// SetHelperFile makes Run's install check stat path instead of HelperPath,
// until the returned restore runs. It moves only that check: the command
// Run starts still names HelperPath, polkit's exec.path.
var SetHelperFile func(path string) (restore func())
