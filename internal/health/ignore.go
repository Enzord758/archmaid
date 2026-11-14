package health

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Enzord758/archmaid/internal/logstore"
)

// Ignoring (#287). A check that is Look names the things it is about as Items,
// each with a stable Key; the user can ignore an item, and an ignored item no
// longer counts. A check whose Look items are all ignored is Ignored; a check
// that finds an item that is not ignored is Look again for that item, the
// others staying ignored. The ignore file is the only thing health writes, and
// only through IgnoreStore, when the user clicks; Run only reads it.

// Item is one thing a Look check is about, for ignoring it by itself.
type Item struct {
	// Key is stable across runs and unique within the report (the contract
	// lists them in docs/design/health.md): "unit:cups.service",
	// "aur-gone:foo", "pacnew:/etc/pacman.conf.pacnew", "restart:7.2.12-1".
	Key string
	// Label is what the GUI shows for it ("cups.service").
	Label string
}

// IgnoredItem is an ignored item of a report, for the GUI's Ignored list.
type IgnoredItem struct {
	CheckID string
	Title   string // the check's title
	Item    Item
	// Since is when it was ignored.
	Since time.Time
}

// Limits of the ignore file and of a key.
const (
	// IgnoreFile is the file's name in the state dir.
	IgnoreFile = "health-ignore.json"
	// KeyMax is the longest key, in bytes.
	KeyMax = 300
	// MaxIgnored is how many keys the file holds at most.
	MaxIgnored = 500
	// maxIgnoreFile caps the file as read.
	maxIgnoreFile = 256 << 10
)

// ErrBadKey: the key is empty, too long, not valid text or holds a control
// character.
var ErrBadKey = errors.New("not a valid ignore key")

// ErrIgnoreFull: the file already holds MaxIgnored keys.
var ErrIgnoreFull = errors.New("too many ignored items")

// ValidKey reports whether key may be stored.
func ValidKey(key string) bool {
	if key == "" || len(key) > KeyMax || !utf8.ValidString(key) {
		return false
	}
	return !strings.ContainsFunc(key, func(r rune) bool { return unicode.IsControl(r) || r == ' ' || r == ' ' })
}

// IgnoreStore is the ignore file: <state dir>/health-ignore.json,
// {"v":1,"ignored":{"<key>":"<RFC3339 time>"}}. It is written atomically (a
// 0600 temp file in the 0700 state dir, fsynced, renamed over the file) and
// read only if it is a regular file we own that nobody else can write, of a
// sane size: a symlink, someone else's file, a huge or garbled one reads as
// empty, and is never followed.
type IgnoreStore struct {
	// Dir is the state dir ("" is logstore's default).
	Dir string
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu sync.Mutex
}

// DefaultIgnoreStore is the real store, in archmaid's state dir.
func DefaultIgnoreStore() *IgnoreStore { return &IgnoreStore{} }

func (s *IgnoreStore) dir() string {
	if s.Dir != "" {
		return s.Dir
	}
	return logstore.DefaultDir()
}

func (s *IgnoreStore) path() string {
	d := s.dir()
	if d == "" || !filepath.IsAbs(d) {
		return ""
	}
	return filepath.Join(d, IgnoreFile)
}

type ignoreDoc struct {
	V       int               `json:"v"`
	Ignored map[string]string `json:"ignored"`
}

