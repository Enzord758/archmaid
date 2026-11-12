package driver

import (
	"testing"

	"github.com/Enzord758/archmaid/internal/privilege"
)

// The kept-password note names the cause (#286): a config that sets its own
// sudo (#282) is not "the PATH check found something that could stand in for
// sudo". Through PreviewKeepOff (the gate's note), which shares keepOffReason
// with the password dialog's NeedsPassword.KeepOff.
func TestKeepOffReasonNamesTheCause(t *testing.T) {
	saveP, saveC := pathShadows, configShadows
	t.Cleanup(func() { pathShadows, configShadows = saveP, saveC })
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := func(path, note string) privilege.Shadow { return privilege.Shadow{Path: path, Note: note} }
	onPath := privilege.Shadow{Path: "/home/u/.local/bin/sudo"}
	paru := cfg(home+"/.config/paru/paru.conf", "sets Sudo")
	for _, c := range []struct {
		name    string
		path    []privilege.Shadow
		cfgs    []privilege.Shadow
		want    string
		wantNot string
	}{
		{"a paru config", nil, []privilege.Shadow{paru},
			"your paru config (~/.config/paru/paru.conf) sets Sudo", "PATH"},
		{"a yay config that can't be read", nil, []privilege.Shadow{cfg(home+"/.config/yay/config.json", "can't be read")},
			"your yay config (~/.config/yay/config.json) can't be read", "PATH"},
		{"a topgrade config", nil, []privilege.Shadow{cfg(home+"/.config/topgrade.toml", "sets sudo_command")},
			"your topgrade config (~/.config/topgrade.toml) sets sudo_command", "PATH"},
		{"two configs", nil, []privilege.Shadow{paru, cfg(home+"/.config/yay/config.json", "sets sudoBin")},
			"your paru config (~/.config/paru/paru.conf) sets Sudo (and 1 more)", "PATH"},
		{"a file on PATH", []privilege.Shadow{onPath}, nil,
			"the PATH check found something that could stand in for sudo", "config"},
		{"both", []privilege.Shadow{onPath}, []privilege.Shadow{paru},
			"the PATH check found something that could stand in for sudo, and your paru config (~/.config/paru/paru.conf) sets Sudo", ""},
	} {
		pathShadows = func(string) []privilege.Shadow { return c.path }
		configShadows = func(_, _ []string) []privilege.Shadow { return c.cfgs }
		if got := PreviewKeepOff(); got != c.want {
			t.Errorf("%s: reason %q, want %q", c.name, got, c.want)
		}
	}
	// Nothing found: the password stays kept.
	pathShadows = func(string) []privilege.Shadow { return nil }
	configShadows = func(_, _ []string) []privilege.Shadow { return nil }
	if got := PreviewKeepOff(); got != "" {
		t.Errorf("clean: %q", got)
	}
}
