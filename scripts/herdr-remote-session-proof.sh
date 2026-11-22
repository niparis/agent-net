#!/usr/bin/env bash
# Shows why a remote open broke agent-net's local herdr server, and that the
# daemon's remote-client environment keeps the two session folders apart.
#
# herdr keeps a client's local state, including herdr-client.log, under
# <XDG_CONFIG_HOME>/herdr/sessions/<name>. A local server named agent-net uses
# that folder, and so does `herdr --remote <host> --session agent-net`. The
# first run below shows both writing into one folder. The second run uses the
# environment the daemon now gives a remote client, and the local server's
# folder stays untouched.
#
# No second host is reachable here, so a fake ssh runs the remote command under
# a second throwaway HOME: the closest local equivalent. Everything uses
# temporary HOME and XDG folders. The owner's herdr servers and config are
# never touched.
#
# Usage: scripts/herdr-remote-session-proof.sh
set -euo pipefail
cd "$(dirname "$0")/.."

herdr="$(command -v herdr)" || { echo "herdr-remote-session-proof: herdr is not installed" >&2; exit 1; }
command -v script >/dev/null || { echo "herdr-remote-session-proof: script is not installed" >&2; exit 1; }

root="$(mktemp -d "${TMPDIR:-/tmp}/agent-net-herdr-remote.XXXXXX")"
local="$root/local"
remote="$root/remote"
fakebin="$root/bin"
mkdir -p "$local/home" "$local/config" "$local/state" "$local/cache" \
         "$remote/home" "$remote/config" "$remote/state" "$remote/cache" "$fakebin"
mkdir -p "$local/config/herdr"
printf 'onboarding = false\n' >"$local/config/herdr/config.toml"

unset HERDR_ENV HERDR_SESSION HERDR_SOCKET_PATH HERDR_CONFIG_PATH HERDR_PANE_ID

run_local() {
  env HOME="$local/home" XDG_CONFIG_HOME="$local/config" XDG_STATE_HOME="$local/state" \
      XDG_CACHE_HOME="$local/cache" "$@"
}
run_remote() {
  env HOME="$remote/home" XDG_CONFIG_HOME="$remote/config" XDG_STATE_HOME="$remote/state" \
      XDG_CACHE_HOME="$remote/cache" "$@"
}
local_server="$local/config/herdr/sessions/agent-net"
local_socket="$local_server/herdr.sock"

cleanup() {
  run_local "$herdr" --session agent-net server stop >/dev/null 2>&1 || true
  run_remote "$herdr" --session agent-net server stop >/dev/null 2>&1 || true
  if [ -n "${local_pid:-}" ]; then kill "$local_pid" 2>/dev/null || true; fi
  if [ -n "${remote_pid:-}" ]; then kill "$remote_pid" 2>/dev/null || true; fi
  rm -rf "$root"
}
trap cleanup EXIT

cat >"$fakebin/ssh" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >>"$root/ssh.log"
while [ \$# -gt 0 ]; do
  case "\$1" in
    -F|-S|-o|-i|-p|-l) shift 2 ;;
    -*) shift ;;
    *) break ;;
  esac
done
[ \$# -gt 0 ] && shift
case "\$*" in
  *"/bin/sh -s"*) exec env HOME="$remote/home" XDG_CONFIG_HOME="$remote/config" \
      XDG_STATE_HOME="$remote/state" XDG_CACHE_HOME="$remote/cache" /bin/sh -s ;;
esac
exec env HOME="$remote/home" XDG_CONFIG_HOME="$remote/config" \
    XDG_STATE_HOME="$remote/state" XDG_CACHE_HOME="$remote/cache" /bin/sh -c "\$*"
EOF
chmod +x "$fakebin/ssh"

wait_for() {
  local path="$1" tries=100
  while [ "$tries" -gt 0 ] && [ ! -e "$path" ]; do
    sleep 0.05
    tries=$((tries - 1))
  done
  [ -e "$path" ]
}

listing() {
  if [ -d "$1" ]; then
    (cd "$1" && ls -1A | sort)
  else
    echo "(missing)"
  fi
}

echo "herdr: $herdr"
echo "temp:  $root"
echo "note:  a fake ssh runs the remote command under a second throwaway HOME; no second host is reachable here."
echo

run_local "$herdr" --session agent-net server >"$root/local-server.out" 2>&1 &
local_pid=$!
run_remote "$herdr" --session agent-net server >"$root/remote-server.out" 2>&1 &
remote_pid=$!
wait_for "$local_socket" || { echo "local server did not start" >&2; cat "$root/local-server.out" >&2; exit 1; }
wait_for "$remote/config/herdr/sessions/agent-net/herdr.sock" || { echo "remote server did not start" >&2; cat "$root/remote-server.out" >&2; exit 1; }

echo "local agent-net server folder before:"
listing "$local_server"
echo

# The exact remote command the daemon builds, with the client's own state in
# the local config folder, as it was before the fix.
(
  printf 'n\n' | timeout 15 script -qec "env PATH=$fakebin:$PATH HOME=$local/home XDG_CONFIG_HOME=$local/config XDG_STATE_HOME=$local/state XDG_CACHE_HOME=$local/cache $herdr --remote research --session agent-net" /dev/null
) >"$root/remote-before.out" 2>&1 || true
wait_for "$local_server/herdr-client.log" || true

echo "local agent-net server folder after a remote open named agent-net:"
listing "$local_server"
if [ -f "$local_server/herdr-client.log" ]; then
  echo "SHARED: the remote client wrote its herdr-client.log into the local server's session folder."
  echo "This machine's herdr did not remove the live server's sockets in this run;"
  echo "the shared folder is the part that matters, and on the owner's Mac the sockets went with it."
else
  echo "The remote client left no log in the local server's folder on this herdr version."
fi
echo

# Now the environment the daemon gives a remote client: its own
# XDG_CONFIG_HOME, and the owner's config through HERDR_CONFIG_PATH.
rm -f "$local_server/herdr-client.log"
isolated="$local/state-agent-net/herdr-remote"
(
  printf 'n\n' | timeout 15 script -qec "env PATH=$fakebin:$PATH HOME=$local/home XDG_CONFIG_HOME=$isolated XDG_STATE_HOME=$local/state XDG_CACHE_HOME=$local/cache HERDR_CONFIG_PATH=$local/config/herdr/config.toml $herdr --remote research --session agent-net" /dev/null
) >"$root/remote-after.out" 2>&1 || true
wait_for "$isolated/herdr/sessions/agent-net/herdr-client.log" || true

echo "agent-net state folder after the same open with the daemon's environment:"
listing "$isolated/herdr/sessions/agent-net"
echo
echo "local agent-net server folder after that open:"
listing "$local_server"
echo

failed=0
if [ ! -f "$isolated/herdr/sessions/agent-net/herdr-client.log" ]; then
  echo "FAIL: the remote client wrote no state in the isolated folder." >&2
  failed=1
fi
if [ -f "$local_server/herdr-client.log" ]; then
  echo "FAIL: the remote client still shares the local server's session folder." >&2
  failed=1
fi
if [ ! -S "$local_socket" ]; then
  echo "FAIL: the local server's socket disappeared." >&2
  failed=1
fi
if [ "$failed" -eq 0 ]; then
  echo "OK: the local server keeps its folder and socket, and the remote client keeps its state in agent-net's state folder."
fi
exit "$failed"
