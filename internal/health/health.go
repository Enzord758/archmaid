// Package health is the read-only "System health" check behind the GUI's
// Overview cell (docs/design/health.md is the contract). Eleven independent
// checks in three groups, run in parallel under one deadline (failed systemd
// units, a stale kernel, clock sync, crashes, a left-over pacman lock; .pacnew
// files, AUR status, Arch news, the mirror; disk space, drive health). Everything runs as the user: no root, no password, nothing that
// changes state; external programs are started as argv (never a shell)
// through Deps.Query; every outside source is a Deps seam, so tests use fakes.
//
// Allowed imports: the standard library, internal/proc (Query), internal/report
// (pacman.log parsing and the kernel check), internal/sanitize, internal/pkgdb and
// internal/aursearch (the AUR check). Not allowed:
// steps, engine, privilege, gotk4.
package health

import (
	"context"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/report"
)

// State is a check's verdict.
type State int

const (
	// OK: nothing to do.
	OK State = iota
	// Look: the user should look at this.
	Look
	// Unknown: the check could not tell (offline, no tool, unreadable). It is
	// neutral: never counted by Attention.
	Unknown
)

func (s State) String() string {
	switch s {
	case OK:
		return "ok"
	case Look:
		return "look"
	}
	return "unknown"
}

// Check IDs, in the order Run returns them.
const (
	IDFailedUnits = "failed-units"
	IDRestart     = "restart"
	IDPacnew      = "pacnew"
	IDNews        = "news"
	IDDisk        = "disk"
	IDPacmanLock  = "pacman-lock"
	IDTimeSync    = "time-sync"
	IDCrashes     = "crashes"
	IDAUR         = "aur"
	IDDiskHealth  = "disk-health"
	IDMirror      = "mirror"
)

// Groups: the sheet's sections, in this order.
const (
	GroupSystem   = "system"
	GroupPackages = "packages"
	GroupHardware = "hardware"
)

// Check is one row of the sheet.
type Check struct {
	ID    string
	Title string
	// Group is GroupSystem, GroupPackages or GroupHardware: the sheet's
	// section. Run returns the checks already ordered by group.
	Group string
	State State
	// Detail is one plain-text line (sanitized, at most DetailMax runes).
	Detail string
	// Copy is a command line for the user to paste into a terminal ("" =
	// none). The GUI only copies it to the clipboard; archmaid never runs it.
	Copy string
	// Link is an https://archlinux.org/ address ("" = none).
	Link string
}

// Report is the result of Run.
type Report struct {
	Checks []Check
	At     time.Time
}

// Attention is the number of checks in state Look.
func (r Report) Attention() int {
	n := 0
	for _, c := range r.Checks {
		if c.State == Look {
			n++
		}
	}
	return n
}

// Limits.
const (
	// Deadline bounds the whole of Run.
	Deadline = 15 * time.Second
	// DetailMax caps a Detail, in runes.
	DetailMax = 200
)

// Deps are every outside seam. DefaultDeps has the real ones. Nothing is
// filled in behind a test's back: a nil Query, RunningKernel, FS or HTTPGet
// makes its check Unknown (so a test that forgets a fake never reaches the real
// systemctl or network); a nil Now is time.Now.
type Deps struct {
	// Query runs a short read-only program and returns its stdout (proc.Query).
	Query func(ctx context.Context, name string, args ...string) ([]byte, error)
	// Now is the clock.
	Now func() time.Time
	// PacmanLog is pacman's log; ModulesDir the kernels' modules folder;
	// CacheDir the user's cache folder (the news cache lives in
	// CacheDir/archmaid/arch-news.xml).
	PacmanLog, ModulesDir, CacheDir string
	// RunningKernel is `uname -r` ("" = unknown).
	RunningKernel func() string
	// Exists reports whether a file is there (os.Lstat).
	Exists func(path string) bool
	// FS reads a mount's free space and device (statfs).
	FS func(path string) (FSInfo, error)
	// HTTPGet fetches url and returns at most MaxNewsBody bytes of body; it
	// fails on any status but 200 and on a body over the cap.
	HTTPGet func(ctx context.Context, url string) ([]byte, error)
	// NewsURL is the feed to fetch.
	NewsURL string

	// LockFile is pacman's database lock; Procs lists the names of the running
	// processes (/proc/*/comm); Uptime is the time since boot.
	LockFile string
	Procs    func() ([]string, error)
	Uptime   func() (time.Duration, error)
	// Foreign lists the installed foreign packages (pacman -Qm, from the local
	// DB); AURInfo asks the AUR RPC about names (one multi-info request).
	Foreign func() ([]Foreign, error)
	AURInfo func(ctx context.Context, names []string) ([]AURPkg, error)
	// MirrorList is pacman's mirrorlist; MirrorGet fetches the mirror's
	// lastsync file (at most MirrorBodyMax bytes).
	MirrorList string
	MirrorGet  func(ctx context.Context, url string) ([]byte, error)
}

