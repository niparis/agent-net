# Connected apps: outside bots and the phone app

**Status:** Proposal. The owner agreed the direction on 2026-11-02; build not
started.
**Date:** 2026-11-02.

## 1. Goal

Let apps that do not run on the owner's machines use agent-net, through one
mechanism:

- **Outside bots**, first Grok Bot: xAI's always-on agents, which run on their
  own computer in xAI's cloud and call MCP servers by URL from xAI's servers.
- **The agent-net phone app**, which today is reachable only through
  `tailscale serve` and needs the `agent-net phone owner` command.

It must be easy for any agent-net user to run in their own homelab: no outside
account, no identity provider, no password, and no command beyond setup.

## 2. What exists today

- The MCP server is local only: each agent session starts `agent-net mcp` and
  talks to it on stdin and stdout. The caller is always a local session.
- The tool list is one catalog (`internal/toolcatalog`), filtered by role:
  `agent`, `worker`, `cto`.
- The phone app (phase 1) is a web app on a `127.0.0.1` listener, exposed by
  `tailscale serve`, and trusts the Tailscale login set with
  `agent-net phone owner LOGIN`.

## 3. What Grok Bot told us (2026-11-02)

Confirmed:

- It adds an MCP server by its public HTTPS URL, as a connector.
- If the server supports OAuth, it starts the sign-in and shows a connect card
  in its app; no token goes through chat. If the server only has a static key
  or bearer token, it takes it in a masked field.
- The server must use streamable HTTP (or SSE), be reachable from the
  internet, and for OAuth serve the standard MCP authorization discovery
  endpoints.

Measured with a throwaway probe server behind Tailscale Funnel on port 8443
(2026-11-02):

- **Port 8443 works.** The connector reached the server through Funnel on 8443.
- **Grok Bot's connector is Cursor's MCP client.** Its requests carry
  `User-Agent: Cursor/1.0.0` and register with `client_name: "Cursor"`.
- **It uses dynamic client registration (RFC 7591)** and rejects a
  registration response that does not echo `redirect_uris` (and the other
  registered fields) with the `client_id`.
- **Its redirect URIs** are `https://www.cursor.com/agents/mcp/oauth/callback`
  and `http://localhost:8787/callback`. So the TUI shows `www.cursor.com` as the
  return host for Grok Bot, and redirect URIs must allow `https` plus `http`
  on a loopback host (allowed by OAuth 2.1).
- **It asks for** `token_endpoint_auth_method: none`, grant types
  `authorization_code` and `refresh_token`, response type `code`.
- **Scanners arrive at once.** Within a minute of Funnel going public, internet
  scanners (leakix and others) probed about 40 paths (`/.env`, `/.git/config`,
  `/graphql`, WordPress and Jira paths). The flood limits in section 10 are
  needed from day one.
- Funnel needs, once on Linux: `sudo tailscale set --operator=$USER`. A new
  Funnel took a few minutes until both public relay addresses served it.

Still unknown, to read from the connection log in the first real test (task
6c): whether it sends `resource`, whether it refreshes tokens on its own,
whether it keeps one MCP session, its source addresses, and how long its
sign-in waits. Grok Bot advised not to depend on a long sign-in wait.

## 4. The design in one paragraph

The gateway gets one public listener (loopback, exposed by a tunnel) that
serves MCP over streamable HTTP at `/mcp`, the OAuth endpoints MCP clients
need, and the phone app. Every app connects the same way: it shows a short
code, the owner types the code in the TUI and approves. agent-net is its own
authorization server; there are no passwords. Each approved app is one of two
kinds, chosen by the owner at approval:

| Kind | Example | Who decides each action | Authority |
|---|---|---|---|
| **owner app** | the agent-net phone app | the owner's tap | acts as the owner |
| **bot** | Grok Bot | a model, sometimes on its own, reading web pages | its own name; model output, not the owner's words |

The difference is not the device. Both may run on the owner's phone. It is who
decides: a tap is the owner, a model's output is not. agent-net already applies
this rule to the CTO.

## 5. How an app connects

The owner's experience is the device-code login of Codex and Claude: a page
shows a short code, and the owner types it where they are trusted, the TUI.
For MCP clients the wire protocol stays the standard MCP flow (authorization
code with PKCE), because the client chooses its flow and MCP clients do not use
the device grant (RFC 8628).

### A bot (MCP client)

