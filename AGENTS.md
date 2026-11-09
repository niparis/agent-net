# AGENTS.md

Notes for coding agents working on this repository. For what the project is, read [README.md](README.md).

## herdr is how the owner reaches sessions

[herdr](https://herdr.dev/tui/docs/install/) is the primary way the owner opens and works in agent sessions. Every change to entering, starting, resuming or opening a session must keep these rules ([decision 0046](docs/decisions/0046-open-sessions-in-herdr.md)):

- **A session in herdr on another machine opens with `herdr --remote <host>`** whenever this machine has herdr, even when the TUI runs in a plain terminal such as Ghostty. herdr's remote client is the only path that carries the owner's clipboard, so image paste depends on it. Never fall back to `ssh` while a local herdr exists. (v0.4.72 to v0.4.90 broke this, and image paste failed.)
- **A TUI that runs in herdr** opens sessions as panes or tabs in that same herdr.
- **A TUI outside herdr, for a session on this machine,** hands over its own terminal; ctrl+] returns. It never opens a herdr pane the owner cannot see.
- **Workers never open a view by themselves.** The owner opens one with Enter.

The tests that hold these rules are in `internal/daemon/enter_herdr_test.go`. Run them for any change to `internal/daemon/enter.go`, `herdr*.go` or the TUI's enter path.

## Build and test

Go 1.27.3 or newer. If `go` is not on your `PATH`, add it first.

```
go build -o agent-net ./cmd/agent-net
go vet ./... && scripts/test.sh -- -timeout 30m
gofmt -l .                  # must print nothing
sh -n install.sh            # syntax check of the installer
bash -n scripts/test.sh
plugins/pi/typecheck.sh     # type-check the Pi extension (needs Pi installed)
plugins/claude/test-agent-net-workers.sh # validate and test Claude hooks (needs npm)
```

If you change guidance text, also run go test ./internal/guidance ./internal/agentfiles (both have golden fixtures; use -update).

CI runs `gofmt`, `go vet`, the daemon and gateway shards (`scripts/test.sh --package daemon|gateway --shard-index I --shard-count 4`), the other Go packages in one job, and `sh -n install.sh`. All must pass. Tests use localhost only. No test may sleep for more than 2 seconds: use a polling helper with a timeout.

## Layout

| Path | What |
|---|---|
| `cmd/agent-net` | the binary and its subcommands |
| `internal/gateway` | gateway: TLS, enrollment, directory, message store (SQLite), install endpoints |
| `internal/daemon` | daemon: gateway link, local socket, caller checks, outbox |
| `internal/daemon/claude` | Claude Code adapter: session discovery, delivery, reply capture |
| `internal/mcpserver` | the stdio MCP server for Claude Code sessions |
| `internal/protocol` | types and limits shared by all parts |
| `internal/setup` | `setup`, `invite`, `uninstall`, service files |
| `internal/guidance` | the guidance text written to agent instruction files |
| `plugins/pi` | the Pi extension (TypeScript, embedded in the binary) |
| `plugins/claude/agent-net-workers` | the Claude Code plugin that shows a session's workers (embedded in the binary) |
| `install.sh` | the one-line installer, published with each release and on GitHub Pages |
| `scripts/dist.sh` | builds the Linux and macOS binaries from source |
| `scripts/diagram.py` | draws the README diagrams in `docs/img` |

## Conventions

- Match the style of the code around you. Keep changes small and focused.
- Tests are required for new behavior and for every bug fix. Add the test that fails first.
- Pure Go only: no cgo, so the four release binaries build from one machine. SQLite is `modernc.org/sqlite`.
- Docs are plain English: short sentences, concrete words, no marketing words. Do not use the em dash character anywhere. Use a plain hyphen.
- Never commit secrets, tokens, real node IDs, join strings or personal host names. Use throwaway values in tests and docs.
- Do not edit generated files by hand. Do not edit `CHANGELOG.md` if one appears: release notes are generated.
- Tests and manual runs must never touch the real user setup. Point them at temporary directories with `AGENT_NET_CONFIG_DIR`, `AGENT_NET_STATE_DIR`, `AGENT_NET_SOCKET`, `AGENT_NET_GATEWAY_DATA` and `AGENT_NET_CLAUDE_SESSIONS_DIR` (see [docs/reference.md](docs/reference.md)), and never send messages to sessions you did not start.
- If you change what setup writes to a machine, update `docs/reference.md` and the uninstall code together.
- If you change a limit or a security check, update [docs/security.md](docs/security.md).

## Where things are written down

- [docs/README.md](docs/README.md) is the index.
- Current docs: `docs/setup.md`, `docs/reference.md`, `docs/security.md`, `docs/troubleshooting.md`.
- Design history (decisions and early plans, not kept up to date): `docs/brief.md`, `docs/review.md`, `docs/decisions/`, `docs/spike/`, `docs/step2-*.md`. Read `docs/decisions/` before you change an architectural choice, and add a new numbered file there when you make one.
- Releasing: [RELEASING.md](RELEASING.md).

## Screen previews

Run `scripts/screens.sh [directory]` to render the golden TUI scenes as ANSI files and PNGs. This uses the pinned Freeze developer tool and does not add it to the binary.
