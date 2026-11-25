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
# Needs: Claude Code 2.1.330 or newer, tmux for the frames and the table.
#
# Checks:
#   1. the link alone makes agent-net-workers@skills-dir enabled.
#   2. the band shows the worker row at 120x34 and 80x24.
#   3. one window in each settings state, with the link always in place:
#      old entry and marketplace present; both entries lost (the owner's loss);
#      only the marketplace entry lost; only the enabled entry lost. The last
#      two show no window until the installed old copy is removed, which is
#      what agent-net's background migration does.
#   4. the refresh commands carry a changed panel to the old copy and write no
#      settings file, also when that file is a link.
#   5. an explicit false for either id is respected: old false shows no window;
#      skills false shows no window and `claude plugin enable` turns it back on.
#   6. a stale old entry whose copy is not installed still shows the skills
#      copy, one window.
#   7. a disable in Claude Code sticks after the link is removed and put back.
#
# The Go tests in internal/agentfiles and internal/setup cover the daemon's
# part: restoring the link, the repair of the installed old copy, refreshing
# the old copy, and never writing a settings file that is a link.
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
out_arg=""; claude_bin="${CLAUDE_BIN:-}"
while [ $# -gt 0 ]; do
  case "$1" in
    --claude) claude_bin="${2:?--claude needs a path}"; shift 2;;
    --claude=*) claude_bin="${1#*=}"; shift;;
    -h|--help) sed -n '2,38p' "$0"; exit 0;;
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

# Claude Code needs onboarding done; this adds no plugin or marketplace entry
# and approves only the throwaway key below.
export ANTHROPIC_API_KEY="$(printf 'sk-ant-%s' 'plugin-proof')"
export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 DISABLE_TELEMETRY=1
: > "$CLAUDE_CONFIG_DIR/.claude.json"
python3 - "$CLAUDE_CONFIG_DIR/.claude.json" "$root/work" "$ANTHROPIC_API_KEY" <<'PY'
import json, sys
path, work, key = sys.argv[1], sys.argv[2], sys.argv[3]
d = {"hasCompletedOnboarding": True, "theme": "dark",
     "customApiKeyResponses": {"approved": [key[-20:]], "rejected": []},
     "projects": {work: {"hasTrustDialogAccepted": True, "allowedTools": []}}}
json.dump(d, open(path, "w"), indent=2)
PY

