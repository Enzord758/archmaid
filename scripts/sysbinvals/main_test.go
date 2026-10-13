package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Enzord758/archmaid/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Run(m)) }

// TestValuesRefusals: a file that isn't ELF, or one without the package.
func TestValuesRefusals(t *testing.T) {
	p := filepath.Join(t.TempDir(), "not-elf")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Values(p); err == nil {
		t.Error("a non-ELF file has values")
	}
	if _, err := Values(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing file has values")
	}
	// go test strips a test binary's symbol table: no values to read.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Values(exe); err == nil {
		t.Error("a binary without symbols has values")
	}
}
