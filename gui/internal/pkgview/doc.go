// Package pkgview is the Packages page's model: plain Go, no gotk4. It holds
// the search state (debounce, the repo and AUR merge), the install/remove
// list, the confirm sheet's state machine, the updates-pending gate and the
// installed view. The gotk4 views in gui/internal/ui only draw what it says.
//
// Threading: every method runs on one goroutine, the GTK main thread in the
// app. Work that may take time (a repo search, an AUR request) runs through
// Deps.Go and comes back through Deps.Post, so a test makes both inline and
// drives the clock by hand (FakeClock).
//
// Seams (docs/design/packages-contract.md): Catalog is the part of
// *pkgdb.DB the page reads, aursearch.Searcher is the AUR client, Backend is
// what the engine's StartPackages/Confirm/Start(topgrade) will sit behind,
// and Event is the engine's Packages* events, translated by one adapter in
// the ui package once the engine lands. Every string shown is plain text.
package pkgview
