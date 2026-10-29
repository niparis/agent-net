# Upgrade

One command upgrades agent-net:

```
agent-net upgrade
```

Run it on the gateway host. It does this:

1. Finds the latest release on GitHub (or the one you give with `--version vX.Y.Z`). If this machine already runs it, it says so and goes to step 4. `--force` runs all steps anyway.
2. Downloads the binary for this machine, the binaries for all four platforms and `checksums.txt` to a temporary directory, and checks the SHA-256 of each file. If one does not match, it stops and nothing has changed.
3. Keeps the current binary as `agent-net.prev` next to it, replaces `~/.local/bin/agent-net` in one atomic step, refreshes `~/.local/share/agent-net/dist`, and runs the new binary's `agent-net setup --gateway`. That restarts the gateway and daemon services and refreshes the integrations.
4. Tells every connected machine to upgrade, waits up to 2 minutes (`--timeout`), and prints one line per machine:

```
laptop n_5c05f2151c059f44  v0.1.0 -> v0.2.0  ok
mac    n_80933bfb6b6377da  v0.1.0 -> v0.2.0  failed (checksum mismatch for agent-net-darwin-arm64)
nas    n_870459ef2d2d18f2  v0.1.0 -> v0.2.0  offline (will not upgrade until you run agent-net upgrade there)
```

A machine that is offline is not upgraded. Run `agent-net upgrade` on it when it is back.

## On another machine

`agent-net upgrade` on a joined machine downloads its binary and the checksum from the gateway, over the CA the machine already trusts. It verifies the file, keeps `agent-net.prev`, replaces the binary and runs `agent-net setup`. If the gateway runs an older version than the latest release, it prints a hint to run `agent-net upgrade` on the gateway first.

When the gateway tells a machine to upgrade, the daemon does the same by itself. It reports the result to the gateway, then restarts through the service manager (`systemctl --user restart agent-net-daemon` on Linux, an exit and `KeepAlive` on macOS). The machine reports its new version when it reconnects.

## Look and undo

```
agent-net upgrade --check      # versions: this machine, latest release, gateway, every machine. Changes nothing.
agent-net upgrade --rollback   # swap agent-net.prev back and restart services, on this machine only
agent-net nodes                # host, node, version and online state of every machine
agent-net status               # also shows the version of this machine
```

## Running sessions

Running Claude, Pi and OpenCode sessions keep the old version until they restart. A Claude session keeps its old `agent-net mcp` process and talks to the new daemon. The local socket protocol stays compatible within a release, and the client says which protocol it speaks. If a daemon ever needs a newer client, the old one gets the error "restart this session" and not a silent failure. Pi loads the rewritten extension file at the next session start.

## Notes

- A dev build (version `dev`) refuses to upgrade without `--force`.
- `AGENT_NET_RELEASE_BASE_URL` replaces `https://github.com/niparis/agent-net/releases`. Tests use it.
- With `AGENT_NET_NO_SERVICE=1` there is no service manager, so the gateway and the daemons restart themselves in place. `scripts/e2e-upgrade.sh` uses this to test the whole flow with real binaries and throwaway state.
- Machines that run a version from before this feature cannot be upgraded from the gateway. They show up as "too old to upgrade remotely". Run `agent-net upgrade` or the install line there once.
