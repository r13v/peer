.PHONY: all build web verify-web test lint fmt run

all: fmt lint test verify-web build

build: web
	go build -trimpath -o peer .

web:
	npm run build:web

verify-web:
	npm run verify

test:
	go test -race ./...

lint:
	golangci-lint run

fmt:
	golangci-lint fmt

run: build
	./peer
