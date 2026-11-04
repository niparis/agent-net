#!/bin/sh
# agent-net installer.
#
#   curl -fsSL https://niparis.github.io/agent-net/install | sh
#
# With no options it looks at the machine and picks a mode:
#   - gateway data found here   -> update the gateway (as --gateway)
#   - node.json found here      -> update this joined machine (as --client)
#   - neither                   -> set this machine up as the gateway (as --gateway)
# It prints which mode it chose. Add other machines from the CTO inside the
# TUI (`agent-net`); it prints the line to paste on the new machine.
#
# Overrides:
#   --gateway          install everything for a gateway and run `agent-net setup --gateway`
#                      (also downloads the binaries for all platforms, which the gateway serves)
#   --client           install the binary and run `agent-net setup` (no join, no gateway)
#   --binary-only      only install ~/.local/bin/agent-net and print next steps
#   --all              passed to setup: turn all six integrations on, without asking
#   --none             passed to setup: turn all six integrations off, without asking
#   --accept-peer-messages     passed to setup: set Claude Code "crossSessionInbound" to "accept" (only if unset)
#   --no-accept-peer-messages  passed to setup: do not set it and do not ask
#   --version vX.Y.Z   install this release (default: latest)
#
# AGENT_NET_BASE_URL overrides the release root URL (used by tests).
# AGENT_NET_NO_SERVICE=1 is passed on to setup: no service files, nothing started.

# Everything is inside main, called on the last line, so a truncated download
# runs nothing.
main() {
  set -eu

  REPO="niparis/agent-net"
  # The release signing public key (SSH form). Rotation: see RELEASING.md.
  RELEASE_SSH_PUBLIC_KEY="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICfe4AYiEPeRCxo90cOw16p3SMEGTKIE0egPpfCuBfAB"
  MODE=""
  FRESH=""
  PEER_FLAG=""
  INTEG_FLAG=""
  VERSION=""
  USAGE="usage: install.sh [--gateway | --client | --binary-only] [--all | --none] [--accept-peer-messages | --no-accept-peer-messages] [--version vX.Y.Z]"

  die() { echo "agent-net install: $*" >&2; exit 1; }

  while [ $# -gt 0 ]; do
    case "$1" in
      --gateway) MODE=gateway ;;
      --client) MODE=client ;;
      --binary-only) MODE=binary ;;
      --all|--none) INTEG_FLAG="$1" ;;
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
  command -v ssh-keygen >/dev/null 2>&1 || die "ssh-keygen (OpenSSH 8.1 or newer) is required to verify the release signature"
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

  RELEASES="${AGENT_NET_BASE_URL:-https://github.com/$REPO/releases}"
  RELEASES="${RELEASES%/}"
  if [ -z "$VERSION" ]; then
    # Read GitHub's one redirect without following it. Every asset then comes
    # from that tag, even if a new latest release appears during the install.
    location="$(curl -fsSI -o /dev/null -w '%{redirect_url}' "$RELEASES/latest/download/checksums.txt")" || die "cannot find the latest release"
    case "$location" in
      */download/*/checksums.txt) VERSION="${location%/checksums.txt}"; VERSION="${VERSION##*/download/}" ;;
      *) die "cannot read the latest release tag from $location" ;;
    esac
  fi
  case "$VERSION" in
    ''|*[!A-Za-z0-9._-]*) die "invalid release tag: $VERSION" ;;
  esac
  BASE="$RELEASES/download/$VERSION"

  BIN_DIR="$HOME/.local/bin"
  DIST_DIR="$HOME/.local/share/agent-net/dist"
  mkdir -p "$BIN_DIR" || die "cannot create $BIN_DIR"

  WORK="$(mktemp -d "${TMPDIR:-/tmp}/agent-net-install.XXXXXX")" || die "cannot create a temporary directory"
  trap 'rm -rf "$WORK"' EXIT HUP INT TERM

  echo "downloading checksums ($BASE)"
  curl -fsSL -o "$WORK/checksums.txt" "$BASE/checksums.txt" || die "cannot download $BASE/checksums.txt (is the version right?)"
  curl -fsSL -o "$WORK/checksums.txt.sshsig" "$BASE/checksums.txt.sshsig" 2>/dev/null || die "release checksums signature is missing or invalid"
  printf 'agent-net %s\n' "$RELEASE_SSH_PUBLIC_KEY" > "$WORK/allowed_signers"
  ssh-keygen -Y verify -f "$WORK/allowed_signers" -I agent-net -n agent-net \
    -s "$WORK/checksums.txt.sshsig" < "$WORK/checksums.txt" >/dev/null 2>&1 || die "release checksums signature is missing or invalid (ssh-keygen needs OpenSSH 8.1 or newer)"
  if [ "$MODE" = gateway ]; then
    curl -fsSL -o "$WORK/checksums.txt.sig" "$BASE/checksums.txt.sig" 2>/dev/null || die "release checksums signature is missing or invalid"
  fi

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
    for o in linux darwin; do
      for a in amd64 arm64; do
        echo "downloading agent-net-$o-$a"
        fetch "agent-net-$o-$a"
      done
    done
  else
    echo "downloading agent-net-$OS-$ARCH"
    fetch "agent-net-$OS-$ARCH"
  fi
  chmod 755 "$WORK/agent-net-$OS-$ARCH" || die "cannot run downloaded binary"
  got_version="$("$WORK/agent-net-$OS-$ARCH" version)" || die "downloaded binary cannot report its version"
  [ "$got_version" = "$VERSION" ] || die "release $VERSION binary reports version $got_version"

  if [ "$MODE" = gateway ]; then
    mkdir -p "$DIST_DIR" || die "cannot create $DIST_DIR"
    # Invalidate the previous signed dist before changing any binary, so an
    # interrupted refresh cannot leave a trusted manifest for mixed files.
    rm -f "$DIST_DIR/checksums.txt.sig" "$DIST_DIR/checksums.txt" || die "cannot invalidate signed dist"
    for o in linux darwin; do
      for a in amd64 arm64; do
        place "$WORK/agent-net-$o-$a" "$DIST_DIR" "agent-net-$o-$a"
      done
    done
    place "$WORK/checksums.txt" "$DIST_DIR" checksums.txt
    place "$WORK/checksums.txt.sig" "$DIST_DIR" checksums.txt.sig
  fi
  place "$WORK/agent-net-$OS-$ARCH" "$BIN_DIR" agent-net
  echo "installed $BIN_DIR/agent-net ($got_version)"

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
        echo "Other machines: open agent-net and ask the CTO to add one."
        echo
      fi
      exec "$BIN_DIR/agent-net" setup --gateway ${INTEG_FLAG:+"$INTEG_FLAG"} ${PEER_FLAG:+"$PEER_FLAG"}
    fi
    exec "$BIN_DIR/agent-net" setup ${INTEG_FLAG:+"$INTEG_FLAG"} ${PEER_FLAG:+"$PEER_FLAG"}
  fi

  echo
  echo "Next"
  echo "  agent-net setup"
  echo "  agent-net"
}

main "$@"
