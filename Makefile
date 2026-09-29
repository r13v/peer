.PHONY: all build test lint fmt run

all: fmt lint test build

build:
	go build -trimpath -o peer .

test:
	go test -race ./...

lint:
	golangci-lint run

fmt:
	golangci-lint fmt

run: build
	./peer
