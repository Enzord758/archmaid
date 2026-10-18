package report

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Enzord758/archmaid/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

// logFixture is a pacman.log as pacman writes it: the run's lines, noise
// around them, a config warning pair, a scriptlet line.
const logFixture = `[2026-10-18T08:00:00+0200] [PACMAN] Running 'pacman -Syu'
[2026-10-18T08:00:01+0200] [ALPM] upgraded old-before (1.0-1 -> 1.1-1)
[2026-10-18T09:12:03+0200] [PACMAN] Running 'pacman -S -y -u --config /etc/pacman.conf --'
[2026-10-18T09:12:04+0200] [ALPM] upgraded linux-cachyos (7.2.10-1 -> 7.2.12-1)
[2026-10-18T09:12:05+0200] [ALPM] upgraded systemd (257.9-1 -> 258.1-2)
[2026-10-18T09:12:05+0200] [ALPM] installed zenity (4.0.1-1)
[2026-10-18T09:12:06+0200] [ALPM] removed gtk2 (2.24.33-5)
[2026-10-18T09:12:06+0200] [ALPM] upgraded google-chrome (140.0.1-1 -> 141.0.1-1)
[2026-10-18T09:12:07+0200] [ALPM] warning: /etc/pacman.d/mirrorlist installed as /etc/pacman.d/mirrorlist.pacnew
[2026-10-18T09:12:07+0200] [ALPM] warning: /etc/foo.conf saved as /etc/foo.conf.pacsave
[2026-10-18T09:12:08+0200] [ALPM-SCRIPTLET] ==> Updating initramfs
[2026-10-18T09:12:09+0200] [ALPM] reinstalled keepme (1.0-1)
[2026-10-18T09:12:09+0200] [ALPM] downgraded slowpoke (2.0-1 -> 1.9-1)
`

func names(ps []Pkg) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

// TestParseLogKinds: upgraded, installed, removed, downgraded, with old and
// new versions; reinstalls change nothing; the config warnings are listed.
func TestParseLogKinds(t *testing.T) {
	ch, err := ParseLog(strings.NewReader(logFixture))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(ch.Upgraded), []string{"old-before", "linux-cachyos", "systemd", "google-chrome"}; !reflect.DeepEqual(got, want) {
		t.Errorf("upgraded %v, want %v", got, want)
	}
	if got := ch.Upgraded[1]; got.Old != "7.2.10-1" || got.New != "7.2.12-1" {
		t.Errorf("linux-cachyos %+v", got)
	}
	if got := ch.Installed; len(got) != 1 || got[0] != (Pkg{Name: "zenity", New: "4.0.1-1"}) {
		t.Errorf("installed %+v", got)
	}
	if got := ch.Removed; len(got) != 1 || got[0] != (Pkg{Name: "gtk2", Old: "2.24.33-5"}) {
		t.Errorf("removed %+v", got)
	}
	if got := ch.Downgraded; len(got) != 1 || got[0].Name != "slowpoke" {
		t.Errorf("downgraded %+v", got)
	}
	if want := []string{"/etc/pacman.d/mirrorlist.pacnew"}; !reflect.DeepEqual(ch.Pacnew, want) {
		t.Errorf("pacnew %v", ch.Pacnew)
	}
	if want := []string{"/etc/foo.conf.pacsave"}; !reflect.DeepEqual(ch.Pacsave, want) {
		t.Errorf("pacsave %v", ch.Pacsave)
	}
}

// TestParseLogFold: a package's lines are folded to its net change.
func TestParseLogFold(t *testing.T) {
	cases := []struct {
		name string
		log  string
		up   []Pkg
		in   []Pkg
		rm   []Pkg
	}{
		{"build dependency installed then removed cancels",
			"[t] [ALPM] installed makedep (1-1)\n[t] [ALPM] removed makedep (1-1)\n", nil, nil, nil},
		{"upgraded twice is one upgrade",
			"[t] [ALPM] upgraded a (1-1 -> 2-1)\n[t] [ALPM] upgraded a (2-1 -> 3-1)\n",
			[]Pkg{{Name: "a", Old: "1-1", New: "3-1"}}, nil, nil},
		{"upgraded then removed is a removal of the old version",
			"[t] [ALPM] upgraded a (1-1 -> 2-1)\n[t] [ALPM] removed a (2-1)\n",
			nil, nil, []Pkg{{Name: "a", Old: "1-1"}}},
		{"removed then installed again is an upgrade",
			"[t] [ALPM] removed a (1-1)\n[t] [ALPM] installed a (2-1)\n",
			[]Pkg{{Name: "a", Old: "1-1", New: "2-1"}}, nil, nil},
		{"removed then the same version installed is nothing",
			"[t] [ALPM] removed a (1-1)\n[t] [ALPM] installed a (1-1)\n", nil, nil, nil},
		{"installed then upgraded is an install of the last version",
			"[t] [ALPM] installed a (1-1)\n[t] [ALPM] upgraded a (1-1 -> 2-1)\n",
			nil, []Pkg{{Name: "a", New: "2-1"}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch, err := ParseLog(strings.NewReader(c.log))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(ch.Upgraded, c.up) || !reflect.DeepEqual(ch.Installed, c.in) || !reflect.DeepEqual(ch.Removed, c.rm) {
				t.Errorf("up %+v in %+v rm %+v, want %+v %+v %+v", ch.Upgraded, ch.Installed, ch.Removed, c.up, c.in, c.rm)
			}
		})
	}
}

