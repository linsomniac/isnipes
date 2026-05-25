.PHONY: build test test-race test-testhooks test-synthetic test-load vet fmt clean run check-frozen

BIN := isnipes

# PHASE8.md §10 — production build: web bundle → embed → single binary.
# npm ci (lockfile-pinned, no install fallback) keeps the build reproducible.
# BOTH output dirs are wiped before (re)building so no stale/obsolete file
# (e.g. a retired chunk left in web/dist) can be embedded and then served
# at / from the binary.
build:
	rm -rf web/dist
	npm -C web ci
	npm -C web run build
	@mkdir -p cmd/isnipes/dist
	@find cmd/isnipes/dist -mindepth 1 ! -name .gitkeep -delete
	cp -r web/dist/* cmd/isnipes/dist/
	go build -tags embed -o $(BIN) ./cmd/isnipes

test:
	go test -count=1 ./...

# The CI race gate also enforces the frozen-file guard (DoD #2) so a
# frozen-schema/sim edit fails the standard CI command, not just an
# operator who remembers `make check-frozen`.
test-race: check-frozen
	go test -race -count=1 ./...

# Phase 5: tests that depend on internal/sim test-only mutators
# (SetLivesForTest, ForceKillForTest, ...) are fenced behind the
# `testhooks` build tag so production binaries don't link them.
test-testhooks:
	go test -race -count=1 -tags testhooks ./...

test-synthetic:
	go test -race -count=1 -tags synthetic ./...

# PHASE8.md DoD #3-#6 — PR-gating smoke load (16 players × 60s; P99 tick
# < 10ms; bandwidth ≤ 12 KB/s; no goroutine leak). Its own step so it does
# not slow the default unit run. Use `go test -short` locally for a 5s pass.
test-load:
	go test -race -count=1 -tags loadtest ./internal/loadtest/...

# PHASE7.md DoD #2 — fail if a frozen wire-schema / sim file changed.
check-frozen:
	@bash scripts/check-frozen.sh

vet:
	go vet ./...

fmt:
	gofmt -w .

run: build
	./$(BIN) --addr=:8080

clean:
	rm -f $(BIN)
