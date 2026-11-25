#!/usr/bin/env bash
# Prove with real Claude Code that the worker window loads from one link in the
# skills folder of the config directory, with no marketplace and no settings
# entry, and that one window runs in the states the owner's settings file can
# be in. Everything runs in a temporary HOME and CLAUDE_CONFIG_DIR with a stub
# `agent-net`; the real Claude config, the real agent-net state and the network
# are not touched, and no model is called (the session only reaches its prompt).
#
# Usage: scripts/claude-plugin-skills-proof.sh [--claude PATH] [output folder]
#   --claude PATH  use that Claude Code binary (for example 2.1.330).
# Needs: Claude Code 2.1.330 or newer, tmux for the two frames.
#
# Checks:
#   1. the link alone makes agent-net-workers@skills-dir enabled.
#   2. the band shows the worker row at 120x34 and 80x24.
#   3. old marketplace entry enabled: the old copy runs, the skills copy does
#      not, so one window.
#   4. old entry lost, installed list kept: the skills copy runs.
#   5. old entry lost, installed list gone: the skills copy runs.
#   6. stale old entry true while the old copy is not installed and its
#      marketplace registration is gone: the skills copy runs, one window.
#   7. a disable in Claude Code sticks after the link is removed and put back.
#
# The Go tests in internal/agentfiles and internal/setup cover the daemon's
# part: restoring the link, leaving a foreign entry alone, and never writing a
# settings file that is a link.
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
out_arg=""; claude_bin="${CLAUDE_BIN:-}"
while [ $# -gt 0 ]; do
  case "$1" in
    --claude) claude_bin="${2:?--claude needs a path}"; shift 2;;
    --claude=*) claude_bin="${1#*=}"; shift;;
    -h|--help) sed -n '2,28p' "$0"; exit 0;;
    *) out_arg="$1"; shift;;
  esac
