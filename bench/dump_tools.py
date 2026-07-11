#!/usr/bin/env python3
"""Spawn an MCP stdio server, run initialize + tools/list, dump the tools JSON."""
import json, subprocess, sys, threading

def rpc(proc, msg):
    proc.stdin.write((json.dumps(msg) + "\n").encode())
    proc.stdin.flush()

def read_msg(proc, want_id):
    while True:
        line = proc.stdout.readline()
        if not line:
            raise RuntimeError("server exited: " + proc.stderr.read().decode()[:500])
        line = line.strip()
        if not line:
            continue
        try:
            m = json.loads(line)
        except json.JSONDecodeError:
            continue
        if m.get("id") == want_id:
            return m

def main():
    out_path = sys.argv[1]
    cmd = sys.argv[2:]
    proc = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    try:
        rpc(proc, {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {
            "protocolVersion": "2024-11-05",
            "capabilities": {},
            "clientInfo": {"name": "bench", "version": "0.1"}}})
        read_msg(proc, 1)
        rpc(proc, {"jsonrpc": "2.0", "method": "notifications/initialized"})
        rpc(proc, {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}})
        resp = read_msg(proc, 2)
        tools = resp["result"]["tools"]
        json.dump(tools, open(out_path, "w"), indent=1)
        blob = json.dumps(tools)
        print(f"{len(tools)} tools, {len(blob)} chars raw JSON -> {out_path}")
    finally:
        proc.kill()

if __name__ == "__main__":
    main()