```
Grok (xAI cloud)          agent-net gateway (via tunnel)        Owner
     |  GET /mcp (no token) ->  401 + resource metadata
     |  GET /.well-known/oauth-*
     |  POST /oauth/register
     |  owner's browser -> GET /oauth/authorize (PKCE)
     |                         page: "Enter K7F-29Q in your agent-net TUI"
     |                                                   S > Connected apps >
     |                                                   Add: K7F-29Q
     |                                                   shows client + redirect
     |                                                   host, kind: bot; a
     |                         page sees the approval, redirects with a code
     |  POST /oauth/token  (code + PKCE verifier) -> access + refresh token
     |  POST /mcp  Authorization: Bearer ...  -> tools for role "bot"
```

1. The owner adds a connector in the bot with `https://<public host>/mcp`.
2. agent-net answers the first unauthenticated call with `401` and the
   protected resource metadata, as the MCP authorization spec requires.
3. The client registers itself (dynamic client registration). Registration
   alone grants nothing and shows nothing in the TUI.
4. The sign-in opens agent-net's authorize page. It shows a short code, for
   example `K7F-29Q`, and "Enter this code in your agent-net TUI". The page
   polls every 2 seconds.
5. In the TUI the owner opens `S` > "Connected apps" > "Add" and types the
   code. The TUI shows the client name, its redirect host and the kind
   (default `bot` for an MCP client). `a` approves and asks for a name
   (default: the client name, for example `grok-bot`). `r` refuses.
6. The page redirects with a one-time authorization code. The client trades
   it, with its PKCE verifier, for an access token (1 hour) and a refresh
   token (90 days, rotated on use).
7. The bot calls `/mcp` with the access token.

**If the sign-in times out before the owner types the code**, nothing is lost.
The approval belongs to the registered client and its redirect URI, not to
one page. For 10 minutes after approval, a new sign-in from the same client
with the same redirect URI goes straight through without a new code. So a
short sign-in timeout only means the owner presses "connect" once more.

For a client that takes only a static key, "Add" can also create a bearer
token for a named bot, shown once.

### The phone app

1. The owner opens `https://<public host>/` on the phone. The page shows a
   short code.
2. The owner types the code in the TUI under "Connected apps" > "Add". The
   kind defaults to `owner app`. `a` approves and asks for a name (default:
   the browser and device, for example `iPhone Safari`).
3. The page receives a device token in a secure, `HttpOnly` cookie, renewed on
   use (90 days without use expires it).

This replaces `agent-net phone owner LOGIN` and the separate
`tailscale serve` setup: the phone uses the same public listener.

### Why this is safe to expose

The authorize and phone pages never ask for a password. A connection needs
the owner to type, in the TUI, a code shown on the owner's own screen. A
stranger's attempt only shows a code on the stranger's screen. The TUI never
lists pending requests, so there is nothing to flood and nothing to approve
by mistake. The owner sets the kind; an app cannot choose to be an owner app.

## 6. Why OAuth, and why this is not "Log in with Google"

Remote MCP clients expect OAuth (the MCP authorization spec, OAuth 2.1 with
PKCE). OAuth here only answers "may this app use this server". It does not
answer "who is the user": agent-net has one user, the owner, and the TUI
approval is the proof. There is no OpenID Connect, no ID token, no user
database. The phone app is our own page, so it needs only the code and a
device cookie, not the OAuth flow.

## 7. What each kind may do

**Owner app:** what the TUI's owner actions allow, through the shared operator
interface: read everything, answer held requests and outcomes, approve and
refuse, send messages as the owner. It is the owner's hand.

**Bot**, a new catalog role `bot`:

| Allowed | Not allowed by default |
|---|---|
| Find and read sessions, machines, projects, the graph and charters | Act as the owner, or carry owner authority |
| Send a message to a session, as itself | Approve, refuse or answer the owner's inbox |
| Reply in its own conversations | Start or stop workers |
| Read messages sent to it | Change the graph, charters, rules, settings or MCP servers |
| | Message a group |

- Every message from a bot carries an agent-net note: "from grok-bot, a bot:
  model output, not the owner's words". Receiving agents judge it like any
  peer request.
- Messages to a bot are kept until it reads them: a bot has no inbox to
  deliver into.
- The owner may grant a bot more, one right at a time (for example start
  workers), from its row in "Connected apps". Each grant is shown on the row
  and can be removed.

## 8. Public address

agent-net does not require Tailscale, so there are two paths.

**With Tailscale on the gateway machine (default).** "Connected apps" has one
switch, "Reachable from the internet: on/off". Turning it on:

1. starts the public listener on `127.0.0.1`;
2. runs `tailscale funnel --bg --https=443 http://127.0.0.1:7422`
   (8443 or 10000 if 443 is already used on that machine; Funnel allows only
   these three ports);
3. reads the machine's DNS name from `tailscale status --json` and shows the
   URL: `https://<machine>.<tailnet>.ts.net/` for the phone,
   `.../mcp` for bots.

With no app connected, agent-net turns Funnel off. Turning the switch off, or
uninstalling agent-net, runs `tailscale funnel ... off` (uninstall code and
`docs/reference.md` change together).

