package report

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DefaultModulesDir is where every kernel package puts its modules, in a
// folder named like `uname -r` that holds a `pkgbase` file with the package
// base name (linux, linux-lts, linux-cachyos, ...).
const DefaultModulesDir = "/usr/lib/modules"

// restartPkg are the packages whose upgrade a running system keeps using
// the old copy of until it restarts: the init system, the graphics driver
// and its userspace. (A kernel is found differently, see kernelStale.)
// nvidia-settings and nvidia-prime are tools, not the driver.
var restartPkg = regexp.MustCompile(`^(lib32-)?(systemd|mesa|nvidia(-[a-z0-9-]+)?)$`)

var restartNot = map[string]bool{"nvidia-settings": true, "nvidia-prime": true, "lib32-nvidia-settings": true}

// IsRestartPkg reports whether upgrading the package called name calls for a
// restart (not the kernel). The DMS widget's state.js isRestartPkg mirrors it;
// packaging/dms checks the two agree.
func IsRestartPkg(name string) bool {
	return restartPkg.MatchString(name) && !restartNot[name]
}

// restartCandidates are the packages upgraded in this run that call for a
// restart (not the kernel).
func restartCandidates(upgraded []Pkg) []string {
	var names []string
	for _, p := range upgraded {
		if IsRestartPkg(p.Name) {
			names = append(names, p.Name)
		}
	}
	return names
}

// kernelStale compares the running kernel (`uname -r`, running) with what
// is installed: every kernel package leaves /usr/lib/modules/<release>/
// with its pkgbase, so when the running release's folder is gone, an
// upgrade replaced the kernel that is running. kernelPkgs are the upgraded
// packages that are kernels (their name is some folder's pkgbase), and
// installed the release that replaced it, when one of them says so ("" when
// unknown). A second kernel being upgraded while another one runs is not
// stale: the running one's folder is still there.
//
// stale is false when running is empty or the folder can't be read: nothing
// is claimed that wasn't seen.
func kernelStale(running, modulesDir string, upgraded []Pkg) (stale bool, installed string, kernelPkgs []string) {
	if running == "" {
		return false, "", nil
	}
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		return false, "", nil
	}
	base := map[string]string{} // release -> pkgbase
	runningHere := false
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if e.Name() == running {
			runningHere = true
		}
		if b, err := os.ReadFile(filepath.Join(modulesDir, e.Name(), "pkgbase")); err == nil {
			base[e.Name()] = strings.TrimSpace(string(b))
		}
	}
	if runningHere || len(base) == 0 {
		// Running's folder is there, or no kernel package left a folder at
		// all (a container, a kernel not from a package): nothing to say.
		return false, "", nil
	}
	for _, p := range upgraded {
		for rel, b := range base {
			if b == p.Name {
				kernelPkgs = append(kernelPkgs, p.Name)
				if installed == "" || rel > installed {
					installed = rel
				}
			}
		}
	}
	return true, installed, dedupe(kernelPkgs)
}

func dedupe(s []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
