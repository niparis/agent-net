#!/bin/sh
# agent-net installer.
#
#   curl -fsSL https://niparis.github.io/agent-net/install | sh
#
# With no options it looks at the machine and picks a mode:
#   - gateway data found here   -> update the gateway (as --gateway)
#   - node.json found here      -> update this joined machine (as --client)
#   - neither                   -> set this machine up as the gateway (as --gateway)
# It prints which mode it chose. Other machines join with the line that
# `agent-net invite` prints on the gateway, not with this script.
#
# Overrides:
#   --gateway          install everything for a gateway and run `agent-net setup --gateway`
#                      (also downloads the binaries for all platforms, which the gateway serves)
#   --client           install the binary and run `agent-net setup` (no join, no gateway)
#   --binary-only      only install ~/.local/bin/agent-net and print next steps
#   --accept-peer-messages     passed to setup: set Claude Code "crossSessionInbound" to "accept" (only if unset)
#   --no-accept-peer-messages  passed to setup: do not set it and do not ask
#   --version vX.Y.Z   install this release (default: latest)
#
# AGENT_NET_BASE_URL overrides the download base URL (used by tests).
# AGENT_NET_NO_SERVICE=1 is passed on to setup: no service files, nothing started.

# Everything is inside main, called on the last line, so a truncated download
# runs nothing.
main() {
  set -eu

  REPO="niparis/agent-net"
  MODE=""
  FRESH=""
  PEER_FLAG=""
  VERSION=""
  USAGE="usage: install.sh [--gateway | --client | --binary-only] [--accept-peer-messages | --no-accept-peer-messages] [--version vX.Y.Z]"

  die() { echo "agent-net install: $*" >&2; exit 1; }

  while [ $# -gt 0 ]; do
    case "$1" in
      --gateway) MODE=gateway ;;
      --client) MODE=client ;;
      --binary-only) MODE=binary ;;
      --accept-peer-messages|--no-accept-peer-messages) PEER_FLAG="$1" ;;
      --version)
        [ $# -ge 2 ] || die "--version needs a value, like v0.1.0"
        VERSION="$2"
        shift
        ;;
      -h|--help)
        echo "$USAGE"
        exit 0
        ;;
      *) die "unknown option: $1 ($USAGE)" ;;
    esac
    shift
  done

  command -v curl >/dev/null 2>&1 || die "curl is required"
  [ -n "${HOME:-}" ] || die "HOME is not set"

  # No option given: look at what is on this machine.
  if [ -z "$MODE" ]; then
    GW_DATA="${AGENT_NET_GATEWAY_DATA:-$HOME/.local/state/agent-net/gateway}"
    if [ -n "${AGENT_NET_CONFIG_DIR:-}" ]; then
      CONF_DIR="$AGENT_NET_CONFIG_DIR"
    elif [ -n "${XDG_CONFIG_HOME:-}" ]; then
      CONF_DIR="$XDG_CONFIG_HOME/agent-net"
    else
      CONF_DIR="$HOME/.config/agent-net"
    fi
    if [ -f "$GW_DATA/ca.pem" ]; then
      MODE=gateway
      echo "gateway found ($GW_DATA): updating"
    elif [ -f "$CONF_DIR/node.json" ]; then
      MODE=client
      echo "this machine is already joined ($CONF_DIR/node.json): updating"
    else
      MODE=gateway
      FRESH=1
      echo "no agent-net here yet: setting this machine up as the gateway"
    fi
  fi

  if command -v sha256sum >/dev/null 2>&1; then
    sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
  elif command -v shasum >/dev/null 2>&1; then
    sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
  else
    die "sha256sum or shasum is required to verify the download"
  fi

  case "$(uname -s)" in
    Linux) OS=linux ;;
    Darwin) OS=darwin ;;
    *) die "unsupported operating system: $(uname -s)" ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    arm64|aarch64) ARCH=arm64 ;;
    *) die "unsupported CPU: $(uname -m)" ;;
  esac

  if [ -n "${AGENT_NET_BASE_URL:-}" ]; then
    BASE="${AGENT_NET_BASE_URL%/}"
  elif [ -n "$VERSION" ]; then
    BASE="https://github.com/$REPO/releases/download/$VERSION"
  else
    BASE="https://github.com/$REPO/releases/latest/download"
  fi

  BIN_DIR="$HOME/.local/bin"
  DIST_DIR="$HOME/.local/share/agent-net/dist"
  mkdir -p "$BIN_DIR" || die "cannot create $BIN_DIR"

  WORK="$(mktemp -d "${TMPDIR:-/tmp}/agent-net-install.XXXXXX")" || die "cannot create a temporary directory"
  trap 'rm -rf "$WORK"' EXIT HUP INT TERM

  echo "downloading checksums ($BASE)"
  curl -fsSL -o "$WORK/checksums.txt" "$BASE/checksums.txt" || die "cannot download $BASE/checksums.txt (is the version right?)"

  # fetch NAME: download into $WORK and check its SHA-256 against checksums.txt.
  fetch() {
    name="$1"
    want="$(awk -v n="$name" '$2 == n || $2 == "*" n { print $1; exit }' "$WORK/checksums.txt")"
    [ -n "$want" ] || die "$name is not listed in checksums.txt"
    curl -fsSL -o "$WORK/$name" "$BASE/$name" || die "cannot download $BASE/$name"
    got="$(sha256 "$WORK/$name")"
    [ "$got" = "$want" ] || die "checksum mismatch for $name (wanted $want, got $got)"
  }

  # place SRC DIR NAME: copy into DIR through a temp file in DIR, then rename.
  place() {
    tmp="$(mktemp "$2/.$3.XXXXXX")" || die "cannot write in $2"
    if ! cp "$1" "$tmp" || ! chmod 755 "$tmp" || ! mv -f "$tmp" "$2/$3"; then
      rm -f "$tmp"
      die "cannot install $2/$3"
    fi
  }

  if [ "$MODE" = gateway ]; then
    mkdir -p "$DIST_DIR" || die "cannot create $DIST_DIR"
    for o in linux darwin; do
      for a in amd64 arm64; do
        echo "downloading agent-net-$o-$a"
        fetch "agent-net-$o-$a"
        place "$WORK/agent-net-$o-$a" "$DIST_DIR" "agent-net-$o-$a"
      done
    done
    place "$WORK/agent-net-$OS-$ARCH" "$BIN_DIR" agent-net
  else
    echo "downloading agent-net-$OS-$ARCH"
    fetch "agent-net-$OS-$ARCH"
    place "$WORK/agent-net-$OS-$ARCH" "$BIN_DIR" agent-net
  fi
  echo "installed $BIN_DIR/agent-net ($("$BIN_DIR/agent-net" version))"

  case ":${PATH:-}:" in
    *":$BIN_DIR:"*) ;;
    *)
      echo
      echo "$BIN_DIR is not on your PATH. Add it:"
      echo "  echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.profile"
      ;;
  esac

  if [ "$MODE" != binary ]; then
    echo
    rm -rf "$WORK" # exec skips the exit trap
    if [ "$MODE" = gateway ]; then
      if [ -n "$FRESH" ]; then
        echo "Other machines join with the line that \`agent-net invite\` prints on this machine."
        echo
      fi
      exec "$BIN_DIR/agent-net" setup --gateway ${PEER_FLAG:+"$PEER_FLAG"}
    fi
    exec "$BIN_DIR/agent-net" setup ${PEER_FLAG:+"$PEER_FLAG"}
  fi

  echo
  echo "Next: on the gateway machine run"
  echo "  agent-net invite"
  echo "and paste the line it prints here."
}

main "$@"
