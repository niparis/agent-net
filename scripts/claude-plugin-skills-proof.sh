#!/usr/bin/env bash
# Prove whether one plugin in the Claude config skills folder carries the
# agent-net worker window and the five agent-net hooks, with no enabledPlugins
# or marketplace entry.
#
# Everything runs in a temporary HOME and CLAUDE_CONFIG_DIR. A local stub of
# the Messages API replaces the model, so no model call is made and no login is
# read. A stub `agent-net` prints one worker row for the band.
#
# Usage: scripts/claude-plugin-skills-proof.sh [--claude PATH] [output folder]
#   --claude PATH  run every check on that Claude Code binary instead of the
#                  one on PATH, so a second installed version can be checked.
# Needs: Claude Code 2.1.330 or newer, python3. tmux is used for the
# interactive checks and for the band; without it those checks print SKIP.
#
# Checks, in order:
#   1. plugin list: <name>@skills-dir is enabled with no settings.json.
#   2. print sessions: each hook event and matcher runs the stub command.
#   3. interactive session: dialogs, Stop, and the band from a session.
#   4. background session: hooks run.
#   5. plugin disable: no hooks in two sessions; enable: hooks again.
#   6. old settings entry plus plugin: the same command runs twice; removing
#      the settings entry leaves one run.
#   7. old marketplace plugin plus skills plugin: the marketplace one shadows
#      the skills one; removing it lets the skills one load.
#   8. with no settings.json, Claude Code obeys the plugin's output: a
#      PreToolUse deny and allow, additionalContext on PreToolUse,
#      UserPromptSubmit, PostToolUse, PostToolUseFailure and SessionStart
#      (compact), and a Stop block. Each has a control without that hook, and
#      the disabled plugin has no effect.
#   9. a stale enabledPlugins entry from an earlier install keeps the plugin
#      off when it comes back, until the entry is cleared.
#
# The five agent-net hooks print these kinds of output: additionalContext
# (hint, policy-hook, urgent-hook), permissionDecision allow (tool-decide), and
# nothing at all, with one daemon call (needs-hook). Section 8 checks the first
# two kinds and a Stop block.
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
out_arg=""; claude_bin="${CLAUDE_BIN:-}"
while [ $# -gt 0 ]; do
  case "$1" in
    --claude) claude_bin="${2:?--claude needs a path}"; shift 2;;
    --claude=*) claude_bin="${1#*=}"; shift;;
    -h|--help) sed -n '2,30p' "$0"; exit 0;;
    *) out_arg="$1"; shift;;
  esac
done
[ -n "$claude_bin" ] || claude_bin="$(command -v claude || true)"
[ -n "$claude_bin" ] && [ -x "$claude_bin" ] || { echo "claude not found; give --claude PATH" >&2; exit 2; }
claude_bin="$(readlink -f "$claude_bin")"
root="$(mktemp -d "${TMPDIR:-/tmp}/agent-net-claude-plugin-proof.XXXXXX")"
mkdir -p "$root/claude-bin"
ln -s "$claude_bin" "$root/claude-bin/claude"
export PATH="$root/claude-bin:$PATH"
for tool in python3 timeout; do
  command -v "$tool" >/dev/null 2>&1 || { echo "needs $tool on PATH" >&2; exit 2; }
done
printf 'claude: %s (%s)\n' "$(claude --version 2>/dev/null | head -1)" "$claude_bin"
out="${out_arg:-$root/frames}"
mkdir -p "$out"
out="$(cd "$out" && pwd)"
trap 'rm -rf "$root"' EXIT

export HOME="$root/home"
export CLAUDE_CONFIG_DIR="$root/config"
export XDG_CONFIG_HOME="$root/xdg/config"
export XDG_CACHE_HOME="$root/xdg/cache"
export XDG_STATE_HOME="$root/xdg/state"
mkdir -p "$HOME" "$CLAUDE_CONFIG_DIR" "$XDG_CONFIG_HOME" "$XDG_CACHE_HOME" "$XDG_STATE_HOME" "$root/work" "$root/bin" "$out"
case "$HOME" in "$root"/*) ;; *) echo "refusing to run outside a temporary HOME" >&2; exit 1;; esac

model="${PROBE_MODEL:-claude-sonnet-4-5-20250929}"
export ANTHROPIC_API_KEY=sk-ant-plugin-proof
export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 DISABLE_TELEMETRY=1
log="$root/events.tsv"
api_log="$root/api.log"
pass=0 fail=0 skip=0
tmux_socket="anp-$$"

ok()   { pass=$((pass+1)); printf 'PASS  %s\n' "$1"; }
bad()  { fail=$((fail+1)); printf 'FAIL  %s\n' "$1"; }
note() { skip=$((skip+1)); printf 'SKIP  %s\n' "$1"; }
check() { if eval "$2"; then ok "$1"; else bad "$1"; fi; }

rows() { awk -F'\t' -v n="$1" '$1==n{c++} END{print c+0}' "$log"; }
has()  { [ "$(rows "$1")" -ge "${2:-1}" ]; }
hasnt(){ [ "$(rows "$1")" -eq 0 ]; }
clear_log() { : > "$log"; }

# The hook stub appends one row: name, event, tool. The payload is one line.
cat > "$root/bin/loghook.sh" <<'EOF'
#!/bin/sh
name=$1; log=$2; payload=$(cat)
event=$(printf '%s' "$payload" | sed -n 's/.*"hook_event_name":"\([^"]*\)".*/\1/p')
tool=$(printf '%s' "$payload" | sed -n 's/.*"tool_name":"\([^"]*\)".*/\1/p')
printf '%s\t%s\t%s\n' "$name" "$event" "$tool" >> "$log"
printf '{}\n'
EOF
cat > "$root/bin/allowhook.sh" <<'EOF'
#!/bin/sh
cat >/dev/null
printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"plugin proof"}}\n'
EOF
# The output stubs: a decision for PreToolUse, additionalContext for an event,
# and a Stop block that fires once (the second Stop lets the turn end).
cat > "$root/bin/decide.py" <<'EOF'
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    sys.exit(0)
cmd = (d.get("tool_input") or {}).get("command") or ""
if "deny-me" in cmd:
    print(json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse",
        "permissionDecision": "deny", "permissionDecisionReason": "probe deny"}}))
