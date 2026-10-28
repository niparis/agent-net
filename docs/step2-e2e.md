# Step 2 end-to-end results

Date: 2026-10-28. Linux VM. Real binaries and real sessions, all on this machine: gateway on `127.0.0.1:7420` (HTTPS, own CA), one daemon joined with a one-time code, a throwaway Claude Code session (Haiku 5.5, `--mcp-config` with `agent-net mcp`), and a throwaway Pi session (`gpt-6-astra`, `-e plugins/pi/agent-net.ts`). Credentials and state were in scratch folders (`AGENT_NET_CONFIG_DIR`, `AGENT_NET_STATE_DIR`).

## Results

| Test | Result |
|---|---|
| Join with a wrong CA fingerprint | Refused: `CA fingerprint mismatch`, exit 1, nothing enrolled. |
| Join with the real join string | `joined ... as node n_...`. `node.json` mode 0600. |
| Discovery | `agent-net status` listed every live Claude session on the VM with status, plus `e2e-claude` and `e2e-pi` as soon as they started. |
| **A: Claude -> Pi, Pi replies with `agent_reply`** | Claude's `agent_send` returned `m_5eef...` (dispatched). Pi got it in its running session and called `agent_reply`. The reply showed in the running Claude session as "Message from @e2e-pi". Request state `replied`, reply `delivered`, `hop: 1`. **About 4 s round trip.** |
| **B: Pi -> Claude, Claude replies with native `SendMessage`** | Delivered into the running Claude session. Claude did **not** answer on its own: it asked its user first, because a peer message is not a user instruction. After approval, Claude's `SendMessage` went to the daemon's reply socket `r/<sender>.sock`. It was stored as a `reply` with the right parent, and it showed in Pi with the sender label and reply instructions. |
| **C: gateway down, send, gateway up** | With the gateway stopped, Pi's `agent_send` returned `{"queued": true, "idempotency_key": ...}`. After the restart on the same data dir, the daemon reconnected by itself and sent the queued message, which was stored once and delivered to Claude. All earlier messages and states were still there. |

## Findings

1. **The Claude preview line was useless.** Claude previews the first content line, and that line was our header. The body now comes first, and the agent-net details come after it (fixed in this step). Checked on screen afterwards: `Message from @e2e-pi: Preview check: the schema migration finished, rebase is safe.`
5. **The Pi peer check is weak.** It accepts any caller whose command line contains "pi". It is safe enough for step 2, because the socket only accepts processes running as the same user. Step 3 should match Pi's real executable.
2. **Claude asks its user before answering a peer.** That is Claude Code's own safety behaviour, and it is correct. Autonomous Claude-to-peer answers depend on how the user sets up the session. We must not try to get around it.
3. **Every live Claude session on the machine joins automatically**, by design (brief 1.2, point 1). Nothing is delivered unless it is addressed to that session's exact ID. Whether a session should be able to opt out is an open question for the owner.
4. Pi shows tool results as raw JSON. It works, but it is noisy. Shorter text results are a later polish.
