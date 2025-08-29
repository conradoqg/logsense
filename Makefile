.PHONY: build run test lint

build:
	go build -o logsense ./cmd/logsense

run:
	go run ./cmd/logsense

test:
	go test ./...

lint:
	go vet ./...

