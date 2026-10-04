# Entry points for the checks CI runs. Run `make all` before pushing.
#
# One Go module at the repo root; the TUI is cmd/archmaid, the root helper
# cmd/archmaid-helper. The GUI is its own module in gui/ (gotk4 needs cgo and
# GTK, so the root gate never builds it). Paths live ONLY in the variables
# below (and in `env:` in the CI workflow), so update them in that one spot.
TUI_DIR    := cmd/archmaid
E2E_RUN    := $(TUI_DIR)/e2e/run.sh
CHECK      := scripts/check.sh
GUI_DIR    := gui

.PHONY: all gate e2e perf bench gui gui-test

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

# Benchmark HEAD (the working tree) against BASE (default: merge-base with
# origin/main) on this machine and fail on a significant slowdown; the CI bench
# job runs the same script. Usage: make bench [BASE=origin/refactor/p0]
BASE ?=
bench:
	scripts/bench-compare.sh $(BASE)

# The GUI module without a display: build, vet, the plain-Go view-model and
# pump tests under -race (they don't build gotk4; the race build of gotk4
# alone would add minutes), the rest without. Needs gtk4, libadwaita, gcc.
gui:
	cd $(GUI_DIR) && go build ./... && go vet ./... && test -z "$$(gofmt -l .)"
	cd $(GUI_DIR) && go test -race -count=1 ./internal/runview ./internal/overview ./internal/logview ./internal/settings ./internal/realdriver && go test -count=1 ./...

# `gui` plus the display tests in a private headless weston (no window on the
# desktop): every action from its Overview row, the logs, Stop, close mid-run, the
# 50k-line throughput proof and first frame/RAM, all leak-checked. Add
# ARCHMAID_GUI_PERF=1 to also enforce the budgets (machine-specific).
gui-test: gui
	$(GUI_DIR)/scripts/headless.sh