done
[ -n "$claude_bin" ] || claude_bin="$(command -v claude || true)"
[ -n "$claude_bin" ] && [ -x "$claude_bin" ] || { echo "claude not found; give --claude PATH" >&2; exit 2; }
claude_bin="$(readlink -f "$claude_bin")"
root="$(mktemp -d "${TMPDIR:-/tmp}/agent-net-claude-plugin-proof.XXXXXX")"
mkdir -p "$root/claude-bin" "$root/bin"
ln -s "$claude_bin" "$root/claude-bin/claude"
export PATH="$root/bin:$root/claude-bin:$PATH"
export HOME="$root/home" CLAUDE_CONFIG_DIR="$root/config"
export XDG_CONFIG_HOME="$root/xdg/config" XDG_CACHE_HOME="$root/xdg/cache" XDG_STATE_HOME="$root/xdg/state"
mkdir -p "$HOME" "$CLAUDE_CONFIG_DIR/skills" "$XDG_CONFIG_HOME" "$XDG_CACHE_HOME" "$XDG_STATE_HOME" "$root/work"
out="${out_arg:-$root/frames}"; mkdir -p "$out"; out="$(cd "$out" && pwd)"
trap 'tmux -L "$tmux_socket" kill-server 2>/dev/null || true; rm -rf "$root"' EXIT
case "$HOME" in "$root"/*) ;; *) echo "refusing to run outside a temporary HOME" >&2; exit 1;; esac
printf 'claude: %s (%s)\n' "$(claude --version 2>/dev/null | head -1)" "$claude_bin"

pass=0 fail=0 skip=0
ok()   { pass=$((pass+1)); printf 'PASS  %s\n' "$1"; }
bad()  { fail=$((fail+1)); printf 'FAIL  %s\n' "$1"; }
note() { skip=$((skip+1)); printf 'SKIP  %s\n' "$1"; }
check(){ if eval "$2"; then ok "$1"; else bad "$1"; fi; }
tmux_socket="anc-$$"

# The plugin files stay in agent-net's own folder. Only a link goes in the
# skills folder, and the stub agent-net supplies one worker row for the band.
plugin="$root/agent-net/agent-net-workers"
mkdir -p "$plugin"
cp -R "$repo/plugins/claude/agent-net-workers/." "$plugin/"
cat > "$root/bin/agent-net" <<'EOF'
#!/bin/sh
if [ "$1" = "workers" ]; then
  printf '%s\n' '{"workers":[{"session_id":"w_probe","harness":"opencode","model":"deepseek-v4.1-flash","project":"probe","name":"probe-worker","online":true,"status":"running","owner":"s_owner","owner_name":"probe-owner","started_at":1795615000,"task":"Probe task line","title":"Probe worker title","context_tokens":84000,"context_limit":200000}],"queued":[]}'
fi
exit 0
EOF
chmod +x "$root/bin/agent-net"

# Claude Code 2.1.us needs onboarding done; this adds no plugin or marketplace
# entry and approves only the throwaway key below.
export ANTHROPIC_API_KEY="$(printf 'sk-ant-%s' 'plugin-proof')"
export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 DISABLE_TELEMETRY=1
mkdir -p "$root/work"; : > "$CLAUDE_CONFIG_DIR/.claude.json"
python3 - "$CLAUDE_CONFIG_DIR/.claude.json" "$root/work" "$ANTHROPIC_API_KEY" <<'PY'
import json, sys
path, work, key = sys.argv[1], sys.argv[2], sys.argv[3]
d = {"hasCompletedOnboarding": True, "theme": "dark",
     "customApiKeyResponses": {"approved": [key[-20:]], "rejected": []},
     "projects": {work: {"hasTrustDialogAccepted": True, "allowedTools": []}}}
json.dump(d, open(path, "w"), indent=2)
PY

plugin_json() { claude plugin list --json 2>/dev/null; }
enabled() {
  plugin_json | python3 -c 'import json,sys
d=json.load(sys.stdin)
print(next((str(p.get("enabled")).lower() for p in d if p.get("id")==sys.argv[1]), "missing"))' "$1"
}
# exactly_one passes when exactly one of the two ids is enabled.
exactly_one() {
  old=$(enabled agent-net-workers@agent-net); skills=$(enabled agent-net-workers@skills-dir)
  printf '      old=%s skills=%s\n' "$old" "$skills"
  [ "$old" = true ] && [ "$skills" = true ] && return 1
  [ "$old" = true ] || [ "$skills" = true ]
}
wait_pane() {
  for _ in $(seq 1 200); do
    tmux -L "$tmux_socket" capture-pane -p -t "$1" 2>/dev/null | grep -qF -- "$2" && return 0
    sleep 0.05
  done
  return 1
}
link_plugin() { rm -rf "$CLAUDE_CONFIG_DIR/skills/agent-net-workers"; ln -s "$plugin" "$CLAUDE_CONFIG_DIR/skills/agent-net-workers"; }
market_plugin() {
  mkdir -p "$root/market/agent-net-workers" "$root/market/.claude-plugin"
  cp -R "$repo/plugins/claude/agent-net-workers/." "$root/market/agent-net-workers/"
  printf '%s\n' '{"name":"agent-net","owner":{"name":"proof"},"plugins":[{"name":"agent-net-workers","source":"./agent-net-workers"}]}' > "$root/market/.claude-plugin/marketplace.json"
  claude plugin marketplace add "$root/market" >/dev/null 2>&1 || true
  claude plugin install agent-net-workers@agent-net -s user >/dev/null 2>&1 || true
}

# --- 1. Link only. ---------------------------------------------------------
link_plugin
check "no settings.json" "[ ! -e '$CLAUDE_CONFIG_DIR/settings.json' ]"
check "the link alone enables the skills plugin" "[ \"\$(enabled agent-net-workers@skills-dir)\" = true ]"
check "no enabledPlugins entry was needed" "! grep -q enabledPlugins '$CLAUDE_CONFIG_DIR/settings.json' 2>/dev/null"

# --- 2. The band at two sizes. --------------------------------------------
if command -v tmux >/dev/null 2>&1; then
  for size in 120x34 80x24; do
    cols=${size%x*}; lines=${size#*x}
    tmux -L "$tmux_socket" -f /dev/null new-session -d -s "band-$size" -x "$cols" -y "$lines" "cd $root/work && claude --model claude-sonnet-4-5-20250929"
    if wait_pane "band-$size" "Probe worker title"; then
      tmux -L "$tmux_socket" capture-pane -p -t "band-$size" > "$out/band-$size.txt"
      check "the band renders the worker through the link at $size" "grep -q 'agent-net workers' '$out/band-$size.txt' && grep -q 'Probe worker title' '$out/band-$size.txt'"
    else
      tmux -L "$tmux_socket" capture-pane -p -t "band-$size" 2>/dev/null | tail -12 >&2 || true
      bad "the band did not render at $size"
    fi
    tmux -L "$tmux_socket" kill-session -t "band-$size" 2>/dev/null || true
  done
  tmux -L "$tmux_socket" kill-server 2>/dev/null || true
else
  note "the two frames need tmux"
fi

# --- 3-6. One window in the settings states the owner can have. -----------
# 3. The old entry is enabled and the old copy is installed.
market_plugin
check "old entry enabled: old copy runs, skills copy off" "exactly_one"
# 4. The entry is lost; Claude's installed list (its own state) survives.
printf '{}\n' > "$CLAUDE_CONFIG_DIR/settings.json"
check "old entry lost: the skills copy runs" "exactly_one"
check "old entry lost: the skills copy is the one" "[ \"\$(enabled agent-net-workers@skills-dir)\" = true ]"
# 5. The entry and the installed list are gone; the old copy cannot run.
python3 - "$CLAUDE_CONFIG_DIR/plugins/installed_plugins.json" <<'PY'
import json, sys
json.dump({"version": 2, "plugins": {}}, open(sys.argv[1], "w"))
PY
check "old entry and installed list lost: the skills copy runs" "exactly_one"
# 6. Stale old entry true while the old copy is not installed and its
# marketplace registration is gone. The skills copy runs, one window.
printf '%s\n' '{"enabledPlugins":{"agent-net-workers@agent-net":true}}' > "$CLAUDE_CONFIG_DIR/settings.json"
printf '{}\n' > "$CLAUDE_CONFIG_DIR/plugins/known_marketplaces.json"
check "stale old entry: one window" "exactly_one"
check "stale old entry: the skills copy is the one" "[ \"\$(enabled agent-net-workers@skills-dir)\" = true ]"

# --- 7. A user's disable sticks. -------------------------------------------
link_plugin
check "disable the skills plugin" "claude plugin disable agent-net-workers@skills-dir >/dev/null 2>&1"
check "disable is recorded" "[ \"\$(enabled agent-net-workers@skills-dir)\" = false ]"
rm -rf "$CLAUDE_CONFIG_DIR/skills/agent-net-workers"; link_plugin
check "the disable survives the link being removed and put back" "[ \"\$(enabled agent-net-workers@skills-dir)\" = false ]"
check "enable the skills plugin again" "claude plugin enable agent-net-workers@skills-dir >/dev/null 2>&1"
check "the plugin is on again" "[ \"\$(enabled agent-net-workers@skills-dir)\" = true ]"
check "the old copy is not on" "[ \"\$(enabled agent-net-workers@agent-net)\" != true ]"

printf '\nPASS %d  FAIL %d  SKIP %d\n' "$pass" "$fail" "$skip"
printf 'Frames: %s\n' "$out"
printf 'Not shown: what Claude Code does when a stale old entry is true but the old copy is not installed and the marketplace is still registered. That state is not producible from losing the settings file, because the installed list is Claude Code state and survives. The daemon reports it with one fix and adds no second window.\n'
[ "$fail" -eq 0 ]
