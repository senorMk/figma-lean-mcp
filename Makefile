.PHONY: build server plugin run

# Build everything: Go server + Figma plugin bundle.
build: server plugin

# Build the Go MCP server binary.
server:
	go build -o bin/figma-lean-mcp ./cmd/figma-lean-mcp

# Build the Figma plugin (dist/code.js + dist/index.html).
plugin:
	cd plugin && npm install && npm run build

# Run the server directly (serves MCP over stdio; bridge on 127.0.0.1:1996).
run:
	go run ./cmd/figma-lean-mcp
