# figma-lean-mcp

A deliberately **minimal, token-efficient Figma MCP server**. It exposes **one
powerful primitive — `run_code` — plus a tiny set of read/export tools**, and
relays them to a Figma plugin over a single WebSocket bridge.

It exists as a **benchmark**: to measure, in isolation, the token cost of the
"one primitive" approach versus a granular server that ships 70+ tools (e.g.
[`figma-mcp-go`](https://github.com/vkhanhqui/figma-mcp-go)). Same proven
stack (Go MCP server + TypeScript/Vite Figma plugin, WebSocket bridge), stripped
to the essentials.

## Design rationale: one primitive vs 70 tools

A typical Figma MCP exposes a tool per Plugin-API operation:
`create_frame`, `create_text`, `set_fills`, `set_auto_layout`,
`set_corner_radius`, `create_rectangle`, `bind_variable_to_node`, … dozens of
them. Two costs follow:

1. **Definition tokens** — every tool's name, description, and JSON schema is
   loaded into the model's context on *every* request. 70 tools is a large,
   fixed tax before the model has done anything.
2. **Call tokens** — building a screen means many sequential calls, each with a
   verbose JSON request *and* a verbose JSON response echoing the created node.

`run_code` collapses both. The model already knows the Figma Plugin API. So we
give it the API directly: it writes a single JavaScript snippet that builds the
whole screen — frames, text, auto-layout, fills, variables — and returns a
compact id map in **one** round-trip. The tool surface stays tiny (one big
description instead of 70), and a screen is one call instead of twenty.

The remaining tools exist only because they are awkward or wasteful to express
through `run_code`: compact reads (`get_node`, `get_document`, `get_pages`) and
disk export (`save_screenshots`, which keeps base64 out of the context).

## Tools exposed

| Tool | Purpose |
|------|---------|
| **`run_code`** | Execute a JS snippet as `async (figma) => {…}` in the plugin. `await` supported; explicit `return` is JSON-serialized back (circular nodes fall back to `String()`); throws return `{error, stack}`. ~120 s timeout. **This is the core — it replaces all create/set tools.** |
| **`get_node`** | Shallow node inspection. `nodeId`, `depth` (default 1), optional `fields` whitelist. Compact output. |
| **`get_document`** | Current page + its top-level children (id/name/type/bounds). Enough to locate frames/sections. |
| **`get_pages`** | List all pages (id + name) and the current page id. |
| **`save_screenshots`** | Export node(s) to PNG file(s) on disk. `items: [{nodeId, path, scale?}]`. Base64 is written to disk and kept **out** of the response. |

That's the entire surface. No `create_frame`, `set_fills`, etc. — `run_code` is
how you create and modify anything.

## Architecture

```
Claude / MCP client  ──stdio──▶  Go MCP server  ──WebSocket(/ws)──▶  Figma plugin (UI iframe)
                                 (bridge :1996)                        │ postMessage
                                                                       ▼
                                                                 plugin main thread
                                                                 (figma.* Plugin API)
```

The Go server serves an HTTP endpoint (`/ws`) that the plugin's UI iframe
connects to. Each MCP tool call becomes a `{type, requestId, nodeIds, params}`
frame; the plugin dispatches on `type`, calls the Plugin API, and returns
`{type, requestId, data}`, matched back to the request by `requestId`.

## Build

Requires Go 1.26+ and Node 18+.

```bash
make build          # builds the Go server (bin/figma-lean-mcp) + the plugin (plugin/dist/)
# or individually:
make server         # go build -o bin/figma-lean-mcp ./cmd/figma-lean-mcp
make plugin         # cd plugin && npm install && npm run build
```

The plugin build produces `plugin/dist/code.js` and `plugin/dist/index.html`
(self-contained — the UI script is inlined).

## Run the server

```bash
go run ./cmd/figma-lean-mcp          # bridge on 127.0.0.1:1996, MCP over stdio
# flags: --host 127.0.0.1  --port 1996
```

Normally you don't run it by hand — your MCP client launches it (see below).

## Load the plugin in Figma and connect

1. Figma desktop → **Plugins → Development → Import plugin from manifest…**
2. Select `plugin/manifest.json` in this repo.
3. Run **Plugins → Development → Figma Lean MCP**. A small panel opens.
4. The panel connects to `ws://127.0.0.1:1996` automatically and shows
   **Connected** (green) once the server is running. Click the address at the
   bottom-left to point it at a different host/port.

The server and the plugin are independent: start the server (via your MCP
client), then open the plugin — it reconnects automatically.

## Register with Claude / an MCP client

Build the binary first (`make server`), then add this to your MCP config
(e.g. Claude Desktop's `claude_desktop_config.json`, or `.mcp.json` for Claude
Code):

```json
{
  "mcpServers": {
    "figma-lean-mcp": {
      "command": "/Users/penjani/Documents/Work/Projects/figma-lean-mcp/bin/figma-lean-mcp",
      "args": ["--port", "1996"]
    }
  }
}
```

Or run straight from source without pre-building:

```json
{
  "mcpServers": {
    "figma-lean-mcp": {
      "command": "go",
      "args": ["run", "./cmd/figma-lean-mcp", "--port", "1996"],
      "cwd": "/Users/penjani/Documents/Work/Projects/figma-lean-mcp"
    }
  }
}
```

## Worked example: build a whole screen in ONE `run_code` call

This is the whole point — one call replaces a dozen granular ones. Call
`run_code` with this `code`:

```js
// Login card: a padded auto-layout frame with a title, subtitle, and a button.
await figma.loadFontAsync({ family: "Inter", style: "Bold" });
await figma.loadFontAsync({ family: "Inter", style: "Regular" });

const hex = (h) => {
  const n = parseInt(h.replace("#", ""), 16);
  return { r: ((n >> 16) & 255) / 255, g: ((n >> 8) & 255) / 255, b: (n & 255) / 255 };
};

const card = figma.createFrame();
card.name = "Login Card";
card.layoutMode = "VERTICAL";
card.primaryAxisSizingMode = "AUTO";
card.counterAxisSizingMode = "FIXED";
card.resize(320, 100);
card.paddingTop = card.paddingBottom = card.paddingLeft = card.paddingRight = 24;
card.itemSpacing = 8;
card.cornerRadius = 16;
card.fills = [{ type: "SOLID", color: hex("#FFFFFF") }];

const title = figma.createText();
title.fontName = { family: "Inter", style: "Bold" };
title.fontSize = 22;
title.characters = "Welcome back";
card.appendChild(title);

const subtitle = figma.createText();
subtitle.fontName = { family: "Inter", style: "Regular" };
subtitle.fontSize = 14;
subtitle.characters = "Sign in to continue";
subtitle.fills = [{ type: "SOLID", color: hex("#6B7280") }];
card.appendChild(subtitle);

const button = figma.createFrame();
button.name = "Button";
button.layoutMode = "HORIZONTAL";
button.primaryAxisAlignItems = "CENTER";
button.counterAxisAlignItems = "CENTER";
button.layoutAlign = "STRETCH";
button.primaryAxisSizingMode = "FIXED";
button.counterAxisSizingMode = "FIXED";
button.resize(272, 44);
button.cornerRadius = 10;
button.fills = [{ type: "SOLID", color: hex("#2563EB") }];
const label = figma.createText();
label.fontName = { family: "Inter", style: "Bold" };
label.fontSize = 15;
label.characters = "Sign in";
label.fills = [{ type: "SOLID", color: hex("#FFFFFF") }];
button.appendChild(label);
card.appendChild(button);

figma.currentPage.appendChild(card);
figma.viewport.scrollAndZoomIntoView([card]);

// Return only a compact id map — never whole nodes.
return { card: card.id, title: title.id, subtitle: subtitle.id, button: button.id };
```

Result:

```json
{ "card": "1:23", "title": "1:24", "subtitle": "1:25", "button": "1:26" }
```

One tool definition loaded, one call made, one compact response — versus a
granular server's ~10 calls (create_frame ×3, create_text ×3, set_fills ×N,
set_auto_layout ×2, …), each echoing a verbose node object. That delta is what
this repo lets you measure.

## License

MIT.
