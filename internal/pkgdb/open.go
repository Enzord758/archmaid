package pkgdb

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/klauspost/compress/zstd"
)

const maxArchiveBytes = 1 << 30 // decompressed size of one sync DB

// Limits on entry counts; variables only so a test can lower them.
var (
	maxEntries = 500000 // tar entries per sync DB
	maxLocal   = 100000 // installed packages
)

const maxConfBytes = 1 << 20

// Paths says where the databases are. Production: Sync
// "/var/lib/pacman/sync", Local "/var/lib/pacman/local", Conf
// "/etc/pacman.conf". An empty Conf reads every sync/*.db, sorted by name.
type Paths struct{ Sync, Local, Conf string }

// DB is a loaded, read-only snapshot of the databases. Methods may be called
// from several goroutines, also while Reload runs; each call sees one
// consistent snapshot.
type DB struct {
	paths Paths
	mu    sync.Mutex // serialises Reload
	cur   atomic.Pointer[snapshot]
}

// entry is a sync package plus what Search needs, lower-cased once at load.
type entry struct {
	pkg      Package
	lname    string
	ldesc    string
	lprov    []string
	repoRank int
}

type snapshot struct {
	repos    []string
	sync     []*entry
	byName   map[string]*entry
	local    map[string]*Package
	syncTime time.Time
	warnings []string
	stamp    string
}

// Open parses the sync databases in pacman.conf's [section] order and the
// local one. A repo whose database is missing or unreadable is skipped and
// named in Warnings; Open fails only when nothing at all could be read. It
// takes 100-300 ms for a full system: call it off the UI thread.
func Open(p Paths) (*DB, error) {
	s, err := load(p)
	if err != nil {
		return nil, err
	}
	db := &DB{paths: p}
	db.cur.Store(s)
	return db, nil
}

// Reload re-reads everything when a sync file, the local directory or
// pacman.conf changed since the last load (their size and modification time
// are compared; nothing is parsed otherwise). On an error the old data stays.
func (db *DB) Reload() (changed bool, err error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if stampOf(db.paths) == db.cur.Load().stamp {
		return false, nil
	}
	s, err := load(db.paths)
	if err != nil {
		return false, err
	}
	db.cur.Store(s)
	return true, nil
}

// Warnings lists what was skipped (a repo without a database, a broken
// archive, a bad local entry), for a quiet note.
func (db *DB) Warnings() []string { return db.cur.Load().warnings }

// Repos is the sync repos that loaded, in pacman.conf order.
func (db *DB) Repos() []string { return db.cur.Load().repos }

// SyncTime is the modification time of the oldest sync DB (the conservative
// answer for "package lists from ..."); zero when there is none.
func (db *DB) SyncTime() time.Time { return db.cur.Load().syncTime }

// SyncAge is how old the package lists are at now; zero when unknown.
func (db *DB) SyncAge(now time.Time) time.Duration {
	t := db.SyncTime()
	if t.IsZero() || now.Before(t) {
		return 0
	}
	return now.Sub(t)
}

// repoNames returns the repo sections of pacman.conf, in order. Include
// files are not followed.
func repoNames(conf string) ([]string, error) {
	fi, err := os.Stat(conf)
	if err != nil {
		return nil, err
	}
	if fi.Size() > maxConfBytes {
		return nil, errors.New("pacman.conf too large")
	}
	data, err := os.ReadFile(conf)
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if len(line) < 3 || line[0] != '[' || line[len(line)-1] != ']' {
			continue
		}
		name := strings.TrimSpace(line[1 : len(line)-1])
		if name == "options" || !ValidName(name) || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

// stampOf is a cheap fingerprint of every input: name, size and mtime of the
// sync files and pacman.conf, and the local directory's mtime.
func stampOf(p Paths) string {
	var b strings.Builder
	files, _ := filepath.Glob(filepath.Join(p.Sync, "*.db"))
	sort.Strings(files)
	if p.Conf != "" {
		files = append(files, p.Conf)
	}
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil {
			fmt.Fprintf(&b, "%s|%d|%d\n", f, fi.Size(), fi.ModTime().UnixNano())
		}
	}
	if fi, err := os.Stat(p.Local); err == nil {
		fmt.Fprintf(&b, "local|%d\n", fi.ModTime().UnixNano())
	}
	return b.String()
}

func load(p Paths) (*snapshot, error) {
	s := &snapshot{byName: map[string]*entry{}, local: map[string]*Package{}, stamp: stampOf(p)}
	var order []string
	if p.Conf != "" {
		var err error
		if order, err = repoNames(p.Conf); err != nil {
			s.warnings = append(s.warnings, "pacman.conf: "+err.Error())
		}
	}
	if order == nil {
		files, _ := filepath.Glob(filepath.Join(p.Sync, "*.db"))
		sort.Strings(files)
		for _, f := range files {
			if n := strings.TrimSuffix(filepath.Base(f), ".db"); ValidName(n) {
				order = append(order, n)
			}
		}
	}
	for rank, repo := range order {
		file := filepath.Join(p.Sync, repo+".db")
		if fi, err := os.Stat(file); err == nil && (s.syncTime.IsZero() || fi.ModTime().Before(s.syncTime)) {
			s.syncTime = fi.ModTime()
		}
		pkgs, err := readSync(file, repo)
		if err != nil {
			s.warnings = append(s.warnings, fmt.Sprintf("sync/%s.db: %v", repo, err))
			continue
		}
		s.repos = append(s.repos, repo)
		for _, pk := range pkgs {
			if first, dup := s.byName[pk.Name]; dup {
				first.pkg.AlsoIn = append(first.pkg.AlsoIn, repo)
				continue
			}
			e := &entry{pkg: *pk, lname: strings.ToLower(pk.Name), ldesc: strings.ToLower(pk.Description), repoRank: rank}
			for _, pr := range pk.Provides {
				e.lprov = append(e.lprov, strings.ToLower(pr.Name))
			}
			s.byName[pk.Name] = e
			s.sync = append(s.sync, e)
		}
	}
	if err := s.readLocal(p.Local); err != nil {
		s.warnings = append(s.warnings, "local: "+err.Error())
		if len(s.sync) == 0 {
			return nil, errors.New("no readable package database: " + strings.Join(s.warnings, "; "))
		}
	}
	s.index()
	return s, nil
}

