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
claude code           ok
claude peer messages  held for review (default)
pi extension          ok
claude guidance       updated
agent guidance        ok
daemon status         connected to https://192.168.1.10:7420 as node n_...
```

Steps that do not apply say `skipped (reason)`. Setup:

- joins the gateway (skipped if this machine already joined the same gateway),
- writes and starts the daemon service (systemd user unit on Linux, launchd agent on macOS),
- registers the MCP server with Claude Code (`claude mcp add -s user agent-net`) if `claude` is found,
- when `claude` is found and Claude Code's `crossSessionInbound` setting is not set, asks once whether Claude Code should accept agent-net messages without holding them (see below),
- writes the Pi extension to `~/.pi/agent/extensions/agent-net.ts` if `~/.pi` exists (a symlink is left alone),
- writes the OpenCode plugin to `~/.config/opencode/plugins/agent-net.ts` if `~/.config/opencode` exists (a symlink is left alone; see [opencode.md](opencode.md)),
- registers the MCP server `agent-net` (`agent-net mcp --codex`) in `~/.codex/config.toml` if Codex is found, after asking once whether Codex may call its tools without asking (see [codex.md](codex.md)),
- adds the guidance text between `<!-- agent-net:begin -->` and `<!-- agent-net:end -->` in `~/.claude/CLAUDE.md`, `~/.pi/agent/AGENTS.md`, `~/.config/opencode/AGENTS.md` and `~/.codex/AGENTS.md`. Other content is never touched. An older unmarked `## agent-net: working with other agents` section is replaced by the marked block.

### The Claude Code hook question

Setup can add a Claude Code hook, `agent-net hint`, that runs after a failed tool call. If another online session may own the failing service, Claude gets one line telling it to ask that session. It asks once:

```
agent-net can add a Claude Code hook to ~/.claude/settings.json. After a failed
tool call it checks the error text on this machine against your other online
sessions (project, host, task, expertise). If one of them may own the failing
service, Claude gets one line telling it to ask that session. No model is called.
Add the hook? [y/N]
```

- The default is No. The answer is read from the terminal, like the question below. With no terminal nothing changes.
- `--claude-hint-hook` adds it without a question, `--no-claude-hint-hook` skips it without a question.
- It adds entries under `hooks` in `~/.claude/settings.json` and keeps everything else. `agent-net uninstall` removes only those entries. See [peer trust and hints](reference.md#peer-trust-and-hints).
- The Pi extension and the OpenCode plugin do this without a setting; they come with the extension and the plugin.

### The Claude Code question

Claude Code holds a message from another session for your review when the sender's permission mode class differs from the receiver's. agent-net messages go through that path, so a message to a session in a different mode waits until you approve it. Setup asks once:

```
Claude Code holds messages from other sessions for review when the sender's
permission mode differs from yours. Accepting delivers messages from any agent on
your network without review, even from sessions in a looser mode. A peer message
still never counts as your approval.
Accept agent-net messages in Claude Code without holding them? [y/N]
```

- The default is No. Only `y` or `yes` turns it on. Yes sets `"crossSessionInbound": "accept"` in the Claude Code user settings file (`~/.claude/settings.json`, or `$CLAUDE_CONFIG_DIR/settings.json`). No other key is changed.
- The answer is read from the terminal (`/dev/tty`), so it works under `curl ... | sh`. With no terminal, setup changes nothing and prints how to turn it on later.
- `--accept-peer-messages` sets it without a prompt. `--no-accept-peer-messages` skips it without a prompt. Both work on `install.sh` and on the invite line, which pass them to setup: `curl -fsSL https://niparis.github.io/agent-net/install | sh -s -- --accept-peer-messages`, or add `-s -- --accept-peer-messages` after the `sh` of an invite line.
- If `crossSessionInbound` already has any value, setup never changes it and does not ask. The summary line shows it: `claude peer messages hold (your setting)`.
- After you answer once (yes or no), setup does not ask again. Run `agent-net setup --accept-peer-messages` later to change your mind.
- If the settings file is not valid JSON, setup leaves it untouched and says so.
- `agent-net uninstall` removes the key, but only if agent-net set it and the value is still `"accept"`.

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
agent-net uninstall          # services, Claude MCP entry, the crossSessionInbound value agent-net set, the Codex config block, Pi extension, guidance blocks
agent-net uninstall --purge  # also ~/.config/agent-net and the daemon state
```

The binary and the gateway data are kept.

## Options and tests

- `agent-net setup --no-service` (or `AGENT_NET_NO_SERVICE=1`, which the install script passes through) writes no service files and starts nothing.
- `agent-net invite --host H --port P --data DIR` overrides the address, port and gateway data dir. By default the host is the IPv4 address of the interface with the default route.
- `agent-net enroll` still prints the raw join string. Use it with `agent-net setup --join '<string>'` when you already have the binary.
- Service logs: see [troubleshooting.md](troubleshooting.md#first-checks).

## Known limits

- Problems with a changed gateway address, a failed `curl | sh`, or macOS "no route to host" are covered in [troubleshooting.md](troubleshooting.md).
- The macOS path (launchd agent, `curl --pinnedpubkey` with the system curl) is newer and less tested than Linux. Linux is tested end to end, including a clean Debian container.
