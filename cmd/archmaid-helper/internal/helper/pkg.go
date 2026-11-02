package helper

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The repo install and remove verbs (plan-install, pkg-install, plan-remove,
// pkg-remove). The plan verbs are read-only and need no root; the root verbs
// take the hash of the plan the user confirmed, recompute it under the lock
// and run pacman only if it is the same. README.md has the contract and
// docs/design/packages-contract.md section 3 the front end's side.

// MaxPkgNames caps the names of one install or remove run, exported so a
// front end's list can be tied to it in a test.
const MaxPkgNames = 32

// The result event's machine-readable reasons (steps.RefuseReason strings).
const (
	ReasonUpdatesPending = "updates-pending"
	ReasonConflict       = "conflict"
	ReasonProtected      = "protected"
	ReasonNotFound       = "not-found"
	ReasonNotInstalled   = "not-installed"
	ReasonRequiredBy     = "required-by"
	ReasonBusy           = "busy"
	ReasonStale          = "stale"
	ReasonBadRequest     = "bad-request"
	ReasonUnsafeFile     = "unsafe-file" // pkg-install-local: a file or a folder on its path is not safe to read as root
	ReasonBadPackage     = "bad-package" // pkg-install-local: pacman cannot read it or it is not the package it is named
)

// warnAbove is the plan size above which the plan carries a warning.
const warnAbove = 30

// repoPattern is what pacman prints for %r.
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9@_+][A-Za-z0-9@._+-]{0,63}$`)

// The modes a plan hash commits to, with the flag folded in.
const (
	modeInstall       = "install"
	modeInstallAsDeps = "install-asdeps"
	modeRemove        = "remove"   // pacman -Rs
	modeRemoveN       = "remove-n" // pacman -Rns
	pkgPlanMagic      = "archmaid pkg plan v1\n"

	flagNoConfig = "-n"       // plan-remove / pkg-remove: -Rns
	flagAsDeps   = "--asdeps" // plan-install / pkg-install: install as dependency
)

// PkgPlanHash is the canonical hash of an install or remove plan: the mode,
// the sorted targets and the sorted items. mode is "install",
// "install-asdeps", "remove" (-Rs) or "remove-n" (-Rns). Install items are
// "repo name version" (the plan event's items are "name version" and the
// repo is in rows[].repo, so the hash input is built from the rows); remove
// items are "name version". This is helperclient.PkgPlanHash byte for byte.
func PkgPlanHash(mode string, targets, items []string) string {
	h := sha256.New()
	fmt.Fprint(h, pkgPlanMagic)
	fmt.Fprintf(h, "mode %s\n", mode)
	for _, s := range slices.Sorted(slices.Values(targets)) {
		fmt.Fprintf(h, "target %s\n", s)
	}
	for _, s := range slices.Sorted(slices.Values(items)) {
		fmt.Fprintf(h, "item %s\n", s)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// ---- argv builders ---------------------------------------------------------

// installPlanArgv: what `pacman -S` would install, read-only (--print takes
// no lock). --needed makes an up-to-date target vanish from the list.
func installPlanArgv(names []string) []string {
	return pacmanArgv([]string{"-Sp", "--needed", "--noconfirm", "--print-format", "%r %n %v"}, colourNever, names)
}

// installPkgArgv is the install itself; asdeps marks the packages as
// installed as dependencies.
func installPkgArgv(names []string, asDeps bool) []string {
	opts := []string{"-S", "--needed", "--noconfirm"}
	if asDeps {
		opts = append(opts, flagAsDeps)
	}
	return pacmanArgv(opts, colourAlways, names)
}

func removeFlag(noConfig bool) string {
	if noConfig {
		return "-Rns"
	}
	return "-Rs"
}

// removePlanArgv: what `pacman -Rs[n]` would remove, read-only. It is always
// -Rs: pacman refuses -Rns with --print ("invalid option: '--nosave' and
// '--print' may not be used together"), and -n only decides whether config
// files go, not which packages: the cascade is the same. The hash keeps mode
// remove-n, so the confirmed choice is bound.
func removePlanArgv(names []string, _ bool) []string {
	return pacmanArgv([]string{"-Rs", "--print", "--print-format", "%n %v"}, colourNever, names)
}

// removePkgArgv is the removal itself. Never -Rc, never -Rdd.
func removePkgArgv(names []string, noConfig bool) []string {
	return pacmanArgv([]string{removeFlag(noConfig), "--noconfirm"}, colourAlways, names)
}

func updatesArgv() []string            { return pacmanArgv([]string{"-Qu"}, colourNever, nil) }
func allInstalledArgv() []string       { return pacmanArgv([]string{"-Q"}, colourNever, nil) }
func syncInfoArgv(s []string) []string { return pacmanArgv([]string{"-Si"}, colourNever, s) }
func localInfoArgv(n []string) []string {
	return pacmanArgv([]string{"-Qi"}, colourNever, n)
}

// ---- input -----------------------------------------------------------------

// pkgNames validates and sorts a name list: 1–MaxPkgNames names, each a
// pacman package name (so none starts with '-', has a '/', ':' or a version
// constraint), none twice. It returns why the list is refused, or "".
func pkgNames(verb string, args []string) ([]string, string) {
	if len(args) == 0 || len(args) > MaxPkgNames {
		return nil, fmt.Sprintf("%s takes 1–%d package names, got %d", verb, MaxPkgNames, len(args))
	}
	seen := map[string]bool{}
	for _, a := range args {
		if !namePattern.MatchString(a) {
			return nil, fmt.Sprintf("invalid package name %q", a)
		}
		if seen[a] {
			return nil, fmt.Sprintf("package %q is named twice", a)
		}
		seen[a] = true
	}
	return slices.Sorted(slices.Values(args)), ""
}

// cutFlag reports whether args starts with the one fixed option token flag,
// and the rest. The token is only ever recognised first.
func cutFlag(args []string, flag string) (bool, []string) {
	if len(args) > 0 && args[0] == flag {
		return true, args[1:]
	}
	return false, args
}

func installMode(asDeps bool) string {
	if asDeps {
		return modeInstallAsDeps
	}
	return modeInstall
}

func removeMode(noConfig bool) string {
	if noConfig {
		return modeRemoveN
	}
	return modeRemove
}

// badRequest refuses malformed input: exit 2, reason bad-request.
func (s *session) badRequest(format string, a ...any) int {
	s.reason = ReasonBadRequest
	return s.usage(format, a...)
}

// ---- plans -----------------------------------------------------------------

// pkgFail is a refused or failed plan: the exit code, the machine-readable
// reason ("" for a plain failure), the packages it is about, and the pending
// count for updates-pending.
type pkgFail struct {
	code   int
	reason string
	msg    string
	names  []string
	count  int
}

func failed(format string, a ...any) *pkgFail {
	return &pkgFail{code: ExitFailed, msg: fmt.Sprintf(format, a...)}
}

// finishFail ends the run with f.
func (s *session) finishFail(f *pkgFail) int {
	s.reason, s.names, s.count = f.reason, f.names, f.count
	return s.finish(f.code, f.msg)
}

// installPlan computes (and vets) the plan for installing names. Any
// problem is returned as a *pkgFail.
func (s *session) installPlan(names []string, asDeps bool) (Plan, *pkgFail) {
	r := s.o.Runner
	for _, n := range names {
		if _, _, exit, err := r.Query(queryArgv("-Si", n)); err != nil {
			return Plan{}, failed("pacman -Si: %v", err)
		} else if exit != 0 {
			return Plan{}, &pkgFail{code: ExitUsage, reason: ReasonNotFound, names: []string{n},
				msg: fmt.Sprintf("package %q is not in a sync repository", n)}
		}
	}

	if f := s.checkNoUpdates(); f != nil {
		return Plan{}, f
	}

	installed, f := s.installedVersions()
	if f != nil {
		return Plan{}, f
	}

	out, errOut, exit, err := r.Query(installPlanArgv(names))
	if err != nil {
		return Plan{}, failed("pacman -Sp: %v", err)
	}
	if exit != 0 {
		return Plan{}, failed("could not plan the install: pacman -Sp exited %d: %s", exit, firstLine(errOut))
	}
	var lines []planLine
	seen := map[string]bool{}
	for _, l := range nonEmptyLines(out) {
		fl := strings.Fields(l)
		if len(fl) != 3 || !repoPattern.MatchString(fl[0]) || !namePattern.MatchString(fl[1]) || !versionPattern.MatchString(fl[2]) {
			return Plan{}, failed("unexpected line from pacman -Sp: %q", l)
		}
		if seen[fl[1]] {
			return Plan{}, failed("pacman -Sp lists %s twice", fl[1])
		}
		seen[fl[1]] = true
		if v, ok := installed[fl[1]]; ok && v != fl[2] {
			// an upgrade is the same condition as a pending update
			return Plan{}, &pkgFail{code: ExitStale, reason: ReasonUpdatesPending, names: []string{fl[1]},
				msg: fmt.Sprintf("%s is installed at %s and the repo has %s: update the system first", fl[1], v, fl[2])}
		}
		lines = append(lines, planLine{repo: fl[0], name: fl[1], version: fl[2]})
	}
	slices.SortFunc(lines, func(a, b planLine) int { return strings.Compare(a.name, b.name) })

	var specs []string
	for _, l := range lines {
		specs = append(specs, l.repo+"/"+l.name)
	}
	info, f := s.syncDetails(specs, installed)
	if f != nil {
		return Plan{}, f
	}

	p := Plan{Targets: names, Mode: modeInstall, AsDeps: asDeps}
	var hashItems []string
	for _, l := range lines {
		isTarget := slices.Contains(names, l.name)
		p.Items = append(p.Items, l.name+" "+l.version)
		hashItems = append(hashItems, l.repo+" "+l.name+" "+l.version)
		e := info[l.name]
		p.Rows = append(p.Rows, PkgRow{Name: l.name, Version: l.version, Repo: l.repo, Target: isTarget, Dl: e.dl, Inst: e.inst})
	}
	if n := len(p.Items); n > warnAbove {
		p.Warnings = append(p.Warnings, fmt.Sprintf("pulls in %d packages", n-len(names)))
	}
	p.Hash = PkgPlanHash(installMode(asDeps), names, hashItems)
	return p, nil
}

// checkNoUpdates is the updates-pending rule of every install: a pending
// update blocks it, because installing into a half-updated system is a
// partial upgrade. pkg-install and pkg-install-local share it.
func (s *session) checkNoUpdates() *pkgFail {
	out, errOut, exit, err := s.o.Runner.Query(updatesArgv())
	if err != nil {
		return failed("pacman -Qu: %v", err)
	}
	switch pending := len(nonEmptyLines(out)); {
	case pending > 0:
		return &pkgFail{code: ExitStale, reason: ReasonUpdatesPending, count: pending,
			msg: fmt.Sprintf("%d update(s) are waiting: update the system first", pending)}
	case exit != 0 && exit != 1:
		return failed("pacman -Qu exited %d: %s", exit, firstLine(errOut))
	}
	return nil
}

type planLine struct{ repo, name, version string }

// installedVersions is name → version of every installed package.
func (s *session) installedVersions() (map[string]string, *pkgFail) {
	out, errOut, exit, err := s.o.Runner.Query(allInstalledArgv())
	if err != nil {
		return nil, failed("pacman -Q: %v", err)
	}
	if exit != 0 {
		return nil, failed("pacman -Q exited %d: %s", exit, firstLine(errOut))
	}
	m := map[string]string{}
	for _, l := range nonEmptyLines(out) {
		f := strings.Fields(l)
		if len(f) != 2 || !namePattern.MatchString(f[0]) {
			return nil, failed("unexpected line from pacman -Q: %q", l)
		}
		m[f[0]] = f[1]
	}
	return m, nil
}

// syncDetails reads `pacman -Si` for every planned item (specs are
// "repo/name"): the sizes for the plan's rows, and the vetting. A package
// that replaces or conflicts with an installed one is refused (reason
// conflict): pacman answers "replace?" yes under --noconfirm, which would
// remove an installed package nobody asked to remove.
func (s *session) syncDetails(specs []string, installed map[string]string) (map[string]syncEntry, *pkgFail) {
	info := map[string]syncEntry{}
	if len(specs) == 0 {
		return info, nil
	}
	out, errOut, exit, err := s.o.Runner.Query(syncInfoArgv(specs))
	if err != nil {
		return nil, failed("pacman -Si: %v", err)
	}
	if exit != 0 {
		return nil, failed("could not read the package details: pacman -Si exited %d: %s", exit, firstLine(errOut))
	}
	for _, e := range parseSyncInfo(out) {
		info[e.name] = e
		for _, c := range e.relations {
			if c.name == e.name {
				continue
			}
			if _, ok := installed[c.name]; ok {
				return nil, &pkgFail{code: ExitRefused, reason: ReasonConflict, names: []string{e.name, c.name},
					msg: fmt.Sprintf("%s %s the installed package %s: remove %s first, then install %s.", e.name, c.kind, c.name, c.name, e.name)}
			}
		}
	}
	return info, nil
}

type syncRel struct{ kind, name string }

type syncEntry struct {
	name      string
	relations []syncRel
	dl, inst  int64 // bytes, 0 when unknown
}

// parseSize reads pacman's "12.34 MiB" into bytes (0: unparseable).
func parseSize(v string) int64 {
	f := strings.Fields(v)
	if len(f) != 2 {
		return 0
	}
	n, err := strconv.ParseFloat(f[0], 64)
	if err != nil || n < 0 {
		return 0
	}
	mult := map[string]float64{"B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40}[f[1]]
	return int64(n * mult)
}

// parseSyncInfo reads the Name, Replaces, Conflicts With, Download Size and
// Installed Size fields of `pacman -Si` / `-Qi` (LC_ALL=C, so English
// labels), one entry per package block. Names are cut at a version
// constraint. Continuation lines (leading space) belong to the field above.
func parseSyncInfo(out string) []syncEntry {
	var entries []syncEntry
	cur := -1
	field := ""
	for l := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(l) == "" {
			field = ""
			continue
		}
		var val string
		if l[0] == ' ' || l[0] == '\t' {
			val = l
		} else if k, v, ok := strings.Cut(l, ":"); ok {
			field, val = strings.TrimSpace(k), v
			if field == "Name" {
				entries = append(entries, syncEntry{name: strings.TrimSpace(v)})
				cur = len(entries) - 1
				continue
			}
		} else {
			continue
		}
		if cur < 0 {
			continue
		}
		kind := ""
		switch field {
		case "Replaces":
			kind = "replaces"
		case "Conflicts With":
			kind = "conflicts with"
		case "Download Size":
			entries[cur].dl = parseSize(strings.TrimSpace(val))
			continue
		case "Installed Size":
			entries[cur].inst = parseSize(strings.TrimSpace(val))
			continue
		default:
			continue
		}
		for _, f := range strings.Fields(val) {
			if i := strings.IndexAny(f, "<>="); i >= 0 {
				f = f[:i]
			}
			if f != "" && f != "None" {
				entries[cur].relations = append(entries[cur].relations, syncRel{kind, f})
			}
		}
	}
	return entries
}

// pacman's removal refusal, as its translation catalogues give the English
// msgids (/usr/share/locale/*/LC_MESSAGES/pacman.mo and libalpm.mo; the
// helper runs pacman with LC_ALL=C): stderr gets "error: failed to prepare
// transaction (could not satisfy dependencies)" and stdout one ":: removing
// %s breaks dependency '%s' required by %s" per broken dependency
// (pacman's remove.c prints it with colon_printf).
const (
	pacmanUnsatisfied = "could not satisfy dependencies"
	pacmanBreaks      = "breaks dependency '"
)

var breaksPattern = regexp.MustCompile(`removing (\S+) breaks dependency '[^']*' required by (\S+)`)