// decompress picks the decoder by magic bytes: gzip (1f 8b), zstd
// (28 b5 2f fd) or an uncompressed tar.
func decompress(f io.Reader) (io.Reader, func(), error) {
	br := bufio.NewReaderSize(f, 64<<10)
	magic, _ := br.Peek(4)
	switch {
	case bytes.HasPrefix(magic, []byte{0x1f, 0x8b}):
		zr, err := gzip.NewReader(br)
		if err != nil {
			return nil, nil, err
		}
		return zr, func() { zr.Close() }, nil
	case bytes.HasPrefix(magic, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		zr, err := zstd.NewReader(br, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(256<<20))
		if err != nil {
			return nil, nil, err
		}
		return zr, zr.Close, nil
	}
	return br, func() {}, nil // plain tar (or garbage: tar reports it)
}

func readSync(file, repo string) ([]*Package, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r, closeFn, err := decompress(f)
	if err != nil {
		return nil, err
	}
	defer closeFn()
	tr := tar.NewReader(io.LimitReader(r, maxArchiveBytes))
	var out []*Package
	for n := 0; ; n++ {
		if n >= maxEntries {
			return nil, errors.New("too many entries")
		}
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg || path.Base(h.Name) != "desc" || h.Size > maxEntryBytes {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		p, ok := parseDesc(data)
		if !ok {
			continue
		}
		p.Repo = repo
		out = append(out, &p)
	}
	if len(out) == 0 {
		return nil, errors.New("no packages in database")
	}
	return out, nil
}

func (s *snapshot) readLocal(dir string) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if len(s.local) >= maxLocal {
			return errors.New("too many installed packages")
		}
		data, err := readSmall(filepath.Join(dir, e.Name(), "desc"))
		if err != nil {
			s.warnings = append(s.warnings, fmt.Sprintf("local/%.60q: %v", sanitize.Field(e.Name(), 60), err))
			continue
		}
		p, ok := parseDesc(data)
		if !ok {
			s.warnings = append(s.warnings, fmt.Sprintf("local/%.60q: invalid entry", sanitize.Field(e.Name(), 60)))
			continue
		}
		p.Installed, p.InstalledVersion = true, p.Version
		if p.Reason == ReasonNone { // no %REASON% means explicit
			p.Reason = ReasonExplicit
		}
		s.local[p.Name] = &p
	}
	return nil
}

// readSmall reads a regular file of at most maxEntryBytes; a symlink or a
// device in the database tree is refused.
func readSmall(file string) ([]byte, error) {
	fi, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fs.ErrInvalid
	}
	if fi.Size() > maxEntryBytes {
		return nil, errors.New("entry too large")
	}
	return os.ReadFile(file)
}

// index computes the reverse links, Orphan, Foreign and Update, and merges the
// installed state into the sync entries.
func (s *snapshot) index() {
	type ref struct {
		name     string
		optional bool
	}
	consumer := map[string][]ref{}
	for name, l := range s.local {
		for _, d := range l.Depends {
			consumer[d.Name] = append(consumer[d.Name], ref{name, false})
		}
		for _, d := range l.OptDepends {
			consumer[d.Name] = append(consumer[d.Name], ref{name, true})
		}
	}
	for name, l := range s.local {
		keys := []string{name}
		for _, pr := range l.Provides {
			keys = append(keys, pr.Name)
		}
		seenR, seenO := map[string]bool{}, map[string]bool{}
		for _, k := range keys {
			for _, c := range consumer[k] {
				switch {
				case c.name == name:
				case c.optional && !seenO[c.name]:
					seenO[c.name] = true
					l.OptionalFor = append(l.OptionalFor, c.name)
				case !c.optional && !seenR[c.name]:
					seenR[c.name] = true
					l.RequiredBy = append(l.RequiredBy, c.name)
				}
			}
		}
		sort.Strings(l.RequiredBy)
		sort.Strings(l.OptionalFor)
		l.Orphan = l.Reason == ReasonDep && len(l.RequiredBy) == 0 && len(l.OptionalFor) == 0
		if e, ok := s.byName[name]; ok {
			e.pkg.Installed, e.pkg.InstalledVersion, e.pkg.Reason, e.pkg.InstallDate = true, l.Version, l.Reason, l.InstallDate
			e.pkg.RequiredBy, e.pkg.OptionalFor, e.pkg.Orphan = l.RequiredBy, l.OptionalFor, l.Orphan
			e.pkg.Update = Vercmp(e.pkg.Version, l.Version) > 0
			l.Update = e.pkg.Update
		} else {
			l.Foreign = true
		}
	}
}
