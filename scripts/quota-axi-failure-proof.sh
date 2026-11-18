#!/bin/sh
# scripts/quota-axi-failure-proof.sh: setup on a machine whose npm refuses quota-axi.
#
# Usage: scripts/quota-axi-failure-proof.sh /path/to/agent-net
#
# It runs `agent-net setup --quota-axi --quota` in a temporary home. A fake node
# 22.19 and a fake npm stand in for the machine's tools, so no network is used
# and the real setup is not touched. Setup must print one quota-axi warning with
# npm's reason, print the fix, and exit 0. Before the change it exited 1 with
# "failed (install quota-axi: exit status 1)".
set -u

bin=${1:?usage: $0 /path/to/agent-net}
t=$(mktemp -d "${TMPDIR:-/tmp}/agent-net-quota-proof.XXXXXX") || exit 2
trap 'rm -rf "$t"' EXIT
mkdir -p "$t/bin" "$t/home"

printf '%s\n' '#!/bin/sh' 'if [ "$1" = "--version" ]; then echo v22.19.0; exit 0; fi' 'exit 1' > "$t/bin/node"
printf '%s\n' '#!/bin/sh' 'if [ "$1" = "prefix" ]; then exit 0; fi' \
  'echo "npm error code E403" >&2' \
  'echo "npm error 403 Forbidden - GET https://registry.example.test/quota-axi" >&2' \
  'exit 1' > "$t/bin/npm"
# The login shell prints the PATH the fake tools live on; setup reads it.
printf '%s\n' '#!/bin/sh' "printf '%s\\0' '$t/bin' '' '' '' '' '' '' '' ''" > "$t/shell"
chmod +x "$t/bin/node" "$t/bin/npm" "$t/shell"

cd "$t" || exit 2
env -i PATH="$t/bin" SHELL="$t/shell" HOME="$t/home" \
  AGENT_NET_CONFIG_DIR="$t/cfg" AGENT_NET_STATE_DIR="$t/state" AGENT_NET_SOCKET="$t/sock" \
  AGENT_NET_GATEWAY_DATA="$t/gw" AGENT_NET_CLAUDE_SESSIONS_DIR="$t/claude" AGENT_NET_NO_SERVICE=1 \
  "$bin" setup --quota-axi --quota </dev/null
status=$?
echo "exit=$status"
exit "$status"
