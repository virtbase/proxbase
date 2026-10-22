GO ?= $(shell command -v go 2>/dev/null || echo .tools/go/bin/go)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/virtbase/proxbase/internal/cli.version=$(VERSION)

.PHONY: build test lint fmt schema packaging clean

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/proxbase ./cmd/proxbase

test:
	$(GO) test -race ./...

lint:
	golangci-lint run

fmt:
	$(GO) fmt ./...

# The committed schema is published at https://proxbase.virtbase.com/schema/;
# TestSchemaFile fails when it is out of date.
schema:
	$(GO) run ./cmd/proxbase config schema > schema/v1alpha1/cluster.json

# Man pages, shell completions and the AppStream metainfo that archives and packages
# ship in build/; GoReleaser runs the same before its builds.
packaging:
	$(GO) run ./cmd/gendist -version $(VERSION:v%=%) build

clean:
	rm -rf bin dist build
