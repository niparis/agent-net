#!/bin/bash
# End-to-end test of `agent-net upgrade` with real binaries and throwaway state.
#
#   scripts/e2e-upgrade.sh
#
# Builds two fake releases (v0.0.1-test, v0.0.2-test), serves them from a local
# HTTP server that stands in for GitHub, and runs a gateway plus daemons for
# three "machines" (gateway host, B, C), each with its own HOME, config, state,
# socket and install dir. Nothing real is touched: no services, no real HOME.
set -u

cd "$(dirname "$0")/.."
ROOT="$PWD"
GO="${GO:-go}"
command -v "$GO" >/dev/null 2>&1 || GO=/home/nicolas/.local/go/bin/go
V1=v0.0.1-test
V2=v0.0.2-test

T="$(mktemp -d "${TMPDIR:-/tmp}/agent-net-e2e.XXXXXX")"
PIDS=()
FAILS=0
cleanup() {
  for p in "${PIDS[@]}"; do kill "$p" 2>/dev/null; done
  # re-exec keeps the pid, but be sure nothing started from the temp dir survives
  pkill -f "$T/" 2>/dev/null
  if [ -z "${KEEP:-}" ]; then rm -rf "$T"; else echo "kept $T"; fi
}
trap cleanup EXIT

say() { echo; echo "=== $*"; }
check() { # check DESCRIPTION CONDITION-COMMAND...
  local d="$1"; shift
  if "$@"; then echo "PASS: $d"; else echo "FAIL: $d"; FAILS=$((FAILS + 1)); fi
}
contains() { grep -qF -- "$2" <<<"$1"; }
free_port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])'; }
wait_for() { # wait_for DESCRIPTION SECONDS COMMAND...
  local d="$1" n="$2"; shift 2
  for _ in $(seq $((n * 5))); do "$@" >/dev/null 2>&1 && return 0; sleep 0.2; done
  echo "timed out waiting for: $d"; FAILS=$((FAILS + 1)); return 1
}

# ---- fake releases ----
say "build $V1 and $V2"
REL="$T/releases"
for v in $V1 $V2; do
  mkdir -p "$REL/download/$v"
  for os in linux darwin; do
    for arch in amd64 arm64; do
      CGO_ENABLED=0 GOOS=$os GOARCH=$arch "$GO" build -trimpath -ldflags "-s -w -X main.version=$v" \
        -o "$REL/download/$v/agent-net-$os-$arch" ./cmd/agent-net || exit 1
    done
  done
  (cd "$REL/download/$v" && sha256sum agent-net-* > checksums.txt)
done
echo "$V2" > "$REL/LATEST"

cat > "$T/release_server.py" <<'EOF'
# python3 -m http.server, plus the redirect GitHub does for releases/latest/download/<file>.
import http.server, os, sys
root = sys.argv[1]
class H(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *a, **k): super().__init__(*a, directory=root, **k)
    def log_message(self, *a): pass
    def redirect(self):
        if self.path.startswith("/latest/download/"):
            tag = open(os.path.join(root, "LATEST")).read().strip()
            self.send_response(302)
            self.send_header("Location", "/download/%s/%s" % (tag, self.path.split("/")[-1]))
            self.end_headers()
            return True
    def do_GET(self):
        if not self.redirect(): super().do_GET()
    def do_HEAD(self):
        if not self.redirect(): super().do_HEAD()
http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[2])), H).serve_forever()
EOF
RP=$(free_port)
python3 "$T/release_server.py" "$REL" "$RP" &
PIDS+=($!)
RELEASE_URL="http://127.0.0.1:$RP"
wait_for "release server" 10 curl -sf "$RELEASE_URL/LATEST"

# ---- three machines ----
GP=$(free_port)
for m in gw b c; do mkdir -p "$T/$m/home" "$T/$m/bin" "$T/$m/claude-sessions"; done
mkdir -p "$T/gw/dist"

