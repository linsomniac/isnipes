.PHONY: build test test-race test-testhooks test-synthetic vet fmt clean run

BIN := isnipes

build:
	@mkdir -p cmd/isnipes/dist
	@if [ -d web/dist ]; then cp -r web/dist/* cmd/isnipes/dist/; fi
	go build -o $(BIN) ./cmd/isnipes

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

# Phase 5: tests that depend on internal/sim test-only mutators
# (SetLivesForTest, ForceKillForTest, ...) are fenced behind the
# `testhooks` build tag so production binaries don't link them.
test-testhooks:
	go test -race -count=1 -tags testhooks ./...

test-synthetic:
	go test -race -count=1 -tags synthetic ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

run: build
	./$(BIN) --addr=:8080

clean:
	rm -f $(BIN)
