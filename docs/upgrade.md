# Upgrade

One command upgrades agent-net:

```
agent-net upgrade
```

Run it on the gateway host. It does this:

1. Finds the latest release on GitHub (or the one you give with `--version vX.Y.Z`). If this machine already runs it, it says so and goes to step 4. `--force` runs all steps anyway.
2. Downloads the binary for this machine, the binaries for all four platforms and `checksums.txt` to a temporary directory, and checks the SHA-256 of each file. If one does not match, it stops and nothing has changed.
3. Keeps the current binary as `agent-net.prev` next to it, replaces `~/.local/bin/agent-net` in one atomic step, refreshes `~/.local/share/agent-net/dist`, and runs the new binary's `agent-net setup --gateway`. That restarts the gateway and daemon services and refreshes the integrations.
4. Waits for the machines that were online to reconnect to the restarted gateway (up to 30 seconds, and not longer than `--timeout`), tells every connected machine to upgrade, waits up to 2 minutes (`--timeout`), and prints one line per machine. A machine that already upgraded itself while it reconnected shows `ok (upgraded on reconnect)`.

```
laptop n_5c05f2151c059f44  v0.1.0 -> v0.2.0  ok
mac    n_80933bfb6b6377da  v0.1.0 -> v0.2.0  failed (checksum mismatch for agent-net-darwin-arm64)
nas    n_870459ef2d2d18f2  v0.1.0 -> v0.2.0  offline (upgrades itself when it reconnects, unless auto-upgrade is turned off there)
```

A machine that is offline is not upgraded now. It upgrades itself when it is back (see "Automatic catch-up" below). Run `agent-net upgrade` on it if you do not want to wait or if automatic upgrade is off.

## On another machine

`agent-net upgrade` on a joined machine downloads its binary and the checksum from the gateway, over the CA the machine already trusts. It verifies the file, keeps `agent-net.prev`, replaces the binary and runs `agent-net setup`. If the gateway runs an older version than the latest release, it prints a hint to run `agent-net upgrade` on the gateway first.

When the gateway tells a machine to upgrade, the daemon does the same by itself. It reports the result to the gateway, then restarts through the service manager (`systemctl --user restart agent-net-daemon` on Linux, an exit and `KeepAlive` on macOS). The machine reports its new version when it reconnects.

## Automatic catch-up

A machine can be offline when you run `agent-net upgrade` on the gateway. So after each connect, the daemon asks the gateway which version it serves to this machine. That is the version of the gateway's dist binary for the machine's OS and architecture, the one the machine would install (`GET /install/version?os=linux&arch=amd64`). The gateway runs the dist binary of its own platform with `version` and takes that answer for all four, since the dist dir holds one release. If it is a newer release than the one the daemon runs, the daemon upgrades itself:

- It waits a random 0 to 10 seconds first, so machines that reconnect together do not all download at once.
- It then does exactly what it does for a request from the gateway: downloads from the gateway over the pinned TLS, checks the SHA-256 and that the binary reports the target version, keeps `agent-net.prev`, replaces the binary and restarts through the service manager.
- The daemon log has one line before (`auto-upgrade: the gateway serves v0.2.0 and this machine runs v0.1.0, upgrading`) and one after (`auto-upgrade: v0.1.0 -> v0.2.0 installed, restarting`). The gateway learns the new version from the next hello, so `agent-net nodes` shows it.

Rules:

- Only releases are compared (`v1.2.3`, so `v0.10.0` is newer than `v0.9.9`). A dev build, or any version that is not a release, never upgrades by itself. A machine that is newer than the gateway does nothing. There is no automatic downgrade.
- If an automatic upgrade to a version fails (download, checksum, version check or replace), the daemon writes the version and the time to `auto-upgrade.json` in its state dir, logs the reason and does not try that version again for 1 hour. It tries again on the next reconnect after that hour, or at once if the gateway serves a different version.
- `agent-net upgrade --rollback` on a machine holds the version it rolled back from. Automatic upgrade skips that version and older ones. A newer release lifts the hold and the machine upgrades. `agent-net upgrade` on that machine lifts it too. `agent-net status` and `agent-net upgrade --check` show `auto-upgrade held at vX after rollback`.
- The gateway host's own daemon follows the same rules. Its gateway is local and normally serves the same version, so it does nothing.

### Turn it off

Set `AGENT_NET_NO_AUTO_UPGRADE=1` in the daemon's environment, or start the daemon with `--no-auto-upgrade`. Setup has no flag for this.

