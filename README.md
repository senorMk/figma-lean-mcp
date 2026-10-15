# figma-lean-mcp

A deliberately **minimal, token-efficient Figma MCP server**. It exposes **one
powerful primitive — `run_code` — plus a tiny set of read/export tools**, and
relays them to a Figma plugin over a single WebSocket bridge.

**[Install the server and plugin](#installation)** from prebuilt downloads.

It exists as a **benchmark**: to measure, in isolation, the token cost of the
"one primitive" approach versus a granular server that ships 70+ tools (e.g.
[`figma-mcp-go`](https://github.com/vkhanhqui/figma-mcp-go)). Same proven
stack (Go MCP server + TypeScript/Vite Figma plugin, WebSocket bridge), stripped
to the essentials.

**Measured result: 92% fewer tool-definition context tokens per request**
(+1,509 vs +19,266 input tokens over a no-server baseline; 5 tools vs 73;
Claude Sonnet, end-to-end headless runs). Methodology and reproduction
scripts in [`bench/`](bench/README.md).

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

## Installation

Download the server and plugin from the **[latest release](https://github.com/senorMk/figma-lean-mcp/releases/latest)**.
Go, Node.js, and a source checkout are not required. You need Figma desktop
and an MCP client that can start a local server.

### 1. Download the server

Choose the archive for the computer that will run your MCP client:

| Computer | Server archive |
|---|---|
| macOS, Apple silicon | `figma-lean-mcp_darwin_arm64.tar.gz` |
| macOS, Intel | `figma-lean-mcp_darwin_amd64.tar.gz` |
| Windows, Intel or AMD (64-bit) | `figma-lean-mcp_windows_amd64.zip` |
| Windows, ARM64 | `figma-lean-mcp_windows_arm64.zip` |
| Linux, Intel or AMD (64-bit) | `figma-lean-mcp_linux_amd64.tar.gz` |
| Linux, ARM64 | `figma-lean-mcp_linux_arm64.tar.gz` |

Extract the archive into a permanent folder. On macOS or Linux, you can use:

```bash
mkdir -p "$HOME/.local/bin"
# Example for a Mac with Apple silicon:
tar -xzf "$HOME/Downloads/figma-lean-mcp_darwin_arm64.tar.gz" -C "$HOME/.local/bin"
"$HOME/.local/bin/figma-lean-mcp" --version
```

On Windows, use **Extract All** and keep `figma-lean-mcp.exe` in a permanent
folder, such as `C:\Tools\figma-lean-mcp`.

The release includes `SHA256SUMS`. To check an archive, compare its hash with
the entry in that file:

```bash
# macOS
shasum -a 256 figma-lean-mcp_darwin_arm64.tar.gz
# Linux
sha256sum figma-lean-mcp_linux_amd64.tar.gz
```

```powershell
# Windows PowerShell
Get-FileHash .\figma-lean-mcp_windows_amd64.zip -Algorithm SHA256
```

Automatic builds are not code-signed or notarized. Your operating system
can ask for approval before the server starts. On macOS, after a blocked
start, use **System Settings → Privacy & Security → Open Anyway** if you
trust the downloaded file.

### 2. Configure your MCP client

Add the server to your client's MCP configuration. Use the full path to the
extracted executable. The client starts the server when it needs it.

For a client that uses `mcpServers`, such as Claude Desktop or Claude Code:

```json
{
  "mcpServers": {
    "figma-lean-mcp": {
      "command": "/Users/YOUR_NAME/.local/bin/figma-lean-mcp",
      "args": ["--port", "1996"]
    }
  }
}
```

On Windows, set `command` to a path such as
`"C:\\Tools\\figma-lean-mcp\\figma-lean-mcp.exe"`. JSON requires two
backslashes for each path separator. Restart your MCP client after the change.

### 3. Install the built Figma plugin

1. Download `figma-lean-mcp-plugin.zip` from the **same release** as the server.
2. Extract the ZIP into a permanent folder. Keep `manifest.json` and the
   `dist` folder together inside `figma-lean-mcp-plugin`.
3. In Figma desktop, select **Plugins → Development → Import plugin from manifest…**.
4. Select the extracted `figma-lean-mcp-plugin/manifest.json`.
5. Open a design file. Run **Plugins → Development → Figma Lean MCP**.
6. Start your MCP client. Keep the plugin open until its panel shows **Connected**.

The plugin connects to `ws://127.0.0.1:1996` automatically. To use another
host or port, click the address at the bottom-left of the plugin panel.
Use the same port in your MCP client configuration.

Try this request in your MCP client:

> List the pages in my open Figma file.

### Bridge port conflicts

Only one server process can use a bridge address, such as
`127.0.0.1:1996`. If another process uses that address, the new server exits
with a startup error. The error identifies the address. The other process
can be another MCP client or an unrelated application.

If the plugin shows **Connected** but a second MCP client cannot start its
server, the plugin can still be connected to the first client's server.
Restarting the plugin does not release the port.

Use the MCP client that already owns the bridge. To run a separate server,
set a different port in that client's configuration, for example:

```json
"args": ["--port", "1997"]
```

Then click the address in the plugin panel and set it to
`ws://127.0.0.1:1997`. The plugin and server must use the same host and port.
Keep the plugin connected to the server for the client you are using.

### Multiple MCP clients

Each server process has its own bridge and plugin connection. Separate
processes need separate ports. A plugin connection does not serve several
MCP clients. Shared routing is a separate architecture change; this server
does not select a new port or forward commands to another process.

### Updates

Download both packages from the new release. Close the plugin and stop the
MCP client before you replace the extracted files. Then start the client
and open the plugin again. Keep the same file paths so that the client
configuration and Figma plugin import remain valid.

The packages do not update themselves. The release workflow builds new
downloads automatically after each push to `main`, including merged pull
requests. Each release has a unique `build-<run number>-<commit>` tag.
The latest-release link changes when a successful build of the current
`main` commit is published.

## Build from source

Requires Go 1.26+ and Node.js 24+.

```bash
make build          # Build the server and plugin.
make server         # Build bin/figma-lean-mcp only.
make plugin         # Install locked dependencies and build plugin/dist/.
```

Configure your MCP client with the full path to `bin/figma-lean-mcp`.
Import `plugin/manifest.json` in Figma desktop. The connection steps are
the same as for a downloaded release.

For development, you can also start the server from the repository:

```bash
go run ./cmd/figma-lean-mcp
# Options: --host 127.0.0.1 --port 1996 --version
```

### Build release packages

Python 3 is also required. Use an empty output directory:

```bash
make release VERSION=local-test
python3 scripts/check_release.py dist/release local-test
```

This builds six server archives, the plugin ZIP, and `SHA256SUMS` in
`dist/release/`. To keep an earlier build, choose a new output directory:

```bash
python3 scripts/build_release.py --version local-test-2 --output dist/local-test-2
```

The [release workflow](.github/workflows/release.yml) runs these checks for
pull requests and pushes to `main`. Only `main` builds publish releases.
A maintainer can also run the workflow manually on `main`. It uses the
repository's `GITHUB_TOKEN`; no separate release token is required.

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
