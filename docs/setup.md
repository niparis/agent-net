# Setup

One machine runs the gateway (the VM `research`). Every machine, including the VM, runs one daemon. You add a machine by pasting one line.

## 1. The gateway host, once

Build the binaries and install this machine's one:

```
scripts/dist.sh
```

This builds Linux and macOS (amd64 and arm64) into `~/.local/share/agent-net/dist`, which the gateway serves, and copies the right one to `~/.local/bin/agent-net`. Make sure `~/.local/bin` is on your `PATH`.

Then:

```
agent-net setup --gateway
```

This installs and starts the gateway (on `0.0.0.0:7420`) and the daemon as services, and joins the gateway host to its own network. The server key is kept in `server-key.pem` in the gateway data dir, so the pin in invite lines stays the same across restarts.

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
join               ok (node n_...)
daemon service     ok
claude code        ok
pi extension       ok
claude guidance    updated
agent guidance     ok
daemon status      connected to https://192.168.1.10:7420 as node n_...
```

Steps that do not apply say `skipped (reason)`. Setup:

- joins the gateway (skipped if this machine already joined the same gateway),
- writes and starts the daemon service (systemd user unit on Linux, launchd agent on macOS),
- registers the MCP server with Claude Code (`claude mcp add -s user agent-net`) if `claude` is found,
- writes the Pi extension to `~/.pi/agent/extensions/agent-net.ts` if `~/.pi` exists (a symlink is left alone),
- adds the guidance text between `<!-- agent-net:begin -->` and `<!-- agent-net:end -->` in `~/.claude/CLAUDE.md` and `~/.pi/agent/AGENTS.md`. Other content is never touched. An older unmarked `## agent-net: working with other agents` section is replaced by the marked block.

## 3. Update a machine

Run `scripts/dist.sh` on the gateway host to build new binaries, run `agent-net setup --gateway` there to restart its services, then run a new invite line on each other machine. On a machine that already joined, the line updates the binary and the integrations, restarts the daemon, and skips the join.

## 4. Check it

Start a Pi session on one machine and a Claude Code session on another. Ask one to use `agent_list`, then message the other. On either machine, `agent-net messages` should show the request as `replied` and the answer as `delivered`.

## Remove a machine

```
agent-net uninstall          # services, Claude MCP entry, Pi extension, guidance blocks
agent-net uninstall --purge  # also ~/.config/agent-net and the daemon state
```

The binary and the gateway data are kept.

## Options and tests

- `agent-net setup --no-service` (or `AGENT_NET_NO_SERVICE=1`, which the install script passes through) writes no service files and starts nothing.
- `agent-net invite --host H --port P --data DIR` overrides the address, port and gateway data dir. By default the host is the IPv4 address of the interface with the default route.
- `agent-net enroll` still prints the raw join string. Use it with `agent-net setup --join '<string>'` when you already have the binary.
- Service logs: `journalctl --user -u agent-net-daemon` on Linux, `~/Library/Logs/agent-net-daemon.log` on macOS.

## Known limits

- A machine keeps the gateway address it joined with (in `node.json`). If the gateway's address changes, delete `~/.config/agent-net/node.json` on that machine and paste a new invite line. Use the Tailscale line if you want an address that does not change.
- The macOS path (launchd agent, `curl --pinnedpubkey` with the system curl) has not been run on a real Mac yet. Linux is tested end to end, including a clean Debian container.
- If the download fails, `curl ... | sh` still exits 0 (the shell gets an empty script). Read the `curl:` error above.
