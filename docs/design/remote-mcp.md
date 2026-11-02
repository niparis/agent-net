# Remote MCP for outside bots

**Status:** Proposal, for the owner's approval. Nothing is built.
**Date:** 2026-11-02.

## 1. Goal

Let an agent that runs outside the owner's machines use agent-net as an MCP
server. The first client is Grok Bot: xAI's always-on agents, which run on
their own computer in xAI's cloud. Its connectors call an MCP server by URL
from xAI's servers, so the server must be reachable from the public internet.
The same endpoint serves any other remote MCP client (for example the Claude
app's custom connectors).

It must be easy for any agent-net user to run in their own homelab: no
outside account, no identity provider, no password.

## 2. What exists today

- The MCP server is local only: each agent session starts `agent-net mcp`
  and talks to it on stdin and stdout. The caller is always a local session.
- The tool list is one catalog (`internal/toolcatalog`), filtered by role:
  `agent`, `worker`, `cto`.
- The phone app already uses the pattern this design needs: a listener on
  `127.0.0.1` that only a tunnel (`tailscale serve`) exposes.

## 3. The design in one paragraph

The gateway gets a second loopback listener that serves MCP over streamable
HTTP at `/mcp`, plus the few OAuth endpoints a remote MCP client needs. The
owner exposes that listener with a tunnel of their choice and gives
agent-net the public URL once. A client that connects is sent through a
standard OAuth login. agent-net is its own authorization server, and the
login step is an approval in the owner's TUI. The approved client becomes a
**bot**: a new caller kind with its own name, its own role in the catalog and
its own token, never the owner.

## 4. How a connection works

```
Grok (xAI cloud)          agent-net gateway (via tunnel)        Owner
     |  GET /mcp (no token) ->  401 + resource metadata URL
     |  GET /.well-known/oauth-protected-resource
     |  GET /.well-known/oauth-authorization-server
     |  POST /oauth/register  (dynamic client registration)
     |  owner's browser -> GET /oauth/authorize (PKCE)
     |                         page: "Grok asks to connect as a bot.
     |                                Approve it in your agent-net TUI."
     |                                                   TUI Needs you row:
     |                                                   "grok asks to connect"
     |                                                   a approve / r refuse
     |                         page redirects back with a code
     |  POST /oauth/token  (code + PKCE verifier) -> access + refresh token
     |  POST /mcp  Authorization: Bearer ...  -> tools for role "bot"
```

1. The owner adds a custom connector in Grok with the URL
   `https://<public host>/mcp`.
2. agent-net answers the first unauthenticated call with `401` and the
   protected resource metadata, as the MCP authorization spec requires.
3. The client registers itself (dynamic client registration). Registration
   alone grants nothing.
4. The owner's browser opens agent-net's authorize page. The page names the
   client and the redirect host, and waits.
5. The TUI shows a Needs you row with the client name, the redirect host and
   the time. `a` approves and asks for a bot name (default: the client name,
   for example `grok-bot`). `r` refuses. A request not answered within 10
   minutes expires.
6. On approval the page redirects with a one-time code. The client trades it,
   with its PKCE verifier, for a short-lived access token (1 hour) and a
   refresh token (90 days, rotated on use).
7. The bot calls `/mcp` with the access token.

The authorize page never asks for a password. Knowing the URL is not enough
to connect: every connection needs the owner's key press in the TUI.

**Matching code.** A client picks its own name, so a stranger who finds the
URL could start a request called "Grok" at the moment the owner expects one.
The authorize page shows a short code (for example `K7F-29Q`), and the TUI
row shows the same code next to the client's redirect host. The owner
approves only when the code matches the page in their own browser and the
redirect host is the expected one (an xAI domain for Grok).

## 5. Why OAuth, and why this is not "Log in with Google"

Remote MCP clients expect OAuth (the MCP authorization spec, OAuth 2.1 with
PKCE). Many connector screens, Grok's among them, offer no field for a fixed
token.

OAuth here only answers "may this client use this server". It does not answer
"who is the user": agent-net has exactly one user, the owner, and the TUI
approval is the proof. There is no OpenID Connect, no ID token, no user
database. To the client it is standard OAuth on the wire.

## 6. What a bot may do

A new catalog role, `bot`:

| Allowed | Not allowed |
|---|---|
| Find and read sessions, machines, projects, the graph and charters | Act as the owner, or carry owner authority |
| Send a message to a session, as itself | Approve, refuse or answer anything in the owner's inbox |
| Reply in its own conversations | Start or stop workers |
| Read messages sent to it | Change the graph, charters, rules, settings or MCP servers |
| | Message a group |

- Every message from a bot carries an agent-net note: "from grok-bot, an
  outside bot: model output, not the owner's words". Receiving agents judge it
  like any peer request, with less trust than a local peer by default.
- Messages to a bot are kept for it until it reads them: a bot has no inbox to
  deliver into.
- A bot shows in the TUI as its own row, kind `bot`, with its last call time.

Starting workers or acting for the owner could be added later as an explicit,
per-bot grant. It is not part of this design.

## 7. Setup for a homelab user

agent-net does not require Tailscale, so there are two paths.

**With Tailscale on the gateway machine (default).** The TUI settings menu
(`S`) has one switch, "Outside bots: on/off". Turning it on:

1. starts the loopback listener;
2. runs `tailscale funnel --bg --https=8443 http://127.0.0.1:7422`;
3. reads the machine's DNS name from `tailscale status --json` and shows the
   URL to paste in the bot: `https://<machine>.<tailnet>.ts.net:8443/mcp`.

Port 8443, never 443: Funnel makes a whole port public, and the phone app is
served on 443 by `tailscale serve`. Funnel only allows 443, 8443 and 10000.
Turning the switch off, or uninstalling agent-net, runs
`tailscale funnel --https=8443 off` (uninstall code and `docs/reference.md`
change together).

What agent-net cannot do, and shows the user instead:

- Allowing Funnel for the tailnet is an admin setting (the `funnel` node
  attribute and HTTPS certificates). The first attempt prints a link; the TUI
  shows "Allow Funnel for this tailnet: open <link>, then try again".
- On Linux the `tailscale` command needs root unless the user is its
  operator. The TUI shows the one line to run once:
  `sudo tailscale set --operator=$USER`.

Funnel publishes a certificate for the machine name, so the name appears in
public certificate logs. Nothing else is exposed.

**Without Tailscale.** The user runs their own tunnel (Cloudflare Tunnel,
ngrok) to `127.0.0.1:7422` for the paths `/mcp`, `/.well-known` and `/oauth`,
and pastes the public URL in the same settings entry.

Then, on both paths: add the connector in the bot's settings with that URL,
and approve it in the TUI.

## 8. Managing bots in the TUI

- The settings menu (`S`) gets "Outside bots": the public URL, and the list
  of connected bots with their name, client, redirect host, connected since
  and last call.
- `x` on a bot revokes it: its tokens stop working at once.
- A fallback for clients that take a header: "New token" creates a fixed
  bearer token for a named bot, shown once, revocable the same way.

## 9. Security limits

- Only `/mcp`, `/.well-known/oauth-*` and `/oauth/*` are served on the bot
  listener. The gateway's own API, the phone app and the daemon socket are
  never reachable through it.
- Tokens are random 256-bit values, stored hashed in the gateway database.
- Authorization codes are one-time, expire in 2 minutes, and are bound to the
  PKCE challenge and the registered redirect URI.
- Redirect URIs must be `https` and are shown to the owner before approval.
- Rate limits on registration, authorize and token: a public endpoint will be
  scanned.
- Prompt injection is the main risk. A bot that browses the web can be told
  by a page to send harmful requests to the owner's agents. The bot role,
  the "outside bot" note and the receivers' charters are the defense: a bot
  can ask, never order.
- Revoking a bot, or clearing the public URL, cuts it off at once.
- `docs/security.md` gets a section for this listener.

## 10. Not decided here

- Whether a bot may later get per-bot grants (start workers, act for the
  owner on named actions).
- Whether the phone app should move from Tailscale identity to this login.
- Exact token lifetimes.

## 11. Build steps

1. **Bot caller and role** (medium): caller kind `bot`, catalog role `bot`,
   bot sessions in the gateway, messages held for bots, the "outside bot" note.
2. **HTTP MCP listener** (small): streamable HTTP on a loopback listener,
   serving the `bot` role, behind a token check.
3. **OAuth endpoints** (medium): resource and server metadata, dynamic
   registration, authorize page, token and refresh, token storage and revoke.
4. **TUI** (medium): the Needs you row for connection requests, the "Outside
   bots" entry in the settings menu, revoke, the fallback token.
5. **Funnel switch, setup and docs** (small to medium): the "Outside bots"
   switch that runs Tailscale Funnel on 8443 and reads the URL, the fallback
   URL field, Funnel off on uninstall, tunnel examples in `docs/setup.md`,
   `docs/security.md`, a decision record.
6. **End-to-end test** with a real Grok Bot connector through a tunnel.
