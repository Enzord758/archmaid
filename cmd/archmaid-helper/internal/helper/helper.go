// Package helper implements archmaid-helper: a fixed set of root maintenance
// verbs, run through pkexec, that validate and recompute their own input and
// report progress as JSON lines. See ../../README.md for the protocol and
// threat model.
package helper

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
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
	namePattern  = regexp.MustCompile(`^[A-Za-z0-9@_+][A-Za-z0-9@._+-]{0,127}$`)          // makepkg allows uppercase (local builds)
	unitPattern  = regexp.MustCompile(`^[A-Za-z0-9:_@][A-Za-z0-9:_.@-]{0,249}\.service$`) // never a leading - or .
	hashPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	keepPattern  = regexp.MustCompile(`^[1-9]$`)
	journalSpans = []string{"1week", "2weeks", "1month"}
)

// maxNames caps every name list (packages, units).
const maxNames = 256

// MaxNames is maxNames, exported so a front end's lists can be tied to it in
// a test (cmd/archmaid-helper).
const MaxNames = maxNames

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
	LockPath string
	// PacmanDBLock "" (production) means pacman-conf DBPath + db.lck,
	// read once per run (dblock.go); tests set a path.
	PacmanDBLock string
	// Dev is set by the devhelper build tag: refuse root, skip the install check.
	Dev bool

	// Test seams of the download-leftovers tidy (leftovers.go); nil in
	// production. now is its clock (nil: time.Now); underPacmanLock is
	// called with each candidate's name while pacman's lock is held.
	now             func() time.Time
	underPacmanLock func(name string)
	// KernelPkgbases lists the /usr/lib/modules/*/pkgbase files for the
	// protected list (protected.go); nil: the real glob.
	KernelPkgbases func() ([]string, error)
	// lockDirCheck replaces checkLockDir (the lock folder is root's alone).
	lockDirCheck func(dir string) error

	// pkg-install-local (pkglocal.go). StagingRoot "" is DefaultStagingRoot
	// and PasswdPath "" is DefaultPasswd; tests point them at temp files.
	StagingRoot string
	PasswdPath  string
	// Test seams, nil in production: walkAnchor starts the path walk below
	// a trusted folder (the temp dir) instead of "/"; fstatHook may change
	// what the walk sees of each folder or file (a foreign owner); afterOpen
	// runs between the walk's open of a file and its copy (a swap);
	// uname replaces uname(2).
	walkAnchor string
	fstatHook  func(label string, st *unix.Stat_t)
	afterOpen  func(path string)
	afterStage func(dir string) // after every file is staged, before it is read back
	uname      func() (string, error)
}

// DefaultOptions returns the production seams (everything but CallerUID,
// Euid and Dev, which main supplies).
func DefaultOptions() Options {
	return Options{
		Runner:   ExecRunner{},
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Exe:      os.Executable,
		Stat:     LstatFile,
		LockPath: DefaultLockPath,
	}
}

// verb describes one command.
type verb struct {
	root       bool // needs root (all but the plan-* verbs)
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
	"full-cleanup":   {root: true, pacmanLock: true, run: runFullCleanup},
	"plan-install":   {root: false, run: runPlanInstall},
	"pkg-install":    {root: true, pacmanLock: true, run: runPkgInstall},
	"plan-remove":    {root: false, run: runPlanRemove},
	"pkg-remove":     {root: true, pacmanLock: true, run: runPkgRemove},

	"pkg-install-local": {root: true, pacmanLock: true, run: runPkgInstallLocal},
}

// Verbs lists the verb names, sorted.
func Verbs() []string { return slices.Sorted(maps.Keys(verbs)) }

// session is one invocation's state.
type session struct {
	o         Options
	em        *Emitter
	childExit *int
	skipped   []Skip
	pacman    bool // verb wants the pacman lock check
	// reason, names and count: the package verbs' machine-readable refusal,
	// set before finish.
	reason string
	names  []string
	count  int
	part   string // the combined verb's part running now (its verb), or ""

	dbLock       string // pacman's db.lck for this run, see lockPath
	lockResolved bool
}

// progress announces step n of `of`, as a part of a combined verb while
// one runs (s.part).
func (s *session) progress(n, of int, label string) {
	if s.part != "" {
		s.em.PartProgress(n, of, s.part, label)
		return
	}
	s.em.Progress(n, of, label)
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
	switch {
	case v.root || o.Dev:
		if err := checkPrivilege(o); err != nil {
			return s.finish(ExitRefused, err.Error())
		}
	case o.Euid == 0:
		// A verb that needs no root may still be run as root (pkexec's
		// generic action, sudo). Then it must be the installed binary like
		// any other root run, not a copy anyone could have written.
		if err := CheckInstall(o.Exe, o.Stat); err != nil {
			return s.finish(ExitRefused, err.Error())
		}
	}
	s.pacman = v.pacmanLock
	return v.run(s, args[1:])
}

