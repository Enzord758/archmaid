# Entry points for the checks CI runs. Run `make all` before pushing.
#
# One Go module at the repo root; the TUI binary is cmd/archmaid (its code internal/tui), the root helper
# cmd/archmaid-helper. The GUI is its own module in gui/ (gotk4 needs cgo and
# GTK, so the root gate never builds it). Paths live ONLY in the variables
# below (and in `env:` in the CI workflow), so update them in that one spot.
TUI_DIR    := cmd/archmaid
E2E_RUN    := $(TUI_DIR)/e2e/run.sh
CHECK      := scripts/check.sh
GUI_DIR    := gui

.PHONY: all gate e2e e2e-cover cover-build perf perf-baseline bench gui gui-test

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

# Coverage beyond `go test` (docs/coverage-map.md; TESTING.md, "Coverage from
# real runs"). e2e-cover: the unit tests and the e2e tapes on a cover binary,
# merged into one number (COVER_GUI=1 adds the GUI's headless tests;
# COVER_OUT=dir keeps the data, default a temp dir). cover-build: the three
# binaries built with -cover into COVER_BIN (default a temp dir), for the
# test VM and the owner's measured runs; never packaged.
e2e-cover:
	scripts/cover-report.sh

cover-build:
	scripts/cover-build.sh "$${COVER_BIN:-$$(mktemp -d "$${TMPDIR:-/tmp}/archmaid-cover-bin.XXXXXX")}"

# Re-measure the plan's perf baselines (benchmarks, perf guard, GUI first
# frame/RAM/throughput, binary sizes) on a quiet machine only: it refuses
# (exit 3) under load, with a VM or another gate running. PERF_COUNT=1
# PERF_FORCE=1 is a smoke test. See scripts/perf-baseline.sh.
perf-baseline:
	scripts/perf-baseline.sh

# Benchmark HEAD (the working tree) against BASE (default: merge-base with
# origin/main) on this machine and fail on a significant slowdown; the CI bench
# job runs the same script. Usage: make bench [BASE=origin/refactor/p0]
BASE ?=
bench:
	scripts/bench-compare.sh $(BASE)

# The GUI module without a display: build, vet, the plain-Go view-model and
# pump tests under -race (they don't build gotk4; the race build of gotk4
# alone would add minutes), the rest without. Needs gtk4, libadwaita, gcc
# and dbus (nullbroadway's tests run headless.sh with a no-op command).
gui:
	cd $(GUI_DIR) && go build ./... && go vet ./... && test -z "$$(gofmt -l .)"
	@# internal/testenv's init would make archmaid-gui look su, sudo, pkexec and pkttyagent up on PATH again.
	@# The list is captured first, so a go list that fails fails here instead of reading as "not linked".
	cd $(GUI_DIR) && deps="$$(go list -deps ./cmd/archmaid-gui)" && test -n "$$deps" && ! printf '%s\n' "$$deps" | grep -qx 'github.com/Enzord758/archmaid/internal/testenv'
	cd $(GUI_DIR) && go test -race -count=1 ./internal/runview ./internal/overview ./internal/logview ./internal/settings ./internal/realdriver ./internal/plural ./internal/polkitagent ./scripts/nullbroadway && go test -count=1 ./...

# `gui` plus the display tests on a private headless display (GTK Broadway on
# gui/scripts/nullbroadway: no compositor, no window on the
# desktop): every action from its Overview row, the logs, Stop, close mid-run, the
# 50k-line throughput proof and first frame/RAM, all leak-checked. Add
# ARCHMAID_GUI_PERF=1 to also enforce the budgets (machine-specific).
gui-test: gui
	$(GUI_DIR)/scripts/headless.sh
