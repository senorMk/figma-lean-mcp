package internal

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStartRejectsOccupiedPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	addr := listener.Addr().String()
	bridge := NewBridge()
	t.Cleanup(bridge.Stop)
	started := time.Now()
	err = bridge.Start(addr)
	if err == nil {
		t.Fatal("Start accepted an occupied port")
	}
	if time.Since(started) > time.Second {
		t.Fatal("Start did not return the bind error promptly")
	}
	var networkError *net.OpError
	if !errors.As(err, &networkError) {
		t.Fatalf("Start did not wrap the network error: %v", err)
	}
	for _, text := range []string{addr, "Another process may", "--port", "same host and port"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("error does not contain %q: %v", text, err)
		}
	}
	if bridge.server != nil {
		t.Fatal("Start created an HTTP server after a failed bind")
	}
}

func TestStartOwnsListenerAndStopReleasesIt(t *testing.T) {
	bridge := NewBridge()
	if err := bridge.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	addr := bridge.server.Addr
	listener, err := net.Listen("tcp", addr)
	if err == nil {
		listener.Close()
		t.Fatal("Start returned before it owned the listener")
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + addr + "/ping")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var health struct {
		Status    string `json:"status"`
		Connected bool   `json:"connected"`
	}
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || health.Status != "ok" || health.Connected {
		t.Fatalf("unexpected health response: %+v", health)
	}
	if _, err := bridge.Send(context.Background(), "get_pages", nil, nil); err == nil || !strings.Contains(err.Error(), "plugin not connected") {
		t.Fatalf("unexpected error without a plugin: %v", err)
	}
	bridge.Stop()
	listener, err = net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("Stop did not release the listener: %v", err)
	}
	listener.Close()
}
