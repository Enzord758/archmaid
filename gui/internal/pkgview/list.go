package pkgview

import (
	"errors"
	"fmt"
	"slices"

	"github.com/Enzord758/archmaid/internal/pkgdb"
	"github.com/Enzord758/archmaid/internal/steps"
)

// Mode is what the list holds: installs or removals, never both (owner
// decision 1: install and remove are separate runs).
type Mode int

const (
	ModeNone Mode = iota
	ModeInstall
	ModeRemove
)

func (m Mode) String() string {
	switch m {
	case ModeInstall:
		return "install"
	case ModeRemove:
		return "remove"
	}
	return "none"
}

// MaxNames is the helper's limit of names per run (contract section 3).
const MaxNames = 32

// AddKind is why a package was not added.
type AddKind int

const (
	AddOtherMode        AddKind = iota // the list holds the other kind
	AddAlreadyInstalled                // install: it is installed (updates come with the system)
	AddNotInstalled                    // remove: it is not installed
	AddProtected                       // remove: system-critical
	AddOrphan                          // remove: orphans are Full Cleanup's
	AddFull                            // MaxNames reached
)

// AddError is a refused Add; its text is what the page says.
type AddError struct {
	Kind   AddKind
	Name   string
	Reason string
}

func (e *AddError) Error() string { return e.Reason }

// ErrKind is e's Kind when err is an *AddError, else -1.
func ErrKind(err error) AddKind {
	var ae *AddError
	if errors.As(err, &ae) {
		return ae.Kind
	}
	return -1
}

// Item is one entry of the list.
type Item struct {
	Name    string
	Version string
	Repo    string
	AUR     bool
	Size    int64 // download (install) or installed size (remove), 0 if unknown
}

// List is the install/removal tray.
type List struct {
	mode       Mode
	items      []Item
	WithConfig bool // removals only: also delete the config files (-Rns)
	prot       Protector
}

// NewList makes an empty list; prot (may be nil) greys protected packages.
func NewList(prot Protector) *List { return &List{prot: prot} }

// Mode is what the list holds now (ModeNone when empty).
func (l *List) Mode() Mode { return l.mode }

// Len is the number of entries (the sidebar's badge).
func (l *List) Len() int { return len(l.items) }

// Items are the entries in the order they were added.
func (l *List) Items() []Item { return slices.Clone(l.items) }

// Names are the entries' names in order.
func (l *List) Names() []string {
	out := make([]string, len(l.items))
	for i, it := range l.items {
		out[i] = it.Name
	}
	return out
}

// Has reports whether name is in the list.
func (l *List) Has(name string) bool {
	return slices.ContainsFunc(l.items, func(it Item) bool { return it.Name == name })
}

// HasAUR reports whether an AUR package is in the list.
func (l *List) HasAUR() bool {
	return slices.ContainsFunc(l.items, func(it Item) bool { return it.AUR })
}

// CanAdd says whether p could be added in mode, and if not why; it
// changes nothing.
func (l *List) CanAdd(p pkgdb.Package, mode Mode) error {
	if l.mode != ModeNone && l.mode != mode && len(l.items) > 0 {
		return &AddError{AddOtherMode, p.Name, otherModeText(l.mode)}
	}
	switch mode {
	case ModeInstall:
		if p.Installed {
			return &AddError{AddAlreadyInstalled, p.Name, "Already installed. Updates come with the system."}
		}
	case ModeRemove:
		if !p.Installed {
			return &AddError{AddNotInstalled, p.Name, "Not installed."}
		}
		if p.Orphan {
			return &AddError{AddOrphan, p.Name, "Orphans are removed by Full Cleanup."}
		}
		if l.prot != nil {
			if why, ok := l.prot.Protected(p.Name); ok {
				return &AddError{AddProtected, p.Name, why}
			}
		}
	}
	if !l.Has(p.Name) && len(l.items) >= MaxNames {
		return &AddError{AddFull, p.Name, fmt.Sprintf("The list holds at most %d packages.", MaxNames)}
	}
	return nil
}

func otherModeText(m Mode) string {
	if m == ModeInstall {
		return "The list holds installs. Install and remove are separate runs: review or clear it first."
	}
	return "The list holds removals. Install and remove are separate runs: review or clear it first."
}

// Add puts p in the list. A name already there is not added twice (no
// error). The size is the download size for an install, the installed size
// for a removal.
func (l *List) Add(p pkgdb.Package, mode Mode) error {
	if err := l.CanAdd(p, mode); err != nil {
		return err
	}
	if l.Has(p.Name) {
		return nil
	}
	it := Item{Name: p.Name, Version: p.Version, Repo: p.Repo, AUR: p.Repo == pkgdb.RepoAUR}
	if mode == ModeRemove {
		it.Size = p.InstalledSize
		if p.InstalledVersion != "" {
			it.Version = p.InstalledVersion
		}
	} else {
		it.Size = p.DownloadSize
	}
	l.mode = mode
	l.items = append(l.items, it)
	return nil
}

// Remove takes name out of the list.
func (l *List) Remove(name string) {
	l.items = slices.DeleteFunc(l.items, func(it Item) bool { return it.Name == name })
	if len(l.items) == 0 {
		l.mode, l.WithConfig = ModeNone, false
	}
}

// Clear empties the list.
func (l *List) Clear() { l.items, l.mode, l.WithConfig = nil, ModeNone, false }

// Request is the engine request for the list as it is: install, or remove
// (-Rs; -Rns with WithConfig). Never both, never -Rc or -Rdd.
func (l *List) Request() (steps.PkgRequest, bool) {
	if len(l.items) == 0 {
		return steps.PkgRequest{}, false
	}
	return RequestFor(l.mode, l.Names(), l.WithConfig), true
}

// RequestFor builds a request: WithConfig only means something for a removal.
func RequestFor(mode Mode, names []string, withConfig bool) steps.PkgRequest {
	r := steps.PkgRequest{Names: slices.Clone(names)}
	if mode == ModeRemove {
		r.Mode, r.WithConfig = steps.PkgRemove, withConfig
	} else {
		r.Mode = steps.PkgInstall
	}
	return r
}

// Total is the sum of the entries' sizes.
func (l *List) Total() int64 {
	var n int64
	for _, it := range l.items {
		n += it.Size
	}
	return n
}

// Protector says whether a package may not be removed, and why.
type Protector interface {
	Protected(name string) (reason string, ok bool)
}

// ProtectedSet is the page's view of the protected list. The helper's list
// is the authority (it also blocks through the cascade); this one holds a
// short advisory core, so the common cases are greyed at once, and every
// name a refusal named (Learn), so a package the helper protected stays
// greyed for the rest of the session.
type ProtectedSet struct {
	names map[string]string
}

// ProtectedReason is the text of an advisory entry.
const ProtectedReason = "System-critical: archmaid does not remove it."

// NewAdvisoryProtected is the short core list.
func NewAdvisoryProtected() *ProtectedSet {
	s := &ProtectedSet{names: map[string]string{}}
	for _, n := range []string{"base", "filesystem", "glibc", "pacman", "systemd", "linux", "bash", "coreutils"} {
		s.names[n] = ProtectedReason
	}
	return s
}

// Protected implements Protector.
func (s *ProtectedSet) Protected(name string) (string, bool) {
	r, ok := s.names[name]
	return r, ok
}

// Learn adds names the helper refused as protected, with its reason.
func (s *ProtectedSet) Learn(names []string, reason string) {
	if reason == "" {
		reason = ProtectedReason
	}
	for _, n := range names {
		s.names[n] = reason
	}
}