elif "allow-me" in cmd:
    print(json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse",
        "permissionDecision": "allow", "permissionDecisionReason": "probe allow"}}))
EOF
cat > "$root/bin/context.sh" <<'EOF'
#!/bin/sh
cat >/dev/null
printf '{"hookSpecificOutput":{"hookEventName":"%s","additionalContext":"%s"}}\n' "$1" "$2"
EOF
cat > "$root/bin/stopblock.sh" <<'EOF'
#!/bin/sh
cat >/dev/null
n=$(cat "$1" 2>/dev/null || echo 0)
echo $((n+1)) > "$1"
if [ "$n" -eq 0 ]; then
  printf '{"decision":"block","reason":"PROBE_STOP_REASON_CONTINUE"}\n'
fi
EOF
chmod +x "$root/bin/loghook.sh" "$root/bin/allowhook.sh" "$root/bin/decide.py" "$root/bin/context.sh" "$root/bin/stopblock.sh"

# A local stand-in for the Messages API. It picks a tool from the prompt and
# ends the turn after the tool result. No network, no model.
cat > "$root/bin/fakeapi.py" <<'PY'
import json, os, sys, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse

LOG = os.environ["FAKE_API_LOG"]

def log(obj):
    with open(LOG, "a") as f:
        f.write(json.dumps(obj) + "\n")

def sse(w, event, data):
    w.write(f"event: {event}\ndata: {json.dumps(data)}\n\n".encode())
    w.flush()

def user_text(body):
    parts = []
    for m in body.get("messages", []):
        if m.get("role") != "user":
            continue
        c = m.get("content")
        if isinstance(c, str):
            parts.append(c)
        elif isinstance(c, list):
            parts.extend(b.get("text", "") for b in c if b.get("type") == "text")
    return "\n".join(parts)

def last_has_tool_result(body):
    ms = body.get("messages", [])
    if not ms:
        return False
    c = ms[-1].get("content")
    return isinstance(c, list) and any(b.get("type") == "tool_result" for b in c)

def plan(body):
    if last_has_tool_result(body):
        return ("text", None, None)
    p = user_text(body)
    if "use-bash-deny" in p:
        return ("tool", "Bash", {"command": "echo deny-me >> " + os.environ["PROBE_DENY_FILE"], "description": "proof"})
    if "use-bash-allow" in p:
        return ("tool", "Bash", {"command": "echo allow-me >> " + os.environ["PROBE_ALLOW_FILE"], "description": "proof"})
    if "use-bash-ok" in p:
        return ("tool", "Bash", {"command": "echo ok", "description": "proof"})
    if "use-bash-fail" in p:
        return ("tool", "Bash", {"command": "false", "description": "proof"})
    if "use-gh" in p:
        return ("tool", "Bash", {"command": "gh pr merge --help", "description": "proof"})
    if "use-mcp" in p:
        return ("tool", "mcp__agent-net__probe_echo", {})
    if "use-ask" in p:
        return ("tool", "AskUserQuestion", {"questions": [{"question": "Probe question?", "header": "Probe", "options": [{"label": "A", "description": "first"}, {"label": "B", "description": "second"}], "multiSelect": False}]})
    if "use-plan" in p:
        return ("tool", "ExitPlanMode", {"plan": "Probe plan."})
    if "use-write" in p:
        return ("tool", "Write", {"file_path": os.environ["PROBE_WRITE"], "content": "x"})
    return ("text", None, None)

