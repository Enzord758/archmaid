package privilege

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOwnFilesIs: ownFiles.is takes a file only as recorded: the same
// inode, a regular file of mode exactly 0700 (not 0600/0500/0100/0710,
// which "no group/other bits" would let through), and byte-for-byte the
// same contents (not a longer, shorter or other file with the same start).
func TestOwnFilesIs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sudo")
	body := []byte("#!/bin/sh\nexec /usr/bin/sudo -A \"$@\"\n")
	if err := os.WriteFile(path, body, 0o700); err != nil {
		t.Fatal(err)
	}
	var reg ownFiles
	lstat := func() os.FileInfo {
		fi, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		return fi
	}
	reg.add(path, lstat(), body)
	if !reg.is(path, lstat()) {
		t.Fatal("the file as written isn't taken for its own")
	}
	if !reg.is(filepath.Join(dir, ".", "sub", "..", "sudo"), lstat()) {
		t.Fatal("the same path spelled differently isn't taken for its own")
	}
	for _, mode := range []os.FileMode{0o600, 0o500, 0o100, 0o710, 0o701, 0o755} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if reg.is(path, lstat()) {
			t.Errorf("mode %v taken for the wrapper as written", mode)
		}
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{
		"longer":  append(append([]byte{}, body...), '#'),
		"shorter": body[:len(body)-1],
		"prefix":  append([]byte("#"), body...),
		"other":   append(append([]byte{}, body[:len(body)-2]...), 'X', '\n'),
	} {
		if err := os.WriteFile(path, content, 0o700); err != nil {
			t.Fatal(err)
		}
		if reg.is(path, lstat()) {
			t.Errorf("%s contents taken for the wrapper as written", name)
		}
	}
	reg.remove(path)
	if err := os.WriteFile(path, body, 0o700); err != nil {
		t.Fatal(err)
	}
	if reg.is(path, lstat()) {
		t.Error("a removed entry is still taken for its own")
	}
}

// TestOwnFilesIsInodeReuse: a file that has the recorded device, inode,
// mode and contents but a changed ctime is not the wrapper as written. This
// is what a removed and rewritten wrapper looks like on a filesystem that
// hands the new file the old inode number (CI's /tmp does, tmpfs does not).
// The reuse can't be forced, so the recorded ctime is moved instead: the
// file is, by device, inode, mode and text, exactly the one recorded.
func TestOwnFilesIsInodeReuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sudo")
	body := []byte("#!/bin/sh\nexec /usr/bin/sudo -A \"$@\"\n")
	if err := os.WriteFile(path, body, 0o700); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	var reg ownFiles
	reg.add(path, fi, body)
	if !reg.is(path, fi) {
		t.Fatal("the file as written isn't taken for its own")
	}
	reg.mu.Lock()
	e := reg.m[path]
	if e.ctime == 0 {
		reg.mu.Unlock()
		t.Fatal("no ctime recorded")
	}
	e.ctime++
	reg.m[path] = e
	reg.mu.Unlock()
	if reg.is(path, fi) {
		t.Error("same inode, mode and text but another ctime taken for the wrapper as written")
	}
}
