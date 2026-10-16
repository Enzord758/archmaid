// Package sysbin names the system programs archmaid runs for privilege, by
// absolute path: never looked up on PATH, whatever PATH the session has.
//
// A directory early on PATH (~/.local/bin, a relative entry, one an
// installer added) can hold a program named sudo, su or pkexec that is not
// the real one. Started by name, archmaid would hand it a password (su's
// check, `sudo -S -v`) or let it stand in for polkit. So archmaid names
// each one here, by its path on Arch (/usr/bin, where each of these
// packages installs it), and runs that file. What archmaid's children start
// by name (topgrade's yay, yay's sudo) still goes through their PATH:
// internal/privilege's PATH guard (pathguard.go) covers the kept password
// there (docs/HANDOVER.md, "archmaid doesn't trust PATH for privilege").
//
// They are variables, in a package that imports nothing, so that tests can
// still trap them: internal/testenv points every one back at its bare name
// in each process of a test binary (its init, which also runs in a
// re-exec'd child that never calls testenv.Run), so testenv's traps and a
// test's own shims, first on PATH, catch them as before. A test that proves
// a call site uses the absolute path sets the variable to a shim of its own
// outside PATH. The e2e harness builds the real binary with each of them
// set back to its bare name (-ldflags -X, cmd/archmaid/e2e/run.sh), so its
// PATH shims catch them; the package build (packaging/arch/PKGBUILD) and
// the cover builds for the test VM must not (scripts/check.sh). Read them
// at the call, never copy one into another package-level variable: a copy
// made at init can be taken before testenv's init has run.
//
// testenv's TestSysbinDefaultsAbsolute keeps each default an absolute
// /usr/bin path, and TestNoBarePrivilegedExec keeps the bare names out of
// every exec call in the module.
package sysbin

var (
	// Su checks a password (privilege.CheckPassword): `su -s /usr/bin/true
	// <user>`, single-shot PAM with no pam_faillock.
	Su = "/usr/bin/su"
	// Sudo is the sudo ticket's (probe, `-S -v`, keep-alive, `-k`), every
	// sudo step's (privilege.Sudo.Command) and Repair's root recheck's.
	Sudo = "/usr/bin/sudo"
	// Pkexec starts the root helper (helperclient.Run).
	Pkexec = "/usr/bin/pkexec"
	// Pkttyagent is polkit's text agent, the GUI's own
	// (gui/internal/polkitagent).
	Pkttyagent = "/usr/bin/pkttyagent"
	// Topgrade is the update step's program (privilege.Sudo.Command runs a
	// step's "topgrade" from here). The kept password's allowlist
	// (privilege.Requester.KeptMayAnswer) trusts /usr/bin/topgrade only.
	Topgrade = "/usr/bin/topgrade"
)

// All is every path above, in order.
func All() []*string { return []*string{&Su, &Sudo, &Pkexec, &Pkttyagent, &Topgrade} }
