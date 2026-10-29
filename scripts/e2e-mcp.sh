#!/usr/bin/env bash
# End-to-end check of the MCP server registry with real binaries and throwaway
# everything: its own gateway on a free port, two daemons that stand in for two
# machines (separate config, state and socket), and test MCP servers.
#
#   scripts/e2e-mcp.sh           run the checks and clean up
#   scripts/e2e-mcp.sh --keep    run the checks, leave the stack up, print how to use it
#
# It never touches the real agent-net services, sockets or config. The
# directory is short and under /tmp because unix socket paths are limited to
# about 108 bytes.
set -euo pipefail

KEEP=0
[ "${1:-}" = "--keep" ] && KEEP=1
here=$(cd "$(dirname "$0")/.." && pwd)
export PATH="$PATH:$HOME/.local/go/bin"
T=$(mktemp -d /tmp/anet-e2e.XXXXXX)
BIN=$T/bin
mkdir -p "$BIN" "$T/empty"
pids=()
cleanup() {
  [ "$KEEP" = 1 ] && return
  for p in "${pids[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null || true; done
  wait 2>/dev/null || true
  rm -rf "$T"
}
trap cleanup EXIT

pass=0
fail=0
ok() { echo "PASS  $*"; pass=$((pass + 1)); }
bad() { echo "FAIL  $*"; fail=$((fail + 1)); }
expect_contains() { # what, text, needle
  if grep -qF -- "$3" <<<"$2"; then ok "$1"; else bad "$1: wanted '$3' in: $2"; fi
}
expect_absent() { # what, text, needle
  if grep -qF -- "$3" <<<"$2"; then bad "$1: found '$3' in: $2"; else ok "$1"; fi
}

echo "== build"
(cd "$here" && go build -o "$BIN/agent-net" ./cmd/agent-net &&
  go build -o "$BIN/testmcp-stdio" ./internal/testmcp/stdio &&
  go build -o "$BIN/testmcp-http" ./internal/testmcp/http)

port=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')
"$BIN/agent-net" gateway --listen "127.0.0.1:$port" --data "$T/gw" >"$T/gateway.log" 2>&1 &
pids+=($!)
for _ in $(seq 50); do grep -q "listening" "$T/gateway.log" && break; sleep 0.1; done

# an NODE ARGS...: run agent-net as one simulated machine.
an() {
  local n=$1
  shift
  AGENT_NET_CONFIG_DIR=$T/$n/cfg AGENT_NET_STATE_DIR=$T/$n/state AGENT_NET_SOCKET=$T/$n.sock \
    AGENT_NET_CLAUDE_SESSIONS_DIR=$T/empty "$BIN/agent-net" "$@"
}
for n in a b; do
  mkdir -p "$T/$n"
  join=$("$BIN/agent-net" enroll --data "$T/gw" --host 127.0.0.1 --port "$port" 2>/dev/null)
  an $n join "$join" >/dev/null
  # The daemon of each machine gets this variable; a stdio server asks for it by name.
  ( export DAEMON_SECRET="secret-from-$n-0123456789"; an $n daemon >"$T/daemon-$n.log" 2>&1 ) &
  pids+=($!)
done
for n in a b; do
  for _ in $(seq 50); do an $n status >/dev/null 2>&1 && break; sleep 0.1; done
done
echo "gateway 127.0.0.1:$port, daemons a and b up (dir $T)"

echo "== (a) stdio server hosted on A, called from B"
out=$(an a mcp add stdiotest --env DAEMON_SECRET -- "$BIN/testmcp-stdio")
expect_contains "add on A lists tools" "$out" "stdiotest__echo"
expect_contains "add says it is global" "$out" "every agent on every machine"
out=$(an b mcp call stdiotest echo '{"text":"hello from b"}')
expect_contains "B calls the tool hosted on A" "$out" "echo: hello from b"
pidA=$(pgrep -f "$BIN/testmcp-stdio" | head -1)
[ -n "$pidA" ] && ok "the process runs on A's daemon (pid $pidA)" || bad "no testmcp-stdio process"
out=$(an b mcp list)
expect_contains "list shows host and who added" "$out" "stdiotest"
expect_contains "list shows the harness that added it" "$out" "cli"
out=$(an b mcp call stdiotest env '{"name":"DAEMON_SECRET"}')
expect_absent "the env secret never reaches the caller" "$out" "secret-from-a-0123456789"
expect_contains "the secret is redacted" "$out" "[redacted]"
an b mcp call stdiotest crash >/dev/null 2>&1 || true
for _ in $(seq 100); do
  out=$(an b mcp call stdiotest pid 2>/dev/null) && [ "$out" != "$pidA" ] && break
  sleep 0.2
