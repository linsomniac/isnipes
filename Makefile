.PHONY: build test test-race vet fmt clean run

BIN := isnipes

build:
	@mkdir -p cmd/isnipes/dist
	@if [ -d web/dist ]; then cp -r web/dist/* cmd/isnipes/dist/; fi
	go build -o $(BIN) ./cmd/isnipes

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

run: build
	./$(BIN) --addr=:8080

clean:
	rm -f $(BIN)
