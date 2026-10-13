package privilege

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Review #134 (Dev 2) of the PATH guard (#131).

// ownerPATH builds a stand-in for the owner's PATH (2026-10-13): nvm's
// node bin, ~/.local/bin and pnpm's dir before /usr/local/sbin,
// /usr/local/bin and /usr/bin, then cuda, the perl dirs and rustup's. The
// user dirs hold what they hold on the machine (node, npm and npx as
// symlinks, claude, uv, pnpm...), none of the shadow names; /usr/bin is
// the real sudo's dir and also holds su, pkexec and run0 (a symlink to
// systemd-run), as on Arch. It returns the PATH and the real sudo's dir.
func ownerPATH(t *testing.T) (path, usrBin string) {
	t.Helper()
	usrBin = realSudoDir(t)
	home := t.TempDir()
	mk := func(dir string, files ...string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	link := func(dir, name, target string) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	nvm := mk(filepath.Join(home, ".nvm", "versions", "node", "v20.20.2", "bin"), "node")
	link(nvm, "npm", "../lib/node_modules/npm/bin/npm-cli.js")
	link(nvm, "npx", "../lib/node_modules/npm/bin/npx-cli.js")
	link(nvm, "corepack", "../lib/node_modules/corepack/dist/corepack.js")
	local := mk(filepath.Join(home, ".local", "bin"), "uv", "uvx", "archmaid")
	link(local, "claude", filepath.Join(home, ".local", "share", "claude", "versions", "2.1.0"))
	pnpm := mk(filepath.Join(home, ".local", "share", "pnpm"), "pnpm", "pnpx")
	root := t.TempDir()
	localSbin := mk(filepath.Join(root, "usr", "local", "sbin"))
	localBin := mk(filepath.Join(root, "usr", "local", "bin"))
	mk(usrBin, "su", "pkexec", "systemd-run", "yay", "topgrade")
	link(usrBin, "run0", "systemd-run")
	cuda := mk(filepath.Join(root, "opt", "cuda", "bin"), "nvcc")
	perl := []string{
		mk(filepath.Join(usrBin, "site_perl")),
		mk(filepath.Join(usrBin, "vendor_perl")),
		mk(filepath.Join(usrBin, "core_perl"), "perldoc"),
	}
	rustup := mk(filepath.Join(root, "usr", "lib", "rustup", "bin"), "rustc", "cargo")
	path = list(append([]string{nvm, local, pnpm, localSbin, localBin, usrBin, cuda}, append(perl, rustup)...)...)
	return path, usrBin
}

// TestPathShadowsOwnerPATH: the owner's PATH shape, with user dirs before
// /usr/bin that hold no sudo-like program, has no shadow: the kept
// password stays on.
func TestPathShadowsOwnerPATH(t *testing.T) {
	path, _ := ownerPATH(t)
	if got := PathShadows(path); got != nil {
		t.Errorf("PathShadows(owner's PATH) = %q, want none", paths(got))
	}
}

// TestPathShadowsSudoLikeAfterRealSudo: a sudo-like program that the real
// sudo's directory doesn't hold can be found after it. topgrade's sudo
// detection (src/sudo.rs, Sudo::detect) tries doas and please before sudo
// on the whole PATH, and yay (--sudo, sudobin) and makepkg (PACMAN_AUTH)
// can be set to one; a doas script in ~/bin after /usr/bin that execs
// /usr/bin/sudo -A is then sudo started by topgrade, and the kept password
// answers it. The same holds for a relative entry after /usr/bin.
func TestPathShadowsSudoLikeAfterRealSudo(t *testing.T) {
	t.Skip("KNOWN BUG (review #134, should-fix): the scan stops at the real sudo's dir for every name, so a doas, " +
		"please or sudo-rs that /usr/bin doesn't hold, after it on PATH, isn't seen; remove this Skip with the fix " +
		"(and the \"after the real one\" case of TestPathShadows, which pins the current behaviour)")
	real := realSudoDir(t)
	after := put(t, 0o755, "doas", "please", "sudo-rs")
	got := paths(PathShadows(list(real, after, "rel")))
	for _, want := range []string{filepath.Join(after, "doas"), filepath.Join(after, "please"), filepath.Join(after, "sudo-rs"), `the relative PATH entry "rel"`} {
		if !slices.Contains(got, want) {
			t.Errorf("PathShadows = %q, want it to include %q", got, want)
		}
	}
}

// TestPathShadowsEmptyPATH: PATH= (set and empty) is one empty entry to
// glibc's execvp, bash and Rust's split_paths, which search the current
// folder then (TestChildPATH's comment says so too, but checks it with a
// bare sysbin.Sudo, which skips the scan). filepath.SplitList("") is no
// entry at all, so PathShadows sees nothing.
func TestPathShadowsEmptyPATH(t *testing.T) {
	t.Skip("KNOWN BUG (review #134, nit): PathShadows(\"\") finds nothing, while execvp, bash and topgrade's which " +
		"search the current folder for an empty PATH; remove this Skip with the fix")
	realSudoDir(t)
	if got := paths(PathShadows(ChildPATH(nil, []string{"PATH="}))); !slices.Equal(got, []string{"an empty PATH entry (the current folder)"}) {
		t.Errorf("PathShadows(PATH=) = %q, want the empty entry", got)
	}
}
