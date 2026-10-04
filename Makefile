# Entry points for the checks CI runs. Run `make all` before pushing.
#
# The TUI lives in cmd/archmaid/ (its own module until M2): the module and
# helper paths live ONLY in the variables below (and in `env:` in the CI
# workflow), so update them in that one spot.
TUI_DIR    := cmd/archmaid
HELPER_DIR := cmd/archmaid-helper
E2E_RUN    := $(TUI_DIR)/e2e/run.sh
CHECK      := scripts/check.sh

.PHONY: all gate helper-test e2e perf

all: gate helper-test

# gofmt, vet, go test -race -count=3, coverage floors.
gate:
	$(CHECK)

# The helper is its own module, so check.sh does not cover it.
helper-test:
	cd $(HELPER_DIR) && go test -race ./...
	cd $(HELPER_DIR) && go vet ./...
	@out="$$(cd $(HELPER_DIR) && gofmt -l .)"; \
	if [ -n "$$out" ]; then echo "FAIL: not gofmt-clean:"; echo "$$out"; exit 1; fi

# VHS end-to-end tapes (needs vhs, ttyd, ffmpeg).
e2e:
	bash $(E2E_RUN)

# Opt-in perf guard; check.sh ignores the variable until it learns about it.
perf:
	ARCHMAID_PERF_GUARD=1 $(CHECK)
