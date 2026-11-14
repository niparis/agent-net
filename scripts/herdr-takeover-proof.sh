#!/usr/bin/env bash
# Text frames for decision 0167: Enter takes the terminal over from a stale
# herdr attach instead of failing. A real herdr runs in a temporary config
# folder of its own, never the owner's servers, and a tmux server on its own
# socket plays the owner's terminals at 80x24 and 120x34. Needs herdr, tmux and
# script (which gives the old plan a terminal for its refusal frame).
# Usage: scripts/herdr-takeover-proof.sh [output folder]
# Without a folder the frames go to a new temporary folder.
set -euo pipefail
cd "$(dirname "$0")/.."
export LC_ALL=C TZ=UTC
export GOCACHE="${GOCACHE:-/tmp/agent-net-gocache}"
export GOTMPDIR=/tmp/agent-net-gotmp
mkdir -p "$GOTMPDIR"
export AGENT_NET_PROOF_BUILD
AGENT_NET_PROOF_BUILD="$(git rev-parse HEAD)"
out="${1:-$(mktemp -d "${TMPDIR:-/tmp}/agent-net-takeover-proof.XXXXXX")}"
mkdir -p "$out"
# go test runs inside the package folder, so the test gets an absolute path.
export AGENT_NET_PROOF_DIR
AGENT_NET_PROOF_DIR="$(cd "$out" && pwd)"
herdr="$(command -v herdr)" || { echo "herdr-takeover-proof: herdr is not installed" >&2; exit 1; }
command -v tmux >/dev/null || { echo "herdr-takeover-proof: tmux is not installed" >&2; exit 1; }
AGENT_NET_HERDR_BIN="$herdr" go test ./internal/daemon -run '^TestEnterHerdrTakesOverRealAttach$' -count=1 -v
ls "$AGENT_NET_PROOF_DIR"/herdr-takeover-*.txt
