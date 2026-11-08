# Upgrade

One command upgrades agent-net:

```
agent-net upgrade
```

Run it on the gateway host. It does this:

1. Finds the latest release on GitHub (or the one you give with `--version vX.Y.Z`). If this machine already runs it, it says so and goes to step 4. `--force` runs all steps anyway.
2. Downloads `checksums.txt` and its signature, verifies the Ed25519 signature, then downloads the binaries for all four platforms and checks the SHA-256 of each file. If one does not match, it stops and nothing has changed.
3. Keeps the current binary as `agent-net.prev` next to it, replaces `~/.local/bin/agent-net` in one atomic step, refreshes `~/.local/share/agent-net/dist`, and runs the new binary's `agent-net setup --gateway`. That restarts the gateway and daemon services and refreshes the integrations.
4. Waits for the machines that were online to reconnect to the restarted gateway (up to 30 seconds, and not longer than `--timeout`), tells every connected machine to upgrade, waits up to 2 minutes (`--timeout`), and prints one line per machine. A machine that already upgraded itself while it reconnected shows `ok (upgraded on reconnect)`.

```
laptop n_5c05f2151c059f44  v0.1.0 -> v0.2.0  ok
mac    n_80933bfb6b6377da  v0.1.0 -> v0.2.0  failed (checksum mismatch for agent-net-darwin-arm64)
nas    n_870459ef2d2d18f2  v0.1.0 -> v0.2.0  offline (upgrades itself when it reconnects, unless auto-upgrade is turned off there)
```

A machine that is offline is not upgraded now. It upgrades itself when it is back (see "Automatic catch-up" below). Run `agent-net upgrade` on it if you do not want to wait or if automatic upgrade is off.

## On another machine

`agent-net upgrade` on a joined machine downloads the signed checksums and its binary from the gateway, over the CA the machine already trusts. It verifies the signature and the file, keeps `agent-net.prev`, replaces the binary and runs `agent-net setup`. If the gateway runs an older version than the latest release, it prints a hint to run `agent-net upgrade` on the gateway first.

When the gateway tells a machine to upgrade, the daemon does the same by itself, except that it does not run `agent-net setup`. It reports the result to the gateway, then restarts through the service manager (`systemctl --user restart agent-net-daemon` on Linux, an exit and `KeepAlive` on macOS). The machine reports its new version when it reconnects. The new daemon refreshes the agent files when it starts (next section), so no setup run is needed.

## Agent files on a machine

Setup writes the files that agents load: the Pi extension `~/.pi/agent/extensions/agent-net/index.ts`, the OpenCode plugin `~/.config/opencode/plugins/agent-net.ts`, and the guidance block between `<!-- agent-net:begin -->` and `<!-- agent-net:end -->` in `~/.claude/CLAUDE.md`, `~/.pi/agent/AGENTS.md` and `~/.config/opencode/AGENTS.md`. When you installed the Claude Code workers plugin, setup also wrote a local marketplace in `~/.local/share/agent-net/claude-plugins/` with the plugin `agent-net-workers` that Claude Code reads from that folder. A binary can start on a machine without setup running: a pushed upgrade, an automatic catch-up, the install line, or a binary you copied in. So the daemon does this on every start, with the same code as setup, before it connects:

- It compares each of these files with the content embedded in its own binary and rewrites the ones that differ. It logs one line per file it changed (`agent files: pi extension updated`). A start that changes nothing logs nothing.
- It follows the rules of setup. It only touches a harness whose folder exists (`~/.pi`, `~/.config/opencode` or `$XDG_CONFIG_HOME/opencode`, `~/.claude`) and never creates one. A symlinked Pi or OpenCode file to a file agent-net did not write is left alone and logged (`agent files: pi extension left alone (symlink)`). A symlink to an older agent-net copy is replaced with a real file holding the current version, and the old target path is kept in the log line. In a guidance file it replaces only the text between the markers. It refreshes the Claude workers marketplace only when setup already wrote it, so the plugin is never installed without your answer.
- The daemon of a dev build does the same with the files of the dev build.

The running sessions load the new files as described under "Running sessions". A running Claude Code session picks up a rewritten plugin after `/reload-plugins`.

## Automatic catch-up

A machine can be offline when you run `agent-net upgrade` on the gateway. So after each connect, the daemon asks the gateway which version it serves to this machine. That is the version of the gateway's dist binary for the machine's OS and architecture, the one the machine would install (`GET /install/version?os=linux&arch=amd64`). The gateway runs the dist binary of its own platform with `version` and takes that answer for all four, since the dist dir holds one release. If it is a newer release than the one the daemon runs, the daemon upgrades itself:

- It waits a random 0 to 10 seconds first, so machines that reconnect together do not all download at once.
- It then does exactly what it does for a request from the gateway: downloads from the gateway over the pinned TLS, verifies the release signature, checks the SHA-256 and that the binary reports the target version, keeps `agent-net.prev`, replaces the binary and restarts through the service manager.
- The daemon log has one line before (`auto-upgrade: the gateway serves v0.2.0 and this machine runs v0.1.0, upgrading`) and one after (`auto-upgrade: v0.1.0 -> v0.2.0 installed, restarting`). The gateway learns the new version from the next hello, so `agent-net nodes` shows it.

Rules:

