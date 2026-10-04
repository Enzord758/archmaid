package privilege

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Reading pam_faillock's state, so a frontend can say "one more wrong password
// locks you out for 10 minutes" BEFORE it shows a password or polkit prompt.
// On Arch, sudo's and polkit's PAM stacks (system-auth) run pam_faillock with
// deny=3: every wrong password costs a failure, and so does a cancelled
// polkit dialog (docs/polkit-live-test.md). Nothing here needs root or runs
// anything: the tally file of the user's own account is readable by them
// (/run/faillock/<user> is <user>:root 0660), and faillock.conf is world
// readable.
//
// The format and the rules are pam_faillock's (Linux-PAM,
// modules/pam_faillock/faillock.h, pam_faillock.c, faillock_config.c; see
// faillock(8), faillock.conf(5)):
//
//   - The tally file <dir>/<user> is an array of fixed 64-byte records in host
//     byte order, struct tally { char source[52]; uint16_t reserved;
//     uint16_t status; uint64_t time; }: the failure's tty, rhost or service
//     (not necessarily NUL-terminated), status flags (0x1 VALID, 0x2 the
//     source is an rhost, 0x4 a tty; neither means a service), and the time
//     of the failure in seconds since the epoch. A trailing partial record
//     is ignored, as PAM's read_tally does.
//   - check_tally (before a password is asked): latest is the newest VALID
//     record's time; the failures are the VALID records with
//     latest - time < fail_interval. With deny > 0 and failures >= deny the
//     account is locked, unless unlock_time > 0 and
//     latest + unlock_time < now: then it is unlocked, and the next failure
//     invalidates every old record (FAILLOCK_FLAG_UNLOCKED).
//   - write_tally (on a failure): records with now - time >= fail_interval
//     are invalidated before the new one is added.
//
// The admin rules (root_unlock_time, even_deny_root, admin_group) are not
// applied: archmaid only ever asks for the desktop user's own password.

// Faillock's defaults (faillock.conf(5)) and where it keeps its tallies.
const (
	FaillockDefaultDeny         = 3
	FaillockDefaultUnlockTime   = 600 * time.Second
	FaillockDefaultFailInterval = 900 * time.Second
	FaillockDefaultDir          = "/var/run/faillock"
)

// faillockConf is pam_faillock's configuration file; tests point it at a
// fixture (whose dir= points at a fixture tally directory).
var faillockConf = "/etc/security/faillock.conf"

const (
	tallyRecordSize = 64
	tallyStatusOff  = 54 // after source[52] and reserved
	tallyTimeOff    = 56
	tallyValid      = 0x1
	// maxTimeInterval is faillock_config.c's MAX_TIME_INTERVAL: larger
	// fail_interval or unlock_time values are rejected (the default stays).
	maxTimeInterval = 604800
	// maxTallyBytes bounds the read: PAM keeps far fewer records than this.
	maxTallyBytes = 1 << 20
)

// Status is a user's pam_faillock state at a moment.
type Status struct {
	// Failures is how many failures count toward the lockout. Unlocked, it
	// is what the next wrong password adds to (the records within
	// fail_interval of now); locked, it is the count that locked it.
	Failures int
	// Locked: PAM refuses even the right password until Unlock.
	Locked bool
	// Unlock is when a lock lapses; zero while unlocked, or when
	// unlock_time is "never" (an admin must run `faillock --reset`).
	Unlock time.Time
	// Deny is the configured deny: Failures that lock the account. Zero
	// means faillock never locks.
	Deny int
}

// LastAttempt reports whether the next wrong password (or cancelled polkit
// dialog) locks the account: the moment a frontend should warn.
func (s Status) LastAttempt() bool {
	return s.Deny > 0 && !s.Locked && s.Failures >= s.Deny-1
}

// FaillockStatus reads user's pam_faillock tally as PAM would judge it at
// now, honouring deny, unlock_time, fail_interval and dir from
// /etc/security/faillock.conf (faillock's defaults where unset or invalid).
// No tally file means no failures. A frontend calls it before it shows a
// password or polkit prompt, and warns when LastAttempt is true.
func FaillockStatus(user string, now time.Time) (Status, error) {
	return faillockStatus(faillockConf, user, now)
}

