package privilege

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Enzord758/archmaid/internal/sysbin"
)

// The kept password's config guard, the PATH guard's twin (pathguard.go).
// topgrade, paru and yay choose the program they run sudo with, and what
// they run it on, from files the user can write, so a build script under an
// earlier run can plant them:
//
//   - topgrade.toml: sudo_command;
//   - paru.conf ($PARU_CONF, else <config>/paru/paru.conf): Sudo, SudoFlags,
//     Pacman, PacmanConf, and Include (which can pull in any of those);
//   - yay's config.json / init.lua: sudobin, sudoflags, pacmanbin, pacmanconf.
//
// A script named there that execs the real sudo -A with a command of its own
// is sudo run by the helper, which is a kept caller: the request passes
// Request.Verify and KeptMayAnswer and the kept password answers it without a
// dialog. The same goes for a pacman or pacman.conf of the user's own, which
// sudo then runs as root. So the Driver scans these files when a step that
// gets the askpass starts, with the PATH scan, and turns the kept password
// off for the run when one of them sets any of those keys to something but
// the system's own default. Like the PATH scan it sees the files as they are
// when the step starts, not a file planted mid-run.

// ConfigShadows returns the files of the user's topgrade, paru and yay
// config that name a sudo, pacman or pacman.conf of their own, for a child
// started with env and extra (as ChildPATH). A file that exists but can't be
// read counts too. Each Shadow is one file, its Note the key (or the reason).
func ConfigShadows(env, extra []string) []Shadow {
	get := childEnv(env, extra)
	cfg := get("XDG_CONFIG_HOME")
	if !filepath.IsAbs(cfg) {
		home := get("HOME")
		if !filepath.IsAbs(home) {
			return nil
		}
		cfg = filepath.Join(home, ".config")
	}
	paru := get("PARU_CONF")
	if paru == "" {
		paru = filepath.Join(cfg, "paru", "paru.conf")
	}
	var out []Shadow
	scan := func(path string, keys *regexp.Regexp, comment string) {
		if note := configRisk(path, keys, comment); note != "" {
			out = append(out, Shadow{Path: path, Note: note})
		}
	}
	scan(filepath.Join(cfg, "topgrade.toml"), topgradeKeys, "#")
	scan(filepath.Join(cfg, "topgrade/topgrade.toml"), topgradeKeys, "#")
	scan(paru, paruKeys, "#")
	scan(filepath.Join(cfg, "yay", "config.json"), yayKeys, "")
	scan(filepath.Join(cfg, "yay", "init.lua"), yayLuaKeys, "--")
	return out
}

// childEnv looks a name up as exec would: the last NAME= of env (nil: our
// own environment) then extra.
func childEnv(env, extra []string) func(string) string {
	if env == nil {
		env = os.Environ()
	}
	return func(name string) string {
		v := ""
		for _, list := range [][]string{env, extra} {
			for _, kv := range list {
				if s, ok := strings.CutPrefix(kv, name+"="); ok {
					v = s
				}
			}
		}
		return v
	}
}

var (
	topgradeKeys = regexp.MustCompile(`^\s*(sudo_command)\s*=\s*(.*)$`)
	paruKeys     = regexp.MustCompile(`^\s*(Sudo|SudoFlags|Pacman|PacmanConf|Include)\s*=\s*(.*)$`)
	yayKeys      = regexp.MustCompile(`"(sudobin|sudoflags|pacmanbin|pacmanconf)"\s*:\s*("[^"]*"|[^,}\s]*)`)
	// yay 13 configures in Lua; the syntax isn't parsed: a line that names
	// one of the keys counts.
	yayLuaKeys = regexp.MustCompile(`(sudobin|sudoflags|pacmanbin|pacmanconf)\s*(?:=|:)\s*(.*)$`)

	// Values that are the system's own: the default, spelled out.
	systemValues = map[string]bool{
		"": true, "sudo": true, sysbin.Sudo: true, "pacman": true, "/usr/bin/pacman": true,
		"/etc/pacman.conf": true, "-A": true, "--askpass": true,
	}
)

const maxConfigScan = 1 << 20

// configRisk reads path and returns why it is a risk ("" when it is not
// there or sets nothing of the kind).
func configRisk(path string, keys *regexp.Regexp, comment string) string {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		return "can't be read"
	}
	if !fi.Mode().IsRegular() {
		return "isn't a regular file"
	}
	f, err := os.Open(path)
	if err != nil {
		return "can't be read"
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxConfigScan+1))
	if err != nil || len(b) > maxConfigScan {
		return "can't be read"
	}
	for _, line := range strings.Split(string(b), "\n") {
		if comment != "" && strings.HasPrefix(strings.TrimSpace(line), comment) {
			continue
		}
		for _, m := range keys.FindAllStringSubmatch(line, -1) { // json may be on one line
			key, val := m[1], m[2]
			if keys == paruKeys && key == "Include" {
				return "sets Include"
			}
			if !systemValues[cleanValue(val)] {
				return "sets " + key
			}
		}
	}
	return ""
}

// cleanValue strips a trailing comment, a comma and the quotes from a
// config value.
func cleanValue(v string) string {
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	v = strings.TrimSpace(v)
	v = strings.TrimRight(v, ",")
	v = strings.TrimSpace(v)
	return strings.Trim(v, `"'`)
}
