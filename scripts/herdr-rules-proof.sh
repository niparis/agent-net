#!/usr/bin/env bash
# Text frames for decision 0154 (the owner's herdr is read-only for agent-net),
# from the TUI test renderer at 80x24 and 120x34. No herdr is started.
# Usage: scripts/herdr-rules-proof.sh [output folder]
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
out="${1:-$(mktemp -d "${TMPDIR:-/tmp}/agent-net-herdr-proof.XXXXXX")}"
mkdir -p "$out"
# go test runs inside the package folder, so the test gets an absolute path.
out="$(cd "$out" && pwd)"
repo="$(pwd)"
case "$out/" in
"$repo/"*)
	if ! git check-ignore -q --no-index "${out#"$repo/"}/herdr-frame.txt"; then
		echo "herdr-rules-proof: $out is tracked by git; use a temporary folder or one git ignores, such as tmp/" >&2
		exit 1
	fi
	;;
esac
export AGENT_NET_PROOF_DIR="$out"
go test ./internal/tui -run '^TestHerdrRulesProofScenario$' -count=1
ls "$AGENT_NET_PROOF_DIR"/herdr-*.txt
