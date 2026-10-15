package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/senorMk/figma-lean-mcp/internal"
)

// Run the real entry point in a separate process, including under go test -race.
func TestServerProcess(t *testing.T) {
	if os.Getenv("FIGMA_LEAN_MCP_TEST_PROCESS") != "1" {
		t.Skip("used by the process tests")
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		t.Fatal("server arguments are missing")
	}
	os.Args = append([]string{os.Args[0]}, os.Args[separator+1:]...)
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	main()
	os.Exit(0)
}

func TestPortConflictKeepsFirstServerUsable(t *testing.T) {
	addr := unusedAddress(t)
	first := startServer(t, addr)
	first.initialize(t)
	waitForConnection(t, addr, false)
	plugin := connectPlugin(t, addr)
	waitForConnection(t, addr, true)
	assertPages(t, first)

	second := startServer(t, addr)
	assertBindFailure(t, second, addr)
	waitForConnection(t, addr, true)
	assertPages(t, first)

	plugin.Close(websocket.StatusNormalClosure, "test disconnect")
	waitForConnection(t, addr, false)
	result := first.getPages(t)
	if !result.IsError || !strings.Contains(fmt.Sprint(result.Content), "plugin not connected") {
		t.Fatalf("unexpected result after plugin disconnect: %+v", result)
	}
	connectPlugin(t, addr)
	waitForConnection(t, addr, true)
	assertPages(t, first)
	first.stop(t)
}

func TestConcurrentProcessesHaveOneListenerOwner(t *testing.T) {
	addr := unusedAddress(t)
	// Start both processes before waiting for either process to become ready.
	first := startServer(t, addr)
	second := startServer(t, addr)
	var owner, rejected *serverProcess
	select {
	case <-first.done:
		owner, rejected = second, first
	case <-second.done:
		owner, rejected = first, second
	case <-time.After(5 * time.Second):
		t.Fatal("neither process rejected the occupied port")
	}
	assertBindFailure(t, rejected, addr)
	owner.initialize(t)
	waitForConnection(t, addr, false)
	connectPlugin(t, addr)
	waitForConnection(t, addr, true)
	assertPages(t, owner)
	owner.stop(t)
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

type serverProcess struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stderr   lockedBuffer
	messages chan []byte
	done     chan struct{}
	waitErr  error
	nextID   int
}

func startServer(t *testing.T, addr string) *serverProcess {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	p := &serverProcess{messages: make(chan []byte, 16), done: make(chan struct{})}
	p.cmd = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestServerProcess$", "--", "--host", host, "--port", port)
	p.cmd.Env = append(os.Environ(), "FIGMA_LEAN_MCP_TEST_PROCESS=1")
	p.cmd.Stderr = &p.stderr
	p.stdin, err = p.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			p.messages <- bytes.Clone(scanner.Bytes())
		}
		close(p.messages)
		p.waitErr = p.cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() {
		p.stdin.Close()
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
			p.cmd.Process.Kill()
			<-p.done
		}
	})
	return p
}

func (p *serverProcess) call(t *testing.T, method string, params any) json.RawMessage {
	t.Helper()
	p.nextID++
	if err := json.NewEncoder(p.stdin).Encode(map[string]any{
		"jsonrpc": "2.0", "id": p.nextID, "method": method, "params": params,
	}); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-p.messages:
			if !ok {
				t.Fatalf("server closed stdout: %s", p.stderr.String())
			}
			var response struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if err := json.Unmarshal(line, &response); err != nil {
				t.Fatal(err)
			}
			if response.ID == p.nextID {
				if len(response.Error) != 0 {
					t.Fatalf("MCP error: %s", response.Error)
				}
				return response.Result
			}
		case <-timer.C:
			t.Fatalf("MCP %s timed out: %s", method, p.stderr.String())
		}
	}
}

func (p *serverProcess) initialize(t *testing.T) {
	t.Helper()
	p.call(t, "initialize", map[string]any{
		"protocolVersion": "2024-11-05", "capabilities": map[string]any{},
		"clientInfo": map[string]string{"name": "port-conflict-test", "version": "1"},
	})
	if err := json.NewEncoder(p.stdin).Encode(map[string]string{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	}); err != nil {
		t.Fatal(err)
	}
}

func (p *serverProcess) getPages(t *testing.T) mcp.CallToolResult {
	t.Helper()
	var result mcp.CallToolResult
	if err := json.Unmarshal(p.call(t, "tools/call", map[string]any{
		"name": "get_pages", "arguments": map[string]any{},
	}), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func (p *serverProcess) stop(t *testing.T) {
	t.Helper()
	p.stdin.Close()
	select {
	case <-p.done:
		if p.waitErr != nil {
			t.Fatalf("server shutdown failed: %v\n%s", p.waitErr, p.stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop after stdin closed")
	}
}

func assertBindFailure(t *testing.T, p *serverProcess, addr string) {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit after the bind failure")
	}
	if p.waitErr == nil || p.cmd.ProcessState.ExitCode() <= 0 {
		t.Fatalf("expected a nonzero exit status, got %v", p.waitErr)
	}
	stderr := p.stderr.String()
	for _, text := range []string{addr, "bridge start:", "Another process may", "--port", "same host and port"} {
		if !strings.Contains(stderr, text) {
			t.Errorf("stderr does not contain %q: %s", text, stderr)
		}
	}
	for _, text := range []string{"[bridge] listening", "serving MCP over stdio", "plugin not connected"} {
		if strings.Contains(stderr, text) {
			t.Errorf("stderr contains a false startup message %q: %s", text, stderr)
		}
	}
	for message := range p.messages {
		t.Errorf("failed startup wrote to MCP stdout: %s", message)
	}
}

func unusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	return addr
}

func waitForConnection(t *testing.T, addr string, connected bool) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + addr + "/ping")
		if err == nil {
			var health struct {
				Status    string `json:"status"`
				Connected bool   `json:"connected"`
			}
			err = json.NewDecoder(response.Body).Decode(&health)
			response.Body.Close()
			if err == nil && response.StatusCode == http.StatusOK && health.Status == "ok" && health.Connected == connected {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("bridge %s did not report connected=%t", addr, connected)
}

func connectPlugin(t *testing.T, addr string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	conn, _, err := websocket.Dial(ctx, "ws://"+addr+"/ws", nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var request internal.BridgeRequest
			if err := wsjson.Read(ctx, conn, &request); err != nil {
				return
			}
			response := internal.BridgeResponse{Type: request.Type, RequestID: request.RequestID}
			if request.Type != "get_pages" {
				response.Error = "test plugin accepts only get_pages"
			} else {
				response.Data = map[string]any{
					"currentPageId": "0:1",
					"pages":         []map[string]string{{"id": "0:1", "name": "Test page"}},
				}
			}
			if err := wsjson.Write(ctx, conn, response); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		conn.CloseNow()
		<-done
	})
	return conn
}

func assertPages(t *testing.T, p *serverProcess) {
	t.Helper()
	result := p.getPages(t)
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("get_pages failed: %+v", result)
	}
	content, ok := result.Content[0].(mcp.TextContent)
	if !ok || !strings.Contains(content.Text, `"name":"Test page"`) {
		t.Fatalf("get_pages did not return the fake plugin response: %+v", result.Content)
	}
}
