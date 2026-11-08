SHELL := /bin/zsh
BIN := bin
GOFLAGS := -trimpath

.PHONY: build test race bench vet sim demo pdf clean claims ci

build:
	@mkdir -p $(BIN)
	go build $(GOFLAGS) -o $(BIN)/bales-gateway ./cmd/gateway
	go build $(GOFLAGS) -o $(BIN)/bales-mockworker ./cmd/mockworker
	go build $(GOFLAGS) -o $(BIN)/bales-simulate ./cmd/simulate
	go build $(GOFLAGS) -o $(BIN)/bales-calibrate ./cmd/calibrate

test:
	go test ./...

race:
	go test -race ./...

bench:
	go test -run '^$$' -bench . -benchmem ./internal/...

vet:
	go vet ./...

claims:
	python3 scripts/check_claims.py

ci: vet test claims

sim: build
	$(BIN)/bales-simulate -exp all -out evidence/runs

demo: build
	./scripts/demo.sh

pdf:
	python3 scripts/build_docs.py

clean:
	rm -rf $(BIN) reports/.build archive/.build
