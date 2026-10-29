#!/bin/bash
# End-to-end test of `agent-net upgrade` with real binaries and throwaway state.
#
#   scripts/e2e-upgrade.sh
#
# Builds three fake releases (v0.0.1, v0.0.2, v0.0.3), serves them from a local
# HTTP server that stands in for GitHub, and runs a gateway plus daemons for
# throwaway "machines" (gateway host, B, C, D, and later E, F, G), each with its
# own HOME, config, state, socket and install dir. Nothing real is touched: no
# services, no real HOME.
#
# Automatic catch-up upgrades are tested too: a machine that was offline during
# `agent-net upgrade` upgrades itself when it reconnects (C), a bad dist binary
# is tried once (D), a rollback is held until a newer release (B), and the
# opt-outs (E env, F flag, G dev build) do nothing.
set -u

cd "$(dirname "$0")/.."
ROOT="$PWD"
GO="${GO:-go}"
command -v "$GO" >/dev/null 2>&1 || GO=/home/nicolas/.local/go/bin/go
V1=v0.0.1
V2=v0.0.2
V3=v0.0.3

T="$(mktemp -d "${TMPDIR:-/tmp}/agent-net-e2e.XXXXXX")"
PIDS=()
declare -A DPID
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
say "build $V1, $V2 and $V3"
REL="$T/releases"
for v in $V1 $V2 $V3; do
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

# ---- machines ----
GP=$(free_port)
for m in gw b c d e f g; do mkdir -p "$T/$m/home" "$T/$m/bin" "$T/$m/claude-sessions"; done
mkdir -p "$T/gw/dist"

