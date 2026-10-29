# Troubleshooting

## First checks

Run these on the machine that has the problem.

```
agent-net status      # is the daemon up and connected? which sessions does it see?
agent-net list        # which agents does the network know about (--all adds offline ones)?
agent-net messages    # recent messages and their state
agent-net nodes       # machines, their versions and installed harnesses (--wide for paths and models)
agent-net projects    # known projects and where they live
agent-net events --since 1h   # on the gateway machine: what the gateway did in the last hour
```

`status` prints `connected` or `disconnected` next to the gateway URL. If it cannot reach the daemon, the daemon is not running. Check its log:

- Linux: `journalctl --user -u agent-net-daemon -e`
- macOS: `~/Library/Logs/agent-net-daemon.log`

The gateway log has one `key=value` line per message and per delivery step, for example `grep msg=m_0123456789abcdef`. See [reference.md](reference.md#gateway-log-format). On the gateway machine, the gateway log is `journalctl --user -u agent-net-gateway -e` (Linux) or `~/Library/Logs/agent-net-gateway.log` (macOS).

To read a message state: `stored` means the gateway has it, `dispatched` means it was sent to the machine, `delivered` means it went into the session, `replied` means the session answered. `failed` and `expired` mean it was not delivered. A recipient that is offline gets the message when it comes back, for up to 24 hours.

## macOS: "no route to host" when joining

macOS blocks programs that Apple did not sign from reaching your local network until you allow it (System Settings, Privacy and Security, Local Network). A plain `curl` or the agent-net binary can fail with "no route to host" to a LAN address such as `192.168.x.x`, while the same address works from a browser.

Use the invite line that has a Tailscale address (`100.x.x.x`). Tailscale traffic is not affected. `agent-net invite` prints that line next to the LAN line when the gateway machine has a Tailscale address.

## A session cannot send, but it can receive

Sessions that were already open when you installed agent-net can receive messages, because the daemon finds them on its own. They have no agent-net tools yet, because Claude Code and Pi load tools when a session starts. Restart the session, or start a new one. After an upgrade this is not needed for Claude Code sessions started on v0.3.2 or newer (they switch to the new binary by themselves), and Pi needs only `/reload`. See [upgrade.md](upgrade.md#running-sessions).

## `curl ... | sh` of an invite line printed nothing and exited 0

If the download fails, the shell receives an empty script and exits with 0. Look above for a `curl:` error line. Common causes: the gateway is not reachable from this machine, the line was cut when you pasted it (it is one line), or the one-time code expired (10 minutes) or was already used. If the gateway says the link is unknown, expired or already used, run `agent-net invite` again.

The first-machine line (`curl ... niparis.github.io/agent-net/install | sh`) does not have this problem. It runs nothing unless the whole script arrived.

## A joined machine keeps its first gateway URL

A machine saves the address it joined with in `~/.config/agent-net/node.json`. If the gateway's address changes, the daemon keeps trying the old one. To switch:

```
rm ~/.config/agent-net/node.json
```

Then paste a fresh invite line from the gateway. If you can, use the Tailscale line, because that address does not change. If the new address is a name or IP the gateway certificate does not list, restart the gateway with `--san NAME`. The certificate already lists the host name and every interface address of the gateway machine.

## The daemon says it is not connected

- Is the gateway running? On the gateway machine, `systemctl --user status agent-net-gateway` (Linux) or `launchctl print gui/$(id -u)/com.agent-net.gateway` (macOS).
- Can this machine reach port 7420 on the gateway machine? A firewall is the usual cause.
- Does the log say `certificate` or `x509`? Then the address the daemon uses is not in the gateway certificate. See the section above.
- Does the log say `401` or `unauthorized`? Then the gateway no longer knows this machine's token (for example you started a new gateway data directory). Remove `node.json` and join again.

## Services stop when I log out (Linux)

systemd user services stop at logout unless linger is on. Setup prints a hint when it is off. Run it once:

```
sudo loginctl enable-linger $USER
```

## A peer message arrived but the agent did not act on it

This is expected in some cases. Claude Code and Pi check with you before work that is outside the current task or risky, and a message from another agent is never your approval. Answer the agent in its own session. See [security.md](security.md).

## Still stuck

Open an issue on [github.com/niparis/agent-net](https://github.com/niparis/agent-net/issues) with the output of `agent-net version`, `agent-net status`, the relevant log lines and your OS.