class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def log_message(self, *a): pass
    def do_GET(self):
        log({"method": "GET", "path": self.path})
        if "/v1/models" in self.path:
            mid = "claude-sonnet-4-5-20250929"
            b = json.dumps({"data": [{"id": mid, "type": "model", "display_name": "Probe", "created_at": "2025-01-01T00:00:00Z"}], "has_more": False, "first_id": mid, "last_id": mid}).encode()
            self.send_response(200); self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b); return
        self.send_response(404); self.send_header("Content-Length", "0"); self.end_headers()
    def do_POST(self):
        n = int(self.headers.get("Content-Length", "0"))
        body = json.loads(self.rfile.read(n) or b"{}")
        log({"method": "POST", "path": self.path, "body": body})
        route = urlparse(self.path).path
        if route.endswith("/count_tokens"):
            b = json.dumps({"input_tokens": 10}).encode()
            self.send_response(200); self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b); return
        if not route.endswith("/messages"):
            self.send_response(404); self.send_header("Content-Length", "0"); self.end_headers(); return
        kind, name, inp = plan(body)
        mid = "msg_probe_" + str(int(time.time() * 1000))
        model = body.get("model", "claude-probe")
        if not body.get("stream", True):
            content = ([{"type": "text", "text": "probe reply"}] if kind == "text"
                       else [{"type": "tool_use", "id": "toolu_probe", "name": name, "input": inp}])
            b = json.dumps({"id": mid, "type": "message", "role": "assistant", "model": model, "content": content,
                            "stop_reason": "end_turn" if kind == "text" else "tool_use",
                            "usage": {"input_tokens": 10, "output_tokens": 5}}).encode()
            self.send_response(200); self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b); return
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.end_headers()
        sse(self.wfile, "message_start", {"type": "message_start", "message": {"id": mid, "type": "message", "role": "assistant", "model": model, "content": [], "stop_reason": None, "usage": {"input_tokens": 10, "output_tokens": 1}}})
        if kind == "tool":
            sse(self.wfile, "content_block_start", {"type": "content_block_start", "index": 0, "content_block": {"type": "tool_use", "id": "toolu_probe", "name": name, "input": {}}})
            sse(self.wfile, "content_block_delta", {"type": "content_block_delta", "index": 0, "delta": {"type": "input_json_delta", "partial_json": json.dumps(inp)}})
            sse(self.wfile, "content_block_stop", {"type": "content_block_stop", "index": 0})
            sse(self.wfile, "message_delta", {"type": "message_delta", "delta": {"stop_reason": "tool_use", "stop_sequence": None}, "usage": {"output_tokens": 5}})
        else:
            sse(self.wfile, "content_block_start", {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}})
            sse(self.wfile, "content_block_delta", {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": "probe reply"}})
            sse(self.wfile, "content_block_stop", {"type": "content_block_stop", "index": 0})
            sse(self.wfile, "message_delta", {"type": "message_delta", "delta": {"stop_reason": "end_turn", "stop_sequence": None}, "usage": {"output_tokens": 5}})
        sse(self.wfile, "message_stop", {"type": "message_stop"})
        self.close_connection = True

srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
print(srv.server_address[1], flush=True)
srv.serve_forever()
PY
# A minimal MCP server so a tool name starts with mcp__agent-net__.
cat > "$root/bin/mcpstub.py" <<'PY'
import json, sys

def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()

for line in sys.stdin:
    if not line.strip():
        continue
    msg = json.loads(line)
    method, id = msg.get("method"), msg.get("id")
    if method == "initialize":
        send({"jsonrpc": "2.0", "id": id, "result": {
            "protocolVersion": msg.get("params", {}).get("protocolVersion", "2025-06-18"),
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "probe", "version": "1.0.0"}}})
    elif method == "tools/list":
        send({"jsonrpc": "2.0", "id": id, "result": {"tools": [{
            "name": "probe_echo", "description": "Echo a probe value.",
            "inputSchema": {"type": "object", "properties": {}, "additionalProperties": False}}]}})
    elif method == "tools/call":
        send({"jsonrpc": "2.0", "id": id, "result": {
            "content": [{"type": "text", "text": "probe mcp ok"}], "isError": False}})
PY
printf '%s\n' "{\"mcpServers\":{\"agent-net\":{\"command\":\"python3\",\"args\":[\"$root/bin/mcpstub.py\"]}}}" > "$root/mcp.json"

export FAKE_API_LOG="$api_log"
export PROBE_WRITE="$root/work/never-written.txt"
export PROBE_DENY_FILE="$root/work/deny-ran.txt"
export PROBE_ALLOW_FILE="$root/work/allow-ran.txt"
: > "$api_log"
python3 "$root/bin/fakeapi.py" > "$root/port" 2> "$root/api.err" &
api_pid=$!
for _ in $(seq 1 100); do [ -s "$root/port" ] && break; sleep 0.05; done
port=$(cat "$root/port")
export ANTHROPIC_BASE_URL="http://127.0.0.1:$port"
case "$port" in ''|*[!0-9]*) echo "fake API did not start" >&2; cat "$root/api.err" >&2; exit 1;; esac

# One worker row for the band. The module calls `agent-net workers ...`.
cat > "$root/bin/agent-net" <<'EOF'
#!/bin/sh
if [ "$1" = "workers" ]; then
  printf '%s\n' '{"workers":[{"session_id":"w_probe","harness":"opencode","model":"deepseek-v4.1-flash","project":"probe","name":"probe-worker","online":true,"status":"running","owner":"s_owner","owner_name":"probe-owner","started_at":1795615000,"task":"Probe task line","title":"Probe worker title","context_tokens":84000,"context_limit":200000}],"queued":[]}'
  exit 0
fi
exit 0
EOF
chmod +x "$root/bin/agent-net"

claude_run() {
  ( cd "$root/work" && timeout 120 claude "$@" > "$root/out" 2> "$root/err" )
}
plugin_json() { claude plugin list --json 2>/dev/null; }
plugin_enabled() {
  plugin_json | python3 -c 'import json,sys; d=json.load(sys.stdin); print(next((str(p.get("enabled")).lower() for p in d if p.get("id")==sys.argv[1]), "missing"))' "$1"
}

# wait_pane SESSION TEXT: poll the pane until TEXT appears.
wait_pane() {
  for _ in $(seq 1 200); do
    tmux -L "$tmux_socket" capture-pane -p -t "$1" 2>/dev/null | grep -qF -- "$2" && return 0
    sleep 0.05
  done
  return 1
}
# send_keys SESSION TEXT: type text literally, then Enter.
send_keys() { tmux -L "$tmux_socket" send-keys -t "$1" -l "$2"; tmux -L "$tmux_socket" send-keys -t "$1" Enter; }
kill_tmux() { tmux -L "$tmux_socket" kill-server 2>/dev/null || true; }
# answer_dialog SESSION MARKER: press Enter; if MARKER stays, press Space and
# Enter. Returns when MARKER leaves the pane.
answer_dialog() {
  tmux -L "$tmux_socket" send-keys -t "$1" Enter
  for _ in $(seq 1 100); do
    tmux -L "$tmux_socket" capture-pane -p -t "$1" 2>/dev/null | grep -qF -- "$2" || return 0
    sleep 0.05
  done
  tmux -L "$tmux_socket" send-keys -t "$1" Space
  tmux -L "$tmux_socket" send-keys -t "$1" Enter
  for _ in $(seq 1 100); do
    tmux -L "$tmux_socket" capture-pane -p -t "$1" 2>/dev/null | grep -qF -- "$2" || return 0
    sleep 0.05
  done
  return 1
}
trap 'kill_tmux; kill "$api_pid" 2>/dev/null; rm -rf "$root"' EXIT

