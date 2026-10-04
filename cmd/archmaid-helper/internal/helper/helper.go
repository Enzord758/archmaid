// Package helper implements archmaid-helper: a fixed set of root maintenance
// verbs, run through pkexec, that validate and recompute their own input and
// report progress as JSON lines. See ../../README.md for the protocol and
// threat model.
package helper

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
)

// Version is reported in the start event.
const Version = "0.1.0"

// Exit codes. They are part of the protocol (README.md).
const (
	ExitOK      = 0 // done
	ExitFailed  = 1 // a root command failed (or an internal error)
	ExitUsage   = 2 // bad usage or input
	ExitStale   = 3 // state changed since the user confirmed
	ExitRefused = 4 // denylisted, not root, or not the installed binary
	ExitBusy    = 5 // another helper or pacman holds the lock
)

// Input validation.
var (
	namePattern  = regexp.MustCompile(`^[a-z0-9@_+][a-z0-9@._+-]{0,127}$`)
	unitPattern  = regexp.MustCompile(`^[A-Za-z0-9:_.@-]{1,250}\.service$`)
	hashPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	keepPattern  = regexp.MustCompile(`^[1-9]$`)
	journalSpans = []string{"1week", "2weeks", "1month"}
)

// maxNames caps every name list (packages, units).
const maxNames = 256

// Options are the helper's seams. main fills them with the real thing;
// tests substitute fakes so nothing runs as root or executes pacman.
type Options struct {
	Runner    Runner
	Stdout    io.Writer // JSON lines
	Stderr    io.Writer // one human-readable line on failure
	Euid      int
	CallerUID string // PKEXEC_UID, else SUDO_UID, captured before the env is cleared
	Exe       func() (string, error)
	Stat      func(string) (FileStat, error)
	// LockPath is the helper's own lock; PacmanDBLock is pacman's.
	LockPath     string
	PacmanDBLock string
	// Dev is set by the devhelper build tag: refuse root, skip the install check.
	Dev bool
}

// DefaultOptions returns the production seams (everything but CallerUID,
// Euid and Dev, which main supplies).
func DefaultOptions() Options {
	return Options{
		Runner:       ExecRunner{},
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		Exe:          os.Executable,
		Stat:         LstatFile,
		LockPath:     DefaultLockPath,
		PacmanDBLock: DefaultPacmanDBLock,
	}
}

// verb describes one command.
type verb struct {
	root       bool // needs root (all but plan-orphans)
	pacmanLock bool // refuse while pacman's db.lck exists
	run        func(s *session, args []string) int
}

var verbs = map[string]verb{
	"check-files":    {root: true, run: runCheckFiles},
	"paccache":       {root: true, pacmanLock: true, run: runPaccache},
	"paccache-wipe":  {root: true, pacmanLock: true, run: runPaccacheWipe},
	"plan-orphans":   {root: false, run: runPlanOrphans},
	"remove-orphans": {root: true, pacmanLock: true, run: runRemoveOrphans},
	"repair":         {root: true, pacmanLock: true, run: runRepair},
	"vacuum-journal": {root: true, run: runVacuumJournal},
	"clean-tmp":      {root: true, run: runCleanTmp},
	"disable-unit":   {root: true, run: runDisableUnit},
	"upgrade-repos":  {root: true, pacmanLock: true, run: runUpgradeRepos},
	"full-cleanup":   {root: true, pacmanLock: true, run: runFullCleanup},
}

// Verbs lists the verb names, sorted.
func Verbs() []string {
	return slices.Sorted(func(yield func(string) bool) {
		for k := range verbs {
			if !yield(k) {
				return
			}
		}
	})
}

// session is one invocation's state.
type session struct {
	o         Options
	em        *Emitter
	childExit *int
	skipped   []Skip
	pacman    bool // verb wants the pacman lock check
}

