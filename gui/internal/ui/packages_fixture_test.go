package ui

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Enzord758/archmaid/gui/internal/overview"
	"github.com/Enzord758/archmaid/gui/internal/pkgview"
	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/steps"
)

// The Packages page's test fixtures: the demo data (packages_demo.go) and a
// backend that records.

type (
	uiCatalog = demoCatalog
	uiAUR     = demoAUR
)

func pkgFixtures() []pkgdb.Package                                       { return demoPackages() }
func aurFixtures() (hits []pkgdb.Package, info map[string]pkgdb.Package) { return demoAURPackages() }

// uiBackend records what the page asks of the engine.
type uiBackend struct {
	mu      sync.Mutex
	started []steps.PkgRequest
	answers []string
	aborts  int
}

func (b *uiBackend) StartPackages(r steps.PkgRequest) {
	b.mu.Lock()
	b.started = append(b.started, r)
	b.mu.Unlock()
}
func (b *uiBackend) Answer(p int, yes bool) {
	b.mu.Lock()
	b.answers = append(b.answers, answerText(p, yes))
	b.mu.Unlock()
}
func (b *uiBackend) Abort()        { b.mu.Lock(); b.aborts++; b.mu.Unlock() }
func (b *uiBackend) UpdateSystem() {}

func answerText(p int, yes bool) string {
	s := "no"
	if yes {
		s = "yes"
	}
	return strings.Join([]string{"prompt", string(rune('0' + p)), s}, " ")
}

// pkgEnv is the scenario's fixture: made in the Options hook, read by the
// scenario.
type pkgEnv struct {
	cat     *uiCatalog
	aur     *uiAUR
	be      *uiBackend
	clock   *pkgview.FakeClock
	updates overview.Updates
	age     time.Duration
	mu      sync.Mutex
}

var errAURDown = errors.New("aur: dial tcp: no route to host")

func newPkgEnv() *pkgEnv {
	e := &pkgEnv{cat: &uiCatalog{pkgs: pkgFixtures()}, aur: &uiAUR{}, be: &uiBackend{}, clock: pkgview.NewFakeClock(time.Now()), age: 3 * time.Hour}
	e.aur.hits, e.aur.info = aurFixtures()
	return e
}

func (e *pkgEnv) wiring() *PackagesWiring {
	return &PackagesWiring{Catalog: e.cat, AUR: e.aur, Backend: e.be, Clock: e.clock,
		Updates: func() overview.Updates { e.mu.Lock(); defer e.mu.Unlock(); return e.updates },
		DBAge:   func() time.Duration { return e.age }}
}

func (e *pkgEnv) setUpdates(repo int) {
	e.mu.Lock()
	e.updates = overview.Updates{Known: true, Count: repo, Repo: repo, Checked: time.Now().Add(-time.Hour)}
	e.mu.Unlock()
}

func (e *pkgEnv) clearUpdates() {
	e.mu.Lock()
	e.updates = overview.Updates{Known: true, Checked: time.Now()}
	e.mu.Unlock()
}
