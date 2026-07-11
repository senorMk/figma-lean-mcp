# Benchmarks

figma-lean-mcp exists to measure the token cost of the "one primitive"
approach versus a granular 70-tool server
([`figma-mcp-go`](https://github.com/vkhanhqui/figma-mcp-go), 73 tools). Two
costs are measured separately: the **fixed tax** (tool definitions loaded into
context on every request) and the **per-task cost** (call/response tokens to
build a screen).

## 1. Tool-definition tokens (fixed, every request)

Measured end-to-end with headless Claude Code runs: identical trivial prompt,
`--strict-mcp-config` with (a) no server, (b) figma-lean-mcp, (c) figma-mcp-go,
diffing total input tokens against the no-server baseline. Tool search is
disabled (`ENABLE_TOOL_SEARCH=false`) so all schemas load into context — the
behavior of a standard MCP client.

```bash
./bench/definition_tokens.sh          # MODEL=claude-... to override
```

Results (2026-07-10, `claude-sonnet-5`, Claude Code 2.1.206, two runs each,
token counts identical across runs):

| Config | Total input tokens | Tool-definition cost |
|---|---|---|
| No MCP server (baseline) | 52,038 | — |
| **figma-lean-mcp** (5 tools) | 53,547 | **+1,509** |
| figma-mcp-go (73 tools) | 71,304 | +19,266 |

**→ 92.2% fewer tool-definition context tokens (12.8×), paid on every request.**

Raw `tools/list` JSON (via `bench/dump_tools.py`) agrees: 4,342 chars for 5
tools vs 53,819 chars for 73 tools (91.9% smaller).

Footnote: with Claude Code's tool search *enabled* (schemas deferred, names
only), the gap narrows in absolute terms but holds in relative ones:
+94 vs +1,422 tokens (93.4% fewer). Deferral cushions a bloated tool surface;
it doesn't make it free — and most MCP clients don't defer at all.

```bash
# Dump raw tool definitions for inspection
python3 bench/dump_tools.py lean_tools.json ./bin/figma-lean-mcp --port 19961
python3 bench/dump_tools.py go_tools.json npx -y @vkhanhqui/figma-mcp-go@latest
```

## 2. Task tokens (build the same screen)

Protocol (requires Figma desktop open with the corresponding plugin running
and connected):

1. Pick a fixed target screen — e.g. a 360×640 login screen: title, two
   labeled input fields, a primary button, footer link, auto-layout
   throughout.
2. Run the identical prompt headlessly against each server:
   ```bash
   claude -p "Build <screen spec> in Figma at x=0,y=0." \
     --mcp-config bench/<server>.json --strict-mcp-config \
     --output-format json --model claude-sonnet-5
   ```
3. Compare `usage` totals (input + output across all turns) and number of tool
   calls from the JSON output.

Expected shape of the result: figma-lean-mcp completes the screen in **one**
`run_code` round-trip (one compact id-map response); the granular server needs
~12–20 sequential create/set calls, each echoing a verbose JSON node response,
and each intermediate turn re-reads the (larger) context. Numbers to be
recorded here once run.
