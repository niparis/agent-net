#!/bin/sh
# Type-check agent-net.ts against the globally installed Pi types.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
pi="$(npm root -g)/@earendil-works/pi-coding-agent"
[ -d "$pi" ] || { echo "Pi not found at $pi (install it globally first)" >&2; exit 1; }
tmp=$(mktemp "$here/.tsconfig.XXXXXX.json")
trap 'rm -f "$tmp"' EXIT
cat > "$tmp" <<EOF
{
  "extends": "./tsconfig.json",
  "compilerOptions": {
    "typeRoots": ["$pi/node_modules/@types"],
    "paths": {
      "@earendil-works/pi-coding-agent": ["$pi/dist/index.d.ts"],
      "typebox": ["$pi/node_modules/typebox/build/index.d.mts"]
    }
  },
  "include": ["agent-net.ts"]
}
EOF
npx -y -p typescript tsc --noEmit -p "$tmp"
