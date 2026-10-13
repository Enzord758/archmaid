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
// folder then; filepath.SplitList("") is no entry at all, and PathShadows
// saw nothing before #136. (TestChildPATH checks the same through
// ChildPATH.)
func TestPathShadowsEmptyPATH(t *testing.T) {
	realSudoDir(t)
	if got := paths(PathShadows("")); !slices.Equal(got, []string{"an empty PATH entry (the current folder)"}) {
		t.Errorf("PathShadows(\"\") = %q, want the empty entry", got)
	}
}

// TestPathShadowsPerNameBoundary (#136): each name stops at the system's
// own file of that name. The system dir holds sudo, su and run0 (a symlink
// to systemd-run, as on Arch). Reached by another name (as /bin is
// /usr/bin), each of them is the system's own, not a shadow; an su before
// it counts and one after it doesn't; a user's own run0 link to the same
// systemd-run counts before it; a doas, which the system lacks, counts
// after it.
func TestPathShadowsPerNameBoundary(t *testing.T) {
	sys := realSudoDir(t)
	if err := os.WriteFile(filepath.Join(sys, "su"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sys, "systemd-run"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("systemd-run", filepath.Join(sys, "run0")); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "bin")
	if err := os.Symlink(sys, alias); err != nil {
		t.Fatal(err)
	}
	ownRun0 := t.TempDir()
	if err := os.Symlink(filepath.Join(sys, "systemd-run"), filepath.Join(ownRun0, "run0")); err != nil {
		t.Fatal(err)
	}
	before, after := put(t, 0o755, "su"), put(t, 0o755, "su", "doas")
	got := paths(PathShadows(list(before, ownRun0, alias, sys, after)))
	want := []string{filepath.Join(before, "su"), filepath.Join(ownRun0, "run0"), filepath.Join(after, "doas")}
	if !slices.Equal(got, want) {
		t.Errorf("PathShadows = %q, want %q", got, want)
	}
}

// TestPathShadowsSymlinkedSudo (review #139): with /usr/bin/sudo itself a
// symlink, its directory is still sudo's boundary (compared as a link, not
// followed): a sudo before it counts, one after it doesn't, and the scan
// doesn't turn off.
func TestPathShadowsSymlinkedSudo(t *testing.T) {
	sys := realSudoDir(t)
	if err := os.Rename(filepath.Join(sys, "sudo"), filepath.Join(sys, "sudo.real")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sudo.real", filepath.Join(sys, "sudo")); err != nil {
		t.Fatal(err)
	}
	before, after := put(t, 0o755, "sudo"), put(t, 0o755, "sudo")
	got := paths(PathShadows(list(before, sys, after)))
	if !slices.Equal(got, []string{filepath.Join(before, "sudo")}) {
		t.Errorf("PathShadows = %q, want only the sudo before", got)
	}
}

// TestPathShadowsAllBoundariesPassed (review #139): once every name has
// met the system's own file, the scan is over: a relative or empty entry
// after that, and a planted sudo, su or doas after it, are no shadows. A
// relative entry between the first boundary and the last still is one (a
// name whose boundary is ahead is found through it).
func TestPathShadowsAllBoundariesPassed(t *testing.T) {
	sys := realSudoDir(t)
	for _, n := range ShadowNames {
		if n == "sudo" {
			continue
		}
		if err := os.WriteFile(filepath.Join(sys, n), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	planted := put(t, 0o755, ShadowNames...)
	if got := paths(PathShadows(list(sys, "rel", "", planted))); got != nil {
		t.Errorf("after every boundary: PathShadows = %q, want none", got)
	}
	// A dir that holds a hard link to the system's own sudo only: sudo's
	// boundary is passed there, the others' are still ahead.
	hl := t.TempDir()
	if err := os.Link(filepath.Join(sys, "sudo"), filepath.Join(hl, "sudo")); err != nil {
		t.Fatal(err)
	}
	if got := paths(PathShadows(list(hl, "rel", sys, "rel2", planted))); !slices.Equal(got, []string{`the relative PATH entry "rel"`}) {
		t.Errorf("between boundaries: PathShadows = %q, want only rel", got)
	}
}
