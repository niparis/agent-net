#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/agent-net-role-proof.XXXXXX")
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
export HOME="$test_dir/home" XDG_CONFIG_HOME="$test_dir/config" XDG_STATE_HOME="$test_dir/state" XDG_DATA_HOME="$test_dir/data"
export AGENT_NET_CONFIG_DIR="$test_dir/agent-config" AGENT_NET_STATE_DIR="$test_dir/agent-state"
export AGENT_NET_SOCKET="$test_dir/daemon.sock" AGENT_NET_GATEWAY_DATA="$test_dir/gateway" AGENT_NET_CLAUDE_SESSIONS_DIR="$test_dir/claude"
export LC_ALL=C TERM=dumb
mkdir -p "$HOME"
printf 'Build: %s\n' "$(git rev-parse HEAD)"
printf 'Step: start and wake roles with a fake Codex server\n'
go test ./internal/daemon -run '^(TestCodexRoleStartUsesUnattendedPolicy|TestCodexRoleWakeKeepsUnattendedPolicy|TestCodexRoleManualResumeKeepsUnattendedPolicy|TestOrdinaryCodexStartKeepsOwnerPolicy)$' -count=1 -timeout 10m -v
printf 'Step: run a merger command and commit with a real Codex server and local fake model\n'
codex_bin=${AGENT_NET_TEST_REAL_CODEX:-$(command -v codex || true)}
if [ -z "$codex_bin" ]; then
  printf 'A Codex binary is required for this step.\n' >&2
  exit 1
fi
AGENT_NET_TEST_REAL_CODEX="$codex_bin" go test ./internal/daemon -run '^TestCodexMergerRealServerRunsWithoutApproval$' -count=1 -timeout 5m -v