enabled() {
  claude plugin list --json 2>/dev/null | python3 -c 'import json,sys
d=json.load(sys.stdin)
print(next((str(p.get("enabled")).lower() for p in d if p.get("id")==sys.argv[1]), "missing"))' "$1"
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
# render starts one real session and prints how many worker bands it shows.
render() {
  tmux -L "$tmux_socket" -f /dev/null new-session -d -s band -x 120 -y 34 "cd $root/work && claude --model claude-sonnet-4-5-20250929"
  wait_pane band "Probe worker title" || true
  tmux -L "$tmux_socket" capture-pane -p -t band > "$out/$1.txt" 2>/dev/null || true
  tmux -L "$tmux_socket" capture-pane -p -t band 2>/dev/null | grep -c 'agent-net workers' || true
  tmux -L "$tmux_socket" kill-session -t band 2>/dev/null || true
}
# set_state writes a settings file in one of the four states. Claude Code's own
# known_marketplaces.json and installed list are kept, as they are on the Mac.
set_state() {
  python3 - "$CLAUDE_CONFIG_DIR/settings.json" "$1" "$root/market" <<'PY'
import json, sys
path, state, market = sys.argv[1], sys.argv[2], sys.argv[3]
d = {}
if state in ("both-present", "only-market-lost"):
    d["enabledPlugins"] = {"agent-net-workers@agent-net": True}
if state in ("both-present", "only-enabled-lost"):
    d["extraKnownMarketplaces"] = {"agent-net": {"source": {"source": "directory", "path": market}}}
json.dump(d, open(path, "w"), indent=2)
PY
}
# bump_version edits the marketplace copy so Claude Code has a new panel to
# install.
bump_version() {
  python3 - "$root/market/agent-net-workers/.claude-plugin/plugin.json" "$1" <<'PY'
import json, sys
path, version = sys.argv[1], sys.argv[2]
d = json.load(open(path))
d["version"] = version
json.dump(d, open(path, "w"), indent=2)
PY
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
  note "the frames and the table need tmux"
fi

# --- 3. One window in the four settings states, link always in place. -------
if command -v tmux >/dev/null 2>&1; then
  for state in both-present both-lost only-market-lost only-enabled-lost; do
    market_plugin
    set_state "$state"
    check "$state: the old copy starts in Claude's installed list" "grep -q agent-net-workers@agent-net '$CLAUDE_CONFIG_DIR/plugins/installed_plugins.json'"
    bands=$(render "table-$state-raw")
    case "$state" in
      both-lost|only-enabled-lost)
        # Claude Code loads neither copy until the installed old copy goes.
        # This is the repair the daemon runs when the enabled entry is lost.
        claude plugin uninstall agent-net-workers@agent-net -s user >/dev/null 2>&1 || true
        bands=$(render "table-$state")
        ;;
    esac
    check "$state: exactly one window (link always in place)" "[ \"\$bands\" = 1 ]"
  done

  # The owner's Mac keeps settings.json as a link. The repair writes no key
  # when the enabled entry is missing, so the linked file is left byte for byte.
  market_plugin
  printf '{}\n' > "$root/dotfiles-settings.json"
  rm -f "$CLAUDE_CONFIG_DIR/settings.json"
  ln -s "$root/dotfiles-settings.json" "$CLAUDE_CONFIG_DIR/settings.json"
  cp "$root/dotfiles-settings.json" "$root/dotfiles-before.json"
  claude plugin uninstall agent-net-workers@agent-net -s user >/dev/null 2>&1 || true
  check "the repair of a linked settings file writes nothing" "cmp -s '$root/dotfiles-before.json' '$root/dotfiles-settings.json'"
  check "the repair of a linked settings file leaves one window" "[ \"\$(render table-linked-repair)\" = 1 ]"
else
  note "the one-window table needs tmux"
fi

# --- 4. A changed panel reaches the old copy, without writing settings. ----
market_plugin
set_state both-present
bump_version 0.1.1
printf '\n// proof marker one\n' >> "$root/market/agent-net-workers/hooks/register.tsx"
cp "$CLAUDE_CONFIG_DIR/settings.json" "$root/settings-before.json"
claude plugin marketplace update agent-net >/dev/null 2>&1 || true
claude plugin update agent-net-workers@agent-net -s user >/dev/null 2>&1 || true
check "the old copy gets a changed panel" "grep -rq 'proof marker one' '$CLAUDE_CONFIG_DIR/plugins/cache/agent-net/agent-net-workers'"
check "the refresh writes no settings file" "cmp -s '$root/settings-before.json' '$CLAUDE_CONFIG_DIR/settings.json'"
# Again with settings.json as a link, which is the owner's Mac.
cp "$CLAUDE_CONFIG_DIR/settings.json" "$root/dotfiles-refresh.json"
rm "$CLAUDE_CONFIG_DIR/settings.json"; ln -s "$root/dotfiles-refresh.json" "$CLAUDE_CONFIG_DIR/settings.json"
bump_version 0.1.2
printf '\n// proof marker two\n' >> "$root/market/agent-net-workers/hooks/register.tsx"
cp "$root/dotfiles-refresh.json" "$root/dotfiles-before.json"
claude plugin marketplace update agent-net >/dev/null 2>&1 || true
claude plugin update agent-net-workers@agent-net -s user >/dev/null 2>&1 || true
check "the old copy gets a changed panel through a linked settings file" "grep -rq 'proof marker two' '$CLAUDE_CONFIG_DIR/plugins/cache/agent-net/agent-net-workers'"
check "the refresh of a linked settings file writes nothing" "cmp -s '$root/dotfiles-before.json' '$root/dotfiles-refresh.json'"

# --- 5. An explicit false for either id is respected. ----------------------
rm -f "$CLAUDE_CONFIG_DIR/settings.json"
printf '%s\n' '{"enabledPlugins":{"agent-net-workers@agent-net":false},"extraKnownMarketplaces":{"agent-net":{}}}' > "$CLAUDE_CONFIG_DIR/settings.json"
check "an explicit old false shows no window" "[ \"\$(render table-old-false)\" = 0 ]"
printf '%s\n' '{"enabledPlugins":{"agent-net-workers@skills-dir":false}}' > "$CLAUDE_CONFIG_DIR/settings.json"
check "an explicit skills false shows no window" "[ \"\$(render table-skills-false)\" = 0 ]"
claude plugin enable agent-net-workers@skills-dir >/dev/null 2>&1 || true
check "claude plugin enable turns the window back on" "[ \"\$(render table-skills-on)\" = 1 ]"

# --- 6. A stale old entry with no installed copy: one window. -------------
market_plugin
printf '%s\n' '{"enabledPlugins":{"agent-net-workers@agent-net":true}}' > "$CLAUDE_CONFIG_DIR/settings.json"
printf '%s\n' '{"version":2,"plugins":{}}' > "$CLAUDE_CONFIG_DIR/plugins/installed_plugins.json"
check "a stale old entry shows one window" "[ \"\$(render table-stale)\" = 1 ]"

# --- 7. A user's disable sticks. -------------------------------------------
printf '{}\n' > "$CLAUDE_CONFIG_DIR/settings.json"
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
printf 'Not shown: on a linked settings file Claude Code itself writes an explicit false for the old copy when a session starts after the entries are lost. Agent-net does not write a linked file, so it reports the one command that removes the installed copy instead of running it. A regular settings file is repaired.\n'
[ "$fail" -eq 0 ]
