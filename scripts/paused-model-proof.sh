#!/usr/bin/env bash
# Proof that a model the owner scores 0 is paused (decision 0188). Step 1 runs
# the gateway tests that ask each choice path with a paused model. Step 2
# renders the TUI worker log at 80x24 and 120x34 with the note the gateway
# wrote for such a start. No daemon is started and no real setup is read.
# Usage: scripts/paused-model-proof.sh [output folder]
# Without a folder the frames go to a new temporary folder. A folder inside the
# repository must be one git ignores, so frames are never committed.
set -euo pipefail
cd "$(dirname "$0")/.."
export LC_ALL=C TZ=UTC
export GOCACHE="${GOCACHE:-/tmp/agent-net-gocache}"
export GOTMPDIR=/tmp/agent-net-gotmp
mkdir -p "$GOTMPDIR"
work="$(mktemp -d "${TMPDIR:-/tmp}/agent-net-paused-model.XXXXXX")"
trap 'rm -rf "$work"' EXIT HUP INT TERM
export AGENT_NET_CONFIG_DIR="$work/config" AGENT_NET_STATE_DIR="$work/state" AGENT_NET_SOCKET="$work/socket" AGENT_NET_GATEWAY_DATA="$work/gateway" AGENT_NET_CLAUDE_SESSIONS_DIR="$work/sessions"
export AGENT_NET_PROOF_BUILD
AGENT_NET_PROOF_BUILD="$(git rev-parse HEAD)"
out="${1:-$(mktemp -d "${TMPDIR:-/tmp}/agent-net-paused-model-proof.XXXXXX")}"
mkdir -p "$out"
# go test runs inside the package folder, so the test gets an absolute path.
out="$(cd "$out" && pwd)"
repo="$(pwd)"
case "$out/" in
"$repo/"*)
	if ! git check-ignore -q --no-index "${out#"$repo/"}/paused-model-80x24.txt"; then
		echo "paused-model-proof: $out is tracked by git; use a temporary folder or one git ignores, such as tmp/" >&2
		exit 1
	fi
	;;
esac
printf 'Build: %s\n' "$AGENT_NET_PROOF_BUILD"
printf '\nStep 1: ask every choice path with a model the owner paused.\n'
go test ./internal/gateway -count=1 -v -run '^(TestPausedModelWithTheBestRecordIsNotChosen|TestPausedModelIsNotExplored|TestPausedModelIsNotInTheAllowedDraw|TestPausedModelIsNotInTheLevelRule|TestAutomaticStartLeavesOutAPausedModel|TestAgentStartNamingAPausedModelIsRefused|TestOwnerStartNamingAPausedModelGoesAhead|TestOwnerWordsStartNamingAPausedModelGoesAhead|TestContinueOfAPausedWorkerIsRefused|TestMessageToAPausedWorkerDoesNotRestartIt|TestRoleSkipsAPausedModel)$' |
	grep -E '^(--- |ok|FAIL)'
printf '\nStep 2: open the worker log after an automatic start with Opus paused.\n'
export AGENT_NET_PROOF_DIR="$out"
go test ./internal/tui -run '^TestPausedModelProofScenario$' -count=1 >/dev/null
for size in 80x24 120x34; do
	printf '\n'
	cat "$out/paused-model-$size.txt"
done
printf '\nErrors: none\n'
