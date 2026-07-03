package internal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterTools registers the lean tool surface. The whole design principle is
// that run_code replaces the dozens of granular create_*/set_* tools a typical
// Figma MCP exposes: build an entire screen in ONE call.
func RegisterTools(s *server.MCPServer, b *Bridge) {
	registerRunCode(s, b)
	registerGetNode(s, b)
	registerGetDocument(s, b)
	registerGetPages(s, b)
	registerSaveScreenshots(s, b)
}

// renderResponse converts a BridgeResponse into an MCP tool result.
func renderResponse(resp BridgeResponse, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if resp.Error != "" {
		return mcp.NewToolResultError(resp.Error), nil
	}
	text, err := json.Marshal(resp.Data)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshal response: %v", err)), nil
	}
	return mcp.NewToolResultText(string(text)), nil
}

// ── run_code — THE CORE PRIMITIVE ─────────────────────────────────────────────

func registerRunCode(s *server.MCPServer, b *Bridge) {
	s.AddTool(mcp.NewTool("run_code",
		mcp.WithDescription(
			"Execute an arbitrary JavaScript snippet inside the Figma plugin with the full `figma` Plugin API in scope. "+
				"This is THE primary tool: instead of dozens of granular create_*/set_* calls (each with a verbose JSON "+
				"response), assemble an entire screen — create frames, text, set fills, auto-layout, bind variables — and "+
				"`return` a compact result (e.g. an id map) in ONE round-trip.\n\n"+
				"Execution: your code runs as the body of `async (figma) => { <your code> }`, so `await` is supported and "+
				"`figma` is available. Use an explicit `return` for the value you want back — it is JSON-serialized and "+
				"returned as the tool result. Keep the payload small (ids/names, not whole nodes — Figma node objects are "+
				"circular and fall back to String()). On a thrown error the result is {error, stack}. ~120s timeout.\n\n"+
				"Font rule: you MUST `await figma.loadFontAsync({family, style})` before setting `.characters` or font props.\n\n"+
				"Example: `const f = figma.createFrame(); f.name='Card'; f.resize(320,200); "+
				"f.layoutMode='VERTICAL'; f.paddingTop=f.paddingBottom=f.paddingLeft=f.paddingRight=16; f.itemSpacing=8; "+
				"await figma.loadFontAsync({family:'Inter',style:'Bold'}); const t=figma.createText(); t.fontName={family:'Inter',style:'Bold'}; "+
				"t.characters='Hello'; f.appendChild(t); figma.currentPage.appendChild(f); return {frame:f.id, text:t.id};`",
		),
		mcp.WithString("code",
			mcp.Required(),
			mcp.Description("JavaScript to execute. `figma` is in scope, `await` works, and an explicit `return` value is serialized back as JSON."),
		),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		code, _ := req.GetArguments()["code"].(string)
		if strings.TrimSpace(code) == "" {
			return mcp.NewToolResultError("code is required"), nil
		}
		resp, err := b.Send(ctx, "run_code", nil, map[string]interface{}{"code": code})
		return renderResponse(resp, err)
	})
}

// ── get_node — shallow, compact node inspection ───────────────────────────────

func registerGetNode(s *server.MCPServer, b *Bridge) {
	s.AddTool(mcp.NewTool("get_node",
		mcp.WithDescription(
			"Inspect a node compactly. Shallow by default (depth=1: the node plus its direct children as a child count "+
				"or one level). Use `fields` to whitelist only the keys you need. Returns id/name/type/bounds and, for TEXT, "+
				"characters/fontSize. Prefer this over dumping a whole subtree."),
		mcp.WithString("nodeId", mcp.Required(), mcp.Description("Target node id, e.g. 123:456.")),
		mcp.WithNumber("depth", mcp.Description("How many levels of children to include. Default 1. 0 = node only (childCount). -1 = entire subtree.")),
		mcp.WithArray("fields", mcp.Description("Optional whitelist of top-level keys to keep (tree keys children/childCount are always preserved)."),
			mcp.Items(map[string]interface{}{"type": "string"})),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		nodeID, _ := args["nodeId"].(string)
		if nodeID == "" {
			return mcp.NewToolResultError("nodeId is required"), nil
		}
		params := map[string]interface{}{}
		if d, ok := args["depth"]; ok {
			params["depth"] = d
		}
		if f, ok := args["fields"]; ok {
			params["fields"] = f
		}
		resp, err := b.Send(ctx, "get_node", []string{nodeID}, params)
		return renderResponse(resp, err)
	})
}

// ── get_document — current-page tree, shallow ─────────────────────────────────

