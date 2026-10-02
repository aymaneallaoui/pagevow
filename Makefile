MODULE   := github.com/aymaneallaoui/pagevow
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -s -w \
	-X $(MODULE)/internal/version.version=$(VERSION) \
	-X $(MODULE)/internal/version.commit=$(COMMIT) \
	-X $(MODULE)/internal/version.date=$(DATE)

.PHONY: build test lint fmt fmt-check vet check install release-snapshot

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/pagevow ./cmd/pagevow

test:
	go test -race ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -s -w .

fmt-check:
	@out="$$(gofmt -s -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...
	go vet -tags browser ./...

check: fmt-check vet lint test

install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/pagevow

release-snapshot:
	goreleaser release --snapshot --clean
