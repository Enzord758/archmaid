package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Enzord758/archmaid/internal/proc"
	"github.com/Enzord758/archmaid/internal/sanitize"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Querier runs a short read-only command and returns its stdout
// (proc.Query's shape). Every command the report runs goes through it:
// `pacman -Qqm`, `paccache -dk0 --nocolor` and `pacman-conf CacheDir`, all
// as the user, by name (testenv traps them; tests hand in their own).
type Querier func(ctx context.Context, name string, args ...string) ([]byte, error)

// Config is what a Collector reads. The zero value is not usable: start
// from DefaultConfig.
type Config struct {
	// PacmanLog is the log to read the package changes from.
	PacmanLog string
	Query     Querier
	// Running returns `uname -r`; "" means unknown (no restart verdict).
	Running func() string
	// ModulesDir is where the kernels' module folders are (restart.go).
	ModulesDir string
	// TmpDirs, JournalDirs and UserCacheDir are the targets of the temp
	// files, journal and user cache steps. YayDir returns yay's build
	// folder ("" if unknown, then its area is unknown).
	TmpDirs, JournalDirs []string
	UserCacheDir         string
	YayDir               func() string
	// MeasureEach bounds one walk of a target, MeasureTotal the time spent walking
	// over a whole run (after it every walk is unknown at once); waiting for
	// steps, passwords or confirms is not counted.
	MeasureEach, MeasureTotal time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Defaults for the measuring budget.
const (
	DefaultMeasureEach  = 4 * time.Second
	DefaultMeasureTotal = 20 * time.Second
	// DefaultFinishTimeout is how long a Driver gives Finish.
	DefaultFinishTimeout = 20 * time.Second
)

// Environment overrides, for the e2e harness and the tests of other
// packages, which must never read the machine's own log: when set, they
// replace the default path. They change nothing about what is run.
const (
	EnvPacmanLog  = "ARCHMAID_PACMAN_LOG"
	EnvModulesDir = "ARCHMAID_MODULES_DIR"
)

// DefaultConfig is the real thing: /var/log/pacman.log, the commands by
// name, /proc's kernel release, /tmp and /var/tmp, the journal's folders,
// ~/.cache and yay's build folder.
func DefaultConfig() *Config {
	c := &Config{
		PacmanLog:    DefaultPacmanLog,
		Query:        proc.Query,
		Running:      runningKernel,
		ModulesDir:   DefaultModulesDir,
		TmpDirs:      []string{"/tmp", "/var/tmp"},
		JournalDirs:  []string{"/var/log/journal", "/run/log/journal"},
		UserCacheDir: userCacheDir(),
		YayDir:       yayBuildDir,
	}
	if p := os.Getenv(EnvPacmanLog); p != "" {
		c.PacmanLog = p
	}
	if p := os.Getenv(EnvModulesDir); p != "" {
		c.ModulesDir = p
	}
	return c
}

func runningKernel() string {
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func userCacheDir() string {
	if d := os.Getenv("XDG_CACHE_HOME"); filepath.IsAbs(d) {
		return d
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".cache")
	}
	return ""
}

// yayBuildDir is where yay -Sc cleans: the buildDir of yay's config.json
// when it names one, else yay's default, <cache>/yay.
func yayBuildDir() string {
	cfgHome := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(cfgHome) {
		h, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		cfgHome = filepath.Join(h, ".config")
	}
	if b, err := os.ReadFile(filepath.Join(cfgHome, "yay", "config.json")); err == nil {
		var c struct {
			BuildDir string `json:"buildDir"`
		}
		if json.Unmarshal(b, &c) == nil && c.BuildDir != "" {
			d := os.ExpandEnv(c.BuildDir)
			if rest, ok := strings.CutPrefix(d, "~/"); ok {
				if h, err := os.UserHomeDir(); err == nil {
					d = filepath.Join(h, rest)
				}
			}
			if filepath.IsAbs(d) {
				return d
			}
		}
	}
	if d := userCacheDir(); d != "" {
		return filepath.Join(d, "yay")
	}
	return ""
}

// Collector watches one run and makes its Report. It is told when each step
// starts and ends (the Driver does, from the Session's Effects and Events)
// and measures around the cleaning steps itself. Not safe for concurrent
// use.
type Collector struct {
	cfg     Config
	meas    *measurer
	action  string
	started time.Time
	mark    LogMark
	running string

	dirs     []string
	dirsDone bool

	areas     map[string]*areaState
	stepAreas map[string][]string
	tally     StepTally
	pkgs      bool // a step that changes packages started
	cache     bool // a step that touches the package cache started
	orphan    *orphanPlan
	orphans   *Orphans
	audit     *AuditNote
}

type areaState struct {
	id         string
	before     Size
	haveBefore bool
	bytes      uint64
	known      bool
	done       bool
}

type orphanPlan struct {
	names []string
	total int
}

// New starts watching a run of action that started at started: the log's
// end and the running kernel are noted now.
func New(cfg Config, action string, started time.Time) *Collector {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Query == nil {
		cfg.Query = func(context.Context, string, ...string) ([]byte, error) {
			return nil, fmt.Errorf("no command runner")
		}
	}
	if cfg.MeasureEach <= 0 {
		cfg.MeasureEach = DefaultMeasureEach
	}
	if cfg.MeasureTotal <= 0 {
		cfg.MeasureTotal = DefaultMeasureTotal
	}
	c := &Collector{
		cfg: cfg, action: action, started: started,
		mark:      MarkLog(cfg.PacmanLog),
		areas:     map[string]*areaState{},
		stepAreas: map[string][]string{},
	}
	if cfg.Running != nil {
		c.running = cfg.Running()
	}
	c.meas = &measurer{each: cfg.MeasureEach, total: cfg.MeasureTotal, now: cfg.Now}
	return c
}

// The areas, in the order they are shown, and what each is called.
var areaOrder = []string{AreaPaccache, AreaPaccacheWipe, AreaPkgUninstalled, AreaYay, AreaJournal, AreaTmpfiles, AreaUserCache}

var areaLabel = map[string]string{
	// paccache -r keeps three versions of each package (its default).
	AreaPaccache:       "Package cache (keeps 3)",
	AreaPaccacheWipe:   "Entire package cache",
	AreaPkgUninstalled: "Uninstalled packages' cache",
	AreaYay:            "AUR build cache",
	AreaJournal:        "Journal vacuum",
	AreaTmpfiles:       "Temp files",
	AreaUserCache:      "User cache",
}

// areaGroup says which folders an area is measured on; areas of one group
// run one after the other on the same folders.
func areaGroup(id string) string {
	switch id {
	case AreaPaccache, AreaPaccacheWipe, AreaPkgUninstalled:
		return "pkgcache"
	}
	return id
}

// areasOf are the areas step st cleans. Under sudo, yay -Sc does both
// pacman -Sc and yay's own cleaning; under polkit (--aur) only the latter,
// pacman -Sc being the part IDPkgCacheUninstalled.
func areasOf(st steps.Step) []string {
	switch st.ID {
	case steps.IDPaccache:
		return []string{AreaPaccache}
	case steps.IDPaccacheWipe:
		return []string{AreaPaccacheWipe}
	case steps.IDPkgCacheUninstalled:
		return []string{AreaPkgUninstalled}
	case steps.IDYayCache:
		if slices.Contains(st.Argv, "--aur") {
			return []string{AreaYay}
		}
		return []string{AreaPkgUninstalled, AreaYay}
	case steps.IDTmpfiles:
		return []string{AreaTmpfiles}
	case steps.IDJournal:
		return []string{AreaJournal}
	case steps.IDUserCache:
		return []string{AreaUserCache}
	}
	return nil
}

func (c *Collector) cacheDirs(ctx context.Context) []string {
	if !c.dirsDone {
		c.dirs, c.dirsDone = CacheDirs(ctx, c.cfg.Query), true
	}
	return c.dirs
}

// targets are the folders an area is measured on; none means unknown.
func (c *Collector) targets(ctx context.Context, area string) []string {
	var roots []string
	switch areaGroup(area) {
	case "pkgcache":
		roots = c.cacheDirs(ctx)
	case AreaYay:
		if c.cfg.YayDir != nil {
			roots = []string{c.cfg.YayDir()}
		}
	case AreaTmpfiles:
		roots = c.cfg.TmpDirs
	case AreaJournal:
		roots = c.cfg.JournalDirs
	case AreaUserCache:
		roots = []string{c.cfg.UserCacheDir}
	}
	return slices.DeleteFunc(slices.Clone(roots), func(s string) bool { return s == "" })
}

func (c *Collector) measure(ctx context.Context, area string) Size {
	roots := c.targets(ctx, area)
	if len(roots) == 0 {
		return Size{}
	}
	return c.meas.size(ctx, roots...)
}

// StepStarting is told a step is about to start (its child not yet
// running): the sizes of the folders it will clean are taken now. A step
// with Parts (the root helper's combined call) stands for them all, which
// are measured together, before the call starts; each part's end then
// moves the next part's start to where it left off (StepDone).
func (c *Collector) StepStarting(ctx context.Context, st steps.Step) {
	if len(st.Parts) > 0 {
		for _, p := range st.Parts {
			c.StepStarting(ctx, p)
		}
		return
	}
	switch st.ID {
	case steps.IDTopgrade, steps.IDOrphansRemove, steps.IDRepair:
		c.pkgs = true
	}
	if st.ID == steps.IDOrphansRemove {
		total := len(st.Cascade)
		if total < len(st.Items) {
			total = len(st.Items)
		}
		c.orphan = &orphanPlan{names: slices.Clone(st.Items), total: total}
	}
	ids := areasOf(st)
	c.stepAreas[st.ID] = ids
	for _, id := range ids {
		a := c.areas[id]
		if a == nil {
			a = &areaState{id: id}
			c.areas[id] = a
		}
		a.done = false
		a.before, a.haveBefore = c.measure(ctx, id), true
		if areaGroup(id) == "pkgcache" {
			c.cache = true
		}
	}
}

// StepDone is told how a step ended. A step that ran (or was stopped
// part-way) is measured again, and what it freed is its areas' rows; one
// that didn't run has no row. The next area on the same folders starts
// from where this one ended, so two steps on the package cache don't both
// count the first one's work, nor what happened between them.
func (c *Collector) StepDone(ctx context.Context, id string, status steps.Status) {
	switch status {
	case steps.OK:
		c.tally.Done++
	case steps.Failed:
		c.tally.Failed++
	case steps.Skipped, steps.AuthRefused, steps.Aborted:
		c.tally.Skipped++
	}
	ran := status == steps.OK || status == steps.Failed || status == steps.Aborted
	if id == steps.IDOrphansRemove && status == steps.OK && c.orphan != nil {
		c.orphans = &Orphans{Names: c.orphan.names, Total: c.orphan.total}
	}
	for _, aid := range c.stepAreas[id] {
		a := c.areas[aid]
		if a == nil || a.done {
			continue
		}
		if !ran {
			delete(c.areas, aid)
			continue
		}
		after := c.measure(ctx, aid)
		a.bytes, a.known = Freed(a.before, after)
		a.done = true
		for _, other := range c.areas {
			if !other.done && other.id != aid && areaGroup(other.id) == areaGroup(aid) {
				other.before = after
			}
		}
	}
	delete(c.stepAreas, id)
}

// StepResult is told an in-process step's result: the audit's findings are
// kept for the report.
func (c *Collector) StepResult(res steps.Result) {
	a := res.Audit
	if a == nil {
		return
	}
	n := &AuditNote{Total: a.Total, Findings: len(a.Findings)}
	for _, f := range a.Findings {
		if f.High {
			n.High++
		}
		n.Names = append(n.Names, sanitize.Field(f.Name, 80))
	}
	c.audit = n
}

// FinishInfo is what only the engine knows about the end of the run.
type FinishInfo struct {
	Elapsed time.Duration
	// NotRun counts the steps an abort left pending: skipped in the tally.
	NotRun int
}

// Finish makes the report. ctx bounds what it asks of the system now: the
// log, `pacman -Qqm`, `paccache -dk0` (or the folder sum).
func (c *Collector) Finish(ctx context.Context, fin FinishInfo) Report {
	r := Report{
		Action: c.action, Started: c.started, Finished: c.cfg.Now(), Elapsed: fin.Elapsed,
		Steps: c.tally, Orphans: c.orphans, Audit: c.audit,
	}
	r.Steps.Skipped += fin.NotRun
	var upgraded []Pkg
	if c.pkgs {
		ch, err := ReadLog(c.mark)
		if err != nil {
			r.Notes = append(r.Notes, "The package log couldn't be read, so package changes are not shown.")
		} else {
			p := c.packages(ctx, ch)
			r.Packages, upgraded = p, p.Upgraded
			if p.Partial {
				r.Notes = append(r.Notes, "The package log was rotated during the run: package changes before that are not counted.")
			}
			if !p.AURKnown && len(p.Upgraded)+len(p.Installed) > 0 {
				r.Notes = append(r.Notes, "AUR packages are not marked: pacman -Qqm gave no answer.")
			}
			r.Attention = append(r.Attention, c.fileItems(ch)...)
			if it, ok := c.restartItem(upgraded); ok {
				r.Attention = append([]Attention{it}, r.Attention...)
			}
		}
	}
	for _, id := range areaOrder {
		a := c.areas[id]
		if a == nil || !a.done {
			continue
		}
		row := Area{ID: id, Label: areaLabel[id], Bytes: a.bytes, Known: a.known}
		r.Freed = append(r.Freed, row)
		if a.known {
			r.FreedBytes += a.bytes
		} else {
			r.FreedPartial = true
		}
	}
	if c.cache {
		if h, ok := c.cacheStillHolds(ctx); ok {
			r.Cache = &h
		}
	}
	if n := c.audit; n != nil && n.High > 0 {
		r.Attention = append(r.Attention, Attention{
			Kind: KindAudit, Short: plural(n.High, "AUR warning"),
			Text:   fmt.Sprintf("AUR audit: %s (see the audit log)", plural(n.High, "high-severity finding")),
			Detail: slices.Clone(n.Names),
		})
	}
	return r
}

// packages turns the log's changes into Packages, marking the foreign ones.
func (c *Collector) packages(ctx context.Context, ch LogChanges) *Packages {
	p := &Packages{Upgraded: ch.Upgraded, Installed: ch.Installed, Removed: ch.Removed,
		Downgraded: ch.Downgraded, Partial: ch.Partial}
	if len(p.Upgraded)+len(p.Installed) == 0 {
		p.AURKnown = true // nothing to mark
		return p
	}
	out, err := c.cfg.Query(ctx, "pacman", "-Qqm", "--color", "never")
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit) && exit.ExitCode() == 1 && len(out) == 0:
		// pacman -Qm exits 1 with no output when nothing is foreign.
	default:
		return p
	}
	foreign := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			foreign[l] = true
		}
	}
	p.AURKnown = true
	for i := range p.Upgraded {
		if foreign[p.Upgraded[i].Name] {
			p.Upgraded[i].AUR = true
			p.AURUpgraded++
		}
	}
	for i := range p.Installed {
		if foreign[p.Installed[i].Name] {
			p.Installed[i].AUR = true
			p.AURInstalled++
		}
	}
	return p
}

