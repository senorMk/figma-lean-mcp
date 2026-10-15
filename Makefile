.PHONY: build server plugin release run

VERSION ?= dev

# Build everything: Go server + Figma plugin bundle.
build: server plugin

# Build the Go MCP server binary.
server:
	go build -o bin/figma-lean-mcp ./cmd/figma-lean-mcp

# Build the Figma plugin (dist/code.js + dist/index.html).
plugin:
	cd plugin && npm ci && npm run build

# Build all release packages. Requires Python 3, Go, and Node.js.
release:
	python3 scripts/build_release.py --version "$(VERSION)"

# Run the server directly (serves MCP over stdio; bridge on 127.0.0.1:1996).
run:
	go run ./cmd/figma-lean-mcp
