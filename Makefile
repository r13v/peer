.PHONY: all build test lint fmt run app test-app

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

# app bundles the Go CLI into Peer.app; it needs macOS and Xcode's Swift.
app: build
	swift build -c release --package-path macos
	rm -rf Peer.app
	mkdir -p Peer.app/Contents/MacOS Peer.app/Contents/Resources
	cp "$$(swift build -c release --package-path macos --show-bin-path)/Peer" Peer.app/Contents/MacOS/Peer
	# Package resources, such as Textual's code highlighter, load from Resources.
	cp -R "$$(swift build -c release --package-path macos --show-bin-path)"/*.bundle Peer.app/Contents/Resources/
	cp peer Peer.app/Contents/Resources/peer
	sed "s/VERSION/$${VERSION:-0.0.0}/g" macos/Info.plist > Peer.app/Contents/Info.plist
	codesign --force --deep --sign - Peer.app

test-app:
	swift test --package-path macos