# --- 1. The plugin loads from the skills folder with no settings file. ------
mkdir -p "$CLAUDE_CONFIG_DIR/skills/probe/.claude-plugin" "$CLAUDE_CONFIG_DIR/skills/probe/hooks"
cat > "$CLAUDE_CONFIG_DIR/skills/probe/.claude-plugin/plugin.json" <<'EOF'
{"name":"probe","version":"0.1.0","description":"agent-net plugin proof"}
EOF
python3 - "$log" "$CLAUDE_CONFIG_DIR/skills/probe/hooks/hooks.json" "$root" <<'PY'
import json, sys
log, out, root = sys.argv[1], sys.argv[2], sys.argv[3]
def group(name, matcher=None, condition=None, timeout=2):
    h = {"type": "command", "command": f"sh {root}/bin/loghook.sh {name} {log}", "timeout": timeout}
    if condition:
        h["if"] = condition
    g = {"hooks": [h]}
    if matcher is not None:
        g["matcher"] = matcher
    return g
def allow(matcher):
    return {"matcher": matcher, "hooks": [{"type": "command", "command": f"sh {root}/bin/allowhook.sh"}]}
hooks = {
 "SessionStart": [group("policy-sessionstart-compact", "compact"), group("sessionstart-any")],
 "UserPromptSubmit": [group("needs-prompt"), group("policy-prompt")],
 "PreToolUse": [allow("AskUserQuestion|ExitPlanMode"), group("needs-pretool"),
                group("tooldecide-pre", "Bash|mcp__agent-net__.*"),
                group("hint-pre-if", "Bash", "Bash(gh pr merge*)")],
 "PostToolUse": [group("hint-posttooluse", "Bash"), group("needs-postdialog", "AskUserQuestion|ExitPlanMode"), group("urgent-post")],
 "PostToolUseFailure": [group("hint-postfailure"), group("needs-postfaildialog", "AskUserQuestion|ExitPlanMode"), group("urgent-postfail")],
 "Stop": [group("policy-stop")],
 "PermissionRequest": [group("needs-permission")],
 "Notification": [group("needs-notification", "agent_needs_input|elicitation_dialog|elicitation_url_dialog"), group("notification-any")],
 "PostCompact": [group("policy-compact")],
}
json.dump({"hooks": hooks}, open(out, "w"), indent=1)
PY
check "plugin scaffold validates" "claude plugin validate '$CLAUDE_CONFIG_DIR/skills/probe' >/dev/null 2>&1"
check "probe@skills-dir is enabled" "[ \"\$(plugin_enabled probe@skills-dir)\" = true ]"
check "no settings.json needed" "[ ! -e '$CLAUDE_CONFIG_DIR/settings.json' ]"
check "no enabledPlugins entry" "! grep -q enabledPlugins '$CLAUDE_CONFIG_DIR/.claude.json' 2>/dev/null"
check "no marketplace entry" "! grep -q extraKnownMarketplaces '$CLAUDE_CONFIG_DIR/.claude.json' 2>/dev/null"

# Interactive sessions need onboarding done and the throwaway key approved.
# This is test setup only and adds no enabledPlugins or marketplace entry.
python3 - "$CLAUDE_CONFIG_DIR/.claude.json" "$root/work" "$ANTHROPIC_API_KEY" <<'PY'
import json, sys
path, work, key = sys.argv[1], sys.argv[2], sys.argv[3]
d = json.load(open(path))
d["hasCompletedOnboarding"] = True
d["theme"] = "dark"
d["customApiKeyResponses"] = {"approved": [key[-20:]], "rejected": []}
d.setdefault("projects", {})[work] = {"hasTrustDialogAccepted": True, "allowedTools": []}
json.dump(d, open(path, "w"), indent=2)
PY

# --- 2. Print sessions: events and matchers. --------------------------------
clear_log
claude_run -p "use-bash-ok" --model "$model" --allowedTools "Bash" || true
check "print: SessionStart" "has sessionstart-any"
check "print: UserPromptSubmit runs both groups" "[ \"\$(rows needs-prompt)\" -eq 1 ] && [ \"\$(rows policy-prompt)\" -eq 1 ]"
check "print: PreToolUse all + Bash matcher" "has needs-pretool && has tooldecide-pre"
check "print: hint PostToolUse/Bash" "has hint-posttooluse && hasnt hint-postfailure"
check "print: urgent PostToolUse" "has urgent-post"
check "print: Stop" "has policy-stop"

clear_log
claude_run -p "use-bash-fail" --model "$model" --allowedTools "Bash" || true
check "print: PostToolUseFailure all" "has hint-postfailure && has urgent-postfail"
check "print: no PostToolUse on failure" "hasnt hint-posttooluse && hasnt urgent-post"

clear_log
claude_run -p "use-gh" --model "$model" --allowedTools "Bash" || true
check "print: hook 'if' runs only for its command" "has hint-pre-if"