done
[ -n "$out" ] && [ "$out" != "$pidA" ] && ok "the crashed server was restarted (new pid $out)" || bad "no restart: pid $out"

echo "== (b) streamable HTTP server with a bearer token read from a file"
printf 'right-token-abcdef\n' >"$T/token"
"$BIN/testmcp-http" --listen 127.0.0.1:0 --token-file "$T/token" >"$T/http.log" 2>&1 &
pids+=($!)
for _ in $(seq 50); do grep -q listening "$T/http.log" && break; sleep 0.1; done
url="http://$(sed -n 's/^listening on //p' "$T/http.log")/mcp"
printf 'wrong-token-zzzzzz\n' >"$T/wrongtoken"
set +e
out=$(an a mcp add homelab --url "$url" --header-file "Authorization=$T/wrongtoken" 2>&1)
set -e
expect_contains "wrong token is refused clearly" "$out" "rejected the credentials"
expect_absent "wrong token not echoed" "$out" "wrong-token-zzzzzz"
expect_absent "right token not echoed" "$out" "right-token-abcdef"
out=$(an a mcp list)
expect_absent "failed add was not saved" "$out" "homelab"
out=$(an a mcp add homelab --url "$url" --header-file "Authorization=$T/token")
expect_contains "right token is accepted" "$out" "homelab__echo"
out=$(an b mcp call homelab echo '{"text":"over http"}')
expect_contains "B calls the HTTP server" "$out" "echo: over http"
out=$(an b mcp list)
expect_contains "list shows the auth source as a path" "$out" "file $T/token"
expect_absent "list never shows the token" "$out" "right-token-abcdef"
cp "$T/wrongtoken" "$T/token"
set +e
out=$(an b mcp test homelab 2>&1)
set -e
expect_contains "a later bad token is reported by test" "$out" "rejected the credentials"
expect_absent "test output holds no token" "$out" "wrong-token-zzzzzz"
expect_absent "daemon log holds no token" "$(cat "$T"/daemon-*.log "$T/gateway.log")" "right-token-abcdef"
expect_absent "daemon log holds no env secret" "$(cat "$T"/daemon-*.log "$T/gateway.log")" "secret-from-a-0123456789"

echo "== (e, CLI part) remove, disable, enable"
an b mcp disable stdiotest >/dev/null
set +e
out=$(an b mcp call stdiotest echo '{"text":"x"}' 2>&1)
set -e
expect_contains "a disabled server refuses calls" "$out" "disabled"
an b mcp enable stdiotest >/dev/null
for _ in $(seq 50); do out=$(an b mcp call stdiotest echo '{"text":"back"}' 2>/dev/null) && break; sleep 0.2; done
expect_contains "enable brings it back" "$out" "echo: back"
an b mcp remove stdiotest >/dev/null
an b mcp remove homelab >/dev/null
out=$(an a mcp list)
expect_absent "remove empties the registry" "$out" "stdiotest"
for _ in $(seq 50); do pgrep -f "$BIN/testmcp-stdio" >/dev/null || break; sleep 0.2; done
pgrep -f "$BIN/testmcp-stdio" >/dev/null && bad "the removed server still runs" || ok "the host stopped the removed server"

echo
echo "$pass passed, $fail failed"
if [ "$KEEP" = 1 ]; then
  cat <<EOF

Stack kept in $T. Use it with:
  export AGENT_NET_CONFIG_DIR=$T/b/cfg AGENT_NET_STATE_DIR=$T/b/state AGENT_NET_SOCKET=$T/b.sock
  $BIN/agent-net mcp list
Stop it with: pkill -f $T; rm -rf $T
EOF
  trap - EXIT
fi
[ "$fail" = 0 ]
