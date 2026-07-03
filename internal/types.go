package internal

import (
	"regexp"
	"strings"
)

// BridgeRequest is sent from the Go server to the Figma plugin over WebSocket.
type BridgeRequest struct {
	Type      string                 `json:"type"`
	RequestID string                 `json:"requestId"`
	NodeIDs   []string               `json:"nodeIds,omitempty"`
	Params    map[string]interface{} `json:"params,omitempty"`
}

// BridgeResponse is received from the Figma plugin over WebSocket.
type BridgeResponse struct {
	Type      string      `json:"type"`
	RequestID string      `json:"requestId"`
	Data      interface{} `json:"data,omitempty"`
	Error     string      `json:"error,omitempty"`
	// Progress is sent mid-operation for long-running commands to extend the timeout.
	Progress int    `json:"progress,omitempty"`
	Message  string `json:"message,omitempty"`
}

// nodeIDPattern matches a hyphenated node id such as "123-456" that LLMs
// sometimes emit instead of Figma's canonical colon form "123:456".
var nodeIDPattern = regexp.MustCompile(`^(\d+)-(\d+)$`)

// NormalizeNodeID converts hyphenated node ids ("123-456") to Figma's colon
// form ("123:456"). Any other value is returned unchanged.
func NormalizeNodeID(id string) string {
	id = strings.TrimSpace(id)
	if m := nodeIDPattern.FindStringSubmatch(id); m != nil {
		return m[1] + ":" + m[2]
	}
	return id
}
