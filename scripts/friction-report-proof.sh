#!/usr/bin/env bash
# Proof for agent_friction_report with real binaries: a session lists the tool,
# files friction with no step, and the gateway row is the one friction_find and
# the weekly digest read. It also renders the line the lead-facing notice ends
# with. Everything is throwaway: its own gateway on a free port, one daemon,
# config, state and socket under /tmp. The real setup is never touched.
#
#   scripts/friction-report-proof.sh
#
# The MCP server is driven over stdio the way a harness drives it, and each
# wait polls for the state it needs rather than pausing for a fixed time.
set -euo pipefail
cd "$(dirname "$0")/.."
export LC_ALL=C TZ=UTC
export PATH="$PATH:$HOME/.local/go/bin"
export GOCACHE="${GOCACHE:-/tmp/agent-net-gocache}"
T=$(mktemp -d /tmp/anet-friction.XXXXXX)
BIN=$T/bin
mkdir -p "$BIN" "$T/empty"
# The daemon gives a session no project under a system temporary folder, so
# the sample repository lives in the worktree's ignored tmp folder.
repo="$PWD/tmp/friction-proof-repo"
notice_dir="tmp/friction-notice-$$"
pids=()
cleanup() {
	for p in "${pids[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null || true; done
	wait 2>/dev/null || true
	rm -rf "$T" "$notice_dir" "$repo"
}
trap cleanup EXIT

build="$(git rev-parse HEAD)"
echo "build: $build"
echo "== build the binary"
go build -o "$BIN/agent-net" ./cmd/agent-net

echo "== start a throwaway gateway"
port=$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)
"$BIN/agent-net" gateway --listen "127.0.0.1:$port" --data "$T/gw" >"$T/gateway.log" 2>&1 &
pids+=($!)
for _ in $(seq 100); do
	grep -q "listening" "$T/gateway.log" && break
	sleep 0.1
done
if ! grep -q "listening" "$T/gateway.log"; then
	cat "$T/gateway.log"
	exit 1
fi

echo "== join and start a throwaway daemon"
export AGENT_NET_CONFIG_DIR=$T/cfg AGENT_NET_STATE_DIR=$T/state AGENT_NET_SOCKET=$T/anet.sock
export AGENT_NET_CLAUDE_SESSIONS_DIR=$T/empty AGENT_NET_NO_AUTO_UPGRADE=1
join=$("$BIN/agent-net" enroll --data "$T/gw" --host 127.0.0.1 --port "$port")
"$BIN/agent-net" join "$join" >/dev/null
"$BIN/agent-net" daemon >"$T/daemon.log" 2>&1 &
pids+=($!)
for _ in $(seq 100); do
	"$BIN/agent-net" status >/dev/null 2>&1 && break
	sleep 0.1
done
if ! "$BIN/agent-net" status >/dev/null 2>&1; then
	cat "$T/daemon.log"
	exit 1
fi

echo "== register the project the session runs in"
mkdir -p "$repo"
git -C "$repo" init -q
git -C "$repo" -c user.email=proof@example.invalid -c user.name=proof commit --allow-empty -q -m init
"$BIN/agent-net" projects add "$repo" >/dev/null

echo "== step 1: a session opens and lists its tools"
echo "== step 2: the session files friction with no step"
python3 - "$BIN/agent-net" "$repo" "$T" <<'PY'
import json, os, subprocess, sys

bin, repo, tmp = sys.argv[1:4]
env = dict(os.environ)
env["CLAUDE_CODE_SESSION_ID"] = "proof-friction-session"
# The daemon trusts an MCP server only when its parent is a discovered Claude
# session: this process stands in for Claude Code with the same session file.
session = {"pid": os.getpid(), "sessionId": env["CLAUDE_CODE_SESSION_ID"],
           "cwd": repo, "status": "idle", "kind": "interactive"}
with open(os.path.join(env["AGENT_NET_CLAUDE_SESSIONS_DIR"], "%d.json" % os.getpid()), "w") as f:
    json.dump(session, f)
log = open(os.path.join(tmp, "mcp.log"), "w")
p = subprocess.Popen([bin, "mcp"], cwd=repo, env=env, stdin=subprocess.PIPE,
                     stdout=subprocess.PIPE, stderr=log, text=True, bufsize=1)

def send(msg):
    p.stdin.write(json.dumps(msg) + "\n")
    p.stdin.flush()

def wait_id(i):
    while True:
        line = p.stdout.readline()
        if not line:
            raise SystemExit("mcp server closed; see " + os.path.join(tmp, "mcp.log"))
        msg = json.loads(line)
        if msg.get("id") == i:
            return msg

send({"jsonrpc": "2.0", "id": 1, "method": "initialize",
      "params": {"protocolVersion": "2025-06-18", "capabilities": {},
                 "clientInfo": {"name": "friction-proof", "version": "1"}}})
wait_id(1)
send({"jsonrpc": "2.0", "method": "notifications/initialized"})
send({"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
tools = wait_id(2)["result"]["tools"]
tool = next((t for t in tools if t["name"] == "agent_friction_report"), None)
if tool is None:
    raise SystemExit("agent_friction_report is not in the tool list")
print("frame: tool list entry")
print(json.dumps({"name": tool["name"], "description": tool["description"]}, indent=2))

send({"jsonrpc": "2.0", "id": 3, "method": "tools/call",
      "params": {"name": "agent_friction_report",
                 "arguments": {"friction": [{"area": "environment",
                                             "text": "the sandbox cannot bind a local port"}],
                               "change": "ch_proof"}}})
res = wait_id(3)
if "error" in res:
    raise SystemExit("tool call failed: %r" % res["error"])
print("frame: tool result")
print("".join(c.get("text", "") for c in res["result"].get("content", [])))
p.stdin.close()
p.wait(timeout=10)
PY

echo "== step 3: the store friction_find and the digest read"
db=$(find "$T/gw" -name gateway.db -print -quit)
python3 - "$db" <<'PY'
import sqlite3, sys

db = sqlite3.connect("file:%s?mode=ro" % sys.argv[1], uri=True)
row = db.execute("select project,session,change_id,harness,model,area,text,status,digest_id from friction").fetchone()
if row is None:
    raise SystemExit("no friction row was stored")
print("frame: friction row")
print("project=%s session=%s change=%s harness=%s model=%s area=%s\n  text=%s\n  status=%s digest_id=%r" % row)
PY

echo "== step 4: the lead-facing notice ends with the route"
mkdir -p "$notice_dir"
cat >"$notice_dir/main.go" <<'GO'
package main

import (
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func main() {
	n := protocol.WorkerNotice{Status: protocol.StatusDone, Session: "s_proof", Harness: "claude",
		Model: "claude-opus-4", Project: "proof", Host: "proof-host", Title: "Add the friction tool",
		RunTime: 3 * time.Minute, Worktree: "/tmp/proof/w_1", Branch: "agent-net/w_1"}
	fmt.Print(protocol.FormatWorkerNotice(n, "Done."))
}
GO
echo "frame: last lines of the worker notice"
go run "./$notice_dir" | tail -n 4
