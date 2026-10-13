// Command sysbinvals prints the internal/sysbin paths a built Go binary
// holds, one NAME=VALUE per line, read from its ELF file: the string each
// variable is initialised to, as linked (a -ldflags -X value included). It
// only reads the file; it never runs it.
//
// The e2e harness (cmd/archmaid/e2e/run.sh) uses it to refuse a binary
// whose su, sudo, pkexec, pkttyagent or topgrade is not the bare name its
// shims on PATH catch: such a binary would run the real /usr/bin program.
// (-trimpath leaves -ldflags out of the build info, so `go version -m`
// can't tell.)
//
// Usage (from the repo root):
//
//	go run ./scripts/sysbinvals BINARY
package main

import (
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// prefix is the sysbin variables' symbol prefix.
const prefix = "github.com/Enzord758/archmaid/internal/sysbin."

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: sysbinvals BINARY")
		os.Exit(2)
	}
	vals, err := Values(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "sysbinvals:", err)
		os.Exit(1)
	}
	names := make([]string, 0, len(vals))
	for n := range vals {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Printf("%s=%s\n", n, vals[n])
	}
}

// Values reads every sysbin string variable's linked value from the ELF
// file at path: its symbol's string header (pointer, length), then the
// bytes it points at.
func Values(path string) (map[string]string, error) {
	f, err := elf.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	syms, err := f.Symbols()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, s := range syms {
		name, ok := strings.CutPrefix(s.Name, prefix)
		if !ok || s.Size != 16 || strings.ContainsAny(name, ".·") {
			continue // not a string variable of the package
		}
		hdr, err := read(f, s.Value, 16)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.Name, err)
		}
		ptr, n := binary.LittleEndian.Uint64(hdr[:8]), binary.LittleEndian.Uint64(hdr[8:])
		if n > 4096 {
			return nil, fmt.Errorf("%s: length %d", s.Name, n)
		}
		b, err := read(f, ptr, n)
		if err != nil {
			return nil, fmt.Errorf("%s's string: %w", s.Name, err)
		}
		out[name] = string(b)
	}
	if len(out) == 0 {
		return nil, errors.New("no internal/sysbin variables in " + path)
	}
	return out, nil
}

// read returns n bytes at virtual address addr from the section holding it.
func read(f *elf.File, addr, n uint64) ([]byte, error) {
	for _, sec := range f.Sections {
		if sec.Type == elf.SHT_NOBITS || addr < sec.Addr || addr+n > sec.Addr+sec.Size {
			continue
		}
		b := make([]byte, n)
		if _, err := sec.ReadAt(b, int64(addr-sec.Addr)); err != nil {
			return nil, err
		}
		return b, nil
	}
	return nil, fmt.Errorf("address %#x+%d is in no section with data", addr, n)
}