# menv NAME: set E to the clean environment of machine NAME.
menv() {
  local m="$1"
  E=(HOME="$T/$m/home" PATH=/usr/bin:/bin
    AGENT_NET_NO_SERVICE=1
    AGENT_NET_CONFIG_DIR="$T/$m/config" AGENT_NET_STATE_DIR="$T/$m/state" AGENT_NET_SOCKET="$T/$m/d.sock"
    AGENT_NET_CLAUDE_SESSIONS_DIR="$T/$m/claude-sessions"
    AGENT_NET_RELEASE_BASE_URL="$RELEASE_URL")
  if [ "$m" = gw ]; then E+=(AGENT_NET_GATEWAY_DATA="$T/gw/data"); else E+=(AGENT_NET_GATEWAY_DATA="$T/$m/no-gateway"); fi
}
# machine NAME ARGS...: run agent-net as machine NAME.
machine() {
  local m="$1"; shift
  menv "$m"
  env -i "${E[@]}" "$T/$m/bin/agent-net" "$@"
}
install_v() { # install_v MACHINE VERSION
  cp "$REL/download/$2/agent-net-linux-amd64" "$T/$1/bin/agent-net"
}
bg() { # bg MACHINE LOGNAME ARGS...
  local m="$1" log="$2"; shift 2
  ( menv "$m"; exec env -i "${E[@]}" "$T/$m/bin/agent-net" "$@" ) >"$T/$m/$log.log" 2>&1 &
  PIDS+=($!)
}

say "start from $V1: gateway host, B and C"
for m in gw b c; do install_v $m $V1; done
cp "$REL/download/$V1"/agent-net-* "$T/gw/dist/"
bg gw gateway gateway --listen "127.0.0.1:$GP" --data "$T/gw/data" --dist "$T/gw/dist"
wait_for "gateway" 15 test -f "$T/gw/data/admin.json"
machine gw setup --gateway --no-service --port "$GP" --data "$T/gw/data" --dist "$T/gw/dist" | sed 's/^/  gw setup: /'
bg gw daemon daemon
for m in b c; do
  JOIN="$(machine gw enroll --host 127.0.0.1 --port "$GP" 2>/dev/null)"
  machine $m setup --join "$JOIN" --no-service | sed "s/^/  $m setup: /"
  bg $m daemon daemon
done
wait_for "daemons connected" 20 bash -c "[ \"\$(grep -c 'connected to gateway' $T/gw/daemon.log $T/b/daemon.log $T/c/daemon.log | grep -c ':1')\" = 3 ]"
# C joined and connected once; now it goes away so the gateway sees it offline.
C_PID="${PIDS[-1]}"
kill "$C_PID"; sleep 1.5

say "agent-net nodes (from B)"
machine b nodes
say "agent-net status (from B)"
machine b status | head -3

# ---- --check before ----
say "gateway: agent-net upgrade --check (before)"
CHECK_BEFORE="$(machine gw upgrade --check)"; echo "$CHECK_BEFORE"
check "check shows the gateway host binary on $V1" contains "$CHECK_BEFORE" "this machine   $V1"
check "check shows the latest release $V2" contains "$CHECK_BEFORE" "latest release $V2"
check "check shows the gateway version" contains "$CHECK_BEFORE" "gateway        $V1"

# ---- tampered release ----
say "tampered binary: upgrade must abort with nothing changed"
GW_SUM_BEFORE="$(sha256sum "$T/gw/bin/agent-net" | cut -d' ' -f1)"
DIST_SUM_BEFORE="$(cat "$T/gw/dist"/agent-net-* | sha256sum)"
cp "$REL/download/$V2/agent-net-darwin-arm64" "$T/darwin-arm64.orig"
echo "tampered" >> "$REL/download/$V2/agent-net-darwin-arm64"
TAMPER_OUT="$(machine gw upgrade 2>&1)"; TAMPER_RC=$?
echo "$TAMPER_OUT"; echo "exit code: $TAMPER_RC"
cp "$T/darwin-arm64.orig" "$REL/download/$V2/agent-net-darwin-arm64"
check "tamper: upgrade failed" test "$TAMPER_RC" -ne 0
check "tamper: reports a checksum mismatch" contains "$TAMPER_OUT" "checksum mismatch"
check "tamper: binary unchanged" test "$(sha256sum "$T/gw/bin/agent-net" | cut -d' ' -f1)" = "$GW_SUM_BEFORE"
check "tamper: no .prev written" test ! -e "$T/gw/bin/agent-net.prev"
check "tamper: dist unchanged" test "$(cat "$T/gw/dist"/agent-net-* | sha256sum)" = "$DIST_SUM_BEFORE"
check "tamper: gateway host still $V1" test "$(machine gw version)" = "$V1"
check "tamper: B still $V1" test "$(machine b version)" = "$V1"

