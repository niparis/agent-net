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
- writes the OpenCode plugin to `~/.config/opencode/plugins/agent-net.ts` if `~/.config/opencode` exists (a symlink is left alone; see [opencode.md](opencode.md)),
- adds the guidance text between `<!-- agent-net:begin -->` and `<!-- agent-net:end -->` in `~/.claude/CLAUDE.md`, `~/.pi/agent/AGENTS.md` and `~/.config/opencode/AGENTS.md`. Other content is never touched. An older unmarked `## agent-net: working with other agents` section is replaced by the marked block.

## 3. Update a machine

On the gateway host, run the install line from step 1 again (from source: `scripts/dist.sh`, then `agent-net setup --gateway`). It restarts the services. Then run `agent-net invite` and paste the new line on each other machine. On a machine that already joined, the line updates the binary and the integrations, restarts the daemon, and skips the join. The install line from step 1 does the same there.

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
- Service logs: see [troubleshooting.md](troubleshooting.md#first-checks).

## Known limits

- Problems with a changed gateway address, a failed `curl | sh`, or macOS "no route to host" are covered in [troubleshooting.md](troubleshooting.md).
- The macOS path (launchd agent, `curl --pinnedpubkey` with the system curl) is newer and less tested than Linux. Linux is tested end to end, including a clean Debian container.
