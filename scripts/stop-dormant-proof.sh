#!/usr/bin/env bash
# Text frames for a stop of a session that stays online and turns dormant (the
# CTO), from the TUI test renderer at 80x24 and 120x34. No daemon is started.
# Usage: scripts/stop-dormant-proof.sh [output folder]
# Without a folder the frames go to a new temporary folder. A folder inside the
# repository must be one git ignores, so frames are never committed.
set -euo pipefail
cd "$(dirname "$0")/.."
export LC_ALL=C TZ=UTC
export GOCACHE="${GOCACHE:-/tmp/agent-net-gocache}"
export GOTMPDIR=/tmp/agent-net-gotmp
mkdir -p "$GOTMPDIR"
export AGENT_NET_PROOF_BUILD
AGENT_NET_PROOF_BUILD="$(git rev-parse HEAD)"
out="${1:-$(mktemp -d "${TMPDIR:-/tmp}/agent-net-stop-dormant-proof.XXXXXX")}"
mkdir -p "$out"
# go test runs inside the package folder, so the test gets an absolute path.
out="$(cd "$out" && pwd)"
repo="$(pwd)"
case "$out/" in
"$repo/"*)
	if ! git check-ignore -q --no-index "${out#"$repo/"}/stop-dormant-before-80x24.txt"; then
		echo "stop-dormant-proof: $out is tracked by git; use a temporary folder or one git ignores, such as tmp/" >&2
		exit 1
	fi
	;;
esac
export AGENT_NET_PROOF_DIR="$out"
go test ./internal/tui -run '^TestStopDormantProofScenario$' -count=1
ls "$AGENT_NET_PROOF_DIR"/stop-dormant-*.txt