// TestParseLogHostile: names pacman would never write, lines that aren't
// ALPM lines and a pacnew path with an escape never reach the report raw.
func TestParseLogHostile(t *testing.T) {
	log := "[t] [ALPM] upgraded evil;rm (1 -> 2)\n" +
		"[t] [ALPM] upgraded good (1 -> 2)\n" +
		"[t] [ALPM] warning: /etc/x\x1b]52;c;AAAA\x07 installed as /etc/x\x1b]52;c;AAAA\x07.pacnew\n" +
		"garbage line\n" +
		"[t] [ALPM] upgraded broken\n"
	ch, _ := ParseLog(strings.NewReader(log))
	if got := names(ch.Upgraded); !reflect.DeepEqual(got, []string{"good"}) {
		t.Errorf("upgraded %v, want only good", got)
	}
	for _, f := range ch.Pacnew {
		if strings.ContainsAny(f, "\x1b\x07") {
			t.Errorf("pacnew path keeps a control character: %q", f)
		}
	}
}

// TestReadLogOffset: lines before the mark are ignored; the offset, not the
// timestamps, decides (the old lines here are dated after the new ones).
func TestReadLogOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pacman.log")
	before := "[2099-01-01T00:00:00+0000] [ALPM] upgraded before (1 -> 2)\n"
	after := "[2000-01-01T00:00:00+0000] [ALPM] upgraded after (1 -> 2)\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	m := MarkLog(path)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(after)
	f.Close()
	ch, err := ReadLog(m)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(ch.Upgraded); !reflect.DeepEqual(got, []string{"after"}) || ch.Partial {
		t.Errorf("upgraded %v partial %v, want [after] and complete", got, ch.Partial)
	}
}

// TestReadLogTruncated: a log shorter than at the mark was truncated: it is
// read from its start and marked partial.
func TestReadLogTruncated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pacman.log")
	os.WriteFile(path, []byte(strings.Repeat("[t] [ALPM] upgraded old (1 -> 2)\n", 10)), 0o644)
	m := MarkLog(path)
	os.WriteFile(path, []byte("[t] [ALPM] upgraded fresh (1 -> 2)\n"), 0o644) // same inode, shorter
	ch, err := ReadLog(m)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(ch.Upgraded); !reflect.DeepEqual(got, []string{"fresh"}) || !ch.Partial {
		t.Errorf("upgraded %v partial %v, want [fresh] and partial", got, ch.Partial)
	}
}

// TestReadLogRotated: a rotated log (a new file under the same name, even
// a longer one) is read from its start and marked partial.
func TestReadLogRotated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pacman.log")
	os.WriteFile(path, []byte("[t] [ALPM] upgraded old (1 -> 2)\n"), 0o644)
	m := MarkLog(path)
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte(strings.Repeat("[t] [ALPM] upgraded filler (1 -> 2)\n", 3)+"[t] [ALPM] upgraded fresh (1 -> 2)\n"), 0o644)
	ch, err := ReadLog(m)
	if err != nil {
		t.Fatal(err)
	}
	if !ch.Partial || !reflect.DeepEqual(names(ch.Upgraded), []string{"filler", "fresh"}) {
		t.Errorf("upgraded %v partial %v, want [filler fresh] and partial", names(ch.Upgraded), ch.Partial)
	}
}

// TestReadLogMissing: a log that can't be opened is an error, not "nothing
// changed".
func TestReadLogMissing(t *testing.T) {
	if _, err := ReadLog(MarkLog(filepath.Join(t.TempDir(), "none"))); err == nil {
		t.Error("a missing log read as no changes")
	}
}

// TestVersionLess: the downgrade test.
func TestVersionLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		less bool
	}{
		{"1.9-1", "2.0-1", true}, {"2.0-1", "1.9-1", false}, {"1.10-1", "1.9-1", false},
		{"1.0rc1-1", "1.0-1", true}, {"1.0-1", "1.0-2", true}, {"7.2.10-1", "7.2.12-1", true},
	} {
		if got := versionLess(c.a, c.b); got != c.less {
			t.Errorf("versionLess(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

// TestDefaultsAreNotTheMachine: under testenv the environment points the
// log and the modules at nothing, so no test of this module reads the real
// ones through DefaultConfig.
func TestDefaultsAreNotTheMachine(t *testing.T) {
	c := DefaultConfig()
	if c.PacmanLog == DefaultPacmanLog || c.ModulesDir == DefaultModulesDir {
		t.Errorf("DefaultConfig under testenv reads the real %s or %s", c.PacmanLog, c.ModulesDir)
	}
	if _, err := os.Stat(c.PacmanLog); err == nil {
		t.Errorf("%s exists", c.PacmanLog)
	}
	if EnvPacmanLog != "ARCHMAID_PACMAN_LOG" || EnvModulesDir != "ARCHMAID_MODULES_DIR" {
		t.Error("the env names changed: testenv sets them by name")
	}
}
