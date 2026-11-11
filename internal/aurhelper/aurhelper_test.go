package aurhelper

import (
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/Enzord758/archmaid/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

// deps is a machine with the given programs installed (name -> path), config
// files (path -> content) and environment.
func deps(installed map[string]string, files map[string]string, env map[string]string) Deps {
	return Deps{
		LookPath: func(f string) (string, error) {
			if p, ok := installed[f]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		ReadFile: func(p string) ([]byte, error) {
			if s, ok := files[p]; ok {
				return []byte(s), nil
			}
			return nil, os.ErrNotExist
		},
		Getenv: func(k string) string { return env[k] },
		Home:   func() (string, error) { return "/home/u", nil },
	}
}

const tgConf = "/home/u/.config/topgrade.toml"

func linuxConf(v string) string {
	return "[misc]\nassume_yes = true\n\n[linux]\narch_package_manager = \"" + v + "\"\n"
}

func TestDetect(t *testing.T) {
	both := map[string]string{"paru": "/usr/bin/paru", "yay": "/usr/bin/yay"}
	for _, c := range []struct {
		name      string
		installed map[string]string
		files     map[string]string
		env       map[string]string
		want      Helper
	}{
		{"neither installed", nil, nil, nil, Helper{}},
		{"only yay", map[string]string{"yay": "/usr/bin/yay"}, nil, nil, Helper{Yay, "/usr/bin/yay"}},
		{"only paru", map[string]string{"paru": "/usr/bin/paru"}, nil, nil, Helper{Paru, "/usr/bin/paru"}},
		{"both: paru first, as topgrade's autodetect", both, nil, nil, Helper{Paru, "/usr/bin/paru"}},
		{"the topgrade config says yay, both installed", both, map[string]string{tgConf: linuxConf("yay")}, nil, Helper{Yay, "/usr/bin/yay"}},
		{"the topgrade config says paru, both installed", both, map[string]string{tgConf: linuxConf("paru")}, nil, Helper{Paru, "/usr/bin/paru"}},
		{"the config says paru, only yay installed: falls through", map[string]string{"yay": "/usr/bin/yay"}, map[string]string{tgConf: linuxConf("paru")}, nil, Helper{Yay, "/usr/bin/yay"}},
		{"the config says paru, neither installed", nil, map[string]string{tgConf: linuxConf("paru")}, nil, Helper{}},
		{"the config names something unsupported", both, map[string]string{tgConf: linuxConf("pikaur")}, nil, Helper{Paru, "/usr/bin/paru"}},
		{"the config says autodetect", both, map[string]string{tgConf: linuxConf("autodetect")}, nil, Helper{Paru, "/usr/bin/paru"}},
		{"the config says pacman, only yay installed", map[string]string{"yay": "/usr/bin/yay"}, map[string]string{tgConf: linuxConf("pacman")}, nil, Helper{Yay, "/usr/bin/yay"}},
		{"a hostile value", both, map[string]string{tgConf: linuxConf("yay; rm -rf ~")}, nil, Helper{Paru, "/usr/bin/paru"}},
		{"the key outside [linux] is ignored", both, map[string]string{tgConf: "[misc]\narch_package_manager = \"yay\"\n"}, nil, Helper{Paru, "/usr/bin/paru"}},
		{"the second config location", both, map[string]string{"/home/u/.config/topgrade/topgrade.toml": linuxConf("yay")}, nil, Helper{Yay, "/usr/bin/yay"}},
		{"the first location wins", both, map[string]string{tgConf: linuxConf("paru"), "/home/u/.config/topgrade/topgrade.toml": linuxConf("yay")}, nil, Helper{Paru, "/usr/bin/paru"}},
		{"XDG_CONFIG_HOME is honoured", both, map[string]string{"/x/cfg/topgrade.toml": linuxConf("yay"), tgConf: linuxConf("paru")}, map[string]string{"XDG_CONFIG_HOME": "/x/cfg"}, Helper{Yay, "/usr/bin/yay"}},
		{"a relative XDG_CONFIG_HOME is not", both, map[string]string{tgConf: linuxConf("yay")}, map[string]string{"XDG_CONFIG_HOME": "rel"}, Helper{Yay, "/usr/bin/yay"}},
		{"override yay", both, nil, map[string]string{EnvOverride: "yay"}, Helper{Name: Yay}},
		{"override paru, nothing installed", nil, nil, map[string]string{EnvOverride: "paru"}, Helper{Name: Paru}},
		{"override none", both, nil, map[string]string{EnvOverride: "none"}, Helper{}},
		{"an unknown override is ignored", both, nil, map[string]string{EnvOverride: "pikaur"}, Helper{Paru, "/usr/bin/paru"}},
		{"the override beats the config", both, map[string]string{tgConf: linuxConf("yay")}, map[string]string{EnvOverride: "paru"}, Helper{Name: Paru}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Detect(deps(c.installed, c.files, c.env)); got != c.want {
				t.Errorf("Detect = %+v, want %+v", got, c.want)
			}
		})
	}
	if !(Helper{}).None() || (Helper{Name: Yay}).None() {
		t.Error("None")
	}
	if (Helper{}).Label() != "AUR helper" || (Helper{Name: Paru}).Label() != "paru" {
		t.Error("Label")
	}
}

func TestParseArchPackageManager(t *testing.T) {
	for in, want := range map[string]string{
		"[linux]\narch_package_manager = \"paru\"\n":                                                    "paru",
		"[linux]\narch_package_manager='yay' # mine\n":                                                  "yay",
		"[linux] # arch\n  arch_package_manager  =  \"yay\"  # x\n":                                     "yay",
		"[misc]\nx=1\n[linux]\nyay_arguments = \"--nodevel\"\narch_package_manager = \"paru\"\n[git]\n": "paru",
		"# [linux]\narch_package_manager = \"paru\"\n":                                                  "",
		"[linux]\n# arch_package_manager = \"paru\"\n":                                                  "",
		"[linux]\narch_package_manager = paru\n":                                                        "",
		"[linux]\narch_package_manager = \"paru\n":                                                      "",
		"[linux]\narch_package_manager = 3\n":                                                           "",
		"[linux]\naur_command = \"yay\"\n":                                                              "",
		"[linux.x]\narch_package_manager = \"paru\"\n":                                                  "",
		"[[linux]]\narch_package_manager = \"paru\"\n":                                                  "",
		"":                 "",
		"\x00\x01[linux\n": "",
	} {
		if got := ParseArchPackageManager([]byte(in)); got != want {
			t.Errorf("Parse(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanArgv(t *testing.T) {
	colour := []string{"--color", "always"}
	for _, c := range []struct {
		h       Helper
		aurOnly bool
		colour  []string
		want    []string
	}{
		{Helper{Name: Yay}, false, colour, []string{"yay", "-Sc", "--noconfirm", "--color", "always"}},
		{Helper{Name: Yay}, true, colour, []string{"yay", "-Sc", "--aur", "--noconfirm", "--color", "always"}},
		{Helper{Name: Paru}, false, colour, []string{"paru", "-Sc", "--noconfirm", "--color", "always"}},
		{Helper{Name: Paru}, true, colour, []string{"paru", "-Sc", "--aur", "--noconfirm", "--color", "always"}},
		{Helper{Name: Paru}, true, nil, []string{"paru", "-Sc", "--aur", "--noconfirm"}},
		{Helper{}, true, colour, nil},
	} {
		if got := c.h.CleanArgv(c.aurOnly, c.colour); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%+v aurOnly=%v: %q, want %q", c.h, c.aurOnly, got, c.want)
		}
	}
}

func TestBuildDir(t *testing.T) {
	env := map[string]string{"HOME": "/home/u"}
	for _, c := range []struct {
		name, helper string
		files        map[string]string
		env          map[string]string
		want         string
	}{
		{"yay default", Yay, nil, nil, "/home/u/.cache/yay"},
		{"yay XDG_CACHE_HOME", Yay, nil, map[string]string{"XDG_CACHE_HOME": "/c"}, "/c/yay"},
		{"yay buildDir", Yay, map[string]string{"/home/u/.config/yay/config.json": `{"buildDir":"/mnt/other"}`}, nil, "/mnt/other"},
		{"yay buildDir with ~", Yay, map[string]string{"/home/u/.config/yay/config.json": `{"buildDir":"~/b"}`}, nil, "/home/u/b"},
		{"yay relative buildDir", Yay, map[string]string{"/home/u/.config/yay/config.json": `{"buildDir":"rel"}`}, nil, "/home/u/.cache/yay"},
		{"yay broken json", Yay, map[string]string{"/home/u/.config/yay/config.json": `{`}, nil, "/home/u/.cache/yay"},
		{"paru default", Paru, nil, nil, "/home/u/.cache/paru/clone"},
		{"paru XDG_CACHE_HOME", Paru, nil, map[string]string{"XDG_CACHE_HOME": "/c"}, "/c/paru/clone"},
		{"paru CloneDir", Paru, map[string]string{"/home/u/.config/paru/paru.conf": "[options]\nCloneDir = /mnt/clones\n"}, nil, "/mnt/clones"},
		{"paru BuildDir, the older name", Paru, map[string]string{"/home/u/.config/paru/paru.conf": "[options]\nBuildDir = ~/b\n"}, nil, "/home/u/b"},
		{"paru CloneDir outside [options]", Paru, map[string]string{"/home/u/.config/paru/paru.conf": "[bin]\nCloneDir = /x\n"}, nil, "/home/u/.cache/paru/clone"},
		{"paru AURDEST wins", Paru, map[string]string{"/home/u/.config/paru/paru.conf": "[options]\nCloneDir = /mnt/clones\n"}, map[string]string{"AURDEST": "/aurdest"}, "/aurdest"},
		{"paru PARU_CONF", Paru, map[string]string{"/etc/my.conf": "[options]\nCloneDir = /pc\n"}, map[string]string{"PARU_CONF": "/etc/my.conf"}, "/pc"},
		{"paru relative CloneDir", Paru, map[string]string{"/home/u/.config/paru/paru.conf": "[options]\nCloneDir = rel\n"}, nil, "/home/u/.cache/paru/clone"},
		{"unknown helper", "", nil, nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := map[string]string{}
			for k, v := range env {
				e[k] = v
			}
			for k, v := range c.env {
				e[k] = v
			}
			if got := BuildDir(deps(nil, c.files, e), c.helper); got != c.want {
				t.Errorf("BuildDir = %q, want %q", got, c.want)
			}
		})
	}
	// No home at all: nothing is guessed.
	d := deps(nil, nil, nil)
	d.Home = func() (string, error) { return "", errors.New("no home") }
	if got := BuildDir(d, Paru); got != "" {
		t.Errorf("no home: %q", got)
	}
}

// The real seams work (reads under a scratch HOME; nothing is run).
func TestDefaultDeps(t *testing.T) {
	d := Deps{}
	if d.ConfigDir() == "" || d.CacheDir() == "" {
		t.Error("no config or cache dir under the test HOME")
	}
	if _, err := d.readFile("/nonexistent-archmaid-test"); err == nil {
		t.Error("read of a missing file")
	}
	if _, err := d.lookPath("definitely-not-a-program-archmaid"); err == nil {
		t.Error("found a program that is not there")
	}
	t.Setenv(EnvOverride, "yay")
	if h := Detect(Deps{}); h.Name != Yay {
		t.Errorf("override through the real environment: %+v", h)
	}
}
