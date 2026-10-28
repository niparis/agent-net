# Spike: Pi

Pi 1.4.0, Linux, model `gpt-6-astra` (ChatGPT login). The default `gpt-5.5` fails with "not supported when using Codex with a ChatGPT account", so pass `--model`. Target: a throwaway session in tmux, loaded with `pi -e spike/pi/agent-net-spike.ts`. Sender: `spike/ext` Go binary.

## How the extension works

- On `session_start`, it binds `/run/user/<uid>/agent-net-pi-<pid>.sock` and writes `~/.pi/agent-net-spike/<pid>.json` with the session ID, session file, cwd and status.
- `agent_start` sets the status to `busy` and `agent_settled` sets it to `idle`.
- Each incoming JSON line goes to `pi.sendMessage({customType: "agent-net", content, display: true, details}, {deliverAs: "followUp", triggerTurn: true})`. This is a custom message, not `sendUserMessage`, so it shows as `[agent-net]` and never as something the user typed.
- The extension registers an `agent_net_reply` tool that writes the reply to the `reply_to` socket.
- Imports of `@earendil-works/pi-coding-agent` and `typebox` resolved without a local `node_modules`. Pi provides them to extensions.

## Results

| Test | Result |
|---|---|
| Idle target | Acknowledged in 2 ms (`idle: true`). A turn started, and the model called `agent_net_reply`. **Reply arrived 6.8 s after send.** |
| Busy target (25 s command in the foreground) | The status file showed `busy`, and the acknowledgement said `idle: false`. The message waited until the command and "DONE" finished, then was answered (reply 31 s after send). The running turn was not disturbed. |

## Design consequences

- Pi is the cleanest harness. Presence, injection, provenance and replies all live in one in-process extension, with no MCP needed for network operations.
- The real extension talks to the local `agent-netd` over one socket instead of exposing its own socket. The daemon owns discovery.
- Ship the extension globally in `~/.pi/agent/extensions/` so every Pi session joins the network without `-e`.
