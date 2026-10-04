# Entry points for the checks CI runs. Run `make all` before pushing.
#
# One Go module at the repo root; the TUI is cmd/archmaid, the root helper
# cmd/archmaid-helper. Paths live ONLY in the variables below (and in `env:`
# in the CI workflow), so update them in that one spot.
TUI_DIR    := cmd/archmaid
E2E_RUN    := $(TUI_DIR)/e2e/run.sh
CHECK      := scripts/check.sh

.PHONY: all gate e2e perf

all: gate

# gofmt, vet, go test -race -count=3 (TUI and helper), coverage floors.
gate:
	$(CHECK)

# VHS end-to-end tapes (needs vhs, ttyd, ffmpeg).
e2e:
	bash $(E2E_RUN)

# The whole gate plus the opt-in perf guard (TestPerfGuard, without -race or
# coverage); check.sh runs it when ARCHMAID_PERF_GUARD=1. Its thresholds are
# the dev machine's, which is why the default gate leaves it out.
perf:
	ARCHMAID_PERF_GUARD=1 $(CHECK)