- Linux (systemd): run `systemctl --user edit agent-net-daemon` and add

  ```
  [Service]
  Environment=AGENT_NET_NO_AUTO_UPGRADE=1
  ```

  Then `systemctl --user restart agent-net-daemon`. The override lives in a drop-in file, so `agent-net setup` does not remove it.
- macOS (launchd): add this to the top-level `<dict>` of `~/Library/LaunchAgents/com.agent-net.daemon.plist`:

  ```
  <key>EnvironmentVariables</key>
  <dict><key>AGENT_NET_NO_AUTO_UPGRADE</key><string>1</string></dict>
  ```

  Then `launchctl unload` and `launchctl load` the plist. `agent-net setup` rewrites this file, so add the lines again after a setup.

## Look and undo

```
agent-net upgrade --check      # versions: this machine, latest release, gateway, every machine. Changes nothing.
agent-net upgrade --rollback   # swap agent-net.prev back and restart services, on this machine only; holds automatic upgrade at the version you left
agent-net nodes                # host, node, version and online state of every machine
agent-net status               # also shows the version of this machine
```

## Running sessions

`agent-net upgrade` ends with three lines that say the same as this section: Claude Code sessions started on v0.3.5 or later switch by themselves, Pi needs `/reload`, and OpenCode reloads its plugin file by itself.

### Claude Code

From v0.3.5 on, a running Claude Code session picks up the new version without a restart. Each session has its own `agent-net mcp` process. It switches itself to the new binary like this:

1. After an upgrade the daemon restarts. The `agent-net mcp` process notices within a few seconds, because its connection to the daemon drops and it dials again. The daemon's answer to the new `hello` carries its version.
2. If that is a newer release than the process runs, the process runs `<install path> version`. Only if the file on disk reports exactly the daemon's version does it go on. So a half-finished upgrade, a rollback or a dev build never switches.
3. It waits until nothing is in flight: no tool call running, no answer owed. A request the client cancelled does not count. A `subscriptions/listen` request does not count either: newer Claude Code versions (MCP 2026-07-28) open one and keep it open for the whole session. Then it replaces itself with the new binary (`exec`). The process id and the stdin and stdout pipes stay the same, so Claude Code keeps its connection, and the daemon's check of the calling process still passes.
4. The old process hands over what the client sent once at the start (the `initialize` request and `notifications/initialized`, or the open `subscriptions/listen` requests) and any input it had read but not yet handled. The new process replays the start-up messages into its own MCP server, without answering them again, and then sends `notifications/tools/list_changed`. Claude Code reads the tool list again.

The switch is logged to the session's MCP stderr (`agent-net mcp: switching from v0.3.2 to v0.3.3`). Claude Code does not show that stderr, so the daemon also logs one line for each `hello` from a session: `mcp hello: claude session ID pid PID runs agent-net v0.3.2, daemon runs v0.3.3`. A session that switched says hello again with the new version. If it stays on the old one, that line is the proof, and the process logs `still waiting to switch` after 15 seconds with the reason. If it fails, the process stays on the old code and logs why. It does not try the same daemon version again until the daemon restarts. `AGENT_NET_NO_AUTO_UPGRADE=1` in the environment of the Claude session turns the switch off. The `agent-net mcp` process reads it, so setting it only on the daemon is not enough.

A session started before v0.3.5 has no such code. It needs one restart. After that the next upgrades do not need one. Until then it keeps the old process and talks to the new daemon. The local socket protocol stays compatible within a release, and the client says which protocol it speaks. If a daemon ever needs a newer client, the old one gets the error "restart this session" and not a silent failure.

### Pi

A Pi session loads the extension file once, into the Pi process. Setup rewrites the file, but the running session keeps the old code. Run `/reload` in the Pi session to load the new file without leaving the session, or start a new session. The extension cannot reload itself: Pi only allows that from a command handler.

### OpenCode

OpenCode watches `~/.config/opencode/plugins/` and reloads a changed plugin file within seconds, so no action is needed. See [opencode.md](opencode.md).

## Notes

- A dev build (version `dev`) refuses to upgrade without `--force`, and never upgrades automatically.
- `AGENT_NET_RELEASE_BASE_URL` replaces `https://github.com/niparis/agent-net/releases`. Tests use it.
- With `AGENT_NET_NO_SERVICE=1` there is no service manager, so the gateway and the daemons restart themselves in place. `scripts/e2e-upgrade.sh` uses this to test the whole flow, including automatic catch-up, with real binaries and throwaway state. It also sets `AGENT_NET_AUTO_UPGRADE_MAX_DELAY` (like `1s`) to shorten the random wait.
- Machines that run a version from before this feature cannot be upgraded from the gateway. They show up as "too old to upgrade remotely". Run `agent-net upgrade` or the install line there once.