// Load returns the ignored keys and when each was ignored. Whatever is wrong
// with the file (absent, a symlink, not ours, writable by others, huge, not
// JSON, an unknown version) it is an empty set, never an error: ignoring is
// a convenience and must never break the check.
func (s *IgnoreStore) Load() map[string]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *IgnoreStore) load() map[string]time.Time {
	out := map[string]time.Time{}
	p := s.path()
	if p == "" {
		return out
	}
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxIgnoreFile || fi.Mode().Perm()&0o022 != 0 {
		return out
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return out
	}
	f, err := os.Open(p)
	if err != nil {
		return out
	}
	defer f.Close()
	// The file we opened must be the one we examined (no swap in between).
	if fi2, err := f.Stat(); err != nil || !os.SameFile(fi, fi2) {
		return out
	}
	b, err := readCapped(f, maxIgnoreFile)
	if err != nil {
		return out
	}
	var doc ignoreDoc
	if json.Unmarshal(b, &doc) != nil || doc.V != 1 {
		return out
	}
	for k, v := range doc.Ignored {
		if len(out) >= MaxIgnored {
			break
		}
		if !ValidKey(k) {
			continue
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			t = time.Time{}
		}
		out[k] = t
	}
	return out
}

// Ignore adds key (its time is now). Ignoring a key already ignored keeps its
// time. A file that can't be trusted is replaced by a new one holding the key.
func (s *IgnoreStore) Ignore(key string) error {
	if !ValidKey(key) {
		return ErrBadKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.load()
	if _, ok := cur[key]; ok {
		return nil
	}
	if len(cur) >= MaxIgnored {
		return ErrIgnoreFull
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	cur[key] = now()
	return s.write(cur)
}

// Unignore removes key (a key that is not there is not an error).
func (s *IgnoreStore) Unignore(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.load()
	if _, ok := cur[key]; !ok {
		return nil
	}
	delete(cur, key)
	return s.write(cur)
}

// write replaces the file with m atomically.
func (s *IgnoreStore) write(m map[string]time.Time) error {
	p := s.path()
	if p == "" {
		return errors.New("no state directory")
	}
	dir := filepath.Dir(p)
	// The state dir is ours: made 0700 if missing, never a symlink to follow.
	if fi, err := os.Lstat(dir); err == nil && !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	doc := ignoreDoc{V: 1, Ignored: map[string]string{}}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		doc.Ignored[k] = m[k].UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(dir, IgnoreFile+".tmp*") // created 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op after a successful rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// rename replaces the name itself: a symlink at p is replaced, not followed.
	return os.Rename(tmp.Name(), p)
}

// applyIgnores turns a Look check's items into active and ignored ones. All
// ignored: the check is Ignored (its Detail kept, it counts nowhere). Some:
// still Look, Items the active ones, IgnoredItems the others, and the Detail
// says how many are ignored. None: unchanged. Only a Look check changes.
func applyIgnores(c Check, ign map[string]time.Time) Check {
	if c.State != Look {
		return c
	}
	if len(c.Items) == 0 {
		c.Items = []Item{{Key: c.ID, Label: c.Title}}
	}
	var active, hidden []Item
	for _, it := range c.Items {
		if _, ok := ign[it.Key]; ok {
			hidden = append(hidden, it)
		} else {
			active = append(active, it)
		}
	}
	switch {
	case len(hidden) == 0:
	case len(active) == 0:
		c.State, c.Items, c.IgnoredItems = Ignored, nil, hidden
	default:
		c.Items, c.IgnoredItems = active, hidden
		c.Detail = clipDetail(c.Detail + fmt.Sprintf(" · %d ignored", len(hidden)))
	}
	return c
}

// itemKey is prefix+value as a key that is valid: control characters in the
// value (a hostile name) are dropped and it is cut to KeyMax bytes, on a rune
// boundary.
func itemKey(prefix, value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' || r == utf8.RuneError {
			return -1
		}
		return r
	}, value)
	k := prefix + value
	for len(k) > KeyMax {
		_, n := utf8.DecodeLastRuneInString(k)
		k = k[:len(k)-n]
	}
	return k
}

func clipDetail(s string) string {
	if r := []rune(s); len(r) > DetailMax {
		return string(r[:DetailMax-1]) + "…"
	}
	return s
}

// Ignored lists the ignored items of the report, in the report's order, with
// when each was ignored (from the store the report was made with).
func (r Report) Ignored() []IgnoredItem { return r.ignored }