// restartItem is the "restart recommended" item, if the run calls for one.
func (c *Collector) restartItem(upgraded []Pkg) (Attention, bool) {
	stale, installed, kernels := kernelStale(c.running, c.cfg.ModulesDir, upgraded)
	names := append(slices.Clone(kernels), restartCandidates(upgraded)...)
	if !stale && len(names) == 0 {
		return Attention{}, false
	}
	var text string
	switch {
	case stale && installed != "" && len(names) > 0:
		text = fmt.Sprintf("Restart recommended: %s upgraded (running %s, installed %s)",
			strings.Join(names, ", "), sanitize.Field(c.running, 60), sanitize.Field(installed, 60))
	case stale:
		text = fmt.Sprintf("Restart recommended: the running kernel (%s) was replaced", sanitize.Field(c.running, 60))
	default:
		text = fmt.Sprintf("Restart recommended: %s upgraded", strings.Join(names, ", "))
	}
	return Attention{Kind: KindRestart, Short: "Restart recommended", Text: text, Detail: dedupe(names)}, true
}

// fileItems are the new .pacnew and .pacsave files the log warned about.
func (c *Collector) fileItems(ch LogChanges) []Attention {
	var out []Attention
	add := func(kind, ext, advice string, files []string) {
		if len(files) == 0 {
			return
		}
		shown := files
		if len(shown) > 3 {
			shown = shown[:3]
		}
		text := fmt.Sprintf("%d new %s: %s", len(files), ext, strings.Join(shown, ", "))
		if len(files) > len(shown) {
			text += fmt.Sprintf(" +%d more", len(files)-len(shown))
		}
		text += " (" + advice + ")"
		out = append(out, Attention{Kind: kind, Short: fmt.Sprintf("%d new %s", len(files), ext), Text: text, Detail: slices.Clone(files)})
	}
	add(KindPacnew, ".pacnew", "merge it with pacdiff", ch.Pacnew)
	add(KindPacsave, ".pacsave", "a removed package left your edited config", ch.Pacsave)
	return out
}

func plural(n int, what string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, what)
	}
	return fmt.Sprintf("%d %ss", n, what)
}
