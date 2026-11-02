package helper

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// The protected list: packages pkg-remove never removes, as a target or as
// part of the cascade, whatever pacman would allow. pacman already stops a
// removal something else needs; this covers what nothing "needs" but a
// machine does (the kernel it runs, its boot chain, the privilege path,
// archmaid itself). The GUI greys these rows; the helper enforces it.
var protectedStatic = map[string]string{
	"base":              "the base system",
	"filesystem":        "the base system",
	"glibc":             "the C library",
	"bash":              "the system shell",
	"coreutils":         "core system tools",
	"util-linux":        "core system tools",
	"systemd":           "the init system",
	"systemd-libs":      "the init system",
	"pacman":            "the package manager",
	"archlinux-keyring": "pacman's signature keys",
	"sudo":              "the privilege path",
	"sudo-rs":           "the privilege path",
	"polkit":            "the privilege path",
	"linux":             "a kernel",
	"linux-lts":         "a kernel",
	"linux-zen":         "a kernel",
	"linux-hardened":    "a kernel",
	"mkinitcpio":        "the initramfs generator",
	"dracut":            "the initramfs generator",
	"grub":              "the bootloader",
	"limine":            "the bootloader",
	"refind":            "the bootloader",
	"syslinux":          "the bootloader",
	"efibootmgr":        "the bootloader",
}

// protectedPrefix is the one prefix rule: archmaid and its sub-packages.
const protectedPrefix = "archmaid"

// kernelPkgbases lists the kernels' /usr/lib/modules/*/pkgbase files (the
// running kernel's among them): the packages owning them are the installed
// kernels. Options.KernelPkgbases is the test seam.
func (s *session) kernelPkgbases() ([]string, error) {
	if s.o.KernelPkgbases != nil {
		return s.o.KernelPkgbases()
	}
	return filepath.Glob("/usr/lib/modules/*/pkgbase")
}

// protectedSet returns the protected package names with the reason each is
// protected: the static list plus, asked of pacman, every installed kernel
// package and everything `base` depends on. Errors fail closed.
func (s *session) protectedSet() (map[string]string, error) {
	set := map[string]string{}
	for n, r := range protectedStatic {
		set[n] = r
	}
	paths, err := s.kernelPkgbases()
	if err != nil {
		return nil, fmt.Errorf("listing the installed kernels: %w", err)
	}
	var safe []string
	for _, p := range paths {
		if strings.HasPrefix(p, "/usr/lib/modules/") && !strings.ContainsAny(p, "\n\x00") {
			safe = append(safe, p)
		}
	}
	if len(safe) > 0 {
		out, errOut, exit, err := s.o.Runner.Query(pacmanArgv([]string{"-Qqo"}, colourNever, safe))
		if err != nil {
			return nil, fmt.Errorf("pacman -Qqo: %w", err)
		}
		// Exit 1 is allowed: a directory an uninstalled kernel left behind
		// has no owner, and the others are still printed.
		if exit != 0 && exit != 1 {
			return nil, fmt.Errorf("pacman -Qqo exited %d: %s", exit, firstLine(errOut))
		}
		for _, n := range nonEmptyLines(out) {
			if namePattern.MatchString(n) {
				set[n] = "an installed kernel"
			}
		}
	}
	out, _, exit, err := s.o.Runner.Query(queryArgv("-Qi", "base"))
	if err != nil {
		return nil, fmt.Errorf("pacman -Qi base: %w", err)
	}
	if exit == 0 {
		for _, n := range dependsOn(out) {
			if _, ok := set[n]; !ok {
				set[n] = "required by the base system"
			}
		}
	}
	return set, nil
}

// dependsOn parses the "Depends On" field of `pacman -Qi`: names, without
// version constraints.
func dependsOn(out string) []string {
	var names []string
	for _, l := range nonEmptyLines(out) {
		k, v, ok := strings.Cut(l, ":")
		if !ok || strings.TrimSpace(k) != "Depends On" {
			continue
		}
		for _, f := range strings.Fields(v) {
			if i := strings.IndexAny(f, "<>="); i >= 0 {
				f = f[:i]
			}
			if namePattern.MatchString(f) {
				names = append(names, f)
			}
		}
	}
	return names
}

// protectedReason is why name may not be removed, or "".
func protectedReason(set map[string]string, name string) string {
	if r, ok := set[name]; ok {
		return r
	}
	if strings.HasPrefix(name, protectedPrefix) {
		return "archmaid itself"
	}
	return ""
}

// protectedTargets refuses (ExitRefused) a removal whose targets hold a
// protected package, before pacman is asked anything about the cascade.
// names is every protected target.
func protectedTargets(set map[string]string, targets []string) *pkgFail {
	var names []string
	var msg string
	for _, t := range targets {
		if r := protectedReason(set, t); r != "" {
			names = append(names, t)
			if msg == "" {
				msg = fmt.Sprintf("%s is protected (%s) and is never removed here.", t, r)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	return &pkgFail{code: ExitRefused, reason: ReasonProtected, msg: msg, names: names}
}

// protectedCascade refuses a removal whose cascade (items, "name version")
// holds a protected package that is not itself a target.
func protectedCascade(set map[string]string, targets, items []string) *pkgFail {
	var names []string
	var msg string
	for _, it := range items {
		name, _, _ := strings.Cut(it, " ")
		if slices.Contains(targets, name) {
			continue
		}
		if r := protectedReason(set, name); r != "" {
			names = append(names, name)
			if msg == "" {
				msg = fmt.Sprintf("removing this would also remove %s, which is protected (%s).", name, r)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	return &pkgFail{code: ExitRefused, reason: ReasonProtected, msg: msg, names: names}
}
