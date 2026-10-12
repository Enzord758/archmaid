package proc

import (
	"os"
	"slices"
	"testing"
)

// envProbe prints the variables a step's colour switch and askpass hinge
// on, as the child sees them ("unset" if not).
const envProbe = `printf 'CLICOLOR_FORCE=%s\n' "${CLICOLOR_FORCE-unset}"
printf 'SUDO_ASKPASS=%s\n' "${SUDO_ASKPASS-unset}"
printf 'KEEP=%s\n' "${KEEP-unset}"
command -v sh >/dev/null && echo path-ok`

// TestExtraEnv: a Spec's ExtraEnv reaches its child on top of the
// environment it gets anyway (archmaid's own for a nil Env, else Env),
// wins over the same name there, and never carries a SUDO_ASKPASS: only
// the caller's Env hands out the askpass wrapper. archmaid's own
// environment and the caller's Env slice are left as they were.
func TestExtraEnv(t *testing.T) {
	t.Setenv("SUDO_ASKPASS", "/from/the/users/shell")
	t.Setenv("CLICOLOR_FORCE", "0")
	t.Setenv("KEEP", "kept")
	extra := []string{"CLICOLOR_FORCE=1", "SUDO_ASKPASS=/smuggled"}

	run := func(spec Spec) []string {
		t.Helper()
		spec.Name, spec.Args = "sh", []string{"-c", envProbe}
		ch := make(chan Msg, 16)
		New(Options{}).Start(spec, ch, nil)
		lines, err := drain(t, ch)
		if err != nil {
			t.Fatalf("%+v: %v (%q)", spec, err, lines)
		}
		return lines
	}

	// A nil Env: archmaid's environment without its SUDO_ASKPASS, then
	// the step's variables.
	if got, want := run(Spec{ExtraEnv: extra}), []string{"CLICOLOR_FORCE=1", "SUDO_ASKPASS=unset", "KEEP=kept", "path-ok"}; !slices.Equal(got, want) {
		t.Errorf("nil Env + ExtraEnv: child saw %q, want %q", got, want)
	}
	// An explicit Env (privilege.Sudo.Env's, with the wrapper): it stays
	// whole, the step's variables follow, the smuggled askpass doesn't.
	env := []string{"PATH=" + os.Getenv("PATH"), "SUDO_ASKPASS=/run/archmaid/askpass.sh", "CLICOLOR_FORCE=0"}
	before := slices.Clone(env)
	big := append(slices.Clip(env), "")[:len(env)] // spare capacity an append could write into
	if got, want := run(Spec{Env: big, ExtraEnv: extra}), []string{"CLICOLOR_FORCE=1", "SUDO_ASKPASS=/run/archmaid/askpass.sh", "KEEP=unset", "path-ok"}; !slices.Equal(got, want) {
		t.Errorf("Env + ExtraEnv: child saw %q, want %q", got, want)
	}
	spare := big[:cap(big)][len(big):]
	if !slices.Equal(big, before) || len(spare) == 0 || slices.ContainsFunc(spare, func(s string) bool { return s != "" }) {
		t.Errorf("the caller's Env was written to: %q (spare %q)", big, spare)
	}
	// No ExtraEnv: exactly what it was before.
	if got, want := run(Spec{}), []string{"CLICOLOR_FORCE=0", "SUDO_ASKPASS=unset", "KEEP=kept", "path-ok"}; !slices.Equal(got, want) {
		t.Errorf("no ExtraEnv: child saw %q, want %q", got, want)
	}
	if v := os.Getenv("CLICOLOR_FORCE"); v != "0" {
		t.Errorf("archmaid's own CLICOLOR_FORCE is %q now, want it untouched", v)
	}
}