func registerGetDocument(s *server.MCPServer, b *Bridge) {
	s.AddTool(mcp.NewTool("get_document",
		mcp.WithDescription(
			"Get the current page's structure: page id/name plus its top-level children (id/name/type/bounds). "+
				"Just enough to locate frames/sections; drill in with get_node."),
		mcp.WithNumber("depth", mcp.Description("Levels of children to include. Default 1.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		params := map[string]interface{}{}
		if d, ok := req.GetArguments()["depth"]; ok {
			params["depth"] = d
		}
		resp, err := b.Send(ctx, "get_document", nil, params)
		return renderResponse(resp, err)
	})
}

// ── get_pages — list pages to find ids ────────────────────────────────────────

func registerGetPages(s *server.MCPServer, b *Bridge) {
	s.AddTool(mcp.NewTool("get_pages",
		mcp.WithDescription("List all pages (id + name) and the current page id. Use to find a page id before navigating."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resp, err := b.Send(ctx, "get_pages", nil, nil)
		return renderResponse(resp, err)
	})
}

// ── save_screenshots — export node(s) to PNG on disk ──────────────────────────

type saveItem struct {
	NodeID string  `json:"nodeId"`
	Path   string  `json:"path"`
	Scale  float64 `json:"scale,omitempty"`
}

func registerSaveScreenshots(s *server.MCPServer, b *Bridge) {
	s.AddTool(mcp.NewTool("save_screenshots",
		mcp.WithDescription(
			"Export node(s) to PNG file(s) on disk. Base64 is written to the file and kept OUT of the tool response — "+
				"the response only returns paths and sizes, so screenshots never bloat the context. `path` is required and "+
				"caller-provided (absolute, or relative to the server's working directory)."),
		mcp.WithArray("items", mcp.Required(),
			mcp.Description("Array of {nodeId, path, scale?}. path must end in .png. scale defaults to 2."),
			mcp.Items(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"nodeId": map[string]interface{}{"type": "string"},
					"path":   map[string]interface{}{"type": "string", "description": "Output .png file path (caller-provided)."},
					"scale":  map[string]interface{}{"type": "number", "description": "Export scale, default 2."},
				},
				"required": []string{"nodeId", "path"},
			})),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw, _ := req.GetArguments()["items"].([]interface{})
		if len(raw) == 0 {
			return mcp.NewToolResultError("items must be a non-empty array"), nil
		}
		results := make([]map[string]interface{}, 0, len(raw))
		succeeded, failed := 0, 0
		for i, ri := range raw {
			item, err := parseSaveItem(ri)
			res := map[string]interface{}{"index": i}
			if err == nil {
				res["nodeId"] = item.NodeID
				res["path"] = item.Path
				err = saveOne(ctx, b, item)
			}
			if err != nil {
				res["success"] = false
				res["error"] = err.Error()
				failed++
			} else {
				res["success"] = true
				succeeded++
			}
			results = append(results, res)
		}
		out, _ := json.Marshal(map[string]interface{}{
			"total": len(results), "succeeded": succeeded, "failed": failed, "results": results,
		})
		return mcp.NewToolResultText(string(out)), nil
	})
}

func parseSaveItem(raw interface{}) (saveItem, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return saveItem{}, err
	}
	var item saveItem
	if err := json.Unmarshal(b, &item); err != nil {
		return saveItem{}, err
	}
	if item.NodeID == "" {
		return item, errors.New("nodeId is required")
	}
	if item.Path == "" {
		return item, errors.New("path is required")
	}
	if strings.ToLower(filepath.Ext(item.Path)) != ".png" {
		return item, errors.New("path must end in .png")
	}
	return item, nil
}

// saveOne asks the plugin to export the node as a base64 PNG, then decodes and
// writes it to disk — keeping the base64 out of the MCP response entirely.
func saveOne(ctx context.Context, b *Bridge, item saveItem) error {
	scale := item.Scale
	if scale <= 0 {
		scale = 2
	}
	resp, err := b.Send(ctx, "get_screenshot", []string{item.NodeID}, map[string]interface{}{"scale": scale})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	b64, err := extractBase64(resp.Data)
	if err != nil {
		return err
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return fmt.Errorf("base64 decode: %w", err)
	}
	path := item.Path
	if !filepath.IsAbs(path) {
		if wd, err := os.Getwd(); err == nil {
			path = filepath.Join(wd, path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func extractBase64(data interface{}) (string, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	var wrapper struct {
		Base64 string `json:"base64"`
	}
	if err := json.Unmarshal(b, &wrapper); err != nil {
		return "", err
	}
	if wrapper.Base64 == "" {
		return "", errors.New("plugin returned no image data")
	}
	return wrapper.Base64, nil
}
