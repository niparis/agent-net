# AGENTS.md

Notes for coding agents working on this repository. For what the project is, read [README.md](README.md).

## Build and test

Go 1.27 or newer. If `go` is not on your `PATH`, add it first.

```
go build -o agent-net ./cmd/agent-net
go vet ./... && go test -race ./...
gofmt -l .                  # must print nothing
sh -n install.sh            # syntax check of the installer
plugins/pi/typecheck.sh     # type-check the Pi extension (needs Pi installed)
```

CI runs `gofmt`, `go vet`, `go test -race` and `sh -n install.sh`. All must pass. Tests use localhost only. No test may sleep for more than 2 seconds: use a polling helper with a timeout.

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
