package report

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// modules builds a modules folder: release -> pkgbase ("" for a folder
// without a pkgbase file).
func modules(t *testing.T, kernels map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, base := range kernels {
		if err := os.MkdirAll(filepath.Join(dir, rel), 0o755); err != nil {
			t.Fatal(err)
		}
		if base != "" {
			os.WriteFile(filepath.Join(dir, rel, "pkgbase"), []byte(base+"\n"), 0o644)
		}
	}
	return dir
}

func up(names ...string) []Pkg {
	var out []Pkg
	for _, n := range names {
		out = append(out, Pkg{Name: n, Old: "1", New: "2"})
	}
	return out
}

// TestKernelStale: the running kernel's module folder is gone once its
// package was upgraded; a kernel that is not running changes nothing.
func TestKernelStale(t *testing.T) {
	for _, c := range []struct {
		name     string
		kernels  map[string]string
		running  string
		upgraded []Pkg
		stale    bool
		inst     string
		pkgs     []string
	}{
		{"running kernel was replaced",
			map[string]string{"7.2.12-1-cachyos": "linux-cachyos"}, "7.2.10-1-cachyos", up("linux-cachyos", "systemd"),
			true, "7.2.12-1-cachyos", []string{"linux-cachyos"}},
		{"running kernel is installed",
			map[string]string{"7.2.12-1-cachyos": "linux-cachyos"}, "7.2.12-1-cachyos", up("linux-cachyos"), false, "", nil},
		{"another kernel upgraded while this one runs",
			map[string]string{"7.2.10-1-cachyos": "linux-cachyos", "6.12.9-1-lts": "linux-lts"}, "7.2.10-1-cachyos", up("linux-lts"),
			false, "", nil},
		{"replaced, and not by a package of this run",
			map[string]string{"7.2.12-1-cachyos": "linux-cachyos"}, "7.2.10-1-cachyos", up("ripgrep"), true, "", nil},
		{"two kernels, the running one's package upgraded",
			map[string]string{"7.2.12-1-cachyos": "linux-cachyos", "6.12.9-1-lts": "linux-lts"}, "7.2.10-1-cachyos", up("linux-cachyos"),
			true, "7.2.12-1-cachyos", []string{"linux-cachyos"}},
		{"no running kernel known", map[string]string{"7.2.12-1": "linux"}, "", up("linux"), false, "", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			stale, inst, pkgs := kernelStale(c.running, modules(t, c.kernels), c.upgraded)
			if stale != c.stale || inst != c.inst || !reflect.DeepEqual(pkgs, c.pkgs) {
				t.Errorf("= %v, %q, %v; want %v, %q, %v", stale, inst, pkgs, c.stale, c.inst, c.pkgs)
			}
		})
	}
	// Nothing is claimed that wasn't seen.
	if stale, _, _ := kernelStale("7.2.10-1", filepath.Join(t.TempDir(), "none"), up("linux")); stale {
		t.Error("an unreadable modules folder claimed a stale kernel")
	}
}

// TestKernelCheck: the System health check's variant, with no upgrade list.
// It agrees with kernelStale on stale, and names the newest release of the
// running kernel's flavour (7.2.9 before 7.2.12: numbers, not text).
func TestKernelCheck(t *testing.T) {
	for _, c := range []struct {
		name    string
		kernels map[string]string
		running string
		stale   bool
		inst    string
	}{
		{"replaced", map[string]string{"7.2.12-1-cachyos": "linux-cachyos"}, "7.2.10-1-cachyos", true, "7.2.12-1-cachyos"},
		{"running is installed", map[string]string{"7.2.12-1-cachyos": "linux-cachyos"}, "7.2.12-1-cachyos", false, ""},
		{"same flavour wins over a newer lts",
			map[string]string{"7.2.12-1-cachyos": "linux-cachyos", "7.9.0-1-lts": "linux-lts"}, "7.2.10-1-cachyos", true, "7.2.12-1-cachyos"},
		{"numeric order", map[string]string{"7.2.9-1-arch": "linux", "7.2.12-1-arch": "linux"}, "7.2.1-1-arch", true, "7.2.12-1-arch"},
		{"no flavour match falls back to the newest",
			map[string]string{"6.12.9-1-lts": "linux-lts"}, "7.2.10-1-cachyos", true, "6.12.9-1-lts"},
		{"no pkgbase anywhere", map[string]string{"7.2.12-1": ""}, "7.2.10-1", false, ""},
		{"running unknown", map[string]string{"7.2.12-1": "linux"}, "", false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			stale, inst := KernelCheck(c.running, modules(t, c.kernels))
			if stale != c.stale || inst != c.inst {
				t.Errorf("= %v, %q; want %v, %q", stale, inst, c.stale, c.inst)
			}
			if old, _, _ := kernelStale(c.running, modules(t, c.kernels), nil); old != stale {
				t.Errorf("kernelStale says %v", old)
			}
		})
	}
	if stale, _ := KernelCheck("7.2.10-1", filepath.Join(t.TempDir(), "none")); stale {
		t.Error("an unreadable modules folder claimed a stale kernel")
	}
}

// TestEnvPaths: the exported helpers honour the same overrides as DefaultConfig.
func TestEnvPaths(t *testing.T) {
	t.Setenv(EnvPacmanLog, "/x/log")
	t.Setenv(EnvModulesDir, "/x/mods")
	if PacmanLogFromEnv() != "/x/log" || ModulesDirFromEnv() != "/x/mods" {
		t.Error("overrides ignored")
	}
	t.Setenv(EnvPacmanLog, "")
	t.Setenv(EnvModulesDir, "")
	if PacmanLogFromEnv() != DefaultPacmanLog || ModulesDirFromEnv() != DefaultModulesDir {
		t.Error("defaults not used")
	}
	if UserCacheDir() == "" {
		t.Error("no cache dir")
	}
	_ = RunningKernel()
}

// TestRestartCandidates: systemd, mesa and the NVIDIA driver, 32-bit
// builds too; their tools and look-alikes not.
func TestRestartCandidates(t *testing.T) {
	got := restartCandidates(up("systemd", "systemd-libs", "mesa", "lib32-mesa", "nvidia-utils", "nvidia-open-dkms",
		"nvidia-settings", "nvidia-prime", "mesa-utils", "ripgrep", "lib32-nvidia-utils", "linux-cachyos-nvidia-open"))
	// linux-cachyos-nvidia-open is a kernel module package, not the
	// driver's userspace: the anchored pattern leaves it out.
	want := []string{"systemd", "mesa", "lib32-mesa", "nvidia-utils", "nvidia-open-dkms", "lib32-nvidia-utils"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("candidates %v, want %v", got, want)
	}
}
