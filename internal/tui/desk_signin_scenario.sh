#!/bin/sh
set -eu

repo=$(git rev-parse --show-toplevel)
cd "$repo"
PATH=/home/nicolas/.local/go/bin:$PATH
export PATH
GOCACHE=${GOCACHE:-/tmp/agent-net-gocache}
GOTMPDIR=${GOTMPDIR:-/tmp/agent-net-gotmp}
export GOCACHE GOTMPDIR
mkdir -p "$GOTMPDIR"
AGENT_NET_PROOF_DIR=${AGENT_NET_PROOF_DIR:-${TMPDIR:-/tmp}/agent-net-desk-signin-proof}
AGENT_NET_PROOF_BUILD=$(git rev-parse --short HEAD)
export AGENT_NET_PROOF_DIR AGENT_NET_PROOF_BUILD

go test ./internal/tui -run '^TestDeskShowsSignInAskFirstAmongNewerAsks$' -count=1
printf 'Frames written to %s\n' "$AGENT_NET_PROOF_DIR"