clear_log
claude_run -p "use-mcp" --model "$model" --allowedTools "mcp__agent-net__probe_echo" --mcp-config "$root/mcp.json" || true
check "print: MCP tool matches Bash|mcp__agent-net__.*" "has tooldecide-pre"
check "print: urgent hook sees the MCP tool" "has urgent-post"

clear_log
claude_run -p "use-write" --model "$model" || true
check "print: PermissionRequest runs" "has needs-permission"

# --- 3. Interactive session: dialogs, compact, and the band. ----------------
# The throwaway settings allow the two dialog tools, as a user's own settings
# would. No enabledPlugins entry is added.
printf '%s\n' '{"permissions":{"allow":["Bash","AskUserQuestion","ExitPlanMode"]}}' > "$CLAUDE_CONFIG_DIR/settings.json"
if command -v tmux >/dev/null 2>&1; then
  clear_log
  tmux -L "$tmux_socket" -f /dev/null new-session -d -s ask -x 120 -y 34 "cd $root/work && claude --model $model"
  if wait_pane ask "❯"; then
    send_keys ask "use-ask"
    if wait_pane ask "Enter to select"; then
      answer_dialog ask "Enter to select" || true
      for _ in $(seq 1 300); do has needs-postdialog && break; sleep 0.05; done
      if has needs-postdialog; then
        ok "interactive: PostToolUse AskUserQuestion matcher"
      else
        tmux -L "$tmux_socket" capture-pane -p -t ask 2>/dev/null | tail -12 >&2 || true
        bad "interactive: PostToolUse AskUserQuestion matcher"
      fi
    else
      tmux -L "$tmux_socket" capture-pane -p -t ask 2>/dev/null | tail -12 >&2 || true
      bad "interactive: AskUserQuestion dialog did not appear"
    fi
  else bad "interactive: session did not reach the prompt"
  fi
  tmux -L "$tmux_socket" kill-session -t ask 2>/dev/null || true

  clear_log
  tmux -L "$tmux_socket" -f /dev/null new-session -d -s plan -x 120 -y 34 "cd $root/work && claude --model $model --permission-mode plan"
  if wait_pane plan "❯"; then
    send_keys plan "use-plan"
    if wait_pane plan "Exit plan mode"; then
      answer_dialog plan "Exit plan mode" || true
      for _ in $(seq 1 300); do has needs-postdialog && break; sleep 0.05; done
      check "interactive: PostToolUse ExitPlanMode matcher" "has needs-postdialog"
    else bad "interactive: ExitPlanMode dialog did not appear"
    fi
  else bad "interactive: plan session did not reach the prompt"
  fi
  tmux -L "$tmux_socket" kill-session -t plan 2>/dev/null || true

  clear_log
  tmux -L "$tmux_socket" -f /dev/null new-session -d -s compact -x 120 -y 34 "cd $root/work && claude --model $model"
  if wait_pane compact "❯"; then
    send_keys compact "use-bash-ok"
    for _ in $(seq 1 200); do has policy-stop && break; sleep 0.05; done
    if has policy-stop; then
      send_keys compact "/compact"
      for _ in $(seq 1 300); do has policy-compact && break; sleep 0.05; done
      check "interactive: PostCompact" "has policy-compact"
      check "interactive: SessionStart compact" "has policy-sessionstart-compact"
    else bad "interactive: first turn did not finish"
    fi
  else bad "interactive: compact session did not reach the prompt"
  fi
  tmux -L "$tmux_socket" kill-session -t compact 2>/dev/null || true

  # The real plugin, its module, and a stub agent-net in PATH.
  mkdir -p "$CLAUDE_CONFIG_DIR/skills/agent-net-workers"
  cp -R "$repo/plugins/claude/agent-net-workers/." "$CLAUDE_CONFIG_DIR/skills/agent-net-workers/"
  check "worker window plugin is enabled from skills" "[ \"\$(plugin_enabled agent-net-workers@skills-dir)\" = true ]"
  for size in 120x34 80x24; do
    cols=${size%x*}; lines=${size#*x}
    tmux -L "$tmux_socket" -f /dev/null new-session -d -s "band-$size" -x "$cols" -y "$lines" "cd $root/work && PATH=$root/bin:\$PATH claude --model $model"
    if wait_pane "band-$size" "Probe worker title"; then
      tmux -L "$tmux_socket" capture-pane -p -t "band-$size" > "$out/band-$size.txt"
      check "worker window renders at $size" "grep -q 'agent-net workers' '$out/band-$size.txt' && grep -q 'Probe worker title' '$out/band-$size.txt'"
    else
      bad "worker window did not render at $size"
    fi
    tmux -L "$tmux_socket" kill-session -t "band-$size" 2>/dev/null || true
  done
  tmux -L "$tmux_socket" kill-server 2>/dev/null || true
else
  note "interactive checks need tmux"
fi

# --- 4. Background session. -------------------------------------------------
clear_log
bg_out=$(cd "$root/work" && timeout 60 claude --bg "use-bash-ok" --model "$model" --allowedTools "Bash" 2>/dev/null) || true
bg_id=$(printf '%s' "$bg_out" | sed -n 's/^backgrounded · //p' | head -1)
if [ -n "$bg_id" ]; then
  for _ in $(seq 1 400); do has policy-stop && break; sleep 0.05; done
  check "background: hooks run" "has sessionstart-any && has tooldecide-pre && has policy-stop"
  ( cd "$root/work" && timeout 30 claude stop "$bg_id" >/dev/null 2>&1 ) || true
else
  bad "background: no session id"
fi

# --- 5. The user's off switch. ---------------------------------------------
check "plugin disable succeeds" "claude plugin disable probe@skills-dir >/dev/null 2>&1"
check "disable writes enabledPlugins to settings.json" "grep -q 'probe@skills-dir' '$CLAUDE_CONFIG_DIR/settings.json'"
clear_log
claude_run -p "use-bash-ok" --model "$model" --allowedTools "Bash" || true
check "disabled: first session runs no hook" "[ \"\$(wc -l < '$log')\" -eq 0 ]"
clear_log
claude_run -p "use-bash-ok" --model "$model" --allowedTools "Bash" || true
check "disabled: second session runs no hook" "[ \"\$(wc -l < '$log')\" -eq 0 ]"
check "plugin enable succeeds" "claude plugin enable probe@skills-dir >/dev/null 2>&1"
clear_log
claude_run -p "use-bash-ok" --model "$model" --allowedTools "Bash" || true
check "enabled again: hooks run" "has policy-stop"

# --- 6. Old settings entry plus the plugin: the command runs twice. ---------
cat > "$CLAUDE_CONFIG_DIR/settings.json" <<EOF
{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"sh $root/bin/loghook.sh same-command $log"}]}]}}
EOF
mkdir -p "$CLAUDE_CONFIG_DIR/skills/dup/.claude-plugin" "$CLAUDE_CONFIG_DIR/skills/dup/hooks"
python3 - "$log" "$CLAUDE_CONFIG_DIR/skills/dup/hooks/hooks.json" "$root" <<'PY'
import json, sys
log, out, root = sys.argv[1], sys.argv[2], sys.argv[3]
json.dump({"hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": f"sh {root}/bin/loghook.sh same-command {log}"}]}]}}, open(out, "w"))
PY
printf '%s\n' '{"name":"dup","version":"0.1.0","description":"duplicate proof"}' > "$CLAUDE_CONFIG_DIR/skills/dup/.claude-plugin/plugin.json"
clear_log
claude_run -p "use-bash-ok" --model "$model" || true
check "both present: the same command runs twice" "[ \"\$(rows same-command)\" -eq 2 ]"
printf '{}\n' > "$CLAUDE_CONFIG_DIR/settings.json"
clear_log
claude_run -p "use-bash-ok" --model "$model" || true
check "settings entry removed: one run" "[ \"\$(rows same-command)\" -eq 1 ]"
rm -rf "$CLAUDE_CONFIG_DIR/skills/dup"

