#!/usr/bin/env bash
# Measures the per-request context-token cost of each server's tool definitions,
# end-to-end, using headless Claude Code runs.
#
# Method: run `claude -p` with (a) no MCP server, (b) figma-lean-mcp, and
# (c) figma-mcp-go, identical trivial prompt, and diff total input tokens.
# The delta over the no-server baseline is exactly what the server's tool
# definitions cost in context on every request.
#
# ENABLE_TOOL_SEARCH=false forces all tool schemas into context (the classic
# MCP client behavior most clients still have). Requires: claude CLI, python3,
# node/npx, and a built bin/figma-lean-mcp (`make server`).
set -euo pipefail

cd "$(dirname "$0")"
REPO="$(cd .. && pwd)"
MODEL="${MODEL:-claude-sonnet-5}"
OUT="$(mktemp -d)"

cat > "$OUT/none.json" <<EOF
{"mcpServers":{}}
EOF
cat > "$OUT/lean.json" <<EOF
{"mcpServers":{"figma-lean-mcp":{"type":"stdio","command":"$REPO/bin/figma-lean-mcp","args":["--port","19962"]}}}
EOF
cat > "$OUT/go.json" <<EOF
{"mcpServers":{"figma-mcp-go":{"type":"stdio","command":"npx","args":["-y","@vkhanhqui/figma-mcp-go@latest"]}}}
EOF

# Warm the npx cache first so a slow cold start can't race MCP connection
# (a server that connects late is silently excluded, corrupting the measurement).
npx -y @vkhanhqui/figma-mcp-go@latest --help >/dev/null 2>&1 || true

declare -A TOTAL
for cfg in none lean go; do
  ENABLE_TOOL_SEARCH=false claude -p "Reply with exactly: OK" \
    --mcp-config "$OUT/$cfg.json" --strict-mcp-config --max-turns 1 \
    --output-format json --model "$MODEL" > "$OUT/run_$cfg.json"
  TOTAL[$cfg]=$(python3 -c "
import json; u = json.load(open('$OUT/run_$cfg.json'))['usage']
print(u['input_tokens'] + u.get('cache_creation_input_tokens',0) + u.get('cache_read_input_tokens',0))")
done

BASE=${TOTAL[none]}
echo "model: $MODEL"
echo "baseline (no MCP server): $BASE input tokens"
for cfg in lean go; do
  d=$(( TOTAL[$cfg] - BASE ))
  echo "$cfg: ${TOTAL[$cfg]} input tokens (+$d for tool definitions)"
done
LEAN=$(( TOTAL[lean] - BASE )); GO=$(( TOTAL[go] - BASE ))
python3 -c "print(f'lean vs go: {100*(1-$LEAN/$GO):.1f}% fewer definition tokens ({$GO/$LEAN:.1f}x)')"