// Run executes `archmaid-helper <args...>` and returns the exit code. It
// always writes a start line first and a result line last.
func Run(args []string, o Options) int {
	if o.Stdout == nil {
		o.Stdout = io.Discard
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	s := &session{o: o, em: NewEmitter(o.Stdout)}
	if len(args) == 0 {
		s.em.Start("", o.Dev)
		return s.finish(ExitUsage, "usage: archmaid-helper <verb> [args]; verbs: "+strings.Join(Verbs(), ", "))
	}
	name := args[0]
	s.em.Start(name, o.Dev)
	v, ok := verbs[name]
	if !ok {
		return s.finish(ExitUsage, fmt.Sprintf("unknown verb %q", name))
	}
	if o.Runner == nil {
		return s.finish(ExitFailed, "no runner configured")
	}
	if v.root || o.Dev {
		if err := checkPrivilege(o); err != nil {
			return s.finish(ExitRefused, err.Error())
		}
	}
	s.pacman = v.pacmanLock
	return v.run(s, args[1:])
}

// finish writes the result line and returns code.
func (s *session) finish(code int, msg string) int {
	s.em.Result(code, s.childExit, s.skipped, msg)
	if code != ExitOK && msg != "" {
		fmt.Fprintf(s.o.Stderr, "archmaid-helper: %s\n", Sanitize(msg))
	}
	return code
}

// usage reports bad input.
func (s *session) usage(format string, a ...any) int {
	return s.finish(ExitUsage, fmt.Sprintf(format, a...))
}

// lock takes the helper lock (and the pacman check when the verb wants it).
// On failure it writes the result itself; the caller returns the code.
func (s *session) lock() (release func(), code int) {
	release, err := acquireLock(s.o.LockPath, s.o.PacmanDBLock, s.pacman)
	if err != nil {
		if errors.Is(err, errBusy) {
			return nil, s.finish(ExitBusy, err.Error())
		}
		return nil, s.finish(ExitFailed, err.Error())
	}
	return release, ExitOK
}

func (s *session) skip(name, reason string) {
	s.skipped = append(s.skipped, Skip{Name: name, Reason: reason})
}

// step runs one root command, streaming its output, and reports whether it
// succeeded. okExits lists exit codes that count as success besides 0.
func (s *session) step(n, of int, label string, argv []string, okExits ...int) (bool, string) {
	s.em.Progress(n, of, label)
	exit, err := s.o.Runner.Stream(argv, s.em.Line)
	if err != nil {
		return false, fmt.Sprintf("%s: %v", label, err)
	}
	s.childExit = &exit
	if exit == 0 || slices.Contains(okExits, exit) {
		return true, ""
	}
	return false, fmt.Sprintf("%s: %s exited %d", label, argv[0], exit)
}

// single runs a verb that is exactly one root command.
func (s *session) single(label string, argv []string, okExits ...int) int {
	release, code := s.lock()
	if code != ExitOK {
		return code
	}
	defer release()
	if ok, msg := s.step(1, 1, label, argv, okExits...); !ok {
		return s.finish(ExitFailed, msg)
	}
	return s.finish(ExitOK, "")
}

func noArgs(s *session, verb string, args []string) bool {
	if len(args) != 0 {
		s.usage("%s takes no arguments", verb)
		return false
	}
	return true
}

// ---- argv builders (pure; the tests pin them) ------------------------------

func checkFilesArgv() []string       { return []string{Pacman, "-Qk"} }
func paccacheArgv(k string) []string { return []string{Paccache, "-r", "-k", k} }
func journalArgv(d string) []string  { return []string{Journalctl, "--vacuum-time=" + d} }
func cleanTmpArgv() []string         { return []string{SystemdTmpfile, "--clean"} }
func upgradeArgv() []string          { return []string{Pacman, "-Syu", "--noconfirm"} }
func removeArgv(targets []string) []string {
	return append([]string{Pacman, "-Rns", "--noconfirm", "--"}, targets...)
}
func repairArgv(pkgs []string) []string {
	return append([]string{Pacman, "-S", "--noconfirm", "--"}, pkgs...)
}
func disableArgv(units []string) []string {
	return append([]string{Systemctl, "disable", "--now", "--"}, units...)
}

// checkFilesOK: pacman -Qk exits 1 when it found missing files — a result,
// not a failure.
var checkFilesOK = []int{1}

// ---- verbs -----------------------------------------------------------------

func runCheckFiles(s *session, args []string) int {
	if !noArgs(s, "check-files", args) {
		return ExitUsage
	}
	return s.single("Checking package files", checkFilesArgv(), checkFilesOK...)
}

func runPaccache(s *session, args []string) int {
	if len(args) != 1 || !keepPattern.MatchString(args[0]) {
		return s.usage("paccache takes one argument N in 1–9 (cached versions to keep)")
	}
	return s.single("Cleaning package cache (keep "+args[0]+")", paccacheArgv(args[0]))
}

func runPaccacheWipe(s *session, args []string) int {
	if !noArgs(s, "paccache-wipe", args) {
		return ExitUsage
	}
	return s.single("Removing every cached package", paccacheArgv("0"))
}

func runVacuumJournal(s *session, args []string) int {
	if len(args) != 1 || !slices.Contains(journalSpans, args[0]) {
		return s.usage("vacuum-journal takes one of: %s", strings.Join(journalSpans, ", "))
	}
	return s.single("Vacuuming the journal (older than "+args[0]+")", journalArgv(args[0]))
}

func runCleanTmp(s *session, args []string) int {
	if !noArgs(s, "clean-tmp", args) {
		return ExitUsage
	}
	return s.single("Cleaning temporary files", cleanTmpArgv())
}

func runUpgradeRepos(s *session, args []string) int {
	if !noArgs(s, "upgrade-repos", args) {
		return ExitUsage
	}
	return s.single("Upgrading repository packages", upgradeArgv())
}

func runPlanOrphans(s *session, args []string) int {
	if !noArgs(s, "plan-orphans", args) {
		return ExitUsage
	}
	p, err := ComputePlan(s.o.Runner)
	if err != nil {
		return s.finish(ExitFailed, err.Error())
	}
	s.em.Plan(p)
	return s.finish(ExitOK, "")
}

func runRemoveOrphans(s *session, args []string) int {
	if len(args) != 1 || !hashPattern.MatchString(args[0]) {
		return s.usage("remove-orphans takes the plan hash (sha256:<64 hex>) from plan-orphans")
	}
	release, code := s.lock()
	if code != ExitOK {
		return code
	}
	defer release()
	ok, stale, msg := s.removeOrphans(1, 1, args[0])
	switch {
	case !ok:
		return s.finish(ExitFailed, msg)
	case stale:
		return s.finish(ExitStale, msg)
	}
	return s.finish(ExitOK, "")
}

// removeOrphans recomputes the plan and, only if it still hashes to want,
// removes the orphans. stale means it changed and nothing was removed.
func (s *session) removeOrphans(n, of int, want string) (ok, stale bool, msg string) {
	p, err := ComputePlan(s.o.Runner)
	if err != nil {
		return false, false, err.Error()
	}
	if p.Hash != want {
		s.em.Progress(n, of, "Orphan removal skipped: the plan changed since confirm")
		s.em.Plan(p) // the current plan, so the frontend can re-confirm
		s.skip("remove-orphans", "the orphan plan changed since it was confirmed")
		return true, true, "orphan plan changed since confirm; nothing removed"
	}
	if len(p.Targets) == 0 {
		s.em.Progress(n, of, "No orphaned packages")
		s.skip("remove-orphans", "no orphaned packages")
		return true, false, ""
	}
	ok, msg = s.step(n, of, fmt.Sprintf("Removing %d orphaned package(s)", len(p.Targets)), removeArgv(p.Targets))
	return ok, false, msg
}

func runRepair(s *session, args []string) int {
	if len(args) == 0 || len(args) > maxNames {
		return s.usage("repair takes 1–%d package names", maxNames)
	}
	for _, a := range args {
		if !namePattern.MatchString(a) {
			return s.usage("invalid package name %q", a)
		}
	}
	pkgs := slices.Compact(slices.Sorted(slices.Values(args)))
	release, code := s.lock()
	if code != ExitOK {
		return code
	}
	defer release()

	var broken []string
	for _, p := range pkgs {
		if reason := s.repairable(p); reason != "" {
			s.skip(p, reason)
			continue
		}
		broken = append(broken, p)
	}
	if len(broken) == 0 {
		return s.finish(ExitOK, "")
	}
	ok, msg := s.step(1, 1, fmt.Sprintf("Reinstalling %d package(s)", len(broken)), repairArgv(broken))
	if !ok {
		return s.finish(ExitFailed, msg)
	}
	return s.finish(ExitOK, "")
}

// repairable returns "" if pkg is installed, available in a sync repo and
// really missing files; otherwise the reason it is skipped.
func (s *session) repairable(pkg string) string {
	r := s.o.Runner
	if _, _, exit, err := r.Query([]string{Pacman, "-Qq", "--", pkg}); err != nil || exit != 0 {
		return "not installed"
	}
	if _, _, exit, err := r.Query([]string{Pacman, "-Si", "--", pkg}); err != nil || exit != 0 {
		return "not in a sync repository"
	}
	out, _, _, err := r.Query([]string{Pacman, "-Qk", "--", pkg})
	if err != nil {
		return "could not check its files"
	}
	missing, ok := missingFiles(out, pkg)
	switch {
	case !ok:
		return "could not check its files"
	case missing == 0:
		return "no missing files"
	}
	return ""
}

var qkSummary = regexp.MustCompile(`^(\S+): [0-9]+ total files?, ([0-9]+) missing files?$`)

// missingFiles parses pkg's `pacman -Qk` summary line.
func missingFiles(out, pkg string) (int, bool) {
	for _, l := range nonEmptyLines(out) {
		m := qkSummary.FindStringSubmatch(l)
		if m == nil || m[1] != pkg {
			continue
		}
		n := 0
		if _, err := fmt.Sscan(m[2], &n); err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

func runDisableUnit(s *session, args []string) int {
	if len(args) == 0 || len(args) > maxNames {
		return s.usage("disable-unit takes 1–%d unit names", maxNames)
	}
	for _, a := range args {
		if !unitPattern.MatchString(a) {
			return s.usage("invalid unit name %q (must match %s)", a, unitPattern)
		}
	}
	units := slices.Compact(slices.Sorted(slices.Values(args)))
	for _, u := range units {
		if deniedUnit(u) {
			return s.finish(ExitRefused, fmt.Sprintf("refusing to disable %s: it is on the denylist", u))
		}
	}
	release, code := s.lock()
	if code != ExitOK {
		return code
	}
	defer release()

	// Resolve each unit's names (aliases included) before touching anything:
	// a denied unit must not be reachable through an alias.
	states := make(map[string]unitState, len(units))
	for _, u := range units {
		st, err := s.unitState(u)
		if err != nil {
			return s.finish(ExitFailed, err.Error())
		}
		for _, n := range st.names {
			if deniedUnit(n) {
				return s.finish(ExitRefused, fmt.Sprintf("refusing to disable %s: it is %s, which is on the denylist", u, n))
			}
		}
		states[u] = st
	}
	var todo []string
	for _, u := range units {
		st := states[u]
		switch {
		case st.load != "loaded":
			s.skip(u, "unit not found ("+st.load+")")
		case st.fileState != "enabled":
			s.skip(u, "not enabled ("+st.fileState+")")
		case st.active != "inactive" && st.active != "failed":
			s.skip(u, "currently running ("+st.active+")")
		default:
			todo = append(todo, u)
		}
	}
	if len(todo) == 0 {
		return s.finish(ExitOK, "")
	}
	ok, msg := s.step(1, 1, fmt.Sprintf("Disabling %d service(s)", len(todo)), disableArgv(todo))
	if !ok {
		return s.finish(ExitFailed, msg)
	}
	return s.finish(ExitOK, "")
}

type unitState struct {
	names                   []string
	load, fileState, active string
}

func unitShowArgv(u string) []string {
	return []string{Systemctl, "show", "--property=Id,Names,LoadState,UnitFileState,ActiveState", "--", u}
}

func (s *session) unitState(u string) (unitState, error) {
	out, errOut, exit, err := s.o.Runner.Query(unitShowArgv(u))
	if err != nil {
		return unitState{}, fmt.Errorf("systemctl show %s: %w", u, err)
	}
	if exit != 0 {
		return unitState{}, fmt.Errorf("systemctl show %s exited %d: %s", u, exit, firstLine(errOut))
	}
	st := unitState{names: []string{u}}
	for _, l := range nonEmptyLines(out) {
		k, v, _ := strings.Cut(l, "=")
		switch k {
		case "Id":
			st.names = append(st.names, v)
		case "Names":
			st.names = append(st.names, strings.Fields(v)...)
		case "LoadState":
			st.load = v
		case "UnitFileState":
			st.fileState = v
		case "ActiveState":
			st.active = v
		}
	}
	return st, nil
}

// Units the helper never disables, whatever the caller asks.
var (
	unitDenyPrefixes = []string{
		"systemd-", "dbus", "polkit", "getty@", "NetworkManager", "sshd", "coolercontrold",
		"ly@",
	}
	unitDenyExact = []string{
		"sddm.service", "gdm.service", "lightdm.service", "ly.service", "greetd.service",
		"display-manager.service",
	}
)

func deniedUnit(u string) bool {
	if slices.Contains(unitDenyExact, u) {
		return true
	}
	for _, p := range unitDenyPrefixes {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}
