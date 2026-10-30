# 0021 - One integrations question, answered once for the network

**Date:** 2026-10-30
**Status:** Built.
**Context:** `agent-net setup` asked five or six separate consent questions, each with four lines of text, and asked them again on every machine. An upgrade runs setup with no terminal, so it recorded "off (default)" everywhere and the owner had to run setup on each machine. The owner found this too long and too repetitive.

Base: [0008](0008-operator-interface-and-worker-build.md) (the owner gate), [0014](0014-delegation-policy.md) (the gateway setting and the TUI form this follows).

## The change

Setup asks one short question for the six integrations: all on, none, or one line per item. `--all` and `--none` answer without a question. The existing per-item flags remain and override one item.

The first machine that answers stores the answers on the gateway in `owner_settings` (key `integrations`, JSON). Setup on any other machine reads them, applies them with no question, and prints `integrations: 6 of 6 on (your network settings)`. A new machine gets them when its daemon starts. A changed answer reaches every machine at its next daemon start, or within a minute.

The owner can change the answers in the TUI: key `I` opens **Integrations (all machines)**, one checkbox per item, space toggles, Enter saves, Esc cancels.

## The owner gate and the transport

The operator methods are `integrations_get` and `integrations_set`. They are served only to the owner's command line or TUI, like `session_policy`: an agent client and a command line inside an agent session's process tree are refused, and no MCP, Pi or OpenCode tool sets them.

The answers live on the gateway because they are the same for every machine. The gateway serves them over HTTPS with the machine's node token (`GET` and `PUT /v1/integrations`), which only the user of that machine can read. The daemon's operator methods are the only thing an agent could reach, and the gate stops that; the HTTP route is used by setup and by the daemon itself.

## Local answers

A per-item flag at setup is a local answer for that machine. It is kept in `~/.config/agent-net/integrations-local.json` and wins over the network answer at every daemon start. `--all` and `--none` are network answers, not local ones. A value the owner set by hand in a harness file is still never overridden; setup reports it and leaves it alone.

Each machine records the answers it last applied in `~/.config/agent-net/integrations-applied.json`, so a daemon start does not rewrite the same files or run the `claude plugin` commands again.

## Turning off

The answer is the wanted state, not only a permission to add. Turning an item off removes what agent-net added: the `crossSessionInbound` key it set, our hook entries, the workers plugin, or the Codex approval line (the Codex MCP server itself stays). Uninstall still removes the integrations of that machine, with the same rules as before.

## Alternatives not taken

- **Keep the per-item questions and only remember them per machine.** Does not answer the repetitive part; a new machine still asks.
- **Store the answers in a file and copy it between machines.** No place to keep one copy that every machine can read and the owner can change from any of them.
- **Let an agent change the answers.** They decide what runs with the owner's consent; no.
- **Apply a change only at the next daemon start.** The owner would not know when a machine catches up. A minute is short and cheap: one small HTTPS call.