# --- 7. Old marketplace plugin plus the skills plugin. ---------------------
mkdir -p "$root/market/agent-net-workers" "$root/market/.claude-plugin"
cp -R "$repo/plugins/claude/agent-net-workers/." "$root/market/agent-net-workers/"
printf '%s\n' '{"name":"agent-net","owner":{"name":"proof"},"plugins":[{"name":"agent-net-workers","source":"./agent-net-workers","description":"proof copy"}]}' > "$root/market/.claude-plugin/marketplace.json"
claude plugin marketplace add "$root/market" >/dev/null 2>&1 || true
claude plugin install agent-net-workers@agent-net -s user >/dev/null 2>&1 || true
state=$(plugin_enabled agent-net-workers@skills-dir)
check "marketplace copy shadows the skills copy" "[ '$state' = false ]"
check "marketplace entry is in settings.json" "grep -q 'agent-net-workers@agent-net' '$CLAUDE_CONFIG_DIR/settings.json'"
claude plugin uninstall agent-net-workers@agent-net >/dev/null 2>&1 || true
claude plugin marketplace remove agent-net >/dev/null 2>&1 || true
check "old entries removed: skills copy is enabled" "[ \"\$(plugin_enabled agent-net-workers@skills-dir)\" = true ]"

# --- 8. Hook output from the plugin is obeyed, with no settings file. ------
# Claude Code must act on what a skills-folder plugin hook prints, not only run
# it: a PreToolUse decision, additionalContext on each event, and a Stop block.
# Each has a control with that one hook removed; the disabled plugin is the
# last check.
rm -f "$CLAUDE_CONFIG_DIR/settings.json"

# write_probe_hooks writes the probe hooks.json. MODE names the one feature to
# leave out, so a control run has the same plugin without that hook.
write_probe_hooks() {
python3 - "$CLAUDE_CONFIG_DIR/skills/probe/hooks/hooks.json" "$root" "$log" "$1" <<'PY'
import json, sys
out, root, log, mode = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
def loghook(name):
    return {"type": "command", "command": f"sh {root}/bin/loghook.sh {name} {log}"}
def ctx(event, text):
    return {"type": "command", "command": f"sh {root}/bin/context.sh {event} {text}"}
def group(matcher, hooks):
    return {"matcher": matcher, "hooks": hooks} if matcher else {"hooks": hooks}
hooks = {}
if mode != "no-compact":
    hooks["SessionStart"] = [group("compact", [loghook("output-sessionstart"),
        ctx("SessionStart", "PROBE_SESSIONSTART_COMPACT")])]
else:
    hooks["SessionStart"] = [group("compact", [loghook("output-sessionstart")])]
hooks["PostCompact"] = [{"hooks": [loghook("output-postcompact")]}]
if mode != "no-prompt":
    hooks["UserPromptSubmit"] = [{"hooks": [loghook("output-prompt"),
        ctx("UserPromptSubmit", "PROBE_PROMPT_CONTEXT")]}]
else:
    hooks["UserPromptSubmit"] = [{"hooks": [loghook("output-prompt")]}]
if mode != "no-post":
    hooks["PostToolUse"] = [group("Bash", [ctx("PostToolUse", "PROBE_POSTTOOL_CONTEXT")])]
if mode != "no-pretoolctx":
    hooks.setdefault("PreToolUse", []).append(group("Bash", [ctx("PreToolUse", "PROBE_PRETOOL_CONTEXT")]))
if mode != "no-failure":
    hooks["PostToolUseFailure"] = [group("Bash", [ctx("PostToolUseFailure", "PROBE_FAILURE_CONTEXT")])]
if mode != "no-stop":
    hooks["Stop"] = [{"hooks": [{"type": "command", "command": f"sh {root}/bin/stopblock.sh {root}/stopcount"},
        loghook("output-stop")]}]
if mode != "no-decider":
    hooks.setdefault("PreToolUse", []).append(group("Bash", [{"type": "command", "command": f"python3 {root}/bin/decide.py"}]))
json.dump({"hooks": hooks}, open(out, "w"), indent=1)
PY
}
api_calls() { grep -c '"/v1/messages' "$api_log" || true; }
wait_api() {
  for _ in $(seq 1 600); do grep -qF -- "$1" "$api_log" && return 0; sleep 0.05; done
  return 1
}
wait_rows() {
  for _ in $(seq 1 600); do [ "$(rows "$1")" -ge "$2" ] && return 0; sleep 0.05; done
  return 1
}

