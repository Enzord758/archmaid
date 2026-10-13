package actions

import "os"

// Colour in the run output. A step's child writes into a pipe, not a
// terminal, so the tools it runs turn their colour off; the front ends
// show SGR colour (the engine keeps it, sanitize.KeepSGR) and the logs
// stay plain (sanitize.Plain), so the switches below only change what the
// Run page and the TUI's pane show. They are set on the step's own child
// (steps.Step.Env, its Argv), never in archmaid's environment, and only on
// steps that run as the user: a sudo resets the environment for what it
// runs as root. The root helper asks for its own colour, as constants in
// its own argv (cmd/archmaid-helper, helper.go: pacman's --color always on
// what it only relays, --color never on what it parses); nothing here
// reaches it. NO_COLOR is honoured for all output once, by the engine
// (sanitize.ColourMode: the user steps' lines) and for the helper by
// internal/helperclient,
// which then drops the colour of its lines.
//
// What each tool needs, from its source (the versions Arch ships):
//
//   - topgrade 17.12.3 draws with the console crate (Cargo.lock: console
//     0.16.6), whose colors_enabled is (a colour terminal && CLICOLOR != 0)
//     || CLICOLOR_FORCE != 0 (console src/utils.rs, default_colors_enabled).
//     topgrade has no colour flag. CLICOLOR_FORCE=1 colours its own lines
//     (step titles, the summary's OK/FAILED) and reaches everything it
//     starts; tools that honour it (console, anstream: cargo, rustup) colour
//     theirs too.
//   - yay 13.0.1 colours when pacman.conf has Color and stdout is a
//     terminal, unless --color says always/auto/never (pkg/runtime/
//     pacman.go); --color is a global argument it passes on to every pacman
//     it runs (parser.go isGlobal, cmd_builder.go BuildPacmanCmd →
//     FormatGlobals), whose --color always forces it whatever pacman.conf
//     says (pacman 7.1 src/pacman/pacman.c OP_COLOR). It reads no
//     environment variable for it, and topgrade passes yay only its config's
//     [linux] yay_arguments (src/steps/os/archlinux.rs), so yay and pacman
//     under topgrade stay plain unless the owner's topgrade.toml asks.
//   - hyprpm colours always (hyprpm/src/helpers/Colors.hpp, no tty check).
//
// NO_COLOR (no-color.org: set and not empty) turns the switches off.

// colourOff reports whether the user asked for no colour.
var colourOff = func() bool { return os.Getenv("NO_COLOR") != "" }

// topgradeColourEnv is topgrade's step's Env: CLICOLOR_FORCE=1, or nil
// under NO_COLOR.
func topgradeColourEnv() []string {
	if colourOff() {
		return nil
	}
	return []string{"CLICOLOR_FORCE=1"}
}

// yayColourArgs are the arguments that make yay, and the pacman it runs,
// colour into a pipe: --color always, or none under NO_COLOR.
func yayColourArgs() []string {
	if colourOff() {
		return nil
	}
	return []string{"--color", "always"}
}
