BINARY   := storix
PKG      := github.com/asamgx/storix
VERSION  ?= $(shell git describe --tags 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X $(PKG)/internal/cli.Version=$(VERSION)

.PHONY: build build-nocgo test race lint bench tidy clean

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/storix

build-nocgo:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY)-nocgo ./cmd/storix

test:
	go test ./...

race:
	go test -race ./...

lint:
	golangci-lint run ./...

bench:
	go test -run '^$$' -bench . -benchmem ./...

tidy:
	go mod tidy

clean:
	rm -f $(BINARY) $(BINARY)-nocgo
