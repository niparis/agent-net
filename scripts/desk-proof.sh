#!/usr/bin/env bash
# Capture the web Desk at phone and desktop width.
set -euo pipefail
cd "$(dirname "$0")/.."
export LC_ALL=C TZ=UTC
export GOMODCACHE="${GOMODCACHE:-$(go env GOMODCACHE)}"
chrome="${CHROME_BIN:-}"
if [ -z "$chrome" ]; then
  for candidate in "$HOME"/.cache/ms-playwright/chromium-*/chrome-linux64/chrome; do
    if [ -x "$candidate" ]; then chrome="$candidate"; break; fi
  done
fi
chrome="${chrome:-$(command -v chromium || command -v google-chrome || true)}"
if [ -z "$chrome" ]; then echo "Set CHROME_BIN to a Chromium executable." >&2; exit 1; fi
export AGENT_NET_PROOF_CHROME="$chrome"
export GOCACHE="${GOCACHE:-/tmp/agent-net-gocache}" GOTMPDIR=/tmp/agent-net-gotmp
mkdir -p "$GOTMPDIR"
out="${1:-$(mktemp -d "${TMPDIR:-/tmp}/agent-net-desk-proof.XXXXXX")}"
mkdir -p "$out"
out="$(cd "$out" && pwd)"
repo="$(pwd)"
case "$out/" in
"$repo/"*)
  if ! git check-ignore -q --no-index "${out#"$repo/"}/desk-phone.png"; then
    echo "Use a temporary or ignored folder for frames." >&2
    exit 1
  fi ;;
esac
state="$(mktemp -d "${TMPDIR:-/tmp}/agent-net-desk-state.XXXXXX")"
trap 'rm -rf "$state"' EXIT
export HOME="$state/home" AGENT_NET_CONFIG_DIR="$state/config" AGENT_NET_STATE_DIR="$state/state"
export AGENT_NET_SOCKET="$state/socket" AGENT_NET_GATEWAY_DATA="$state/gateway" AGENT_NET_CLAUDE_SESSIONS_DIR="$state/claude"
mkdir -p "$HOME"
export AGENT_NET_PROOF_DIR="$out" AGENT_NET_PROOF_BUILD
AGENT_NET_PROOF_BUILD="$(git rev-parse HEAD)"
go test ./internal/phone -run '^TestDeskWebProofScenario$' -count=1
ls "$out"/desk.html "$out"/desk-*.png "$out"/desk-*.txt