write_probe_hooks full
check "output checks run with no settings.json" "[ ! -e '$CLAUDE_CONFIG_DIR/settings.json' ]"

# 8a. A deny stops a command that the command line allows; an allow runs one
# that Claude Code would otherwise refuse. The control has no decider hook.
clear_log; : > "$api_log"; rm -f "$PROBE_DENY_FILE"
claude_run -p "use-bash-deny" --model "$model" --allowedTools "Bash" || true
check "plugin PreToolUse deny stops an allowed command" "[ ! -e '$PROBE_DENY_FILE' ]"
check "the deny reason reaches the request body" "grep -qF 'probe deny' '$api_log'"
write_probe_hooks no-decider
: > "$api_log"; rm -f "$PROBE_DENY_FILE"
claude_run -p "use-bash-deny" --model "$model" --allowedTools "Bash" || true
check "control without the deny hook runs the command" "[ -s '$PROBE_DENY_FILE' ]"
write_probe_hooks full
: > "$api_log"; rm -f "$PROBE_ALLOW_FILE"
claude_run -p "use-bash-allow" --model "$model" || true
check "plugin PreToolUse allow runs a command needing permission" "[ -s '$PROBE_ALLOW_FILE' ]"
write_probe_hooks no-decider
: > "$api_log"; rm -f "$PROBE_ALLOW_FILE"
claude_run -p "use-bash-allow" --model "$model" || true
check "control without the allow hook refuses the command" "[ ! -e '$PROBE_ALLOW_FILE' ]"

# 8b. additionalContext on UserPromptSubmit and PostToolUse is in the request
# the stub API receives. Each control removes only its own context hook.
write_probe_hooks full
: > "$api_log"
claude_run -p "use-bash-ok" --model "$model" --allowedTools "Bash" || true
check "UserPromptSubmit context is in the request body" "grep -qF 'PROBE_PROMPT_CONTEXT' '$api_log'"
check "PostToolUse context is in the request body" "grep -qF 'PROBE_POSTTOOL_CONTEXT' '$api_log'"
write_probe_hooks no-prompt
: > "$api_log"
claude_run -p "use-bash-ok" --model "$model" --allowedTools "Bash" || true
check "control without the UserPromptSubmit hook: context absent" "! grep -qF 'PROBE_PROMPT_CONTEXT' '$api_log'"
write_probe_hooks no-post
: > "$api_log"
claude_run -p "use-bash-ok" --model "$model" --allowedTools "Bash" || true
check "control without the PostToolUse hook: context absent" "! grep -qF 'PROBE_POSTTOOL_CONTEXT' '$api_log'"
write_probe_hooks full
: > "$api_log"
claude_run -p "use-bash-ok" --model "$model" --allowedTools "Bash" || true
check "PreToolUse context is in the request body" "grep -qF 'PROBE_PRETOOL_CONTEXT' '$api_log'"
write_probe_hooks no-pretoolctx
: > "$api_log"
claude_run -p "use-bash-ok" --model "$model" --allowedTools "Bash" || true
check "control without the PreToolUse context hook: context absent" "! grep -qF 'PROBE_PRETOOL_CONTEXT' '$api_log'"
write_probe_hooks full
: > "$api_log"
claude_run -p "use-bash-fail" --model "$model" --allowedTools "Bash" || true
check "PostToolUseFailure context is in the request body" "grep -qF 'PROBE_FAILURE_CONTEXT' '$api_log'"
write_probe_hooks no-failure
: > "$api_log"
claude_run -p "use-bash-fail" --model "$model" --allowedTools "Bash" || true
check "control without the PostToolUseFailure hook: context absent" "! grep -qF 'PROBE_FAILURE_CONTEXT' '$api_log'"

# 8c. A Stop hook that blocks makes the turn continue: the reason comes back in
# another request. The control ends the turn at once.
write_probe_hooks full
: > "$api_log"; rm -f "$root/stopcount"
claude_run -p "plain-hello" --model "$model" || true
check "plugin Stop block continues the turn" "[ \"\$(api_calls)\" -ge 2 ]"
check "the Stop reason is in the next request" "grep -qF 'PROBE_STOP_REASON_CONTINUE' '$api_log'"
write_probe_hooks no-stop
: > "$api_log"; rm -f "$root/stopcount"
claude_run -p "plain-hello" --model "$model" || true
check "control without the Stop hook ends after one request" "[ \"\$(api_calls)\" -eq 1 ]"

