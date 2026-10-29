#!/bin/sh
# Type-check agent-net.ts against the published @opencode/plugin types.
# OpenCode bundles these types in its binary, so they are installed into a
# temporary directory (needs network the first time).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cp "$here/agent-net.ts" "$here/tsconfig.json" "$tmp/"
cd "$tmp"
npm init -y >/dev/null
npm install --silent @opencode/plugin@2.2.0 effect @types/node typescript
npx tsc --noEmit -p tsconfig.json
echo "typecheck ok"
