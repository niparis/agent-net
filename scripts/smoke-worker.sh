#!/usr/bin/env bash
# One real worker, end to end, with a real harness: claude, pi or codex.
#
#   scripts/smoke-worker.sh claude [MODEL]
#   scripts/smoke-worker.sh pi     [MODEL]
#   scripts/smoke-worker.sh codex  [MODEL]
#
# THIS SPENDS MONEY. It starts one worker with a tiny task ("reply with OK")
# on a cheap model, waits for the hand-back, stops the worker, and cleans up.
# Defaults: claude haiku, pi openai/gpt-5.5, codex gpt-6-luna.
#
# Run it from your own terminal, not from inside an agent session: the daemon
# refuses owner actions (start, stop) from a process under an agent.
#
# Everything else is throwaway: its own gateway on a free port, one daemon with
# its own config, state and socket, and a project that is a copy (a git
# repository in /tmp, or for Claude a worktree of this repository). It never
# touches the real agent-net services or their sockets. What it cannot isolate
# is the harness itself: the real Claude Code, Pi and Codex run with your own
# login and settings, and keep their own session history. The Claude worker
# also shows in the sessions of your real daemon while it runs, because Claude
# Code writes its session file in ~/.claude/sessions.
#
# Claude Code only runs in a folder it trusts. A worktree of a trusted
# repository is trusted, a new folder is not. So the Claude project is this
# repository itself, started with isolation worktree: the worker runs in a
# worktree under the throwaway state folder, and the branch is removed at the
# end because the worker changes nothing.
set -euo pipefail

H=${1:?usage: scripts/smoke-worker.sh claude|pi|codex [MODEL]}
case "$H" in
claude) MODEL=${2:-haiku} ;;
pi) MODEL=${2:-openai/gpt-5.5} ;;
codex) MODEL=${2:-gpt-6-luna} ;;
*)
  echo "unknown harness $H" >&2
  exit 2
  ;;
esac
here=$(cd "$(dirname "$0")/.." && pwd)
export PATH="$PATH:$HOME/.local/go/bin"
T=$(mktemp -d /tmp/anet-smoke.XXXXXX)
BIN=$T/bin
mkdir -p "$BIN" "$T/empty"
pids=()
worker=""
cleanup() {
  set +e
  [ -n "$worker" ] && an stop "$worker" >/dev/null 2>&1
  for p in "${pids[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null; done
  # The daemon runs in a function, so its own process is the child of a subshell.
  for p in $(pgrep -f "^$BIN/" || true); do kill "$p" 2>/dev/null; done
  wait 2>/dev/null
  # Claude keeps stopped background sessions in its list: remove ours.
  if [ -n "${CLAUDE_BG_ID:-}" ]; then
    claude stop "$CLAUDE_BG_ID" >/dev/null 2>&1
    claude rm "$CLAUDE_BG_ID" >/dev/null 2>&1 && echo "removed Claude background session $CLAUDE_BG_ID"
  fi
  rm -rf "$T"
}
trap cleanup EXIT

(cd "$here" && go build -o "$BIN/agent-net" ./cmd/agent-net)
port=$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')
"$BIN/agent-net" gateway --listen "127.0.0.1:$port" --data "$T/gw" >"$T/gateway.log" 2>&1 &
pids+=($!)
for _ in $(seq 50); do grep -q "listening" "$T/gateway.log" && break; sleep 0.1; done

SESSIONS=$T/empty
[ "$H" = claude ] && SESSIONS=$HOME/.claude/sessions
an() {
  AGENT_NET_CONFIG_DIR=$T/cfg AGENT_NET_STATE_DIR=$T/state AGENT_NET_SOCKET=$T/d.sock \
    AGENT_NET_CLAUDE_SESSIONS_DIR=$SESSIONS "$BIN/agent-net" "$@"
}
mkdir -p "$T/cfg"
join=$("$BIN/agent-net" enroll --data "$T/gw" --host 127.0.0.1 --port "$port" 2>/dev/null)
an join "$join" >/dev/null
# Pi and Codex keep their sessions and settings in one folder. The worker gets a
# private one that holds only your login (a link to the real auth file), so the
# run leaves nothing in your own history, and Pi loads the extension of this
# build and not the one you have installed (which may be older).
case "$H" in
pi)
  mkdir -p "$T/pi-agent/extensions"
  for f in auth.json models-store.json; do
    [ -e "$HOME/.pi/agent/$f" ] && ln -s "$HOME/.pi/agent/$f" "$T/pi-agent/$f"
  done
  [ -e "$HOME/.pi/agent/settings.json" ] && cp "$HOME/.pi/agent/settings.json" "$T/pi-agent/settings.json"
  cp "$here/plugins/pi/agent-net.ts" "$T/pi-agent/extensions/agent-net.ts"
  export PI_CODING_AGENT_DIR=$T/pi-agent PI_SKIP_VERSION_CHECK=1
  ;;
codex)
  mkdir -p "$T/codex-home"
  ln -s "$HOME/.codex/auth.json" "$T/codex-home/auth.json"
  export CODEX_HOME=$T/codex-home
  ;;
esac
an daemon >"$T/daemon.log" 2>&1 &
pids+=($!)
for _ in $(seq 50); do an status >/dev/null 2>&1 && break; sleep 0.1; done
for _ in $(seq 100); do an nodes >/dev/null 2>&1 && break; sleep 0.1; done

# The project.
ISO=""
if [ "$H" = claude ]; then
  common=$(git -C "$here" rev-parse --path-format=absolute --git-common-dir)
  PROJ=${common%/.git}
  ISO="--isolation worktree"
else
  PROJ=$T/project
  mkdir -p "$PROJ"
  echo "# smoke" >"$PROJ/README.md"
  git -C "$PROJ" init -q -b main
  git -C "$PROJ" add README.md
  git -C "$PROJ" -c user.name=smoke -c user.email=smoke@example.com commit -q -m first
fi
NAME=$(basename "$PROJ")
an projects add "$PROJ" >/dev/null
for _ in $(seq 100); do an nodes 2>/dev/null | grep -q "$H" && break; sleep 0.2; done

echo "== start a $H worker ($MODEL) in $NAME"
# shellcheck disable=SC2086
out=$(an start --project "$NAME" --harness "$H" --model "$MODEL" $ISO \
  "There is nothing to change. Your whole task is to hand back the single word OK as your report.")
echo "$out"
worker=$(awk '/^started worker/{print $3}' <<<"$out")
if [ "$H" = claude ]; then
  CLAUDE_BG_ID=$(claude agents --json --all 2>/dev/null | python3 -c "
import sys,json
for a in json.load(sys.stdin):
    if a.get('name','').startswith('worker-$NAME-'): print(a['id'])" | head -1)
  echo "claude background session: ${CLAUDE_BG_ID:-?}"
fi

echo "== wait for the hand-back (up to 3 minutes)"
for _ in $(seq 90); do
  if an messages --json 2>/dev/null | grep -q "agent-net worker notice"; then break; fi
  sleep 2
done
an messages --json 2>/dev/null | python3 -c "
import sys,json
d=json.load(sys.stdin)
ms=d.get('messages',d) if isinstance(d,dict) else d
for m in ms:
    b=m.get('body') or ''
    if 'agent-net worker notice' in b: print(b)
" || true
echo "== workers"
an workers || true
echo "== daemon log (worker lines)"
grep -E "worker" "$T/daemon.log" | tail -12 || true
echo "== stop"
an stop "$worker"
worker=""
sleep 2
an workers || true
