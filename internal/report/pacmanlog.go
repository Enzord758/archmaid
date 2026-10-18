package report

import (
	"bufio"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"syscall"

	"github.com/Enzord758/archmaid/internal/sanitize"
)

// DefaultPacmanLog is pacman's log.
const DefaultPacmanLog = "/var/log/pacman.log"

// maxLogRead bounds what one report reads of the log: a run that wrote more
// than this is not a package change anyone can list.
const maxLogRead = 64 << 20

// LogMark is where pacman.log ended when the queue started: its size, and
// which file it was (device and inode), so a rotation is told from a log
// that merely grew. Offsets, not timestamps: a clock change or a run past
// midnight can't move what counts.
type LogMark struct {
	Path     string
	Size     int64
	Dev, Ino uint64
	// Found: the log existed at the mark. Without it the whole file counts.
	Found bool
}

// MarkLog records the end of the log at path now.
func MarkLog(path string) LogMark {
	m := LogMark{Path: path}
	fi, err := os.Stat(path)
	if err != nil {
		return m
	}
	m.Size, m.Found = fi.Size(), true
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		m.Dev, m.Ino = uint64(st.Dev), uint64(st.Ino)
	}
	return m
}

// LogChanges are the net changes the log lines since a mark add up to.
type LogChanges struct {
	Upgraded, Installed, Removed, Downgraded []Pkg
	Pacnew, Pacsave                          []string // the files, sorted by first appearance
	// Partial: the log was rotated or truncated since the mark. Lines from
	// before that are in a file this can't see, so the changes are the
	// part written since.
	Partial bool
}

// ReadLog reads the log lines written since m. A log that is smaller than at
// the mark was truncated, one with another inode was rotated: both are read
// from their start and marked Partial. A log that can't be read is an error.
func ReadLog(m LogMark) (LogChanges, error) {
	f, err := os.Open(m.Path)
	if err != nil {
		return LogChanges{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return LogChanges{}, err
	}
	var out LogChanges
	start := int64(0)
	if m.Found {
		same := true
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			same = uint64(st.Dev) == m.Dev && uint64(st.Ino) == m.Ino
		}
		switch {
		case !same || fi.Size() < m.Size:
			out.Partial = true
		default:
			start = m.Size
		}
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return out, err
	}
	ch, err := ParseLog(io.LimitReader(f, maxLogRead))
	ch.Partial = out.Partial
	return ch, err
}

var (
	alpmLine = regexp.MustCompile(`^\[[^\]]*\] \[ALPM\] (upgraded|downgraded|installed|removed|reinstalled) (\S+) \((.*)\)$`)
	// pacman warns "<file> installed as <file>.pacnew" and "<file> saved as
	// <file>.pacsave" when a package's config file was changed by the user.
	alpmWarn = regexp.MustCompile(`^\[[^\]]*\] \[ALPM\] warning: (\S.*) (?:installed|saved) as (\S.*\.(pacnew|pacsave))$`)
	pkgName  = regexp.MustCompile(`^[A-Za-z0-9@._+-]+$`)
)

// netState is one package's history over the run: whether it was installed
// before the first event touched it and after the last, with the versions.
type netState struct {
	name          string
	before, after bool
	oldV, newV    string
}

// ParseLog adds up the ALPM lines of r. Each package is folded to its net
// change: installed then removed (a build dependency) cancels out, upgraded
// twice is one upgrade from the first old to the last new version, removed
// then installed again is an upgrade or nothing. Lines it doesn't know are
// skipped, so are names pacman would never write.
func ParseLog(r io.Reader) (LogChanges, error) {
	var out LogChanges
	states := map[string]*netState{}
	var order []string
	seenFile := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	touch := func(name string) *netState {
		s := states[name]
		if s == nil {
			s = &netState{name: name}
			states[name] = s
			order = append(order, name)
			return s
		}
		return s
	}
	for sc.Scan() {
		line := sc.Text()
		if m := alpmLine.FindStringSubmatch(line); m != nil {
			verb, name, ver := m[1], m[2], m[3]
			if !pkgName.MatchString(name) {
				continue
			}
			first := states[name] == nil
			s := touch(name)
			oldV, newV, _ := strings.Cut(ver, " -> ")
			if !strings.Contains(ver, " -> ") {
				oldV, newV = ver, ver
			}
			switch verb {
			case "upgraded", "downgraded":
				if first {
					s.before, s.oldV = true, oldV
				}
				s.after, s.newV = true, newV
			case "installed":
				if first {
					s.before = false
				}
				s.after, s.newV = true, ver
			case "removed":
				if first {
					s.before, s.oldV = true, ver
				}
				s.after, s.newV = false, ""
			case "reinstalled":
				if first {
					s.before, s.oldV = true, ver
				}
				s.after, s.newV = true, ver
			}
			continue
		}
		if m := alpmWarn.FindStringSubmatch(line); m != nil {
			file := sanitize.Field(m[2], 300)
			if !seenFile[file] {
				seenFile[file] = true
				if m[3] == "pacnew" {
					out.Pacnew = append(out.Pacnew, file)
				} else {
					out.Pacsave = append(out.Pacsave, file)
				}
			}
		}
	}
	err := sc.Err()
	if errors.Is(err, bufio.ErrTooLong) {
		err = nil // a line no pacman wrote: what was read stands
	}
	for _, name := range order {
		s := states[name]
		p := Pkg{Name: sanitize.Field(s.name, 80), Old: sanitize.Field(s.oldV, 60), New: sanitize.Field(s.newV, 60)}
		switch {
		case s.before && s.after && s.oldV != s.newV:
			if versionLess(s.newV, s.oldV) {
				out.Downgraded = append(out.Downgraded, p)
			} else {
				out.Upgraded = append(out.Upgraded, p)
			}
		case !s.before && s.after:
			p.Old = ""
			out.Installed = append(out.Installed, p)
		case s.before && !s.after:
			p.New = ""
			p.Old = sanitize.Field(s.oldV, 60)
			out.Removed = append(out.Removed, p)
		}
	}
	return out, err
}

// versionLess reports whether a sorts before b by pacman's rule, roughly:
// the numeric and alphabetic runs compared in turn (vercmp, without epochs
// or releases special-cased). Only used to tell a downgrade from an
// upgrade; pacman's own verb ("downgraded") is not kept, as folding two
// events makes it unreliable.
func versionLess(a, b string) bool {
	as, bs := splitRuns(a), splitRuns(b)
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, y := as[i], bs[i]
		if x == y {
			continue
		}
		xd, yd := isDigits(x), isDigits(y)
		switch {
		case xd && yd:
			x, y = strings.TrimLeft(x, "0"), strings.TrimLeft(y, "0")
			if len(x) != len(y) {
				return len(x) < len(y)
			}
			return x < y
		case xd:
			return false // a number is newer than a letter run
		case yd:
			return true
		default:
			return x < y
		}
	}
	return len(as) < len(bs)
}

func isDigits(s string) bool { return s != "" && s[0] >= '0' && s[0] <= '9' }

// splitRuns cuts s into runs of digits and runs of letters; anything else
// separates.
func splitRuns(s string) []string {
	var runs []string
	start, kind := -1, 0 // kind 1 digit, 2 letter
	for i := 0; i <= len(s); i++ {
		k := 0
		if i < len(s) {
			switch c := s[i]; {
			case c >= '0' && c <= '9':
				k = 1
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
				k = 2
			}
		}
		if k != kind {
			if kind != 0 {
				runs = append(runs, s[start:i])
			}
			start, kind = i, k
		}
	}
	return runs
}
