# Setup: VM (gateway) and Mac

The gateway runs on the VM `research` (LAN 192.168.1.10, Tailscale 100.64.0.10). Each machine runs one daemon. The VM is already joined as node `n_0123456789abcdef`.

The gateway's TLS certificate includes every IP address of the VM, so the Mac can use either the LAN IP or the Tailscale IP.

## Part 1: VM

1. Install and start the services:

   ```
   cp ~/code/niparis/a2a-ocs/deploy/systemd/agent-net-*.service ~/.config/systemd/user/
   systemctl --user daemon-reload
   systemctl --user enable --now agent-net-gateway agent-net-daemon
   sudo loginctl enable-linger nicolas
   ```

   `enable-linger` keeps the services running when you are not logged in.

2. Check it:

   ```
   agent-net status
   ```

   Expect `gateway https://localhost:7420 (connected)`.

## Part 2: Mac

Run these commands on the Mac. Replace `VM` with `nicolas@192.168.1.10`, or with `nicolas@100.64.0.10` if the Mac reaches the VM over Tailscale.

1. Check that the Mac can reach the gateway:

   ```
   nc -vz 192.168.1.10 7420
   ```

2. Copy the binary. Use `arm64` for Apple Silicon and `amd64` for Intel. Run `uname -m` to see which one you have.

   ```
   mkdir -p ~/.local/bin
   scp VM:.local/share/agent-net/agent-net-darwin-arm64 ~/.local/bin/agent-net
   chmod +x ~/.local/bin/agent-net
   ```

3. Get a join code. It is valid for 10 minutes and works only once:

   ```
   ssh VM '~/.local/bin/agent-net enroll --host 192.168.1.10'
   ```

4. Join, using the string the last step printed. Keep the single quotes, because the string contains `#`:

   ```
   ~/.local/bin/agent-net join 'https://192.168.1.10:7420#CODE#SHA'
   ```

5. Start the daemon at login with launchd:

   ```
   scp VM:code/niparis/a2a-ocs/deploy/launchd/com.agent-net.daemon.plist /tmp/
   sed "s|HOME_DIR|$HOME|g" /tmp/com.agent-net.daemon.plist > ~/Library/LaunchAgents/com.agent-net.daemon.plist
   launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.agent-net.daemon.plist
   ~/.local/bin/agent-net status
   ```

   The log is in `~/Library/Logs/agent-net-daemon.log`.

6. Add the tools to Claude Code for all sessions:

   ```
   claude mcp add -s user agent-net -- $HOME/.local/bin/agent-net mcp
   ```

7. Add the extension to Pi:

   ```
   mkdir -p ~/.pi/agent/extensions
   scp VM:code/niparis/a2a-ocs/plugins/pi/agent-net.ts ~/.pi/agent/extensions/
   ```

8. Add the guidance text. It is the last section of the VM's files:

   ```
   ssh VM "sed -n '/^## agent-net/,\$p' ~/.claude/CLAUDE.md" >> ~/.claude/CLAUDE.md
   ssh VM "sed -n '/^## agent-net/,\$p' ~/.pi/agent/AGENTS.md" >> ~/.pi/agent/AGENTS.md
   ```

## Part 3: Test across the two machines

1. Start a Pi session on the VM: `pi --model gpt-6-astra`.
2. Start a new Claude Code session on the Mac. Ask it: "Use agent_list, then ask the Pi session on research which model it runs."
3. On either machine, run `agent-net messages`. The request should be `replied` and the answer `delivered`.

## Known limits

- Sessions that were already open before the daemon started can receive messages, but they can send only after a restart.
- The join string contains a fixed IP address. If the VM's LAN IP changes, join the Mac again with a new code, or use the Tailscale IP, which does not change.
- The macOS daemon builds and has its own caller check (`ps` and `LOCAL_PEERPID`), but it has not been run on a real Mac yet.
