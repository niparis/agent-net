# Setup

One machine runs the gateway. Every machine, including that one, runs one daemon. You add a machine by pasting one line. For a shorter version, see the [README](../README.md#quickstart). For files, variables and flags, see [reference.md](reference.md).

## 1. The first machine (the gateway), once

```
curl -fsSL https://niparis.github.io/agent-net/install | sh
```

The script downloads `agent-net-<os>-<arch>` and `checksums.txt` from the latest GitHub release and checks the SHA-256. It installs the binary to `~/.local/bin/agent-net` (through a temp file and a rename). Then it picks a mode from what it finds on the machine and prints it. If gateway data exists (`~/.local/state/agent-net/gateway/ca.pem`), it updates the gateway. Else, if the machine already joined (`~/.config/agent-net/node.json`), it updates the binary and runs `agent-net setup`, which skips the join and refreshes the daemon service and the integrations. Else it is a fresh machine and becomes the gateway. In gateway mode the script also downloads the binaries for Linux and macOS (amd64 and arm64) into `~/.local/share/agent-net/dist`, which the gateway serves, and then runs `agent-net setup --gateway`. Other machines do not use this line. They join with the line that `agent-net invite` prints. Make sure `~/.local/bin` is on your `PATH`. The script prints the line to add if it is not.

Overrides: `--gateway` forces gateway mode. `--client` installs the binary and runs `agent-net setup` (no join, no gateway). `--binary-only` only installs the binary and prints the next steps. `--version vX.Y.Z` installs that release instead of the latest. To pass an option through the pipe, use `sh -s -- --client`. `AGENT_NET_BASE_URL` replaces the download URL (for tests). `AGENT_NET_NO_SERVICE=1` is passed on to setup.

From source, run `scripts/dist.sh` instead of the install line. It builds the four binaries into the same dist dir, copies the right one to `~/.local/bin/agent-net`, and sets the version from `git describe`. Then run `agent-net setup --gateway` yourself.

`setup --gateway` installs and starts the gateway (on `0.0.0.0:7420`) and the daemon as services, and joins the gateway host to its own network. The server key is kept in `server-key.pem` in the gateway data dir, so the pin in invite lines stays the same across restarts.

On Linux, if setup prints a hint about `loginctl enable-linger`, run that command. It keeps the services running when you are logged out.

## 2. Add a machine

On the gateway host:

```
agent-net invite
```

It prints one line:

```
curl -fsSL -k --pinnedpubkey 'sha256//...' 'https://192.168.1.10:7420/install/ABCD2345' | sh
```

If the host has a Tailscale address, it prints a second, labelled line for it. Paste the line in a shell on the Mac or Linux machine. That is all.

The line works for 10 minutes and for one machine. `-k` is safe here: `--pinnedpubkey` checks the gateway's key, and the install script then checks the gateway CA by its SHA-256.

The script downloads the binary to `~/.local/bin/agent-net` and runs `agent-net setup`. Setup prints one line per step:

```
join                  ok (node n_...)
daemon service        ok (restarted)
integrations:         7 of 7 on (your network settings)
claude code           ok
claude peer messages  accept (set by agent-net)
claude workers mod    on (plugin agent-net-workers@agent-net, user scope)
pi extension          ok
claude guidance       updated
agent guidance        ok
daemon status         connected to https://192.168.1.10:7420 as node n_...
```

Steps that do not apply say `skipped (reason)`. Setup:

- joins the gateway (skipped if this machine already joined the same gateway),
- writes and starts the daemon service (systemd user unit on Linux, launchd agent on macOS),
- registers the MCP server with Claude Code (`claude mcp add -s user agent-net`) if `claude` is found,
- asks one question for the seven integrations, or applies the answers your network already has (see [Integrations](#integrations)),
- registers the MCP server `agent-net` (`agent-net mcp --codex`) in `~/.codex/config.toml` if Codex is found (see [codex.md](codex.md)),
- writes the Pi extension to `~/.pi/agent/extensions/agent-net.ts` if `~/.pi` exists (a symlink to an older agent-net copy is replaced; any other symlink is left alone),
- writes the OpenCode plugin to `~/.config/opencode/plugins/agent-net.ts` if `~/.config/opencode` exists (a symlink to an older agent-net copy is replaced; any other symlink is left alone; see [opencode.md](opencode.md)),
- adds the guidance text between `<!-- agent-net:begin -->` and `<!-- agent-net:end -->` in `~/.claude/CLAUDE.md`, `~/.pi/agent/AGENTS.md`, `~/.config/opencode/AGENTS.md` and `~/.codex/AGENTS.md`. Other content is never touched. An older unmarked `## agent-net: working with other agents` section is replaced by the marked block.

### Integrations

Setup asks one short question for the seven integrations:

```
Connect your agents fully? Recommended:
  - Claude: accept messages from your other agents
  - Codex: call agent-net tools without asking
  - Claude: hint which session to ask after an error
  - Claude: apply the delegation policy you set in the TUI
  - Claude: show "Needs you" in the TUI
  - Claude: show your workers above the prompt
  - Quota: read your subscription quotas with quota-axi
Details: docs/setup.md#integrations
Turn all on? [Y/n/c]
```

Enter or `y` turns all seven on. `n` turns all seven off. `c` asks one line per item, `<item>? [Y/n]`. `--all` and `--none` answer without a question. The per-item flags below override one item and are a local answer for this machine.

The first machine that answers stores the answers on the gateway as your network settings. Setup on any other machine applies them without asking and prints `integrations: 7 of 7 on (your network settings)`. A new or upgraded machine gets them with no step: its daemon applies them at start. A change made in the TUI reaches every machine at its next daemon start, or within a minute. `I` in the TUI opens "Integrations (all machines)": space toggles an item, Enter saves for every machine, Esc cancels.

Each item, what it changes, and how to turn it off:

**Claude: accept messages from your other agents.** Claude Code holds a message from another session for review when the sender's permission mode differs from yours. Accepting delivers messages from any agent on your network without review, even from a session in a looser mode. A peer message still never counts as your approval. It sets `"crossSessionInbound": "accept"` in `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`) and changes no other key. An existing value is never changed. `--accept-peer-messages` and `--no-accept-peer-messages` are the per-item flags. Uninstall removes the key only if agent-net set it and it is still `"accept"`.

**Codex: call agent-net tools without asking.** Codex asks before it calls an MCP tool, and refuses the call when its approval policy is "never". agent-net writes `default_tools_approval_mode = "approve"` for its own server only, in the marked block in `~/.codex/config.toml`, so Codex calls the agent-net tools (list, send, reply, start workers) without asking. Messages from other agents still never count as your approval. `--accept-codex-tools` and `--no-accept-codex-tools` are the per-item flags. Turning it off removes only the approval line and keeps the server. Uninstall removes the block.

**Claude: hint which session to ask after an error.** Setup adds a Claude Code hook, `agent-net hint`, to `~/.claude/settings.json`. After a failed tool call it checks the error text on this machine against your other online sessions (project, host, task, expertise). If one of them may own the failing service, Claude gets one line telling it to ask that session. No model is called. It adds `PostToolUseFailure` and `PostToolUse` with matcher `Bash`, each with a 2 second timeout, and keeps every other hook. `--claude-hint-hook` and `--no-claude-hint-hook` are the per-item flags. Uninstall removes only our entries.

**Claude: apply the delegation policy you set in the TUI.** A second hook, `agent-net policy-hook`, runs on each prompt. It asks agent-net for the delegation policy you set for the session in the TUI (for example: hand code changes to a subagent or a worker), and adds it as context. When you set a policy that names a subagent, agent-net also writes a marked file in `~/.claude/agents/`. No model is called. It adds one `UserPromptSubmit` entry with a 3 second timeout and keeps every other hook. `--claude-policy-hook` and `--no-claude-policy-hook` are the per-item flags. Uninstall removes the hook and the marked subagent files.

**Claude: show "Needs you" in the TUI.** Hooks tell the local daemon when a Claude Code session waits for your permission or input, so it appears under "Needs you" with what it asks. A prompt clears that. They add `PermissionRequest`, `Notification` (idle, agent needs input and elicitation waits), `PreToolUse` and `UserPromptSubmit`, each with a 2 second timeout, and keep every other hook. Only the local daemon is called. No model is called. `--claude-needs-hook` and `--no-claude-needs-hook` are the per-item flags. Uninstall removes only our entries.

**Claude: show your workers above the prompt.** Setup installs the `agent-net-workers` Claude Code plugin from a local marketplace, `~/.local/share/agent-net/claude-plugins`. In every Claude Code session on this machine it shows the workers that session started in a band above the prompt and on the status line, and a line when a worker ends. Setup runs `claude plugin marketplace add` and `claude plugin install agent-net-workers@agent-net -s user`. A later setup run, the daemon at start, and an upgrade rewrite the folder when the files differ from the binary. After an upgrade, sessions that were already open need `/reload-plugins`. `--claude-workers-mod` and `--no-claude-workers-mod` are the per-item flags. If the `claude` command is missing or fails, the step says so and setup continues. Uninstall runs the matching `claude plugin` commands and deletes the folder.

**Quota: read your subscription quotas with quota-axi.** agent-net reads your provider quotas with [quota-axi](https://github.com/kunchenguid/quota-axi). You do not install it by hand.

1. Setup looks for quota-axi in this order: the path in `node.json` `quota_axi`, `PATH`, `~/.local/bin`, and on macOS the nvm folders, `/opt/homebrew/bin`, `/usr/local/bin` and the global npm prefix.
2. If you have none, setup installs the pinned version with npm in `~/.local/share/agent-net/quota-axi`. It needs Node 22.19 or newer. agent-net never installs Node. Without it, setup prints: `quota-axi needs Node 22.19 or newer; install Node and agent-net installs quota-axi for you.`
3. The daemon runs that copy at start and every 5 minutes. It calls `quota-axi --json --max-age 5m --no-credential-refresh`, so quota-axi never refreshes a login, and it never uses `--full`, so no account identity is read.
4. A copy you installed yourself always wins over the private one. The same install runs when the daemon applies a changed network answer.
5. The reading chooses the model for a loose name and refuses a start whose provider is exhausted.
6. `--quota-axi` and `--no-quota-axi` are the per-item flags. Turning the item off, and `agent-net uninstall`, remove the private copy. Without an answer it is on when quota-axi is installed or can be installed.

See [reference.md](reference.md#subscription-quotas) and [security.md](security.md#subscription-quotas).

A per-item flag is remembered in `~/.config/agent-net/integrations-local.json` and wins over the network answer on this machine. `agent-net uninstall` still removes what this machine installed, whether the answer came from the network or from a local flag. An answer cannot turn off a value the owner set by hand: setup reports it and leaves it alone.

## 3. Update a machine

On the gateway host, run:

```
agent-net upgrade
```

It downloads and verifies the latest release, replaces the binary (the old one stays as `agent-net.prev`), restarts the gateway and daemon services, and tells every connected machine to upgrade itself. It prints one line per machine. A machine that is offline is listed as such: run `agent-net upgrade` there when it is back. `agent-net upgrade --check` shows the versions and changes nothing, `agent-net upgrade --rollback` goes back to the previous binary on this machine. See [upgrade.md](upgrade.md).

From source: `scripts/dist.sh`, then `agent-net setup --gateway`. The install line from step 1 also still works to update a machine.

## 4. Check it

Start a Pi session on one machine and a Claude Code session on another. Ask one to use `agent_list`, then message the other. On either machine, `agent-net messages` should show the request as `replied` and the answer as `delivered`.

## Workers on a machine

Machines accept workers (agent sessions that other sessions, or you, start there) by default. To refuse them, run `agent-net setup --no-launch` on that machine; `agent-net setup --launch` accepts them again. `agent-net nodes` shows the choice in the WORKERS column. The OpenCode plugin must be installed there for OpenCode workers (`setup` does it when `~/.config/opencode` exists). See [reference.md](reference.md#workers).

## Remove a machine

```
agent-net uninstall          # services, Claude MCP entry, the crossSessionInbound value agent-net set, the Codex config block, Pi extension, guidance blocks, skill and MCP management backups, journal and manifest
agent-net uninstall --purge  # also ~/.config/agent-net and the daemon state
```

The binary and the gateway data are kept. So are skills the owner assigned to a machine and any native MCP file the daemon edited: undo those first if you want them back. See [what setup changes](reference.md#what-setup-changes-on-your-machine).

## Options and tests

- `agent-net setup --no-service` (or `AGENT_NET_NO_SERVICE=1`, which the install script passes through) writes no service files and starts nothing.
- `agent-net invite --host H --port P --data DIR` overrides the address, port and gateway data dir. By default the host is the IPv4 address of the interface with the default route.
- `agent-net enroll` still prints the raw join string. Use it with `agent-net setup --join '<string>'` when you already have the binary.
- Service logs: see [troubleshooting.md](troubleshooting.md#first-checks).

## Known limits

- Problems with a changed gateway address, a failed `curl | sh`, or macOS "no route to host" are covered in [troubleshooting.md](troubleshooting.md).
- The macOS path (launchd agent, `curl --pinnedpubkey` with the system curl) is newer and less tested than Linux. Linux is tested end to end, including a clean Debian container.