# 8d. SessionStart (compact) context is in the first request after a compaction.
if command -v tmux >/dev/null 2>&1; then
  for mode in full no-compact; do
    label="SessionStart compact context"; [ "$mode" = full ] || label="control without the SessionStart compact hook: context absent"
    write_probe_hooks "$mode"
    clear_log; : > "$api_log"; echo 1 > "$root/stopcount"
    tmux -L "$tmux_socket" -f /dev/null new-session -d -s outcompact -x 120 -y 34 "cd $root/work && claude --model $model --allowedTools Bash"
    if wait_pane outcompact "❯"; then
      send_keys outcompact "use-bash-ok"
      if wait_rows output-stop 1; then
        send_keys outcompact "/compact"
        if wait_rows output-postcompact 1 && wait_rows output-sessionstart 1; then
          : > "$api_log"
          send_keys outcompact "use-bash-ok"
          wait_api PROBE_POSTTOOL_CONTEXT || true
          if [ "$mode" = full ]; then
            check "$label is in the next request" "grep -qF 'PROBE_SESSIONSTART_COMPACT' '$api_log'"
          else
            check "$label" "! grep -qF 'PROBE_SESSIONSTART_COMPACT' '$api_log'"
          fi
        else
          bad "$label: no compaction"
        fi
      else bad "$label: the first turn did not finish"
      fi
    else bad "$label: session did not reach the prompt"
    fi
    tmux -L "$tmux_socket" kill-session -t outcompact 2>/dev/null || true
  done
  tmux -L "$tmux_socket" kill-server 2>/dev/null || true
else
  note "SessionStart compact context needs tmux"
fi

# 8e. A background session gets the same UserPromptSubmit context.
write_probe_hooks full
clear_log; : > "$api_log"
bg_out=$(cd "$root/work" && timeout 60 claude --bg "use-bash-ok" --model "$model" --allowedTools "Bash" 2>/dev/null) || true
bg_id=$(printf '%s' "$bg_out" | sed -n 's/^backgrounded · //p' | head -1)
if [ -n "$bg_id" ]; then
  wait_rows output-stop 1 || true
  check "background: UserPromptSubmit context reaches the request" "grep -qF 'PROBE_PROMPT_CONTEXT' '$api_log'"
  ( cd "$root/work" && timeout 30 claude stop "$bg_id" >/dev/null 2>&1 ) || true
else
  bad "background context: no session id"
fi

# 8f. A disabled plugin has none of these effects.
write_probe_hooks full
check "probe can be disabled" "claude plugin disable probe@skills-dir >/dev/null 2>&1"
check "probe reports disabled" "[ \"\$(plugin_enabled probe@skills-dir)\" = false ]"
: > "$api_log"; rm -f "$PROBE_DENY_FILE"
claude_run -p "use-bash-deny" --model "$model" --allowedTools "Bash" || true
check "disabled plugin: the deny hook does not stop the command" "[ -s '$PROBE_DENY_FILE' ]"
: > "$api_log"
claude_run -p "use-bash-ok" --model "$model" --allowedTools "Bash" || true
check "disabled plugin: no additionalContext reaches the request" "! grep -qF 'PROBE_PROMPT_CONTEXT' '$api_log'"
: > "$api_log"; rm -f "$root/stopcount"
claude_run -p "plain-hello" --model "$model" || true
check "disabled plugin: the Stop hook does not continue the turn" "[ \"\$(api_calls)\" -eq 1 ]"

# --- 9. A stale enabledPlugins entry keeps the plugin off when it returns. --
# This is how the worker window stayed off after the old marketplace install:
# disabling writes agent-net-workers@skills-dir:false and it outlives the
# plugin, so a freshly copied plugin at the same id stays disabled.
check "disable writes the off entry" "claude plugin disable probe@skills-dir >/dev/null 2>&1"
mv "$CLAUDE_CONFIG_DIR/skills/probe" "$CLAUDE_CONFIG_DIR/skills/probe-gone"
state_away=$(plugin_enabled probe@skills-dir)
check "the plugin does not load while its folder is away (state=$state_away)" "[ '$state_away' != true ]"
mv "$CLAUDE_CONFIG_DIR/skills/probe-gone" "$CLAUDE_CONFIG_DIR/skills/probe"
check "the stale off entry keeps the returned plugin disabled" "[ \"\$(plugin_enabled probe@skills-dir)\" = false ]"
printf '%s\n' '{"enabledPlugins":{"gone@nosuchmarket":true}}' > "$CLAUDE_CONFIG_DIR/settings.json"
check "a stale entry for another id does not affect it" "[ \"\$(plugin_enabled probe@skills-dir)\" = true ]"
rm -f "$CLAUDE_CONFIG_DIR/settings.json"
check "clearing the stale entry turns the plugin on" "[ \"\$(plugin_enabled probe@skills-dir)\" = true ]"

printf '\nPASS %d  FAIL %d  SKIP %d\n' "$pass" "$fail" "$skip"
printf 'Frames: %s\n' "$out"
printf 'Not shown: the Notification matcher (no Notification event reached a plugin hook or a settings hook in this terminal), PostToolUseFailure for AskUserQuestion/ExitPlanMode (cancelling the dialog produced no failure event), and a PreToolUse decision in a background session (its UserPromptSubmit context is shown).\n'
[ "$fail" -eq 0 ]
