.PHONY: all build test lint vet run clean tidy

BIN     := bin/gatewayd
PKG     := ./cmd/gatewayd
CONFIG  := configs/config.example.yaml
GOFLAGS := -trimpath
LDFLAGS := -s -w

all: build

build:
	@mkdir -p bin
	go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BIN) $(PKG)

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run

tidy:
	go mod tidy

run: build
	./$(BIN) -config $(CONFIG)

clean:
	rm -rf bin