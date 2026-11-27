# Local development. Releases are built by GoReleaser (.goreleaser.yaml in the repo root).

.PHONY: build test check clean

BINARY := sram-s3
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
DEFAULT_BROKER_URL ?= https://sram-s3.production.sdp.surf.nl
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.defaultBrokerURL=$(DEFAULT_BROKER_URL)

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

test:
	go test ./...

# Everything to run before a commit: vet, formatting, tests (incl. the bash script), shellcheck.
check:
	go vet ./...
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt: files need formatting"; exit 1; }
	go test ./...
	shellcheck ../install.sh ../scripts/*.sh

clean:
	rm -f $(BINARY)
