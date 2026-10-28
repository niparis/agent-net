# Spike: Claude Code

Claude Code v2.1.312, Linux. Target: a throwaway interactive session (`claude --name spike-target`, Haiku 5.5, auto mode) in tmux. Sender: `spike/claude` Go binary, a process the target did not start.

## Discovery: works, no hook needed

Claude writes one file per live session at `~/.claude/sessions/<pid>.json`. Sample (this session):

```json
{"pid":673006,"sessionId":"16a63c67-...","cwd":"/home/me/code/niparis/a2a-ocs/...",
 "version":"2.1.312","peerProtocol":1,"peerFeatures":["notify_idle","reply_across_default_dirs","artifact_yield"],
 "kind":"interactive","entrypoint":"cli","messagingSocketPath":"/run/user/1000/cc-socks/673006.sock",
 "name":"a2a-ocs-4f","nameSource":"derived","status":"busy","statusUpdatedAt":1793204033964}
```

- `status` values seen: `idle`, `busy`, `shell`. This gives presence for free.
- Next to it: `<pid>.<sha256(socket path)>.key`, the session's private token. **We do not use it** from the daemon (see rules).
- Liveness: the PID must be alive. Filename PID must equal the PID in the content. OCS also checks `procStart` to catch a reused PID.
- The daemon can watch this directory (inotify / FSEvents) instead of polling.

## Frame format

One JSON line per connection, `\n` at the end, then close:

```json
{"type":"user","msgV":1,"msg_id":"<uuid>","from":"uds:<our reply socket>","priority":"next",
 "message":{"role":"user","content":"<cross-session-message from=\"uds:...\" from-name=\"agent-net-spike\" from-mode=\"prompting\">\nBODY\n</cross-session-message>"}}
```

- The attribute order in the wrapper is fixed: `from`, `from-session`, `hop-chain`, `from-name`, `from-mode`. The receiver re-serializes it and checks for an exact match (according to OCS).
- `from-name` must not contain `"`, `<`, `>` or newlines. The `from` address allows only `A-Za-z0-9%:_/.-`.
- Max line: 1 MiB.
- Unix socket paths have a 108-byte limit. The reply socket lives in `/run/user/<uid>/`.

## Results

| Test | Result |
|---|---|
| Idle target | Woke at once and showed "Message from @agent-net-spike". The model replied with its native `SendMessage` to our `from` address. **Reply arrived 5.4 s after send.** |
| Busy target (status `busy` at send) | Accepted. The model had moved its command to the background and ended its turn, so the message was handled about 4 s later. Correct reply. |
| Reply frame | Same frame format, `from: uds:/run/user/1000/cc-socks/<pid>.sock`, wrapper has `hop-chain="<24 hex>"`. |
| Auth line | Not sent. Delivery still worked (Linux: auth is optional). |
| Receipt | Claude said "the peer hasn't confirmed it read the message yet": it expects a `peer_message_status` receipt. See OCS `src/claude-receipt.ts`. Step 2 work. |

## Session identity for the MCP process: works

MCP servers started by a Claude session get, in their environment:

```
CLAUDE_CODE_SESSION_ID=16a63c67-...
CLAUDE_CODE_MESSAGING_SOCKET=/run/user/1000/cc-socks/673006.sock
```

The daemon checks this against the MCP process's parent PID and `~/.claude/sessions/<ppid>.json`. A model cannot fake its identity this way.

## Design consequences

1. **Claude replies need no MCP tool.** The receiving Claude answers with its own `SendMessage` to the daemon's socket. The daemon binds one reply socket per sender session (or one per daemon plus `msg_id` correlation) and turns that frame into a network reply. `hop-chain` gives loop detection.
2. **Presence and discovery come from `~/.claude/sessions`**, so no Claude hook is needed for registration. A `SessionStart` hook stays optional.
3. Inbound controls still apply. The default delivers to prompting sessions and holds messages for sessions in bypass-permissions mode. **Not tested yet**: a receiver in bypass mode. Expect a hold, with a drop after 5 minutes unless `crossSessionInbound: "accept"` is set.

## Rules

- Never send the session's private token from the daemon. It marks a message as coming from the session's own child process and skips Claude's inbound checks.
- Always set `from-mode="prompting"` truthfully, and `from-name` to the real sender's network name.
- Check the target socket is a real socket owned by our uid, not a symlink, before connecting (from OCS).
