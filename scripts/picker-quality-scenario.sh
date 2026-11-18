#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/picker-scenario.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM
export AGENT_NET_CONFIG_DIR="$work/config" AGENT_NET_STATE_DIR="$work/state" AGENT_NET_SOCKET="$work/socket" AGENT_NET_GATEWAY_DATA="$work/gateway" AGENT_NET_CLAUDE_SESSIONS_DIR="$work/sessions"
export LC_ALL=C TZ=UTC AGENT_NET_SCREEN_CHILD=1
cd "$root"
printf 'Build: %s\n' "$(git rev-parse HEAD)"
go test ./internal/tui -run '^TestPickerQualityV2Scenario$' -screens "$work"
for size in 80x24 120x34; do
  printf '\nStep: open the quality picker at %s with the experimental setting enabled.\n' "$size"
  cat "$work/picker_quality_v2_$size.ansi"
  printf '\nErrors: none\n'
done
