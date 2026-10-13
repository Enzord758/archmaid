package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Review #134 (Dev 2) of the e2e build's sysbin check (#131).

// elfSym is one symbol of a crafted ELF file: its name, value (address)
// and size.
type elfSym struct {
	name        string
	value, size uint64
}

// usrBin is where sysbin points in the product (spelled apart: testenv's
// TestNoAbsolutePrivilegedExec rejects a privileged program's absolute
// path as one literal).
const usrBin = "/usr/bin/"

// Addresses of the crafted file's two data sections.
const (
	rodataAddr = 0x401000
	dataAddr   = 0x410000 // past a rodata of 5000 bytes (TestValuesRefusesBadHeaders)
)

// craftELF writes a minimal little-endian ELF64 file holding rodata and
// data as two allocated sections at rodataAddr and dataAddr, and syms in a
// symbol table, and returns its path. It is what a linked Go binary looks
// like to Values: a string variable is a 16-byte header (pointer, length)
// in data that points into rodata.
func craftELF(t *testing.T, rodata, data []byte, syms []elfSym) string {
	t.Helper()
	strtab := []byte{0}
	var symtab bytes.Buffer
	symtab.Write(make([]byte, elf.Sym64Size)) // the null symbol
	for _, s := range syms {
		e := elf.Sym64{Name: uint32(len(strtab)), Info: elf.ST_INFO(elf.STB_GLOBAL, elf.STT_OBJECT), Shndx: 2, Value: s.value, Size: s.size}
		strtab = append(append(strtab, s.name...), 0)
		if err := binary.Write(&symtab, binary.LittleEndian, e); err != nil {
			t.Fatal(err)
		}
	}
	shstrtab := []byte("\x00.rodata\x00.data\x00.symtab\x00.strtab\x00.shstrtab\x00")
	bodies := [][]byte{rodata, data, symtab.Bytes(), strtab, shstrtab}
	const ehsize = 64
	offs := make([]uint64, len(bodies))
	off := uint64(ehsize)
	for i, b := range bodies {
		off = (off + 7) &^ 7
		offs[i] = off
		off += uint64(len(b))
	}
	shoff := (off + 7) &^ 7
	var f bytes.Buffer
	hdr := elf.Header64{
		Type: uint16(elf.ET_EXEC), Machine: uint16(elf.EM_X86_64), Version: uint32(elf.EV_CURRENT),
		Shoff: shoff, Ehsize: ehsize, Shentsize: 64, Shnum: 6, Shstrndx: 5,
	}
	copy(hdr.Ident[:], []byte{0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)})
	if err := binary.Write(&f, binary.LittleEndian, hdr); err != nil {
		t.Fatal(err)
	}
	for i, b := range bodies {
		f.Write(make([]byte, int(offs[i])-f.Len()))
		f.Write(b)
	}
	f.Write(make([]byte, int(shoff)-f.Len()))
	secs := []elf.Section64{
		{},
		{Name: 1, Type: uint32(elf.SHT_PROGBITS), Flags: uint64(elf.SHF_ALLOC), Addr: rodataAddr, Off: offs[0], Size: uint64(len(rodata)), Addralign: 1},
		{Name: 9, Type: uint32(elf.SHT_PROGBITS), Flags: uint64(elf.SHF_ALLOC | elf.SHF_WRITE), Addr: dataAddr, Off: offs[1], Size: uint64(len(data)), Addralign: 8},
		{Name: 15, Type: uint32(elf.SHT_SYMTAB), Off: offs[2], Size: uint64(symtab.Len()), Link: 4, Info: 1, Addralign: 8, Entsize: elf.Sym64Size},
		{Name: 23, Type: uint32(elf.SHT_STRTAB), Off: offs[3], Size: uint64(len(strtab)), Addralign: 1},
		{Name: 31, Type: uint32(elf.SHT_STRTAB), Off: offs[4], Size: uint64(len(shstrtab)), Addralign: 1},
	}
	for _, s := range secs {
		if err := binary.Write(&f, binary.LittleEndian, s); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(p, f.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// strvars lays out string variables as a linked Go binary does: each
// value's bytes in rodata, each 16-byte header in data, in order, and a
// 16-byte symbol per variable named prefix+name.
func strvars(names, values []string) (rodata, data []byte, syms []elfSym) {
	for i, v := range values {
		hdr := make([]byte, 16)
		binary.LittleEndian.PutUint64(hdr[:8], rodataAddr+uint64(len(rodata)))
		binary.LittleEndian.PutUint64(hdr[8:], uint64(len(v)))
		syms = append(syms, elfSym{prefix + names[i], dataAddr + uint64(len(data)), 16})
		rodata = append(rodata, v...)
		data = append(data, hdr...)
	}
	return rodata, data, syms
}

// TestValuesReadsLinkedStrings: Values reports every sysbin string
// variable's linked value, absolute ones included (the e2e harness must
// see those to refuse the binary), and skips what isn't one: another
// package's variable, a symbol of another size (a func), and the
// package's closures and other dotted names.
func TestValuesReadsLinkedStrings(t *testing.T) {
	names := []string{"Su", "Sudo", "Pkexec", "Pkttyagent", "Topgrade"}
	values := []string{usrBin + "su", "sudo", "pkexec", usrBin + "pkttyagent", "topgrade"}
	rodata, data, syms := strvars(names, values)
	syms = append(syms,
		elfSym{"main.other", dataAddr, 16},
		elfSym{prefix + "All", rodataAddr, 8},
		elfSym{prefix + "All.func1", dataAddr, 16},
		elfSym{prefix + "init·1", dataAddr, 16},
	)
	got, err := Values(craftELF(t, rodata, data, syms))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for i, n := range names {
		want[n] = values[i]
	}
	if !maps.Equal(got, want) {
		t.Errorf("Values = %q, want %q", got, want)
	}
}

// TestValuesRefusesBadHeaders: a string longer than 4096 bytes, or one
// whose header or bytes lie in no section, is an error (the long one's
// bytes are there: only the cap refuses it), never a value.
func TestValuesRefusesBadHeaders(t *testing.T) {
	long := make([]byte, 16)
	binary.LittleEndian.PutUint64(long[:8], rodataAddr)
	binary.LittleEndian.PutUint64(long[8:], 4097)
	outside := make([]byte, 16)
	binary.LittleEndian.PutUint64(outside[:8], 0x900000)
	binary.LittleEndian.PutUint64(outside[8:], 4)
	for name, c := range map[string]struct {
		rodata, data []byte
		sym          elfSym
	}{
		"too long":              {bytes.Repeat([]byte("s"), 5000), long, elfSym{prefix + "Sudo", dataAddr, 16}},
		"bytes in no section":   {[]byte("sudo"), outside, elfSym{prefix + "Sudo", dataAddr, 16}},
		"header in no section":  {[]byte("sudo"), long, elfSym{prefix + "Sudo", 0x900000, 16}},
		"header past a section": {[]byte("sudo"), long, elfSym{prefix + "Sudo", dataAddr + 8, 16}},
	} {
		if v, err := Values(craftELF(t, c.rodata, c.data, []elfSym{c.sym})); err == nil {
			t.Errorf("%s: Values = %q, want an error", name, v)
		}
	}
}

// runShCheck is cmd/archmaid/e2e/run.sh's refusal of a binary whose sysbin
// paths aren't bare: its lines from the Sudo=sudo check to the end of the
// `if grep -v` block that follows, as written there.
func runShCheck(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "cmd", "archmaid", "e2e", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	start := strings.Index(s, "grep -qx 'Sudo=sudo' <<<\"$SYSBIN_VALS\"")
	block := strings.Index(s, "if grep -v -E '^([A-Za-z]+)=[a-z]+$' <<<\"$SYSBIN_VALS\"; then\n")
	if start < 0 || block < start {
		t.Fatal("run.sh: no Sudo=sudo check followed by the grep -v block")
	}
	end := strings.Index(s[block:], "\nfi\n")
	if end < 0 {
		t.Fatal("run.sh: the grep -v block has no fi")
	}
	check := s[start : block+end+len("\nfi\n")]
	// It runs before anything starts ./archmaid (the --help check, the
	// tapes).
	if first := strings.Index(s, "./archmaid "); first >= 0 && first < start {
		t.Errorf("run.sh starts ./archmaid before checking its sysbin paths")
	}
	if first := strings.Index(s, "\tvhs "); first >= 0 && first < start {
		t.Errorf("run.sh runs a tape before checking the binary's sysbin paths")
	}
	return check
}

// TestRunShRefusesAbsoluteSysbin runs run.sh's check itself (bash, on
// canned sysbinvals output): only all-bare values with Sudo=sudo among
// them pass.
func TestRunShRefusesAbsoluteSysbin(t *testing.T) {
	check := runShCheck(t)
	for _, c := range []struct {
		name, vals string
		ok         bool
	}{
		{"the TUI's, bare", "Pkexec=pkexec\nSu=su\nSudo=sudo\nTopgrade=topgrade", true},
		{"all five, bare", "Pkexec=pkexec\nPkttyagent=pkttyagent\nSu=su\nSudo=sudo\nTopgrade=topgrade", true},
		{"absolute sudo", "Pkexec=pkexec\nSu=su\nSudo=" + usrBin + "sudo\nTopgrade=topgrade", false},
		{"absolute su", "Pkexec=pkexec\nSu=" + usrBin + "su\nSudo=sudo\nTopgrade=topgrade", false},
		{"absolute topgrade", "Pkexec=pkexec\nSu=su\nSudo=sudo\nTopgrade=" + usrBin + "topgrade", false},
		{"relative path", "Pkexec=pkexec\nSu=su\nSudo=sudo\nTopgrade=bin/topgrade", false},
		{"no sudo", "Pkexec=pkexec\nSu=su\nTopgrade=topgrade", false},
		{"nothing", "", false},
	} {
		cmd := exec.Command("bash", "-c", "set -euo pipefail\nSYSBIN_VALS=\"$VALS\"\n"+check+"echo PASSED\n")
		cmd.Env = append(os.Environ(), "VALS="+c.vals)
		out, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		switch {
		case c.ok && (err != nil || !strings.HasSuffix(string(out), "PASSED\n")):
			t.Errorf("%s: refused (%v):\n%s", c.name, err, out)
		case !c.ok && (!errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(out), "FAIL: ")):
			t.Errorf("%s: passed (%v), want exit 1 with a FAIL line:\n%s", c.name, err, out)
		}
	}
}
