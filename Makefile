GO ?= $(shell command -v go 2>/dev/null || echo .tools/go/bin/go)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/virtbase/proxbase/internal/cli.version=$(VERSION)

.PHONY: build test lint fmt schema clean

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

clean:
	rm -rf bin dist
