#!/usr/bin/env bash
# Independent text frames, using the existing TUI test renderer without pauses.
set -euo pipefail
cd "$(dirname "$0")/.."
export LC_ALL=C TZ=UTC
export GOCACHE="${GOCACHE:-/tmp/agent-net-gocache}"
export GOTMPDIR=/tmp/agent-net-gotmp
mkdir -p "$GOTMPDIR"
export AGENT_NET_PROOF_BUILD
AGENT_NET_PROOF_BUILD="$(git rev-parse HEAD)"
out="${1:-$(mktemp -d "${TMPDIR:-/tmp}/agent-net-visual-proof.XXXXXX")}"
mkdir -p "$out"
export AGENT_NET_PROOF_DIR
AGENT_NET_PROOF_DIR="$(cd "$out" && pwd)"
go test ./internal/tui -run '^(TestVisualProofScenario|TestOrgTreeMergerProofScenario)$' -count=1
echo "frames: $AGENT_NET_PROOF_DIR"
