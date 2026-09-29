BINARY   := storix
PKG      := github.com/asamgx/storix
VERSION  ?= $(shell git describe --tags 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X $(PKG)/internal/cli.Version=$(VERSION)

.PHONY: build build-nocgo test race lint bench record-probes tidy clean

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

# record-probes re-records the detector fixtures from this machine. Scrub the
# user name out of the result before committing it: the fixtures are read by
# everyone who builds storix.
record-probes: build
	STORIX_RECORD_PROBES=internal/detect/_recorded ./$(BINARY) scan --report --no-cache > /dev/null

tidy:
	go mod tidy

clean:
	rm -f $(BINARY) $(BINARY)-nocgo
