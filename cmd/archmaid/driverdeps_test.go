package main

// The E7b wiring rehearsal: driver.Deps built the way the TUI will build
// them once internal/driver replaces the syncDriver. Test-only for now;
// TestTranscriptRealDriver runs every scenario through it.

import (
	"context"

	"github.com/Enzord758/archmaid/internal/driver"
	"github.com/Enzord758/archmaid/internal/privilege"
	"github.com/Enzord758/archmaid/internal/steps"
)

// auditPrefetch is the prefetched AUR audit as a driver.Prefetch.
type auditPrefetch struct{ *auditFetch }

func (p auditPrefetch) Run(ctx context.Context) steps.Result { return p.run(ctx) }

// startPrefetch starts the named prefetch, or returns nil for a name the
// TUI doesn't know (driver.Deps.Prefetch).
func startPrefetch(name string) driver.Prefetch {
	if name != steps.PrefetchAURAudit {
		return nil
	}
	return auditPrefetch{startAuditFetch()}
}

// tuiDriverDeps is driver.Deps as the TUI wires them: sudo with the
// askpass Server's wrapper (startAskpassServer sets askpassWrapper, which
// privileged reads), the log store, the TUI's kill grace and pipe wait,
// the Server's requests and the audit prefetch. srv may be nil (askpass
// couldn't start): sudo steps then run `sudo -n`.
func tuiDriverDeps(srv *privilege.Server) driver.Deps {
	deps := driver.Deps{
		Priv:      privileged(),
		Log:       logs(),
		KillGrace: sigkillGrace,
		WaitDelay: stepWaitDelay,
		Prefetch:  startPrefetch,
	}
	if srv != nil {
		deps.Askpass = srv.Requests()
	}
	return deps
}
