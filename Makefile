.PHONY: build run test lint loggen simulate-logs

# Ensure Go build cache stays within the workspace sandbox
GOCACHE ?= $(shell pwd)/.gocache

# Versioning
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
LDFLAGS ?= -X logsense/internal/version.Version=$(VERSION) -X logsense/internal/version.Commit=$(COMMIT) -X logsense/internal/version.Date=$(DATE)
RATE ?= 5
FORMAT ?= text,json_lines

build:
	GOCACHE=$(GOCACHE) go build -ldflags "$(LDFLAGS)" -o logsense ./cmd/logsense

run:
	GOCACHE=$(GOCACHE) go run ./cmd/logsense

test:
	GOCACHE=$(GOCACHE) go test ./...

lint:
	GOCACHE=$(GOCACHE) go vet ./...

# Build the log generator helper
loggen:
	GOCACHE=$(GOCACHE) go build -o loggen ./cmd/loggen

# Generate simulated logs into ./simulateddata for multiple formats.
# Usage: make simulate-logs RATE=10 DURATION=30s
# - RATE: messages per second per format (default 5)
# - DURATION: optional run duration like 30s, 2m (empty runs until interrupted)

simulate-logs:
	@mkdir -p simulateddata
	@DFLAG=""; \
	if [ -n "$(DURATION)" ]; then DFLAG="--duration $(DURATION)"; fi; \
	GOCACHE=$(GOCACHE) go run ./cmd/loggen --formats $(FORMAT) --rate $(RATE) $$DFLAG

# Stream a single format to stdout for piping into logsense.
# Usage: make simulate-stdin FORMAT=json_lines RATE=10 DURATION=10s | ./logsense
simulate-stdin:
	@if [ -z "$(FORMAT)" ]; then echo "FORMAT is required (text|json_lines)" >&2; exit 2; fi
	@DFLAG=""; \
	if [ -n "$(DURATION)" ]; then DFLAG="--duration $(DURATION)"; fi; \
	GOCACHE=$(GOCACHE) go run ./cmd/loggen --format $(FORMAT) --stdout --rate $(RATE) $$DFLAG
