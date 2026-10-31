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

Sessions that were already open when you installed agent-net can receive messages, because the daemon finds them on its own. They have no agent-net tools yet, because Claude Code and Pi load tools when a session starts. Restart the session, or start a new one. After an upgrade this is not needed for Claude Code sessions started on v0.3.5 or newer (they switch to the new binary by themselves), and Pi needs only `/reload`. See [upgrade.md](upgrade.md#running-sessions).

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

This is expected in some cases. Claude Code and Pi check with you before work that is outside the current task or risky, and a message from another agent is never your approval, unless you turned on peer trust for that session (press `t` on it in `agent-net tui`). Answer the agent in its own session, or turn trust on. See [security.md](security.md).

## A worker does not start

`agent-net start` prints the reason. The usual ones:

- `no project named X is known`: register it with `agent-net projects add <path>` on the machine that has it.
- `X is on several machines`: add `--machine HOST`.
- `does not accept workers`: that machine was set up with `--no-launch`; run `agent-net setup --launch` there.
- `no HARNESS model matches "NAME" on MACHINE; closest: ...`: the loose model name is not on that machine's list for that harness. Name one of the closest models, or an exact id from `agent-net nodes --wide`.
- `the agent-net plugin did not register the worker session`: the OpenCode plugin is not installed on that machine. Run `agent-net setup` there. The daemon log has the worker's output.
- `queued #N on HOST: ...`: the machine has no room (CPU, memory, the `start_gap` between starts, or the cap of 12 workers). The start waits and a notice comes when it begins. `agent-net workers` shows the room of each machine (`room: cpu|mem|cap|gap|measuring`). Cancel with `agent-net stop <queue id>`. To allow more, raise `workers.cpu_target`, lower `workers.mem_reserve`, raise `workers.max` or lower `workers.start_gap` in `node.json`.
- `runs inside an agent session`: `agent-net start`, `send` and `stop` act as you, so they refuse to run from an agent's shell. Run them in your own terminal.

- `is not available from inside an agent session ... If you do not have agent_start`: the agent tried the command in a shell because its own agent-net tools are older than the machine (no `agent_start`). See the next entry.

A worker that never answers may be waiting for a permission that nobody can give. It ends at the idle time with a `timed out` notice.

## An agent has no agent_start, or other tools are missing (old tools)

Each Claude Code or Codex session keeps the `agent-net mcp` process it started with. A session that began before an upgrade can keep the old tool list. Signs: the agent has no `agent_start`, or it tried `agent-net start` in a shell and was refused.

- In the TUI, select the session. The detail pane shows `tools v0.4.5 (old: reconnect agent-net with /mcp)`, or `tools unknown (from before v0.4.7 ...)`.
- Fix: in Claude Code type `/mcp`, pick `agent-net` and reconnect. For Codex, restart Codex and resume the thread. The new tools appear at once.
- A session on v0.3.5 or later normally switches by itself within seconds of the daemon restart. If it did not, check `AGENT_NET_NO_AUTO_UPGRADE` in its environment and the daemon log lines `mcp hello: ... runs agent-net ...`. See [upgrade.md](upgrade.md#running-sessions).
- A newer agent also gets one line at the end of its next agent-net tool result that tells it to ask you for the reconnect. A process from before v0.4.7 cannot show that line.

## Workers ended with "lost while the daemon was not running" after an upgrade

Workers must survive a daemon restart. On Linux that needs `KillMode=process` in `~/.config/systemd/user/agent-net-daemon.service`; without it systemd kills the whole service group on restart. Check with `systemctl --user cat agent-net-daemon.service`. If the line is missing, run `agent-net setup`. The first restart after an automatic upgrade from an old version can still lose workers; after that they survive. On macOS the plist needs `AbandonProcessGroup` (also written by `setup`). `agent-net uninstall` stops the workers on purpose.

## A Codex thread is not listed, or cannot send

- Not listed: only threads that the shared Codex app server has loaded are listed, which means a window has the thread open (or closed it a short time ago). Check that the server runs: `codex app-server daemon version`. Look in the daemon log for `codex: connected to the shared app server`. If your Codex home is not `~/.codex`, the daemon service needs `CODEX_HOME` too.
- Listed, but the agent has no agent-net tools: the thread started before `agent-net setup` wrote the server to `~/.codex/config.toml`. Start a new thread. If the tools exist but every call fails with "requires approval", run `agent-net setup --accept-codex-tools`.
- A tool call says "peer check failed": the process that Codex started is not a child of the app server the daemon is connected to, for example a Codex that runs its own embedded server (`--no-daemon`). Only threads of the shared server are supported.

## A peer message is held for review in Claude Code

Claude Code shows: `The sending session's permission mode class doesn't match this session's. Review it below, or set "crossSessionInbound" to "accept".` The message waits until you approve it in that session, and is dropped after a while if you do not.

Claude Code does this when the sender and the receiver run in different permission mode classes (for example prompting and bypass). To deliver such messages without review, run `agent-net setup --accept-peer-messages`. It sets `"crossSessionInbound": "accept"` in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`). Read [security.md](security.md#accepting-peer-messages-in-claude-code) first: it turns off the review for all agents on your network.

Setup does not change a value that is already set. `agent-net setup` prints the current one on the `claude peer messages` line. If it says `hold` or `refuse`, change or remove the key yourself. A `hold` in a managed (organization) setting or in a repository's `.claude/settings.json` overrides your `accept`.

## Skills and MCP management

These messages come from `agent-net tools ...`, the MCP import and native removal tools, and the CTO. A target ends as `ok`, `pending`, `conflict`, `manual` or `failed`. See [reference](reference.md#tools-inventory) and [security](security.md#skills-and-mcp-management).

- **`pending`, or `machine offline; applies when it reconnects`**: the machine is not connected, or has not reported yet. Nothing is wrong. The daemon applies the change when it comes back. Check `agent-net nodes`.
- **`this is the last copy and it is not imported: import it first`** (or `this entry was not imported; import it first`): the plan would remove the only copy of a skill, or the native MCP entry has no federated twin. Import the skill with `tools_import` (the source machine must be online), or import the MCP entry with `mcp_import`, then plan again.
- **`changed since plan`**: the file, skill or inventory changed between the plan and the apply. Nothing was touched. Make a new plan and read it again.
- **`changed since apply`**: you asked for an undo, but the files are no longer what the apply left, usually because someone edited them. Undo never overwrites a later edit. Look at the files, then put them back by hand or fix them and undo again.
- **`committed in the repo: remove it with a commit`**: Git tracks that project file (for example `.mcp.json`). agent-net does not edit tracked files. Remove the entry in a normal commit.
- **`owner must choose an ignore mechanism`**: the project checkout is not a Git repository, so agent-net has no safe place to hide the copy. The target stays `pending` until you choose one.
- **`host NAME offline`** (an MCP call) or `unavailable: host NAME offline` (in a tool description): the machine that runs that MCP server is not connected. The assignment is kept. The tools work again when the host returns.
- **`not assigned to this project`**: the server is scoped to other projects, or is `uninstalled`. Ask the owner to change its scope with `agent-net tools plan`. A session may need `/mcp` to refresh its list.
- **`conflict` with `interrupted operation ...`**: the daemon crashed in the middle of a skill change and the files no longer match its journal record. It leaves them alone and logs `skill journal conflict`. Check the path, then delete the record file in `~/.local/state/agent-net/daemon/skill-journal/` once you know the state is fine.
- **A conflict about a name shown twice or shadowed**: two harness folders would show the same skill, or another source already provides it. Remove or rename one by hand, or choose another target.
- **A skill or MCP server is applied but a running session does not see it**: sessions load skills and native MCP config at start. Reload or restart the session. Federated MCP tools update live in Claude Code and Pi only.

## Still stuck

Open an issue on [github.com/niparis/agent-net](https://github.com/niparis/agent-net/issues) with the output of `agent-net version`, `agent-net status`, the relevant log lines and your OS.

If a worker worktree fails because `git-lfs` is not found, install git-lfs in a
directory on the login shell PATH, then run `agent-net setup` to refresh the
daemon service environment. Restart the daemon to use the updated PATH.


## A quota provider needs attention

If a provider rejects its login, or the Quotas tab says it needs you once, run `agent-net setup --quota` on the machine that reads that provider's quota. Setup shows which providers have a local credential source and which need attention. A found source can still be rejected by the provider. Check that you can sign in through that provider's own app or CLI if it remains rejected.

On macOS, setup offers a Keychain prompt. If you agree, answer the dialog and choose Always Allow if you want future reads without another dialog. The check does not refresh credentials. The Subscriptions panel shows which quota-axi copy made the reading; the daemon keeps using its recorded copy until it stops working. Run setup again after correcting the login. The next quota reading can take up to five minutes.
