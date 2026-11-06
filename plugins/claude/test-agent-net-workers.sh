#!/bin/sh
# Validate the contract and run register.test.tsx with Claude's mock engine.
# npm needs network; validation and tests need no account or model calls.
set -eu
for v in $(env | sed -n 's/^\(AGENT_NET_[A-Za-z0-9_]*\)=.*/\1/p'); do unset "$v"; done
# Do not inherit credentials or contact the owner's daemon/configuration.
unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN CLAUDE_CODE_OAUTH_TOKEN
unset ANTHROPIC_BASE_URL CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR
here=$(cd "$(dirname "$0")/agent-net-workers" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export HOME="$tmp/home"
export CLAUDE_CONFIG_DIR="$tmp/config"
export XDG_CONFIG_HOME="$tmp/config"
export XDG_CACHE_HOME="$tmp/cache"
export XDG_STATE_HOME="$tmp/state"
export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
mkdir -p "$HOME"
cd "$tmp"
npm init -y >/dev/null
# Pin the release verified to support account-free module validation and tests.
npm install --silent @anthropic-ai/claude-code@2.1.318
"$tmp/node_modules/.bin/claude" plugin validate "$here"
"$tmp/node_modules/.bin/claude" plugin test "$here"
