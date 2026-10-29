#!/bin/sh
# Build agent-net for Linux and macOS (amd64, arm64) into the dist dir that the
# gateway serves, and install the one for this machine to ~/.local/bin.
#
#   scripts/dist.sh [DIST_DIR]      default: ~/.local/share/agent-net/dist
set -eu

cd "$(dirname "$0")/.."
DIST="${1:-${AGENT_NET_DIST:-$HOME/.local/share/agent-net/dist}}"
mkdir -p "$DIST"

for os in linux darwin; do
  for arch in amd64 arm64; do
    echo "building agent-net-$os-$arch"
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -o "$DIST/agent-net-$os-$arch.tmp" ./cmd/agent-net
    mv -f "$DIST/agent-net-$os-$arch.tmp" "$DIST/agent-net-$os-$arch"
  done
done

HOST_OS="$(go env GOOS)"
HOST_ARCH="$(go env GOARCH)"
# Skip the host install when building into a throwaway dir (tests).
if [ -z "${AGENT_NET_NO_INSTALL:-}" ]; then
  mkdir -p "$HOME/.local/bin"
  cp "$DIST/agent-net-$HOST_OS-$HOST_ARCH" "$HOME/.local/bin/agent-net.tmp"
  mv -f "$HOME/.local/bin/agent-net.tmp" "$HOME/.local/bin/agent-net"
  echo "installed $HOME/.local/bin/agent-net"
fi
