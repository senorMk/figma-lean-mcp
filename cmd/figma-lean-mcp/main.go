// figma-lean-mcp — a minimal, token-efficient Figma MCP server.
//
// It exposes ONE powerful primitive (run_code) plus a tiny set of read/export
// tools, and relays them to a Figma plugin over a single WebSocket bridge.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/mark3labs/mcp-go/server"

	"github.com/senorMk/figma-lean-mcp/internal"
)

var version = "0.1.0"

func main() {
	host := flag.String("host", "127.0.0.1", "host/IP for the plugin WebSocket bridge")
	port := flag.Int("port", 1996, "port for the plugin WebSocket bridge")
	flag.Parse()

	logger := log.New(os.Stderr, "", 0)
	addr := fmt.Sprintf("%s:%d", *host, *port)

	bridge := internal.NewBridge()
	if err := bridge.Start(addr); err != nil {
		logger.Fatalf("bridge start: %v", err)
	}

	s := server.NewMCPServer("figma-lean-mcp", version)
	internal.RegisterTools(s, bridge)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		logger.Printf("shutting down...")
		bridge.Stop()
	}()

	logger.Printf("figma-lean-mcp %s — bridge on %s, serving MCP over stdio", version, addr)
	if err := server.ServeStdio(s); err != nil {
		logger.Fatalf("mcp serve: %v", err)
	}
}
