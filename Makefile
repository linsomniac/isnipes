.PHONY: build test test-race test-testhooks test-synthetic vet fmt clean run check-frozen

BIN := isnipes

build:
	@mkdir -p cmd/isnipes/dist
	@if [ -d web/dist ]; then cp -r web/dist/* cmd/isnipes/dist/; fi
	go build -o $(BIN) ./cmd/isnipes

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
