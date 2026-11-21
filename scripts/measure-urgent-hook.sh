#!/usr/bin/env bash
# Wall time of `agent-net urgent-hook` when no urgent message waits (decision
# 0180). It starts a throwaway gateway and daemon in temp dirs, so it never
# touches the real setup, then runs the hook N times and prints p50 and p95.
#
#   scripts/measure-urgent-hook.sh /path/to/agent-net [runs] [binary|command]
#
# "binary" (default) runs `agent-net urgent-hook` itself. "command" runs the
# command line that setup writes into Claude Code's settings, as Claude Code
# does, through sh -c: it looks for a marker first and starts agent-net only
# when one exists. Keep the text below equal to urgentCommand in
# internal/setup/urgenthook.go.
set -euo pipefail
bin=${1:?usage: measure-urgent-hook.sh /path/to/agent-net [runs]}
runs=${2:-100}
mode=${3:-binary}
bin=$(readlink -f "$bin")
tmp=$(mktemp -d)
pids=()
cleanup() { kill "${pids[@]}" 2>/dev/null || true; wait 2>/dev/null || true; rm -rf "$tmp"; }
trap cleanup EXIT
mkdir -p "$tmp/cfg" "$tmp/state" "$tmp/claude" "$tmp/home" "$tmp/gw"
export HOME="$tmp/home" AGENT_NET_CONFIG_DIR="$tmp/cfg" AGENT_NET_STATE_DIR="$tmp/state" \
  AGENT_NET_SOCKET="$tmp/d.sock" AGENT_NET_CLAUDE_SESSIONS_DIR="$tmp/claude" AGENT_NET_GATEWAY_DATA="$tmp/gw"
unset XDG_STATE_HOME XDG_CONFIG_HOME XDG_RUNTIME_DIR
port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
aport=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')
"$bin" gateway --listen "127.0.0.1:$port" --apps-listen "127.0.0.1:$aport" --data "$tmp/gw" --san 127.0.0.1 >"$tmp/gateway.log" 2>&1 &
pids+=($!)
for _ in $(seq 100); do [ -s "$tmp/gw/ca.pem" ] && break; sleep 0.1; done
line=$("$bin" enroll --data "$tmp/gw" --host 127.0.0.1 --port "$port" 2>/dev/null)
"$bin" join --config-dir "$tmp/cfg" "$line" >"$tmp/join.log" 2>&1
"$bin" daemon >"$tmp/daemon.log" 2>&1 &
pids+=($!)
for _ in $(seq 100); do [ -S "$tmp/d.sock" ] && break; sleep 0.1; done
sleep 2 # the daemon is idle: connected and quiet
"$bin" status >/dev/null || { echo "the throwaway daemon is not up"; cat "$tmp/daemon.log"; exit 1; }
input='{"session_id":"11111111-2222-3333-4444-555555555555","hook_event_name":"PostToolUse","tool_name":"Bash"}'
times=()
for _ in $(seq "$runs"); do
  s=$EPOCHREALTIME
  if [ "$mode" = command ]; then
    sh -c 'set -- "${AGENT_NET_STATE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/agent-net/daemon}"/urgent/*; [ -e "$1" ] || exit 0; exec '"$bin"' urgent-hook' <<<"$input" >/dev/null
  else
    "$bin" urgent-hook <<<"$input" >/dev/null
  fi
  e=$EPOCHREALTIME
  times+=("$(( (${e/./} - ${s/./}) ))")
done
echo "mode=$mode"; printf '%s\n' "${times[@]}" | sort -n | awk -v n="$runs" '{a[NR]=$1} END {
  printf "runs=%d p50=%.1fms p95=%.1fms min=%.1fms max=%.1fms\n", n, a[int((n+1)/2)]/1000, a[int(n*0.95)]/1000, a[1]/1000, a[n]/1000 }'