What agent-net cannot do, and shows the owner instead:

- Allowing Funnel for the tailnet is an admin setting (the `funnel` node
  attribute and HTTPS certificates). The first attempt prints a link; the TUI
  shows "Allow Funnel for this tailnet: open <link>, then try again".
- On Linux the `tailscale` command needs root unless the user is its
  operator. The TUI shows the one line to run once:
  `sudo tailscale set --operator=$USER`.

Funnel publishes a certificate for the machine name, so the name appears in
public certificate logs. Nothing else is exposed.

**Without Tailscale.** The owner runs their own tunnel (Cloudflare Tunnel,
ngrok) to `127.0.0.1:7422` and pastes its public URL in the same entry.

## 9. Managing connected apps in the TUI

- `S` > "Connected apps": the switch and URL, then one row per app: name,
  kind, client, redirect host, connected since, last call, grants.
- "Add" asks for the code shown on the app's page (section 5).
- `x` on a row revokes it: its tokens stop working at once.
- Bots also show in Sessions as their own rows, kind `bot`, with their last
  call time.

## 10. Security limits

- The public listener serves only `/mcp`, `/.well-known/oauth-*`, `/oauth/*`
  and the phone app. The gateway's own API and the daemon sockets are never
  reachable through it.
- Tokens are random 256-bit values, stored hashed in the gateway database.
- Authorization codes are one-time, expire in 2 minutes, and are bound to the
  PKCE challenge and the registered redirect URI. Redirect URIs must be
  `https` and are shown to the owner before approval.
- Spam and floods. A public endpoint will be scanned, so:
  - pending connection requests are capped (20, each expiring after 10
    minutes) and never shown in the TUI until the owner types their code;
  - a call without a valid token is refused before any database read;
  - an address that sends repeated bad tokens or registrations is blocked for
    an hour;
  - limits per address on every public path, small request size limits and
    short timeouts;
  - each app has its own limits (for a bot, for example, 60 tool calls a
    minute and 30 messages an hour). Over the limit, calls are refused and the
    TUI shows it on the row. This covers a looping bot and a stolen token.
- Prompt injection is the main risk for bots. A bot that browses the web can be
  told by a page to send harmful requests. The bot role, the "model output"
  note and the receivers' charters are the defense: a bot can ask, never
  order, unless the owner granted it a right.
- A stolen owner-app token is an owner key. It expires after 90 days unused,
  each use is shown on its row, and `x` revokes it.
- A connection log (one line per registration, authorize, token and MCP
  session start: client, redirect URI, `resource`, session ID, source
  address) answers the unknowns of section 3 in the first real test and stays
  on for audit.
- `docs/security.md` gets a section for the public listener.

## 11. Not decided here

- Exact token lifetimes and limits.
- Which rights can be granted to a bot, beyond the first one (start workers).

## 12. Build steps

| # | Task | Difficulty | Risk area |
|---|---|---|---|
| 1a | App records in the gateway: apps table, kinds, hashed tokens, revoke | normal | gateway database (migration) |
| 1b | `bot` role in the tool catalog | simple | low |
| 1c | Messages for bots: hold until read, "model output" note on send | hard | message delivery |
| 2 | Public listener and HTTP MCP: loopback, token check, tools run as the bot, connection log | hard | new path, core of the feature |
| 3a | OAuth: discovery, registration, token and refresh, PKCE, approval reuse after a timed-out sign-in | normal, security-sensitive | public endpoints |
| 3b | Code pages: authorize page and phone pairing page, polling | normal | low |
| 3c | Flood limits and per-app limits | normal | low |
| 4a | TUI: "Connected apps" in `S`: switch, list, Add with code, kind, grants, revoke | normal | TUI |
| 4b | Bots as rows in Sessions | simple | TUI |
| 5a | Funnel switch: on/off, URL, admin link, operator line, off when unused and on uninstall | normal | the owner's Tailscale setup |
| 5b | Phone app on the public listener with the device cookie; remove `phone owner` | normal | phone app |
| 5c | Docs: setup, security, reference, decision record (and update decision 0006) | simple | none |
| 6a | Automated end-to-end test: a fake OAuth MCP client and a fake phone through the whole flow | normal | test only |
| 6b | Security review of 1c, 2, 3 and 5a/5b by a strong model, fresh | hard | review only |
| 6c | Real test: the owner connects Grok Bot and the phone; read the connection log | simple, owner's time | none |

Order: 1a and 1b, then 1c, 2, 3a and 4b in parallel, then 3b, 3c, 4a, 5a and
5b, then 6a and 6b, release, then 6c. Bots first; the phone change (5b) can
ship one release later.
