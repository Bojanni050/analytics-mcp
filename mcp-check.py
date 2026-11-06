#!/usr/bin/env python3
"""Praat rechtstreeks met de analytics-MCP om de nieuwe tools te controleren.

Stuurt initialize + tools/list (+ optioneel tools/call) naar 127.0.0.1:8097 met
het bearer-token dat Hermes ook gebruikt. Print alleen wat relevant is.
"""
import json
import os
import sys
import urllib.request

TOKEN_FILE = os.path.expanduser("~/.hermes/mcp-tokens/analytics.json")
URL = os.environ.get("MCP_URL", "http://127.0.0.1:8097/")
HEADERS = {"content-type": "application/json", "accept": "application/json, text/event-stream"}


def post(payload, session_id=None):
    headers = dict(HEADERS)
    headers["authorization"] = "Bearer " + token()
    if session_id:
        headers["mcp-session-id"] = session_id
    req = urllib.request.Request(URL, data=json.dumps(payload).encode(), headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=90) as resp:
        body = resp.read().decode()
        sid = resp.headers.get("mcp-session-id")
    if "data:" in body:
        body = "\n".join(line[5:].strip() for line in body.splitlines() if line.startswith("data:"))
    return json.loads(body) if body.strip() else {}, sid


def token():
    with open(TOKEN_FILE) as fh:
        return json.load(fh)["access_token"]


def main():
    init, sid = post({
        "jsonrpc": "2.0", "id": 1, "method": "initialize",
        "params": {"protocolVersion": "2025-06-18", "capabilities": {},
                   "clientInfo": {"name": "hermes-check", "version": "1"}},
    })
    server = init.get("result", {}).get("serverInfo", {})
    print(f"initialize: {server.get('name')} {server.get('version')} (session {sid or '-'})")

    tools, _ = post({"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}}, sid)
    names = sorted(t["name"] for t in tools.get("result", {}).get("tools", []))
    print(f"tools ({len(names)}): {', '.join(names)}")

    if len(sys.argv) > 1 and sys.argv[1] == "call":
        arguments = json.loads(sys.argv[2])
        tool = sys.argv[3] if len(sys.argv) > 3 else "ads_conversion_action_update"
        result, _ = post({"jsonrpc": "2.0", "id": 3, "method": "tools/call",
                          "params": {"name": tool, "arguments": arguments}}, sid)
        print(json.dumps(result, indent=2, ensure_ascii=False)[:2500])


if __name__ == "__main__":
    main()