# ---- the upgrade ----
say "gateway: agent-net upgrade"
UP_OUT="$(machine gw upgrade 2>&1)"; UP_RC=$?
echo "$UP_OUT"; echo "exit code: $UP_RC"
check "upgrade exit code 0" test "$UP_RC" -eq 0
check "gateway host binary is $V2" test "$(machine gw version)" = "$V2"
check "gateway host keeps .prev ($V1)" test "$("$T/gw/bin/agent-net.prev" version)" = "$V1"
check "B binary is $V2" test "$(machine b version)" = "$V2"
check "B keeps .prev ($V1)" test "$("$T/b/bin/agent-net.prev" version)" = "$V1"
check "gateway host line says ok" bash -c "grep -E '  $V1 -> $V2  ok\$' <<<\"\$1\" | wc -l | grep -qx 2" _ "$UP_OUT"
check "C is reported offline" contains "$UP_OUT" "offline (will not upgrade until you run agent-net upgrade there)"
check "C binary still $V1" test "$(machine c version)" = "$V1"
check "sessions note printed" contains "$UP_OUT" "running Claude/Pi/OpenCode sessions keep the old version until they restart"
check "dist refreshed" test "$("$T/gw/dist/agent-net-linux-amd64" version)" = "$V2"

say "agent-net nodes (from B, after)"
NODES_AFTER="$(machine b nodes)"; echo "$NODES_AFTER"
check "daemons report $V2" bash -c "[ \$(grep -c '$V2' <<<\"\$1\") -ge 3 ]" _ "$NODES_AFTER"
say "agent-net status (from B, after)"
machine b status | head -1

say "gateway: agent-net upgrade --check (after)"
CHECK_AFTER="$(machine gw upgrade --check)"; echo "$CHECK_AFTER"
check "check after: gateway host $V2" contains "$CHECK_AFTER" "this machine   $V2"
check "check after: gateway $V2" contains "$CHECK_AFTER" "gateway        $V2"

say "gateway: agent-net upgrade again (already on the latest)"
AGAIN_OUT="$(machine gw upgrade 2>&1)"; echo "$AGAIN_OUT"
check "second run says already on $V2" contains "$AGAIN_OUT" "already on $V2"

# ---- rollback on B ----
say "B: agent-net upgrade --rollback"
RB_OUT="$(machine b upgrade --rollback 2>&1)"; echo "$RB_OUT"
check "rollback: B binary is $V1" test "$(machine b version)" = "$V1"
check "rollback: B .prev is $V2" test "$("$T/b/bin/agent-net.prev" version)" = "$V2"
wait_for "B daemon back on $V1" 20 bash -c "$(declare -f machine menv); T='$T'; RELEASE_URL='$RELEASE_URL'; machine b status | head -1 | grep -q '$V1'"
say "B: agent-net status / nodes after rollback"
machine b status | head -1
machine b nodes
check "rollback: gateway host untouched ($V2)" test "$(machine gw version)" = "$V2"

# ---- joined-machine upgrade ----
say "B: agent-net upgrade (joined machine path)"
JU_OUT="$(machine b upgrade 2>&1)"; echo "$JU_OUT"
check "joined upgrade: B is $V2" test "$(machine b version)" = "$V2"
wait_for "B daemon on $V2" 20 bash -c "$(declare -f machine menv); T='$T'; RELEASE_URL='$RELEASE_URL'; machine b status | head -1 | grep -q '$V2'"
say "B: agent-net upgrade --check"
machine b upgrade --check

# ---- dev build ----
say "dev build refuses without --force"
"$GO" build -o "$T/gw/bin/agent-net-dev" ./cmd/agent-net
DEV_OUT="$(cd "$T" && env -i HOME="$T/gw/home" PATH=/usr/bin:/bin AGENT_NET_NO_SERVICE=1 AGENT_NET_GATEWAY_DATA="$T/gw/data" \
  AGENT_NET_CONFIG_DIR="$T/gw/config" AGENT_NET_RELEASE_BASE_URL="$RELEASE_URL" "$T/gw/bin/agent-net-dev" upgrade 2>&1)"; DEV_RC=$?
echo "$DEV_OUT"
check "dev build refused" test "$DEV_RC" -ne 0
check "dev refusal mentions --force" contains "$DEV_OUT" "--force"

echo
if [ "$FAILS" -eq 0 ]; then echo "ALL CHECKS PASSED"; else echo "$FAILS CHECK(S) FAILED"; exit 1; fi
