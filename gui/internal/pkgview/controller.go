package pkgview

import (
	"errors"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/steps"
)

// ErrBlocked: an install was asked for while updates are waiting.
var ErrBlocked = errors.New("updates are waiting")

// Gating is whether Install may go on.
type Gating struct {
	Blocked bool
	Pending int    // repo updates waiting
	Text    string // "N repository updates are waiting" (only repo updates block an install; the Overview counts AUR ones too)
}

// Config is the Controller's wiring.
type Config struct {
	Search    *Search
	Catalog   Catalog
	Backend   Backend
	Protected *ProtectedSet
	Clock     Clock
	// Updates reads updates-cache.json; DBAge is the sync DBs' age (< 0:
	// unknown); Changed is called on the main thread after any state
	// change; ShowRun brings the Run page up.
	// Dry says the next run is a dry run; Kept says the GUI keeps the run's
	// first password (both nil: no and no). They word the sheet's password
	// note.
	Dry, Kept func() bool
	Updates   func() overview.Updates
	DBAge     func() time.Duration
	// Reload re-reads the pacman DBs and says whether they had changed; it
	// may parse for a while, so it runs through Go. Go runs f off the main
	// thread and Post delivers a result on it (nil: inline, for tests).
	Reload  func() bool
	Go      func(func())
	Post    func(func())
	Changed func()
	ShowRun func()
}

// Controller joins the page's parts: search, list, installed view, sheet
// and the gate.
type Controller struct {
	Search    *Search
	List      *List
	Sheet     *Sheet
	Installed *InstalledView
	Protected *ProtectedSet

	cfg     Config
	updates overview.Updates
	started bool // the current request's run has started
	direct  bool // the request is ReviewAUR's, not the list's

	reloading, reloadAgain bool
}

// NewController wires a Controller.
func NewController(cfg Config) *Controller {
	if cfg.Clock == nil {
		cfg.Clock = RealClock{}
	}
	if cfg.Protected == nil {
		cfg.Protected = NewAdvisoryProtected()
	}
	if cfg.Go == nil {
		cfg.Go = func(f func()) { f() }
	}
	if cfg.Post == nil {
		cfg.Post = func(f func()) { f() }
	}
	if cfg.Changed == nil {
		cfg.Changed = func() {}
	}
	c := &Controller{Search: cfg.Search, Protected: cfg.Protected, cfg: cfg, Sheet: NewSheet()}
	c.List = NewList(cfg.Protected)
	c.Installed = NewInstalled(cfg.Catalog, cfg.Protected)
	c.RefreshUpdates()
	return c
}

// RefreshUpdates re-reads the updates cache; true when it changed.
func (c *Controller) RefreshUpdates() bool {
	if c.cfg.Updates == nil {
		return false
	}
	u := c.cfg.Updates()
	if u == c.updates {
		return false
	}
	c.updates = u
	c.cfg.Changed()
	return true
}

// Gating is the gate for a run of the given kind: only installs wait for
// updates, a removal never does.
func (c *Controller) Gating(m Mode) Gating {
	if m != ModeInstall || !c.updates.BlocksInstall(c.cfg.Clock.Now()) {
		return Gating{}
	}
	return Gating{Blocked: true, Pending: c.updates.Repo, Text: N(c.updates.Repo, "repository update is", "repository updates are") + " waiting"}
}

// Add puts p in the list (the round + on a row).
func (c *Controller) Add(p pkgdb.Package, m Mode) error {
	err := c.List.Add(p, m)
	c.cfg.Changed()
	return err
}

// Now adds p to the list when it is not there yet and goes straight to the
// sheet for the list ("Install" / "Remove" on a detail pane).
func (c *Controller) Now(p pkgdb.Package, m Mode) error {
	if err := c.List.Add(p, m); err != nil {
		c.cfg.Changed()
		return err
	}
	return c.Review()
}

// Review starts the request for the list: the sheet opens in its planning
// state, the engine plans. An install while updates are waiting does not
// start (ErrBlocked); the page shows the banner.
func (c *Controller) Review() error {
	req, ok := c.List.Request()
	if !ok {
		return errors.New("the list is empty")
	}
	if g := c.Gating(c.List.Mode()); g.Blocked {
		c.cfg.Changed()
		return ErrBlocked
	}
	c.direct = false
	c.begin(req)
	return nil
}

// ErrSheetOpen: ReviewAUR was asked while a review is already up.
var ErrSheetOpen = errors.New("a review is already open")

