package proc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeProcDir writes a /proc/<pid> stand-in: a stat for a process named sudo
// and a status whose Uid line is uidLine.
func fakeProcDir(t *testing.T, uidLine string) string {
	t.Helper()
	dir := t.TempDir()
	// pid (comm) state ppid pgrp session … with starttime as field 22.
	fields := []string{"S", "1", "2", "3"}
	for i := 7; i <= 21; i++ {
		fields = append(fields, "0")
	}
	fields = append(fields, "12345")
	stat := "4242 (sudo) " + strings.Join(fields, " ") + "\n"
	status := "Name:\tsudo\n" + uidLine + "\nGid:\t1000\t1000\t1000\t1000\n"
	for name, body := range map[string]string{"stat": stat, "status": status} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestReadEUIDUnparsableIsUnknown: an effective uid that doesn't parse is
// -1 (unknown), never 0. Zero is root, and "euid 0 named sudo" is what the
// askpass check accepts as sudo when sudo's exe link is unreadable.
func TestReadEUIDUnparsableIsUnknown(t *testing.T) {
	for uidLine, want := range map[string]int{
		"Uid:\t1000\t1000\t1000\t1000":                 1000,
		"Uid:\t1000\t0\t0\t0":                          0,
		"Uid:\t1000\tx\t1000\t1000":                    -1,
		"Uid:\t1000\t-5\t1000\t1000":                   -1,
		"Uid:\t1000\t99999999999999999999\t1000\t1000": -1,
		"Uid:\t1000":                                   -1,
	} {
		info, err := readAt(fakeProcDir(t, uidLine), 4242)
		if err != nil {
			t.Fatal(err)
		}
		if info.EUID != want || info.Comm != "sudo" || info.Start != 12345 {
			t.Errorf("%q: EUID = %d (comm %q, start %d), want %d", uidLine, info.EUID, info.Comm, info.Start, want)
		}
	}
}