# menv NAME: set E to the clean environment of machine NAME.
menv() {
  local m="$1"
  E=(HOME="$T/$m/home" PATH=/usr/bin:/bin
    AGENT_NET_NO_SERVICE=1
    AGENT_NET_CONFIG_DIR="$T/$m/config" AGENT_NET_STATE_DIR="$T/$m/state" AGENT_NET_SOCKET="$T/$m/d.sock"
    AGENT_NET_CLAUDE_SESSIONS_DIR="$T/$m/claude-sessions"
    AGENT_NET_RELEASE_BASE_URL="$RELEASE_URL"
    AGENT_NET_AUTO_UPGRADE_MAX_DELAY=1s)
  if [ "$m" = e ]; then E+=(AGENT_NET_NO_AUTO_UPGRADE=1); fi
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
  ( menv "$m"; exec env -i "${E[@]}" "$T/$m/bin/agent-net" "$@" ) >>"$T/$m/$log.log" 2>&1 &
  LASTPID=$!
  DPID[$m]=$LASTPID
  PIDS+=($LASTPID)
}
# join_machine NAME: enroll and set up NAME with the binary it has now.
join_machine() {
  local j
  j="$(machine gw enroll --host 127.0.0.1 --port "$GP" 2>/dev/null)"
  machine "$1" setup --join "$j" --no-service | sed "s/^/  $1 setup: /"
}
# node_line NAME: the `nodes` line of machine NAME, seen from the gateway host.
node_line() {
  local id
  id="$(sed -n 's/.*"node_id": *"\([^"]*\)".*/\1/p' "$T/$1/config/node.json")"
  machine gw nodes | grep -F "$id"
}
# daemon_version NAME: the version the running daemon of NAME reports.
daemon_version() { machine "$1" status 2>/dev/null | awk '/^version /{print $2}'; }
# wait_version NAME VERSION SECONDS: wait until the daemon of NAME reports VERSION.
wait_version() {
  for _ in $(seq $(($3 * 5))); do
    [ "$(daemon_version "$1")" = "$2" ] && return 0
    sleep 0.2
  done
  echo "timed out: $1 does not report $2 (reports: $(daemon_version "$1"))"; FAILS=$((FAILS + 1)); return 1
}
# log_count NAME PATTERN: how often the daemon log of NAME has PATTERN.
log_count() { grep -cF -- "$2" "$T/$1/daemon.log"; }

say "start from $V1: gateway host, B, C and D"
for m in gw b c d; do install_v $m $V1; done
cp "$REL/download/$V1"/agent-net-* "$T/gw/dist/"
bg gw gateway gateway --listen "127.0.0.1:$GP" --data "$T/gw/data" --dist "$T/gw/dist"
wait_for "gateway" 15 test -f "$T/gw/data/admin.json"
machine gw setup --gateway --no-service --port "$GP" --data "$T/gw/data" --dist "$T/gw/dist" | sed 's/^/  gw setup: /'
bg gw daemon daemon
for m in b c d; do
  join_machine $m
  bg $m daemon daemon
done
wait_for "daemons connected" 20 bash -c "[ \"\$(grep -c 'connected to gateway' $T/gw/daemon.log $T/b/daemon.log $T/c/daemon.log $T/d/daemon.log | grep -c ':1')\" = 4 ]"
# C and D joined and connected once; now they go away so the gateway sees them
# offline during the upgrade.
kill "${DPID[c]}" "${DPID[d]}"; sleep 1.5

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

# ---- (1) C was offline during the upgrade: it upgrades itself when it is back ----
say "(1) C comes back (offline during the upgrade, runs $V1): automatic catch-up"
check "C binary is $V1 before it starts" test "$(machine c version)" = "$V1"
bg c daemon daemon
wait_version c $V2 30
echo "--- C daemon log"; grep -E 'auto-upgrade|connected' "$T/c/daemon.log"
echo "--- agent-net nodes (C line, from the gateway host)"; node_line c
check "(1) C binary is $V2" test "$(machine c version)" = "$V2"
check "(1) C keeps .prev ($V1)" test "$("$T/c/bin/agent-net.prev" version)" = "$V1"
check "(1) C logged the upgrade once" test "$(log_count c "auto-upgrade: the gateway serves $V2 and this machine runs $V1, upgrading")" = 1
check "(1) C logged the result" test "$(log_count c "auto-upgrade: $V1 -> $V2 installed")" = 1
C_LINE="$(node_line c)"
check "(1) nodes shows C on $V2" contains "$C_LINE" "$V2"
check "(1) nodes shows C online" contains "$C_LINE" "true"

# ---- (2) a bad dist binary: D tries once, does not loop ----
say "(2) the dist binary for D's platform does not match what the gateway says"
cp "$T/gw/dist/agent-net-linux-amd64" "$T/dist-amd64.orig"
# The gateway runs it from the dist dir and reads $V2. D runs the downloaded copy
# from a temp dir, where it reports $V1: the version check must refuse it.
cat > "$T/gw/dist/agent-net-linux-amd64" <<EOF
#!/bin/sh
case "\$0" in
  */gw/dist/*) echo $V2 ;;
  *) echo $V1 ;;
esac
EOF
chmod +x "$T/gw/dist/agent-net-linux-amd64"
check "(2) the gateway serves $V2 for linux/amd64" test "$(curl -sk "https://127.0.0.1:$GP/install/version?os=linux&arch=amd64")" = "$V2"
bg d daemon daemon
wait_wanted() { grep -q "auto-upgrade to $V2 failed" "$T/d/daemon.log"; }
wait_for "D's failed attempt" 30 wait_wanted
for i in 1 2; do
  kill "${DPID[d]}"; sleep 1
  bg d daemon daemon
  wait_for "D reconnect $i is seen and skipped" 30 bash -c "[ \"\$(grep -cF 'auto-upgrade: not upgrading' '$T/d/daemon.log')\" -ge $i ]"
done
sleep 3
echo "--- D daemon log"; grep -E 'auto-upgrade|connected' "$T/d/daemon.log"
echo "--- D state file"; cat "$T/d/state/auto-upgrade.json"; echo
check "(2) D tried exactly once over three connects" test "$(log_count d "auto-upgrade: the gateway serves")" = 1
check "(2) D logged one failure" test "$(log_count d "auto-upgrade to $V2 failed")" = 1
check "(2) the failure names the version mismatch" test "$(log_count d "the gateway serves $V1 but $V2 was asked for")" = 1
check "(2) D skipped the version on the reconnects" test "$(log_count d "auto-upgrade: not upgrading $V1 to $V2")" = 2
check "(2) failure remembered in the state dir" grep -q "\"version\":\"$V2\"" "$T/d/state/auto-upgrade.json"
check "(2) D binary is still $V1" test "$(machine d version)" = "$V1"
check "(2) D daemon still reports $V1" test "$(daemon_version d)" = "$V1"
check "(2) D left no .prev" test ! -e "$T/d/bin/agent-net.prev"
cp "$T/dist-amd64.orig" "$T/gw/dist/agent-net-linux-amd64"
check "(2) dist restored: the gateway serves $V2 again" test "$(curl -sk "https://127.0.0.1:$GP/install/version?os=linux&arch=amd64")" = "$V2"

# ---- (3) rollback on B sticks ----
say "B: agent-net upgrade --rollback"
RB_OUT="$(machine b upgrade --rollback 2>&1)"; echo "$RB_OUT"
check "rollback: B binary is $V1" test "$(machine b version)" = "$V1"
check "rollback: B .prev is $V2" test "$("$T/b/bin/agent-net.prev" version)" = "$V2"
check "rollback: output says the hold" contains "$RB_OUT" "held at $V2"
wait_version b $V1 20
say "B: status / nodes after rollback"
machine b status | head -5
machine b nodes
check "rollback: gateway host untouched ($V2)" test "$(machine gw version)" = "$V2"
say "(3) B reconnected on $V1 while the gateway serves $V2: held"
wait_for "B logged the hold" 20 grep -q "held at $V2 after rollback" "$T/b/daemon.log"
sleep 4
echo "--- B daemon log"; grep -E 'auto-upgrade|connected' "$T/b/daemon.log"
check "(3) B still $V1 (binary)" test "$(machine b version)" = "$V1"
check "(3) B still $V1 (daemon)" test "$(daemon_version b)" = "$V1"
check "(3) B did not start an upgrade" test "$(log_count b "auto-upgrade: the gateway serves")" = 0
check "(3) B logged the hold" test "$(log_count b "auto-upgrade: not upgrading $V1 to $V2: held at $V2 after rollback")" -ge 1
check "(3) status shows the hold" contains "$(machine b status)" "auto-upgrade held at $V2 after rollback"
HELD_CHECK="$(machine b upgrade --check)"
check "(3) upgrade --check shows the hold" contains "$HELD_CHECK" "auto-upgrade   held at $V2 after rollback"

# ---- joined-machine upgrade ----
say "B: agent-net upgrade (joined machine path; a manual upgrade lifts the hold)"
JU_OUT="$(machine b upgrade 2>&1)"; echo "$JU_OUT"
check "joined upgrade: B is $V2" test "$(machine b version)" = "$V2"
wait_version b $V2 20
check "joined upgrade: the hold is gone" bash -c '! grep -q held "$1"' _ "$T/b/state/auto-upgrade.json"
check "joined upgrade: status shows no hold" bash -c '! grep -q "held at" <<<"$1"' _ "$(machine b status)"
say "B: agent-net upgrade --check"
machine b upgrade --check

# ---- (3, continued) hold again, then a newer release lifts it ----
say "(3) B rolls back again, goes offline, the gateway moves to $V3"
machine b upgrade --rollback 2>&1 | sed 's/^/  b rollback: /'
wait_version b $V1 20
kill "${DPID[b]}" "${DPID[d]}"; sleep 1.5
echo "$V3" > "$REL/LATEST"
UP3_OUT="$(machine gw upgrade 2>&1)"; echo "$UP3_OUT"
check "gateway host binary is $V3" test "$(machine gw version)" = "$V3"
check "dist refreshed to $V3" test "$("$T/gw/dist/agent-net-linux-amd64" version)" = "$V3"
check "C (online) was upgraded by the gateway" contains "$UP3_OUT" "$V2 -> $V3  ok"
check "B is reported offline" contains "$UP3_OUT" "offline"
check "B binary is still $V1" test "$(machine b version)" = "$V1"
check "the gateway serves $V3 for linux/amd64" test "$(curl -sk "https://127.0.0.1:$GP/install/version?os=linux&arch=amd64")" = "$V3"
bg b daemon daemon
bg d daemon daemon
wait_version b $V3 30
wait_version d $V3 30
echo "--- B daemon log (tail)"; grep -E 'auto-upgrade' "$T/b/daemon.log" | tail -3
echo "--- D daemon log (tail)"; grep -E 'auto-upgrade' "$T/d/daemon.log" | tail -3
echo "--- agent-net nodes"; machine gw nodes
check "(3) B auto-upgraded past the hold to $V3" test "$(machine b version)" = "$V3"
check "(3) B keeps .prev ($V1)" test "$("$T/b/bin/agent-net.prev" version)" = "$V1"
check "(3) B logged the upgrade past the hold" test "$(log_count b "auto-upgrade: the gateway serves $V3 and this machine runs $V1, upgrading")" = 1
check "(3) the hold is gone" bash -c '! grep -q held "$1"' _ "$T/b/state/auto-upgrade.json"
check "(3) D retried at once for the new version $V3" test "$(machine d version)" = "$V3"
check "(3) nodes shows B on $V3" contains "$(node_line b)" "$V3"
check "(3) nodes shows D on $V3" contains "$(node_line d)" "$V3"

# ---- (4) opt-out and (5) dev build ----
say "(4) opt-out by environment (E) and by flag (F), (5) dev build (G); the gateway serves $V3"
install_v e $V1; install_v f $V1
"$GO" build -o "$T/g/bin/agent-net" ./cmd/agent-net || exit 1
for m in e f g; do join_machine $m; done
bg e daemon daemon
bg f daemon daemon --no-auto-upgrade
bg g daemon daemon
for m in e f g; do wait_for "$m connected" 20 grep -q "connected to gateway" "$T/$m/daemon.log"; done
sleep 6   # more than the 1 s random delay, and time for a download
echo "--- E (AGENT_NET_NO_AUTO_UPGRADE=1)"; daemon_version e; grep -c 'auto-upgrade' "$T/e/daemon.log"
echo "--- F (--no-auto-upgrade)";           daemon_version f; grep -c 'auto-upgrade' "$T/f/daemon.log"
echo "--- G (dev build)";                    daemon_version g; grep -c 'auto-upgrade' "$T/g/daemon.log"
check "(4) E (env opt-out) stays on $V1" test "$(daemon_version e)" = "$V1"
check "(4) E binary unchanged" test "$(machine e version)" = "$V1"
check "(4) E logged nothing about upgrades" test "$(log_count e auto-upgrade)" = 0
check "(4) F (flag opt-out) stays on $V1" test "$(daemon_version f)" = "$V1"
check "(4) F binary unchanged" test "$(machine f version)" = "$V1"
check "(4) F logged nothing about upgrades" test "$(log_count f auto-upgrade)" = 0
check "(5) G (dev build) stays dev" test "$(daemon_version g)" = "dev"
check "(5) G logged nothing about upgrades" test "$(log_count g auto-upgrade)" = 0

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