// ReviewAUR starts the AUR install review of exactly one package, the one
// of a held update (#280): the same request, plan, PKGBUILD review and
// helper install as an AUR install from the page, without touching the
// page's list (a finished run leaves the list as it was). An install waits
// for repository updates like any other (ErrBlocked). The package may be
// installed already: this is its update.
func (c *Controller) ReviewAUR(name string) error {
	if !pkgdb.ValidName(name) {
		return errors.New("not a package name")
	}
	if c.Sheet.Open() {
		return ErrSheetOpen
	}
	if g := c.Gating(ModeInstall); g.Blocked {
		c.cfg.Changed()
		return ErrBlocked
	}
	c.direct = true
	c.begin(steps.PkgRequest{Mode: steps.PkgInstall, Names: []string{name}, AUR: true})
	return nil
}

func (c *Controller) begin(req steps.PkgRequest) {
	c.started = false
	c.Sheet.Begin(req)
	c.syncSheet()
	if c.cfg.Backend != nil {
		c.cfg.Backend.StartPackages(req)
	}
	c.cfg.Changed()
}

func (c *Controller) syncSheet() {
	c.Sheet.Blocked = 0
	if g := c.Gating(ModeInstall); g.Blocked {
		c.Sheet.Blocked = g.Pending
	}
	c.Sheet.Dry = c.cfg.Dry != nil && c.cfg.Dry()
	c.Sheet.Kept = c.cfg.Kept != nil && c.cfg.Kept()
	if c.cfg.DBAge != nil {
		c.Sheet.DBAge = c.cfg.DBAge()
	} else {
		c.Sheet.DBAge = -1
	}
}

// Handle folds an engine event in.
func (c *Controller) Handle(ev Event) {
	switch e := ev.(type) {
	case Refused:
		if e.Refusal != nil && e.Refusal.Reason == steps.RefuseProtected {
			c.Protected.Learn(e.Refusal.Names, e.Refusal.Detail)
		}
	case RunStarted:
		c.started = true
	case Done:
		if c.started && e.OK && !c.direct {
			c.List.Clear()
		}
		if c.started {
			// Any package run that started may have changed the system,
			// whatever its outcome (a failed install can leave packages).
			c.Reload()
		}
		c.started = false
	}
	if _, ok := ev.(Preview); ok {
		c.syncSheet()
	}
	if c.Sheet.Apply(ev) && c.cfg.ShowRun != nil {
		c.cfg.ShowRun()
	}
	c.cfg.Changed()
}

// Confirm is the sheet's Install / Remove.
func (c *Controller) Confirm() bool {
	p, ok := c.Sheet.Confirm()
	if ok && c.cfg.Backend != nil {
		c.cfg.Backend.Answer(p, true)
	}
	c.cfg.Changed()
	return ok
}

// Dismiss is the sheet's Cancel / Close / Escape.
func (c *Controller) Dismiss() {
	p, answer, abort := c.Sheet.Dismiss()
	if c.cfg.Backend != nil {
		if answer {
			c.cfg.Backend.Answer(p, false)
		}
		if abort {
			c.cfg.Backend.Abort()
		}
	}
	c.cfg.Changed()
}

// Retry is "Review again" on a stale sheet.
func (c *Controller) Retry() {
	if req, ok := c.Sheet.Retry(); ok {
		c.begin(req)
	}
}

// UpdateFirst starts the existing topgrade run. The list is kept; the
// sheet closes (a review in progress is answered no).
func (c *Controller) UpdateFirst() {
	if c.Sheet.Open() {
		c.Dismiss()
	}
	if c.cfg.Backend != nil {
		c.cfg.Backend.UpdateSystem()
	}
	c.cfg.Changed()
}

// SetWithConfig sets the removal's "also delete config files" box.
func (c *Controller) SetWithConfig(on bool) {
	if c.List.Mode() == ModeRemove {
		c.List.WithConfig = on
		c.cfg.Changed()
	}
}

// Reload re-reads the pacman DBs off the main thread (after any package run,
// after topgrade or a Full Cleanup, and when the page is shown) and, when
// they had changed, brings the installed view, the results and the open
// detail up to date, keeping the selection. Calls while one is running
// coalesce into one more.
func (c *Controller) Reload() {
	if c.cfg.Reload == nil {
		return
	}
	if c.reloading {
		c.reloadAgain = true
		return
	}
	c.reloading = true
	c.cfg.Go(func() {
		changed := c.cfg.Reload()
		c.cfg.Post(func() {
			c.reloading = false
			if changed {
				c.Installed.Reload()
				c.Search.Refresh()
				c.cfg.Changed()
			}
			if c.reloadAgain {
				c.reloadAgain = false
				c.Reload()
			}
		})
	})
}
