#!/bin/sh
# Run the plugin against a fake OpenCode context and a fake daemon socket.
# Needs network the first time (installs typescript into a temporary directory).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cd "$tmp"
npm init -y >/dev/null
npm install --silent typescript@5
node "$here/test/plugin.test.mjs" "$here/agent-net.ts" "$tmp"
