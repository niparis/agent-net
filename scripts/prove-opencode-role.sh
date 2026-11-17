#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/agent-net-opencode-proof.XXXXXX")
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
export HOME="$test_dir/home" XDG_CONFIG_HOME="$test_dir/config" XDG_STATE_HOME="$test_dir/state" XDG_DATA_HOME="$test_dir/data" XDG_CACHE_HOME="$test_dir/cache"
export AGENT_NET_CONFIG_DIR="$test_dir/agent-config" AGENT_NET_STATE_DIR="$test_dir/agent-state"
export AGENT_NET_SOCKET="$test_dir/daemon.sock" AGENT_NET_GATEWAY_DATA="$test_dir/gateway" AGENT_NET_CLAUDE_SESSIONS_DIR="$test_dir/claude"
export LC_ALL=C TZ=UTC TERM=dumb
mkdir -p "$HOME"
if ! command -v opencode >/dev/null 2>&1; then
  printf 'OpenCode is required for the real-service step.\n' >&2
  exit 1
fi
printf 'Build: %s\n' "$(git rev-parse HEAD)"
printf 'Step: create, wake and resume roles on a fake service\n'
go test ./internal/daemon -run '^(TestOpenCodeRoleCreationHasUnattendedPermission|TestOpenCodeRoleCreationRequiresConfirmedPermissions|TestOpenCodeRoleWakeAddsUnattendedPermission|TestOpenCodeRoleResumeAddsUnattendedPermission)$' -count=1 -timeout 10m -v
printf 'Step: read a protected file with new and adopted roles on a real service and fake model\n'
AGENT_NET_TEST_REAL_OPENCODE=1 go test ./internal/daemon -run '^TestOpenCodeRolePermissionsRealServer$' -count=1 -timeout 5m -v
printf 'Errors: none\n'
