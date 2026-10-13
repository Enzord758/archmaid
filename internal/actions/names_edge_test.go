package actions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realPackageNames are names Arch repositories and the AUR really use.
var realPackageNames = []string{
	"lib32-gcc-libs", "python-pyqt5", "gtk+", "xorg-server-xvfb", "r", "0ad",
	"ttf-font-awesome@5", "qt6.base", "libc++", "c++-wrapper", "gtk2+", "perl-xml-parser",
	"python-zope.interface", "7zip", "2048-qt", "libx11", "ttf-ms-fonts_x", "gcc-libs-multilib",
	"a", strings.Repeat("a", 128),
}

// TestRealPackageNamesPass: the pattern keeps every name a real package uses.
func TestRealPackageNamesPass(t *testing.T) {
	for _, n := range realPackageNames {
		if !PackageNameOK(n) {
			t.Errorf("real package name %q refused", n)
		}
	}
	if err := checkPackageNames("x", realPackageNames); err != nil {
		t.Errorf("a list of real names refused: %v", err)
	}
}

// TestBadNameStopsTheWholeList: one bad name anywhere in the list (first,
// middle, last) fails the whole list, and orphanPackages returns no names at
// all, not the good ones around it.
func TestBadNameStopsTheWholeList(t *testing.T) {
	for _, bad := range []string{"-x", "a b", "a\x1b[0m", ".hidden", "a;b", "é"} {
		for pos, list := range map[string][]string{
			"first":  {bad, "liba", "libb"},
			"middle": {"liba", bad, "libb"},
			"last":   {"liba", "libb", bad},
		} {
			if err := checkPackageNames("t", list); err == nil {
				t.Errorf("%q %s: list accepted", bad, pos)
			}
		}
		shimPacmanLog(t, "printf 'liba\\n'; printf '%s\\n' '"+bad+"'; printf 'libb\\n'\n")
		names, err := orphanPackages(context.Background())
		if err == nil || names != nil {
			t.Errorf("%q in the middle: orphanPackages = %q, %v; want no names and an error", bad, names, err)
		}
		if err != nil && strings.ContainsRune(err.Error(), '\x1b') {
			t.Errorf("the refusal carries a raw escape: %q", err)
		}
	}
}

// TestSudoQkqArgvIsColourless: the root recheck's argv, the one pacman call
// run through sudo, asks for --color never, and keeps -n and --.
func TestSudoQkqArgvIsColourless(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte("#!/bin/sh\necho \"$*\" >> "+log+"\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, _, err := brokenPackages(context.Background(), []string{"liba", "libb"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	if got, want := strings.TrimSpace(string(b)), "-n pacman -Qkq --color never -- liba libb"; got != want {
		t.Fatalf("sudo argv %q, want %q", got, want)
	}
}
