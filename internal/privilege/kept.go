package privilege

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"os/user"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Enzord758/archmaid/internal/sysbin"
)

// KeptPassword is the GUI's one password for one run (docs/HANDOVER.md,
// "The GUI keeps the run's password"): the first password the user gives
// a run, kept so the run's later sudo and polkit prompts don't ask again.
//
// It lives in one anonymous page of its own (mmap), outside the Go heap,
// so the garbage collector never copies it, and mlocked, so it is never
// swapped out. It is never written anywhere and never logged. Wipe zeroes
// it, unlocks and unmaps the page; Fail does the same and refuses every
// later Keep, so a password that was rejected once is never used again.
// It is wiped by itself once it has been kept for longer than its cap.
//
// The zero value holds nothing and keeps nothing; NewKeptPassword makes one
// that does. Safe for concurrent use.
type KeptPassword struct {
	max time.Duration

	mu     sync.Mutex
	page   []byte // the mapped page; nil when nothing is held
	n      int    // the password's length in page
	spent  bool   // Fail or the end of the run: Keep refuses from now on
	timer  *time.Timer
	zeroed bool // the last Wipe read the page back as all zero
}

// ErrNotKept is With's error when no password is held.
var ErrNotKept = errors.New("no password is kept")

// maxKept caps a kept password's length: one page.
const maxKept = 4096

// NewKeptPassword returns an empty KeptPassword that holds a password for
// at most max once it is kept.
func NewKeptPassword(max time.Duration) *KeptPassword {
	return &KeptPassword{max: max}
}

// Keep holds pw, unless a password is held already, the run is over (Wipe
// or Fail), pw is empty, has a line break or doesn't fit a page, or the
// page can't be mapped and locked: it then holds nothing (and reports
// false), and the run asks as it always did. The caller still owns pw and
// wipes its own copy.
func (k *KeptPassword) Keep(pw []byte) bool {
	if k == nil || len(pw) == 0 || len(pw) > maxKept || bytes.ContainsAny(pw, "\r\n") {
		return false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.spent || k.page != nil || k.max <= 0 {
		return false
	}
	page, err := unix.Mmap(-1, 0, maxKept, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANONYMOUS)
	if err != nil {
		return false
	}
	if err := unix.Mlock(page); err != nil {
		_ = unix.Munmap(page)
		return false
	}
	// Not in a core dump either.
	_ = unix.Madvise(page, unix.MADV_DONTDUMP)
	k.page, k.n = page, copy(page, pw)
	k.zeroed = false
	k.timer = time.AfterFunc(k.max, k.Wipe)
	return true
}

// Has reports whether a password is held.
func (k *KeptPassword) Has() bool {
	if k == nil {
		return false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.page != nil
}

// Copy returns a copy of the held password for one use, which the caller
// clears (clear) as soon as it is done with it, or ErrNotKept. A copy, so
// that su and the write to sudo or polkit, which take a while, never hold
// the lock that Wipe needs.
func (k *KeptPassword) Copy() ([]byte, error) {
	if k == nil {
		return nil, ErrNotKept
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.page == nil {
		return nil, ErrNotKept
	}
	return append([]byte(nil), k.page[:k.n]...), nil
}

// Wipe zeroes the held password, unlocks and unmaps its page, and keeps
// nothing more: the run is over. Safe to call more than once.
func (k *KeptPassword) Wipe() {
	if k == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.spent = true
	k.wipeLocked()
}

// Fail is Wipe for a password that was rejected: it is gone, and nothing
// is kept for the rest of the run. The PATH guard (pathguard.go) uses it
// too: a run whose sudo could be stood in for keeps nothing.
func (k *KeptPassword) Fail() { k.Wipe() }

// Spent reports whether nothing more can be kept this run (Wipe, Fail, or
// the zero value, which keeps nothing).
func (k *KeptPassword) Spent() bool {
	if k == nil {
		return true
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.spent || k.max <= 0
}

func (k *KeptPassword) wipeLocked() {
	if k.timer != nil {
		k.timer.Stop()
		k.timer = nil
	}
	if k.page == nil {
		return
	}
	clear(k.page)
	zero := true
	for _, b := range k.page {
		if b != 0 {
			zero = false
		}
	}
	k.zeroed = zero
	_ = unix.Munlock(k.page)
	_ = unix.Munmap(k.page)
	k.page, k.n = nil, 0
}

// Wiped reports, for tests, whether nothing is held and the last Wipe read
// the page back as all zero before unmapping it.
func (k *KeptPassword) Wiped() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.page == nil && k.zeroed
}

// CheckPasswordBytes is CheckPassword for a password held as bytes: su's
// stdin gets a copy with a newline, wiped once su has exited. The caller
// still owns pw.
func CheckPasswordBytes(ctx context.Context, pw []byte) (detail string, err error) {
	if bytes.ContainsAny(pw, "\r\n") {
		return "", ErrLineBreak
	}
	self, err := user.Current()
	if err != nil {
		return "", err
	}
	line := make([]byte, len(pw)+1)
	defer clear(line)
	copy(line, pw)
	line[len(pw)] = '\n'
	check := exec.CommandContext(ctx, sysbin.Su, "-s", "/usr/bin/true", self.Username)
	check.Stdin = bytes.NewReader(line)
	var stderr strings.Builder
	check.Stderr = &stderr
	if err := check.Run(); err != nil {
		return lockoutNotice(stderr.String()), err
	}
	return "", nil
}

// AnswerBytes is Answer for a password held as bytes (a KeptPassword's):
// the same checks (Verify passed, no line break, su accepts it, under ctx:
// the run's, so Close stops a su in flight), then pw and a newline are
// written to sudo's helper. The caller still owns pw.
func (r *Request) AnswerBytes(ctx context.Context, pw []byte) (detail string, err error) {
	if !r.verified.Load() {
		return "", ErrUnverified
	}
	if bytes.ContainsAny(pw, "\r\n") {
		return "", ErrLineBreak
	}
	if detail, err := CheckPasswordBytes(ctx, pw); err != nil {
		return detail, err
	}
	line := make([]byte, len(pw)+1)
	copy(line, pw)
	line[len(pw)] = '\n'
	r.deliver(line)
	clear(line)
	return "", nil
}