- Only releases are compared (`v1.2.3`, so `v0.10.0` is newer than `v0.9.9`). A dev build, or any version that is not a release, never upgrades by itself. A machine that is newer than the gateway does nothing. There is no automatic downgrade.
- If an automatic upgrade to a version fails (download, checksum, version check or replace), the daemon writes the version and the time to `auto-upgrade.json` in its state dir, logs the reason and does not try that version again for 1 hour. It tries again on the next reconnect after that hour, or at once if the gateway serves a different version.
- `agent-net upgrade --rollback` on a machine holds the version it rolled back from. Automatic upgrade skips that version and older ones. A newer release lifts the hold and the machine upgrades. `agent-net upgrade` on that machine lifts it too. `agent-net status` and `agent-net upgrade --check` show `auto-upgrade held at vX after rollback`.
- The gateway host's own daemon follows the same rules. Its gateway is local and normally serves the same version, so it does nothing.

A gateway rollback clears the signed manifest before running setup with the previous binary, which may replace the gateway platform binary in the dist directory. Signed nodes refuse upgrades while the gateway has no signed manifest. To restore a matching signed dist, run the published installer on the gateway with `--gateway --version vX.Y.Z` for the release you want to serve. It must be a signed release with the currently trusted key.

### Turn it off

Run `agent-net setup --no-auto-upgrade` on the machine. It saves the choice in `node.json`, keeps it through setup and rejoining, and restarts the daemon. Use `agent-net setup --auto-upgrade` to clear the choice. A daemon started with `--no-auto-upgrade` or `AGENT_NET_NO_AUTO_UPGRADE=1` also stays off even if the saved choice is cleared. The environment option can also turn off MCP process switching.

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

`agent-net upgrade` ends with three lines that say the same as this section: Claude Code sessions started on v0.3.5 or later switch by themselves, Pi needs `/reload`, and OpenCode reloads its plugin file by itself. The Pi and OpenCode lines hold for every machine that was upgraded: each one rewrites its files when its daemon restarts, so a Pi session on another machine needs `/reload` too.

### Claude Code

From v0.3.5 on, a running Claude Code session picks up the new version without a restart. Each session has its own `agent-net mcp` process. It switches itself to the new binary like this:

1. At most every 10 seconds, while the MCP connection is idle, the process checks whether the installed executable changed by inode, size or modification time. This also works if the daemon did not restart.
2. It runs `<install path> version`. It switches only if that file reports a newer release than the running process; a rollback, the same version or an unorderable version is left alone.
3. It waits until nothing is in flight: no tool call running, no answer owed and no output being written. A request the client cancelled does not count. A `subscriptions/listen` request does not count either: newer Claude Code versions (MCP 2026-07-28) keep one open. Then it replaces itself with the new binary (`exec`). The process ID and stdin and stdout pipes stay the same.
4. The old process passes a private handover file through an environment variable. It contains the initialize and initialized messages, open subscriptions, and unread input. The new process reads and deletes the file, clears the variable, and replays the messages without answering them again. It sends `notifications/tools/list_changed` only when the client requested those notifications.

The switch is logged to the session's MCP stderr. The new process says hello to the daemon with its new version. If exec fails, the process stays on the old code, logs the error once and does not retry. `AGENT_NET_NO_AUTO_UPGRADE=1` in the MCP process environment turns the switch off.

If a session did not switch (the switch failed, `AGENT_NET_NO_AUTO_UPGRADE=1`, or the process is older than v0.3.5), the daemon still knows: it compares the version in the `hello` with its own and marks the session `tools_old`. The TUI shows it in the detail pane, and a process of v0.4.7 or later adds a one-line note to its next tool result so the agent asks you to reconnect. See "Tools version for each session" in [reference.md](reference.md#what-each-machine-reports).

A session started before v0.3.5 has no such code. It needs one restart. After that the next upgrades do not need one. Until then it keeps the old process and talks to the new daemon. The local socket protocol stays compatible within a release, and the client says which protocol it speaks. If a daemon ever needs a newer client, the old one gets the error "restart this session" and not a silent failure.

### Pi

A Pi session loads the extension file once, into the Pi process. The daemon rewrites the file when it starts after an upgrade, but the running session keeps the old code. Run `/reload` in the Pi session to load the new file without leaving the session, or start a new session. The extension cannot reload itself: Pi only allows that from a command handler.

### OpenCode

OpenCode watches `~/.config/opencode/plugins/` and reloads a changed plugin file within seconds, so no action is needed. See [opencode.md](opencode.md).

## Notes

- A dev build (version `dev`) refuses to upgrade without `--force`, and never upgrades automatically. Manual and pushed downgrades, and versions that cannot be ordered, also require `--force`; automatic upgrades only move forward.
- `AGENT_NET_RELEASE_BASE_URL` replaces `https://github.com/niparis/agent-net/releases`. Tests use it.
- With `AGENT_NET_NO_SERVICE=1` there is no service manager, so the gateway and the daemons restart themselves in place. `scripts/e2e-upgrade.sh` uses this to test the whole flow, including automatic catch-up, with real binaries and throwaway state. It also sets `AGENT_NET_AUTO_UPGRADE_MAX_DELAY` (like `1s`) to shorten the random wait.
- Machines that run a version from before this feature cannot be upgraded from the gateway. They show up as "too old to upgrade remotely". Run `agent-net upgrade` or the install line there once.