// finish writes the result line and returns code.
func (s *session) finish(code int, msg string) int {
	s.em.ResultWhy(code, s.childExit, s.skipped, msg, s.reason, s.names, s.count)
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
	dbLock := ""
	if s.pacman {
		dbLock = s.lockPath() // before the first check that needs it
	}
	release, err := acquireLock(s.o.LockPath, dbLock, s.pacman)
	if err != nil {
		if errors.Is(err, errBusy) {
			s.reason = ReasonBusy
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
	s.progress(n, of, label)
	return s.exec(label, argv, okExits...)
}

// exec is step without its progress event, for a step that writes lines
// of its own between the two (full-cleanup's clean-uninstalled).
func (s *session) exec(label string, argv []string, okExits ...int) (bool, string) {
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

// Colour (task #111; colour_test.go pins every call). The children write
// into pipes, so pacman colours only when its argv asks; --color always
// forces it whatever pacman.conf says, and pacman reads no TERM. A pacman
// whose output the helper only relays gets colourAlways, and its line
// events keep the SGR (Emitter.Line); a pacman whose output the helper
// parses gets colourNever, so a parsed line never holds an escape: every
// Query, and the check-files verb's -Qk, whose summary lines become the
// check event. paccache colours only on a terminal; systemd-tmpfiles and
// journalctl have no colour option (one would fail the call), so the
// relayed systemd calls get SYSTEMD_COLORS=1 in their own env (CallEnv,
// task #133) and nothing else does: see ChildEnv for why TERM stays dumb.
var (
	colourAlways = []string{"--color", "always"}
	colourNever  = []string{"--color", "never"}
)

// pacmanArgv is pacman with opts, then colour, then names after "--"
// (none: no "--").
func pacmanArgv(opts, colour, names []string) []string {
	argv := slices.Concat([]string{Pacman}, opts, colour)
	if names == nil {
		return argv
	}
	return slices.Concat(argv, []string{"--"}, names)
}

// checkFilesArgv is the check-files verb's full pacman -Qk: parsed.
func checkFilesArgv() []string { return pacmanArgv([]string{"-Qk"}, colourNever, nil) }

// integrityArgv is full-cleanup's pacman -Qk part: only relayed.
func integrityArgv() []string { return pacmanArgv([]string{"-Qk"}, colourAlways, nil) }
func installedArgv() []string { return pacmanArgv([]string{"-Qq"}, colourNever, nil) }
func checkPackagesArgv(names []string) []string {
	return pacmanArgv([]string{"-Qk"}, colourNever, names)
}

// queryArgv is `pacman op --color never -- pkg`, a query repairable parses.
func queryArgv(op, pkg string) []string { return pacmanArgv([]string{op}, colourNever, []string{pkg}) }

// orphansArgv and cascadeArgv are the orphan plan's two queries.
func orphansArgv() []string { return pacmanArgv([]string{"-Qdtq"}, colourNever, nil) }
func cascadeArgv(targets []string) []string {
	return pacmanArgv([]string{"-Rs", "--print", "--print-format", "%n %v"}, colourNever, targets)
}
func paccacheArgv(k string) []string { return []string{Paccache, "-r", "-k", k} }
func journalArgv(d string) []string  { return []string{Journalctl, "--vacuum-time=" + d} }
func cleanTmpArgv() []string         { return []string{SystemdTmpfile, "--clean"} }

// cleanUninstalledArgv is pacman -Sc: the cache of packages no longer
// installed (pacman.conf's CleanMethod decides), and unused sync databases;
// --noconfirm answers its two questions with their default, yes.
func cleanUninstalledArgv() []string {
	return pacmanArgv([]string{"-Sc", "--noconfirm"}, colourAlways, nil)
}
func removeArgv(targets []string) []string {
	return pacmanArgv([]string{"-Rns", "--noconfirm"}, colourAlways, targets)
}
func repairArgv(pkgs []string) []string {
	return pacmanArgv([]string{"-S", "--noconfirm"}, colourAlways, pkgs)
}
func disableArgv(units []string) []string {
	return append([]string{Systemctl, "disable", "--now", "--"}, units...)
}

// checkFilesOK: pacman -Qk exits 1 when it found missing files — a result,
// not a failure.
var checkFilesOK = []int{1}

// ---- verbs -----------------------------------------------------------------

// runCheckFiles checks every installed package's files, or with names just
// those packages' (Repair's root recheck of its user scan's candidates).
// Either way a run that gets pacman's verdict ends in a check event.
func runCheckFiles(s *session, args []string) int {
	if len(args) == 0 {
		release, code := s.lock()
		if code != ExitOK {
			return code
		}
		defer release()
		return s.checkPackages(nil)
	}
	if msg := checkNames(args); msg != "" {
		return s.usage("%s", msg)
	}
	release, code := s.lock()
	if code != ExitOK {
		return code
	}
	defer release()
	if code, msg := s.checkInstalled(args); code != ExitOK {
		return s.finish(code, msg)
	}
	return s.checkPackages(args)
}

// checkNames validates check-files' package names before anything runs:
// at most maxNames of them, each a pacman package name (which can't start
// with '-', so none reads as an option), none named twice. It returns why
// the list is refused, or "".
func checkNames(args []string) string {
	if len(args) > maxNames {
		return fmt.Sprintf("check-files takes at most %d package names, got %d", maxNames, len(args))
	}
	seen := make(map[string]bool, len(args))
	for _, a := range args {
		if !namePattern.MatchString(a) {
			return fmt.Sprintf("invalid package name %q", a)
		}
		if seen[a] {
			return fmt.Sprintf("package %q is named twice", a)
		}
		seen[a] = true
	}
	return ""
}

// checkInstalled refuses (exit 2) a name that isn't an installed package. It
// asks pacman (`pacman -Qq`) rather than trusting the caller.
func (s *session) checkInstalled(names []string) (int, string) {
	out, errOut, exit, err := s.o.Runner.Query(installedArgv())
	if err != nil {
		return ExitFailed, fmt.Sprintf("pacman -Qq: %v", err)
	}
	if exit != 0 {
		return ExitFailed, fmt.Sprintf("pacman -Qq exited %d: %s", exit, firstLine(errOut))
	}
	installed := map[string]bool{}
	for _, l := range nonEmptyLines(out) {
		installed[l] = true
	}
	for _, n := range names {
		if !installed[n] {
			return ExitUsage, fmt.Sprintf("package %q is not installed", n)
		}
	}
	return ExitOK, ""
}

// checkPackages runs `pacman -Qk -- names…`, relaying its output like any
// step, and then reports each package's summary line in one check event:
// checked lists the packages it read a summary for, broken those of them
// missing files. A package without a summary is skipped ("could not check
// its files") and is in neither list. With no names (nil) it runs the full
// `pacman -Qk` and reports every summary line, in pacman's order.
func (s *session) checkPackages(names []string) int {
	label := fmt.Sprintf("Checking %d package(s) for missing files", len(names))
	argv := checkPackagesArgv(names)
	if names == nil {
		label, argv = "Checking package files", checkFilesArgv()
	}
	s.em.Progress(1, 1, label)
	var mu sync.Mutex // Stream drains stdout and stderr concurrently
	var out strings.Builder
	exit, err := s.o.Runner.Stream(argv, func(stream, text string) {
		if stream == "out" {
			mu.Lock()
			out.WriteString(text + "\n")
			mu.Unlock()
		}
		s.em.Line(stream, text)
	})
	if err != nil {
		return s.finish(ExitFailed, fmt.Sprintf("%s: %v", label, err))
	}
	s.childExit = &exit
	if exit != 0 && !slices.Contains(checkFilesOK, exit) {
		return s.finish(ExitFailed, fmt.Sprintf("%s: %s exited %d", label, Pacman, exit))
	}
	checked, broken := []string{}, []string{}
	if names == nil {
		for _, sum := range qkSummaries(out.String()) {
			checked = append(checked, sum.name)
			if sum.missing > 0 {
				broken = append(broken, sum.name)
			}
		}
	}
	for _, n := range names {
		missing, ok := missingFiles(out.String(), n)
		if !ok {
			s.skip(n, "could not check its files")
			continue
		}
		checked = append(checked, n)
		if missing > 0 {
			broken = append(broken, n)
		}
	}
	s.em.Check(checked, broken)
	return s.finish(ExitOK, "")
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
	return s.finish(s.removeOrphans(1, 1, args[0]))
}

// removeOrphans recomputes the plan and, only if it still hashes to want,
// removes the orphans: ExitOK, ExitStale (it changed; nothing removed),
// ExitBusy (pacman was running; nothing removed) or ExitFailed, with the
// message for finish.
//
// The recompute sits between two checks that pacman isn't running, right
// before -Rns: the lock check at the verb's start is not one of them (in
// full-cleanup the removal is the first part, but it must not rely on
// that), and a pacman running during the recompute could change the
// database under it. What is left needs a root
// pacman started by someone else that either runs a whole transaction
// inside the recompute (between the checks), or starts in the moment
// between the second check and -Rns taking pacman's lock itself
// (docs/HANDOVER.md, landmines).
func (s *session) removeOrphans(n, of int, want string) (int, string) {
	busy := func(err error) (int, string) {
		s.progress(n, of, "Orphan removal skipped: pacman is running")
		s.skip("remove-orphans", "pacman is running")
		return ExitBusy, err.Error() + "; orphans were not removed"
	}
	if err := pacmanIdle(s.lockPath()); err != nil {
		return busy(err)
	}
	p, err := ComputePlan(s.o.Runner)
	if err != nil {
		return ExitFailed, err.Error()
	}
	if p.Hash != want {
		s.progress(n, of, "Orphan removal skipped: the plan changed since confirm")
		s.em.Plan(p) // the current plan, so the frontend can re-confirm
		s.skip("remove-orphans", "the orphan plan changed since it was confirmed")
		return ExitStale, "orphan plan changed since confirm; nothing removed"
	}
	if len(p.Targets) == 0 {
		s.progress(n, of, "No orphaned packages")
		s.skip("remove-orphans", "no orphaned packages")
		return ExitOK, ""
	}
	if err := pacmanIdle(s.lockPath()); err != nil {
		return busy(err)
	}
	if ok, msg := s.step(n, of, fmt.Sprintf("Removing %d orphaned package(s)", len(p.Targets)), removeArgv(p.Targets)); !ok {
		return ExitFailed, msg
	}
	return ExitOK, ""
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
	installed := map[string]string{} // broken package → installed version
	versionSkips := 0
	for _, p := range pkgs {
		version, reason, otherVersion := s.repairable(p)
		if reason != "" {
			s.skip(p, reason)
			if otherVersion {
				versionSkips++
			}
			continue
		}
		broken = append(broken, p)
		installed[p] = version
	}
	if len(broken) == 0 {
		if versionSkips > 0 {
			return s.finish(ExitStale, "nothing to repair: the repos have a different version of the broken "+
				"package(s), so reinstalling would be a partial upgrade — update the system first")
		}
		return s.finish(ExitOK, "")
	}
	if code, msg := s.vetRepair(broken, installed); code != ExitOK {
		return s.finish(code, msg)
	}
	ok, msg := s.step(1, 1, fmt.Sprintf("Reinstalling %d package(s)", len(broken)), repairArgv(broken))
	if !ok {
		return s.finish(ExitFailed, msg)
	}
	return s.finish(ExitOK, "")
}

// repairable returns pkg's installed version and "" if pkg is installed,
// available in a sync repo at exactly its installed version and really
// missing files; otherwise the reason it is skipped. otherVersion reports
// that the repo's version is the obstacle: `pacman -S` installs the repo's
// version, so reinstalling would be a partial upgrade (or a downgrade).
func (s *session) repairable(pkg string) (version, reason string, otherVersion bool) {
	r := s.o.Runner
	qOut, _, exit, err := r.Query(queryArgv("-Q", pkg))
	if err != nil || exit != 0 {
		return "", "not installed", false
	}
	siOut, _, exit, err := r.Query(queryArgv("-Si", pkg))
	if err != nil || exit != 0 {
		return "", "not in a sync repository", false
	}
	out, _, _, err := r.Query(queryArgv("-Qk", pkg))
	if err != nil {
		return "", "could not check its files", false
	}
	missing, ok := missingFiles(out, pkg)
	switch {
	case !ok:
		return "", "could not check its files", false
	case missing == 0:
		return "", "no missing files", false
	}
	installed, okQ := installedVersion(qOut, pkg)
	repo, okSi := syncVersion(siOut)
	switch {
	case !okQ || !okSi:
		return "", "could not compare its installed and repo versions", false
	case installed != repo:
		return "", fmt.Sprintf("repo has a different version (installed %s, repo %s) — update the system first", installed, repo), true
	}
	return installed, "", false
}

// repairPlanArgv asks pacman what `pacman -S -- pkgs…` would install,
// dependencies included, without doing it (--print takes no lock).
func repairPlanArgv(pkgs []string) []string {
	return pacmanArgv([]string{"-Sp", "--noconfirm", "--print-format", "%n %v"}, colourNever, pkgs)
}

// vetRepair checks the whole transaction `pacman -S -- broken…` would run:
// it must be exactly the broken packages, each at its installed version.
// repairable compared versions one package at a time; this catches what it
// can't see: a dependency that would be installed or upgraded along with
// them (a partial upgrade through the back door), and a sync database
// refreshed in between. Anything else is refused with ExitStale and the
// broken packages are reported as skipped, the extra ones named.
func (s *session) vetRepair(broken []string, installed map[string]string) (int, string) {
	// stdout only: it is just the %n %v lines (LC_ALL=C from ChildEnv);
	// warnings go to stderr.
	out, errOut, exit, err := s.o.Runner.Query(repairPlanArgv(broken))
	if err != nil {
		return ExitFailed, fmt.Sprintf("pacman -Sp: %v", err)
	}
	if exit != 0 {
		return ExitFailed, fmt.Sprintf("could not plan the reinstall: pacman -Sp exited %d: %s", exit, firstLine(errOut))
	}
	planned := map[string]string{}
	var extra []string
	for _, l := range nonEmptyLines(out) {
		name, ver, ok := strings.Cut(l, " ")
		if !ok || !namePattern.MatchString(name) || !versionPattern.MatchString(ver) {
			return ExitFailed, fmt.Sprintf("unexpected line from pacman -Sp: %q", l)
		}
		if _, dup := planned[name]; dup {
			return ExitFailed, fmt.Sprintf("pacman -Sp lists %s twice", name)
		}
		planned[name] = ver
		if _, ok := installed[name]; !ok {
			extra = append(extra, l)
		}
	}
	slices.Sort(extra)
	reasons := map[string]string{}
	var problems []string
	for _, p := range broken {
		ver, ok := planned[p]
		switch {
		case !ok:
			reasons[p] = "pacman would not reinstall it"
			problems = append(problems, p+" is not in the transaction")
		case ver != installed[p]:
			reasons[p] = fmt.Sprintf("pacman would install a different version (installed %s, repo %s) — update the system first", installed[p], ver)
			problems = append(problems, fmt.Sprintf("%s would change from %s to %s", p, installed[p], ver))
		}
	}
	if len(extra) > 0 {
		problems = append(problems, "it would also install or upgrade "+strings.Join(extra, ", "))
	}
	if len(problems) == 0 {
		return ExitOK, ""
	}
	for _, p := range broken {
		reason := reasons[p]
		switch {
		case reason != "":
		case len(extra) > 0:
			reason = "reinstalling would also install or upgrade other packages (" + strings.Join(extra, ", ") + ") — update the system first"
		default:
			reason = "another package in this repair can't be reinstalled at its installed version"
		}
		s.skip(p, reason)
	}
	return ExitStale, "nothing repaired: the reinstall would not be just the broken package(s) at their installed versions: " +
		strings.Join(problems, "; ") + " — update the system first"
}

// installedVersion parses `pacman -Q -- pkg`: one line, "name version".
func installedVersion(out, pkg string) (string, bool) {
	lines := nonEmptyLines(out)
	if len(lines) != 1 {
		return "", false
	}
	f := strings.Fields(lines[0])
	if len(f) != 2 || f[0] != pkg {
		return "", false
	}
	return f[1], true
}

// syncVersion parses the Version of the first package in `pacman -Si --
// pkg` (LC_ALL=C, so the labels are English). A package in several repos is
// listed once per repo in pacman.conf order, and the first is the one
// `pacman -S` installs.
func syncVersion(out string) (string, bool) {
	for _, l := range nonEmptyLines(out) {
		k, v, ok := strings.Cut(l, ":")
		if !ok || strings.TrimSpace(k) != "Version" {
			continue
		}
		f := strings.Fields(v)
		if len(f) != 1 {
			return "", false
		}
		return f[0], true
	}
	return "", false
}

var qkSummary = regexp.MustCompile(`^(\S+): [0-9]+ total files?, ([0-9]+) missing files?$`)

// qkSum is one package's `pacman -Qk` summary line.
type qkSum struct {
	name    string
	missing int
}

// qkSummaries parses every summary line of `pacman -Qk` output, in order.
func qkSummaries(out string) []qkSum {
	var sums []qkSum
	for _, l := range nonEmptyLines(out) {
		m := qkSummary.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		n := 0
		if _, err := fmt.Sscan(m[2], &n); err != nil {
			continue
		}
		sums = append(sums, qkSum{m[1], n})
	}
	return sums
}

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
