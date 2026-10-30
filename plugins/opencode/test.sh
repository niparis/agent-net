#!/bin/sh
# Run the plugin against a fake OpenCode context and a fake daemon socket.
# Needs network the first time (installs typescript into a temporary directory).
set -eu
# Behave the same everywhere, also inside an agent-net worker: the plugin must
# not see the worker or socket variables of the session that runs this test.
for v in $(env | sed -n 's/^\(AGENT_NET_[A-Za-z0-9_]*\)=.*/\1/p'); do unset "$v"; done
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cd "$tmp"
npm init -y >/dev/null
npm install --silent typescript@5
node "$here/test/plugin.test.mjs" "$here/agent-net.ts" "$tmp"