// removeRefusal classifies a failed `pacman -Rs --print`: only pacman's
// dependency refusal is required-by, with the packages that need the
// targets parsed from its lines; anything else is a plain failure, with no
// reason.
func removeRefusal(out, errOut string, exit int) *pkgFail {
	all := out + "\n" + errOut
	if !strings.Contains(all, pacmanUnsatisfied) && !strings.Contains(all, pacmanBreaks) {
		return failed("pacman -Rs --print exited %d: %s", exit, firstLine(errOut))
	}
	var names []string
	for _, m := range breaksPattern.FindAllStringSubmatch(all, -1) {
		if n := m[2]; namePattern.MatchString(n) && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	msg := "pacman would not remove it, but its refusal does not say which packages need it: " + firstLine(errOut)
	if len(names) > 0 {
		msg = "pacman would not remove it: still needed by " + strings.Join(names, ", ")
	}
	return &pkgFail{code: ExitRefused, reason: ReasonRequiredBy, msg: msg, names: names}
}

// removePlan computes (and vets) the plan for removing names.
func (s *session) removePlan(names []string, noConfig bool) (Plan, *pkgFail) {
	r := s.o.Runner
	out, errOut, exit, err := r.Query(installedArgv())
	if err != nil {
		return Plan{}, failed("pacman -Qq: %v", err)
	}
	if exit != 0 {
		return Plan{}, failed("pacman -Qq exited %d: %s", exit, firstLine(errOut))
	}
	have := map[string]bool{}
	for _, l := range nonEmptyLines(out) {
		have[l] = true
	}
	for _, n := range names {
		if !have[n] {
			return Plan{}, &pkgFail{code: ExitUsage, reason: ReasonNotInstalled, names: []string{n},
				msg: fmt.Sprintf("package %q is not installed", n)}
		}
	}
	// The protected list comes before the cascade: pacman's own refusal
	// (something still needs the target) would otherwise hide it.
	set, err := s.protectedSet()
	if err != nil {
		return Plan{}, failed("%s", err.Error())
	}
	if f := protectedTargets(set, names); f != nil {
		return Plan{}, f
	}
	out, errOut, exit, err = r.Query(removePlanArgv(names, noConfig))
	if err != nil {
		return Plan{}, failed("pacman %s --print: %v", removeFlag(noConfig), err)
	}
	if exit != 0 {
		return Plan{}, removeRefusal(out, errOut, exit)
	}
	var items, itemNames []string
	for _, l := range nonEmptyLines(out) {
		name, ver, ok := strings.Cut(l, " ")
		if !ok || !namePattern.MatchString(name) || !versionPattern.MatchString(ver) {
			return Plan{}, failed("unexpected line from pacman %s --print: %q", removeFlag(noConfig), l)
		}
		items = append(items, l)
	}
	items = slices.Compact(slices.Sorted(slices.Values(items)))
	for _, it := range items {
		n, _, _ := strings.Cut(it, " ")
		itemNames = append(itemNames, n)
	}
	if f := protectedCascade(set, names, items); f != nil {
		return Plan{}, f
	}
	// installed sizes, for the rows (not hashed)
	sizes := map[string]int64{}
	if len(itemNames) > 0 {
		if qOut, _, qExit, qErr := r.Query(localInfoArgv(itemNames)); qErr == nil && qExit == 0 {
			for _, e := range parseSyncInfo(qOut) {
				sizes[e.name] = e.inst
			}
		}
	}
	p := Plan{Targets: names, Items: items, Mode: modeRemove, WithConfig: noConfig}
	for _, it := range items {
		n, v, _ := strings.Cut(it, " ")
		p.Rows = append(p.Rows, PkgRow{Name: n, Version: v, Target: slices.Contains(names, n), Inst: sizes[n]})
	}
	if n := len(items); n > warnAbove {
		p.Warnings = append(p.Warnings, fmt.Sprintf("removes %d packages", n))
	}
	p.Hash = PkgPlanHash(removeMode(noConfig), names, items)
	return p, nil
}

// ---- verbs -----------------------------------------------------------------

func runPlanInstall(s *session, args []string) int {
	asDeps, rest := cutFlag(args, flagAsDeps)
	names, why := pkgNames("plan-install", rest)
	if why != "" {
		return s.badRequest("%s", why)
	}
	p, f := s.installPlan(names, asDeps)
	if f != nil {
		return s.finishFail(f)
	}
	s.em.Plan(p)
	return s.finish(ExitOK, "")
}

func runPlanRemove(s *session, args []string) int {
	noConfig, rest := cutFlag(args, flagNoConfig)
	names, why := pkgNames("plan-remove", rest)
	if why != "" {
		return s.badRequest("%s", why)
	}
	p, f := s.removePlan(names, noConfig)
	if f != nil {
		return s.finishFail(f)
	}
	s.em.Plan(p)
	return s.finish(ExitOK, "")
}

func runPkgInstall(s *session, args []string) int {
	if len(args) < 2 || !hashPattern.MatchString(args[0]) {
		return s.badRequest("pkg-install takes the plan hash (sha256:<64 hex>) from plan-install, then [--asdeps] and 1–%d package names", MaxPkgNames)
	}
	asDeps, rest := cutFlag(args[1:], flagAsDeps)
	names, why := pkgNames("pkg-install", rest)
	if why != "" {
		return s.badRequest("%s", why)
	}
	return s.pkgRun(args[0], names, false, false, asDeps)
}

func runPkgRemove(s *session, args []string) int {
	if len(args) < 2 || !hashPattern.MatchString(args[0]) {
		return s.badRequest("pkg-remove takes the plan hash (sha256:<64 hex>) from plan-remove, then [-n] and 1–%d package names", MaxPkgNames)
	}
	noConfig, rest := cutFlag(args[1:], flagNoConfig)
	names, why := pkgNames("pkg-remove", rest)
	if why != "" {
		return s.badRequest("%s", why)
	}
	return s.pkgRun(args[0], names, true, noConfig, false)
}

// pkgRun is the root half of both verbs. Like removeOrphans, the recompute
// sits between two checks that pacman isn't running, right before the
// pacman call; a different plan exits 3 with the new plan emitted and
// nothing run. The same residual window applies (docs/HANDOVER.md,
// landmines).
func (s *session) pkgRun(want string, names []string, remove, noConfig, asDeps bool) int {
	verb := "pkg-install"
	if remove {
		verb = "pkg-remove"
	}
	release, code := s.lock()
	if code != ExitOK {
		return code
	}
	defer release()

	busy := func(err error) int {
		s.skip(verb, "pacman is running")
		s.reason = ReasonBusy
		return s.finish(ExitBusy, err.Error()+"; nothing was changed")
	}
	if err := pacmanIdle(s.lockPath()); err != nil {
		return busy(err)
	}
	var p Plan
	var f *pkgFail
	if remove {
		p, f = s.removePlan(names, noConfig)
	} else {
		p, f = s.installPlan(names, asDeps)
	}
	if f != nil {
		return s.finishFail(f)
	}
	if p.Hash != want {
		s.progress(1, 1, "Skipped: the plan changed since confirm")
		s.em.Plan(p) // the current plan, so the frontend can re-confirm
		s.skip(verb, "the plan changed since it was confirmed")
		s.reason = ReasonStale
		return s.finish(ExitStale, "plan changed since confirm; nothing changed")
	}
	if len(p.Items) == 0 {
		s.progress(1, 1, "Nothing to do")
		s.skip(verb, "nothing to do")
		return s.finish(ExitOK, "")
	}
	if err := pacmanIdle(s.lockPath()); err != nil {
		return busy(err)
	}
	label, argv := fmt.Sprintf("Installing %d package(s)", len(p.Items)), installPkgArgv(names, asDeps)
	if remove {
		label, argv = fmt.Sprintf("Removing %d package(s)", len(p.Items)), removePkgArgv(names, noConfig)
	}
	if ok, msg := s.step(1, 1, label, argv); !ok {
		return s.finish(ExitFailed, msg)
	}
	return s.finish(ExitOK, "")
}
