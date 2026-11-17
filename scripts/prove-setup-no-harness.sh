#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/agent-net-setup-proof.XXXXXX")
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM
export HOME="$test_dir/home" XDG_CONFIG_HOME="$test_dir/config" XDG_STATE_HOME="$test_dir/state" XDG_DATA_HOME="$test_dir/data"
export AGENT_NET_CONFIG_DIR="$test_dir/agent-config" AGENT_NET_STATE_DIR="$test_dir/agent-state"
export AGENT_NET_SOCKET="$test_dir/daemon.sock" AGENT_NET_GATEWAY_DATA="$test_dir/gateway" AGENT_NET_CLAUDE_SESSIONS_DIR="$test_dir/claude"
export LC_ALL=C TZ=UTC TERM=dumb
mkdir -p "$HOME"
printf 'Build: %s\n' "$(git rev-parse HEAD)"
printf 'Step: finish setup with no harness installed\n'
go test ./internal/setup -run '^(TestSetupSuggestsInstallingAHarness|TestSetupSuggestsInstallingAHarnessWhenDaemonIsOffline|TestSetupUsesInventoryOverOldIntegrationFiles|TestSetupOnboardingGolden)$' -count=1 -timeout 10m
for dimensions in '80 24' '120 34'; do
  set -- $dimensions
  printf 'Frame: %sx%s\n' "$1" "$2"
  awk -v width="$1" -v height="$2" '{ printf "%-*s\n", width, $0 } END { for (i = NR; i < height; i++) printf "%-*s\n", width, "" }' internal/setup/testdata/onboarding/empty-first-machine-plain.golden
done
printf 'Errors: none\n'
