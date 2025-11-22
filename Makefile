# Determine root directory
ROOT_DIR=$(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))

# Gather all .go files for use in dependencies below
GO_FILES=$(shell find $(ROOT_DIR) -name '*.go')

# Gather list of expected binaries
BINARIES=nview

# Extract Go module name from go.mod
GOMODULE=$(shell grep ^module $(ROOT_DIR)/go.mod | awk '{ print $$2 }')

# Set version strings: use env vars if set, else git
VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null)
COMMIT_HASH ?= $(shell git rev-parse --short HEAD)
GO_LDFLAGS=-ldflags "-s -w -X '$(GOMODULE)/internal/version.Version=$(VERSION)' -X '$(GOMODULE)/internal/version.CommitHash=$(COMMIT_HASH)'"

.PHONY: build mod-tidy clean test

all: format build

# Alias for building program binary
build: $(BINARIES)

mod-tidy:
	# Needed to fetch new dependencies and add them to go.mod
	go mod tidy

clean:
	rm -f $(BINARIES)

format: mod-tidy
	go fmt ./...
	gofmt -s -w $(GO_FILES)

golines:
	golines -w --ignore-generated --chain-split-dots --max-len=80 --reformat-tags .

test: mod-tidy
	go test -v -race ./...

# Build our program binaries
# Depends on GO_FILES to determine when rebuild is needed
$(BINARIES): mod-tidy $(GO_FILES)
	CGO_ENABLED=0 go build \
		$(GO_LDFLAGS) \
		-o $(@) .
