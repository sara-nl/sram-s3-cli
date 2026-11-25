# Local development. Releases are built by GoReleaser (.goreleaser.yaml in the repo root).

.PHONY: build test clean

BINARY := sram-s3
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
DEFAULT_BROKER_URL ?= https://sram-s3.production.sdp.surf.nl
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.defaultBrokerURL=$(DEFAULT_BROKER_URL)

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

test:
	go test ./...

clean:
	rm -f $(BINARY)
