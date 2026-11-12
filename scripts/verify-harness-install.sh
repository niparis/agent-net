#!/bin/sh
# Capture the updater's user-visible log for four independent install layouts.
set -eu
cd "$(dirname "$0")/.."
export LC_ALL=C LANG=C COLUMNS=120 LINES=34
export GOCACHE=/tmp/agent-net-gocache GOTMPDIR=/tmp/agent-net-gotmp
mkdir -p "$GOTMPDIR"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM
export AGENT_NET_CONFIG_DIR="$work/config" AGENT_NET_STATE_DIR="$work/state"
export AGENT_NET_SOCKET="$work/socket" AGENT_NET_GATEWAY_DATA="$work/gateway"
export AGENT_NET_CLAUDE_SESSIONS_DIR="$work/claude"
build=$(git rev-parse HEAD)
if ! git diff --quiet HEAD; then build="$build (dirty)"; fi
status=0
go test ./internal/daemon -run 'HarnessUpdate|Updater|Brew' -timeout 900s -v "$@" >"$work/output" 2>&1 || status=$?
if [ "$status" -ne 0 ]; then cat "$work/output"; exit "$status"; fi
# Frames contain the actual daemon log after the synchronous update finishes.
# Normalize only the temporary fixture root, in the test helper.
awk -v build="$build" '
function line(s) { printf "%-120.120s\n", s; rows++ }
/=== RUN   TestHarnessUpdate(BrewInstalledCaskOwnership|BrewTappedFormulaOwnership|NPMGlobalOwnership|UnknownOwnershipSkipped)$/ {
 step=$3
}
/frame: / && step != "" {
 frames++
 rows=0
 line("build: " build)
 line("step: " step)
 line("action: detect installation and run one update check")
 line("frame: daemon log (120x34 text; locale C; fixture data)")
 sub(/^.*frame: /, "")
 line($0)
 line("errors: none")
 while (rows<34) line("")
 step=""
}
END { if (frames != 4) exit 1 }
' "$work/output"
