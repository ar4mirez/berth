# Day-to-day targets. Toolchain versions come from mise.toml (run `mise install` first).
PKG      := github.com/ar4mirez/berth/internal/version
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)

.PHONY: all build test integration vet lint vuln snapshot tidy check clean

all: check build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o berth ./cmd/berth

test:
	go test -race ./...

# Needs a Docker daemon (CI: integration.yml).
integration:
	go test -tags integration -count=1 -v ./test/integration/...

vet:
	go vet ./...

lint:
	golangci-lint run

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

snapshot:
	goreleaser release --snapshot --clean

tidy:
	GOFLAGS=-mod=mod go mod tidy

# What CI runs, minus govulncheck and the snapshot.
check: vet test lint

clean:
	rm -rf berth dist