// FSInfo is what statfs says about the mount holding a path.
type FSInfo struct {
	Avail, Total uint64 // bytes available to the user, bytes in all
	Dev          uint64 // st_dev of the path (tells a separate mount)
}

// DefaultDeps are the real seams.
func DefaultDeps() Deps {
	return Deps{
		Query:         proc.Query,
		Now:           time.Now,
		PacmanLog:     report.PacmanLogFromEnv(),
		ModulesDir:    report.ModulesDirFromEnv(),
		CacheDir:      report.UserCacheDir(),
		RunningKernel: report.RunningKernel,
		Exists: func(p string) bool {
			_, err := os.Lstat(p)
			return err == nil
		},
		FS:      statFS,
		HTTPGet: httpGet,
		NewsURL: NewsURL,

		LockFile:   DefaultLockFile,
		Procs:      listProcs,
		Uptime:     readUptime,
		Foreign:    localForeign,
		AURInfo:    aurInfo,
		MirrorList: DefaultMirrorList,
		MirrorGet: func(ctx context.Context, u string) ([]byte, error) {
			return httpGetLimited(ctx, u, MirrorTimeout, MirrorBodyMax)
		},
	}
}

func statFS(path string) (FSInfo, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return FSInfo{}, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return FSInfo{}, err
	}
	var dev uint64
	if s, ok := fi.Sys().(*syscall.Stat_t); ok {
		dev = uint64(s.Dev)
	}
	return FSInfo{Avail: st.Bavail * uint64(st.Frsize), Total: st.Blocks * uint64(st.Frsize), Dev: dev}, nil
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

type checkFn func(ctx context.Context, d Deps) Check

// order is the sheet's order.
var order = []struct {
	id, title, group string
	fn               checkFn
}{
	{IDFailedUnits, TitleFailedUnits, GroupSystem, checkFailedUnits},
	{IDRestart, TitleRestart, GroupSystem, checkRestart},
	{IDTimeSync, TitleTimeSync, GroupSystem, checkTimeSync},
	{IDCrashes, TitleCrashes, GroupSystem, checkCrashes},
	{IDPacmanLock, TitlePacmanLock, GroupSystem, checkPacmanLock},
	{IDPacnew, TitlePacnew, GroupPackages, checkPacnew},
	{IDAUR, TitleAUR, GroupPackages, checkAUR},
	{IDNews, TitleNews, GroupPackages, checkNews},
	{IDMirror, TitleMirror, GroupPackages, checkMirror},
	{IDDisk, TitleDisk, GroupHardware, checkDisk},
	{IDDiskHealth, TitleDiskHealth, GroupHardware, checkDiskHealth},
}

// Run runs every check in parallel under the Deadline (the parent ctx's, if
// shorter) and returns them in the contract's order. A check that fails, panics
// or runs out of time is Unknown with a short reason; it never blocks or
// changes another.
func Run(ctx context.Context, d Deps) Report {
	ctx, cancel := context.WithTimeout(ctx, Deadline)
	defer cancel()
	out := make([]Check, len(order))
	chans := make([]chan Check, len(order))
	for i, o := range order {
		ch := make(chan Check, 1)
		chans[i] = ch
		go func() {
			defer func() {
				if r := recover(); r != nil {
					ch <- grouped(unknown(o.id, o.title, "check failed"), o.group)
				}
			}()
			c := o.fn(ctx, d)
			c.ID, c.Title = o.id, o.title
			ch <- grouped(c, o.group)
		}()
	}
	for i, o := range order {
		select {
		case out[i] = <-chans[i]:
		case <-ctx.Done():
			// The check may have finished just as the deadline hit.
			select {
			case out[i] = <-chans[i]:
			default:
				out[i] = grouped(unknown(o.id, o.title, "timed out"), o.group)
			}
		}
	}
	return Report{Checks: out, At: d.now()}
}

func grouped(c Check, g string) Check {
	c.Group = g
	return c
}

func unknown(id, title, why string) Check {
	return Check{ID: id, Title: title, State: Unknown, Detail: why}
}

// Titles (the sheet's row names).
const (
	TitleFailedUnits = "Failed services"
	TitleRestart     = "Restart recommended"
	TitlePacnew      = "Config files to merge"
	TitleNews        = "Arch news"
	TitleDisk        = "Disk space"
	TitlePacmanLock  = "Package lock"
	TitleTimeSync    = "Clock sync"
	TitleCrashes     = "Crashes"
	TitleAUR         = "AUR packages"
	TitleDiskHealth  = "Drive health"
	TitleMirror      = "Mirror"
)

// readCapped reads at most max bytes of r; more is an error.
func readCapped(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("response over %d bytes", max)
	}
	return b, nil
}
