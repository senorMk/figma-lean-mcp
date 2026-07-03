package internal

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

var bridgeLogger = log.New(os.Stderr, "[bridge] ", 0)

// pendingEntry holds the response channel and inactivity timer for an in-flight request.
type pendingEntry struct {
	ch    chan BridgeResponse
	timer *time.Timer
	once  sync.Once // guards channel close/send — prevents panic on concurrent timeout + response
}

// Bridge owns the single WebSocket connection to the Figma plugin, matches
// responses to pending requests by requestId, and serves the /ws + /ping HTTP
// endpoints the plugin connects to. Everything runs in one process — there is
// no leader/follower election (this is a lean benchmark server).
type Bridge struct {
	mu      sync.RWMutex
	wmu     sync.Mutex // serialises writes — coder/websocket forbids concurrent writes
	conn    *websocket.Conn
	pending map[string]*pendingEntry
	counter atomic.Int64
	server  *http.Server
}

// NewBridge creates a ready-to-use Bridge.
func NewBridge() *Bridge {
	return &Bridge{pending: make(map[string]*pendingEntry)}
}

// Start binds addr and serves /ws (plugin WebSocket) and /ping (health).
func (b *Bridge) Start(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", b.handleUpgrade)
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","connected":%t}`, b.IsConnected())
	})

	srv := &http.Server{Addr: addr, Handler: mux}
	b.server = srv
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			bridgeLogger.Printf("serve error: %v", err)
		}
	}()
	bridgeLogger.Printf("listening on %s (plugin connects to ws://%s/ws)", addr, addr)
	return nil
}

// handleUpgrade upgrades an HTTP request to a WebSocket. Only one plugin
// connection is kept — a new connection replaces the old one.
func (b *Bridge) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // plugin connects from Figma's sandbox origin
	})
	if err != nil {
		bridgeLogger.Printf("upgrade error: %v", err)
		return
	}
	// Figma documents can be large; the 32 KiB default causes disconnects.
	conn.SetReadLimit(100 * 1024 * 1024)

	b.mu.Lock()
	if b.conn != nil {
		b.conn.Close(websocket.StatusNormalClosure, "replaced by new connection")
	}
	b.conn = conn
	b.mu.Unlock()

	bridgeLogger.Printf("plugin connected from %s", r.RemoteAddr)
	go b.readLoop(conn)
}

// readLoop reads plugin messages and resolves matching pending requests.
func (b *Bridge) readLoop(conn *websocket.Conn) {
	defer func() {
		b.mu.Lock()
		if b.conn == conn {
			b.conn = nil
		}
		b.mu.Unlock()
		bridgeLogger.Printf("plugin disconnected")
	}()

	ctx := context.Background()
	for {
		var resp BridgeResponse
		if err := wsjson.Read(ctx, conn, &resp); err != nil {
			if !errors.Is(err, context.Canceled) {
				bridgeLogger.Printf("read error: %v", err)
			}
			return
		}

		// Progress update — extend the timeout, do not resolve.
		if resp.Progress > 0 && resp.RequestID != "" {
			b.mu.RLock()
			entry, ok := b.pending[resp.RequestID]
			b.mu.RUnlock()
			if ok {
				entry.timer.Stop()
				entry.timer.Reset(120 * time.Second)
			}
			continue
		}

		if resp.RequestID == "" {
			continue
		}

		b.mu.Lock()
		entry, ok := b.pending[resp.RequestID]
		if ok {
			delete(b.pending, resp.RequestID)
		}
		b.mu.Unlock()

		if ok {
			entry.timer.Stop()
			entry.once.Do(func() { entry.ch <- resp })
		}
	}
}

// Send delivers a request to the plugin and waits for its matching response.
func (b *Bridge) Send(ctx context.Context, requestType string, nodeIDs []string, params map[string]interface{}) (BridgeResponse, error) {
	// Normalize hyphenated node ids LLMs sometimes emit.
	for i, id := range nodeIDs {
		nodeIDs[i] = NormalizeNodeID(id)
	}
	if v, ok := params["nodeId"].(string); ok {
		params["nodeId"] = NormalizeNodeID(v)
	}

	b.mu.RLock()
	conn := b.conn
	b.mu.RUnlock()
	if conn == nil {
		return BridgeResponse{}, errors.New("plugin not connected — open the Figma plugin and check it shows Connected")
	}

	requestID := b.nextID()
	req := BridgeRequest{Type: requestType, RequestID: requestID, NodeIDs: nodeIDs, Params: params}

	ch := make(chan BridgeResponse, 1)
	entry := &pendingEntry{ch: ch}

	// run_code executes arbitrary user scripts that may build a whole screen in
	// one call, so it gets generous headroom; reads are quicker.
	timeout := 30 * time.Second
	if requestType == "run_code" {
		timeout = 120 * time.Second
	}
	entry.timer = time.AfterFunc(timeout, func() {
		bridgeLogger.Printf("→ %s %s timed out after %s", requestID, requestType, timeout)
		b.mu.Lock()
		delete(b.pending, requestID)
		b.mu.Unlock()
		entry.once.Do(func() { close(ch) })
	})

	b.mu.Lock()
	b.pending[requestID] = entry
	b.mu.Unlock()

	b.wmu.Lock()
	writeErr := wsjson.Write(ctx, conn, req)
	b.wmu.Unlock()
	if writeErr != nil {
		entry.timer.Stop()
		b.mu.Lock()
		delete(b.pending, requestID)
		b.mu.Unlock()
		return BridgeResponse{}, fmt.Errorf("send: %w", writeErr)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return BridgeResponse{}, errors.New("request timed out")
		}
		return resp, nil
	case <-ctx.Done():
		entry.timer.Stop()
		b.mu.Lock()
		delete(b.pending, requestID)
		b.mu.Unlock()
		return BridgeResponse{}, ctx.Err()
	}
}

// IsConnected reports whether the plugin is currently connected.
func (b *Bridge) IsConnected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.conn != nil
}

// Stop shuts down the HTTP server and rejects all pending requests.
func (b *Bridge) Stop() {
	b.mu.Lock()
	for id, entry := range b.pending {
		entry.timer.Stop()
		entry.once.Do(func() { close(entry.ch) })
		delete(b.pending, id)
	}
	if b.conn != nil {
		b.conn.Close(websocket.StatusNormalClosure, "server shutting down")
		b.conn = nil
	}
	srv := b.server
	b.mu.Unlock()
	if srv != nil {
		srv.Shutdown(context.Background())
	}
}

// nextID generates a request id in the format req-HHMMSS-N.
func (b *Bridge) nextID() string {
	n := b.counter.Add(1)
	t := time.Now()
	return fmt.Sprintf("req-%02d%02d%02d-%d", t.Hour(), t.Minute(), t.Second(), n)
}