func faillockStatus(confPath, user string, now time.Time) (Status, error) {
	if user == "" || user == "." || user == ".." || strings.ContainsAny(user, "/\x00") {
		return Status{}, fmt.Errorf("faillock: invalid user name %q", user)
	}
	cfg, err := readFaillockConf(confPath)
	if err != nil {
		return Status{}, err
	}
	st := Status{Deny: cfg.deny}
	times, err := readTally(filepath.Join(cfg.dir, user))
	if err != nil {
		return st, err
	}
	if len(times) == 0 {
		return st, nil
	}
	nowU := uint64(now.Unix())
	failInterval, unlock := uint64(cfg.failInterval/time.Second), uint64(cfg.unlockTime/time.Second)

	var latest uint64
	for _, t := range times {
		latest = max(latest, t)
	}
	// check_tally: count against the newest failure.
	failures := 0
	for _, t := range times {
		if latest-t < failInterval {
			failures++
		}
	}
	if cfg.deny > 0 && failures >= cfg.deny {
		if unlock > 0 && latest+unlock < nowU {
			// Lapsed: PAM unlocks, and the next failure starts afresh.
			return st, nil
		}
		st.Locked, st.Failures = true, failures
		if unlock > 0 {
			st.Unlock = time.Unix(int64(latest+unlock), 0)
		}
		return st, nil
	}
	// write_tally: what the next failure counts with. Unsigned, like PAM:
	// a record from the future is outside the interval.
	for _, t := range times {
		if nowU-t < failInterval {
			st.Failures++
		}
	}
	return st, nil
}

// readTally returns the times of the VALID records in a tally file; a
// missing file has none.
func readTally(path string) ([]uint64, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("faillock: %w", err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxTallyBytes))
	if err != nil {
		return nil, fmt.Errorf("faillock: %w", err)
	}
	var times []uint64
	for off := 0; off+tallyRecordSize <= len(b); off += tallyRecordSize {
		rec := b[off : off+tallyRecordSize]
		if binary.NativeEndian.Uint16(rec[tallyStatusOff:])&tallyValid != 0 {
			times = append(times, binary.NativeEndian.Uint64(rec[tallyTimeOff:]))
		}
	}
	return times, nil
}

type faillockConfig struct {
	dir                      string
	deny                     int
	unlockTime, failInterval time.Duration
}

// readFaillockConf parses faillock.conf the way faillock_config.c does:
// '#' starts a comment, a line is `key [=] value` or a bare flag, and an
// invalid value leaves the default. A missing file means the defaults.
func readFaillockConf(path string) (faillockConfig, error) {
	cfg := faillockConfig{
		dir:          FaillockDefaultDir,
		deny:         FaillockDefaultDeny,
		unlockTime:   FaillockDefaultUnlockTime,
		failInterval: FaillockDefaultFailInterval,
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("faillock: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(io.LimitReader(f, maxTallyBytes))
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		line = strings.TrimSpace(line)
		i := strings.IndexFunc(line, func(r rune) bool { return r == '=' || r == ' ' || r == '\t' })
		if i < 0 {
			continue // a bare flag (even_deny_root, silent, …) or nothing
		}
		key := line[:i]
		val := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line[i:]), "="))
		switch key {
		case "dir":
			if strings.HasPrefix(val, "/") {
				cfg.dir = val
			}
		case "deny":
			if n, err := strconv.ParseUint(val, 10, 16); err == nil {
				cfg.deny = int(n)
			}
		case "fail_interval":
			if n, ok := parseInterval(val, false); ok {
				cfg.failInterval = n
			}
		case "unlock_time":
			if n, ok := parseInterval(val, true); ok {
				cfg.unlockTime = n
			}
		}
	}
	if err := sc.Err(); err != nil {
		return cfg, fmt.Errorf("faillock: %w", err)
	}
	return cfg, nil
}

// parseInterval parses a seconds value up to MAX_TIME_INTERVAL; with never,
// "never" is 0 (no automatic unlock).
func parseInterval(val string, never bool) (time.Duration, bool) {
	if never && val == "never" {
		return 0, true
	}
	n, err := strconv.ParseUint(val, 10, 32)
	if err != nil || n > maxTimeInterval {
		return 0, false
	}
	return time.Duration(n) * time.Second, true
}
