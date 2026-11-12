#!/usr/bin/env bash
# Text frames for decision 0149 (the owner's herdr is read-only for agent-net),
# from the TUI test renderer at 80x24 and 120x34. No herdr is started.
set -euo pipefail
cd "$(dirname "$0")/.."
export LC_ALL=C TZ=UTC
export GOCACHE="${GOCACHE:-/tmp/agent-net-gocache}"
export GOTMPDIR=/tmp/agent-net-gotmp
mkdir -p "$GOTMPDIR"
export AGENT_NET_PROOF_BUILD
AGENT_NET_PROOF_BUILD="$(git rev-parse HEAD)"
export AGENT_NET_PROOF_DIR="${1:-/tmp/agent-net-herdr-proof}"
go test ./internal/tui -run '^TestHerdrRulesProofScenario$' -count=1
ls "$AGENT_NET_PROOF_DIR"/herdr-*.txt
