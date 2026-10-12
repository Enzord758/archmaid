// Package installed names the programs the archmaid package installs that
// archmaid itself executes by absolute path.
//
// internal/testenv traps the system's binaries through PATH, which can't
// catch an absolute path. So every such path lives here, in a package that
// imports nothing (internal/testenv imports it without a cycle), as a
// variable: testenv.Run points each one at a trap for the whole package run,
// so a test that reaches one fails its package like any other trapped call,
// on a machine with the package installed or not. testenv's
// TestNoAbsoluteInstalledExec keeps the literal paths in this file only.
//
// archmaid-helper is not here: archmaid never executes it, it only names it
// to pkexec (helperclient.HelperPath), and pkexec is trapped on PATH.
package installed

// UpdatesScript is the DMS widget's update check
// (packaging/systemd/archmaid-updates-check). driver.RefreshUpdates runs it
// in the background when `systemctl --user start` can't start its unit.
var UpdatesScript = "/usr/lib/archmaid/archmaid-updates-check"

// All is every path above, for internal/testenv to point at its traps.
func All() []*string { return []*string{&UpdatesScript} }
