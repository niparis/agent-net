#!/usr/bin/env bash
# Proof that a continue meets the picker's rules of today (decision 0189).
# Step 1 runs the gateway tests that continue an ended worker against a fake
# machine. Step 2 renders the TUI worker log at 80x24 and 120x34 with the note
# the gateway wrote for a continue that moved off a paused model. No daemon is
# started and no real setup is read.
# Usage: scripts/continue-repick-proof.sh [output folder]
# Without a folder the frames go to a new temporary folder. A folder inside the
# repository must be one git ignores, so frames are never committed.
set -euo pipefail
cd "$(dirname "$0")/.."
export LC_ALL=C TZ=UTC
export GOCACHE="${GOCACHE:-/tmp/agent-net-gocache}"
export GOTMPDIR=/tmp/agent-net-gotmp
mkdir -p "$GOTMPDIR"
work="$(mktemp -d "${TMPDIR:-/tmp}/agent-net-continue-repick.XXXXXX")"
trap 'rm -rf "$work"' EXIT HUP INT TERM
export AGENT_NET_CONFIG_DIR="$work/config" AGENT_NET_STATE_DIR="$work/state" AGENT_NET_SOCKET="$work/socket" AGENT_NET_GATEWAY_DATA="$work/gateway" AGENT_NET_CLAUDE_SESSIONS_DIR="$work/sessions"
export AGENT_NET_PROOF_BUILD
AGENT_NET_PROOF_BUILD="$(git rev-parse HEAD)"
out="${1:-$(mktemp -d "${TMPDIR:-/tmp}/agent-net-continue-repick-proof.XXXXXX")}"
mkdir -p "$out"
# go test runs inside the package folder, so the test gets an absolute path.
out="$(cd "$out" && pwd)"
repo="$(pwd)"
case "$out/" in
"$repo/"*)
	if ! git check-ignore -q --no-index "${out#"$repo/"}/continue-repick-80x24.txt"; then
		echo "continue-repick-proof: $out is tracked by git; use a temporary folder or one git ignores, such as tmp/" >&2
		exit 1
	fi
	;;
esac
printf 'Build: %s\n' "$AGENT_NET_PROOF_BUILD"
printf '\nStep 1: continue an ended worker whose model no longer qualifies, and one that does.\n'
go test ./internal/gateway -count=1 -v -run '^(TestContinueMovesAPausedModel|TestContinueMovesAModelUnderItsFloor|TestContinueMovesAModelBelowTheLevel|TestContinueMovesAProtectedModel|TestContinueKeepsAModelThatQualifies|TestContinueKeepsTheOldModelWhenNoOtherQualifies|TestContinueOfAPausedModelWithNoOtherIsRefused|TestContinueNamingAModelKeepsIt|TestContinueOfAPausedWorkerIsRefused)$' |
	grep -E '^(--- |ok|FAIL)'
printf '\nStep 2: open the worker log after a continue of an Opus worker with Opus paused.\n'
export AGENT_NET_PROOF_DIR="$out"
go test ./internal/tui -run '^TestContinueRepickProofScenario$' -count=1 >/dev/null
for size in 80x24 120x34; do
	printf '\n'
	cat "$out/continue-repick-$size.txt"
done
printf '\nErrors: none\n'
