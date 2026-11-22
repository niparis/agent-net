"""Add 'how the parts talk' and the key lifecycles to final.json (from the 2026-11-22 read-only analysis of 0fc2b1bc)."""
import json
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
d = json.load(open(H + 'final.json'))

TALK_NODES = {
 'browser': ('Owner browser or phone', 130, 60),
 'bot': ('Bot app', 370, 60),
 'tui': ('TUI and CTO', 620, 60),
 'mcp': ('MCP server or plugin', 870, 60),
 'gateway': ('Gateway', 250, 210),
 'daemon': ('Daemon (each machine)', 620, 210),
 'sessions': ('Agent sessions', 870, 210),
 'others': ('Other machines\' daemons', 250, 360),
 'herdr': ('herdr', 620, 360),
}
TALK_EDGES = [
 ('browser', 'gateway', 'HTTPS, cookie'),
 ('bot', 'gateway', '/mcp, OAuth'),
 ('daemon', 'gateway', 'WSS link'),
 ('others', 'gateway', 'WSS link'),
 ('tui', 'daemon', 'local socket'),
 ('mcp', 'daemon', 'local socket'),
 ('sessions', 'mcp', 'stdio MCP'),
 ('daemon', 'sessions', 'adapters'),
 ('daemon', 'herdr', 'opens views'),
]
CHANNELS = [
 ('Daemon to gateway', 'One WebSocket link, GET /v1/node over TLS. The daemon dials out, pins the gateway\'s own CA and sends its machine token. Requests go both ways on the one socket as JSON frames (type, id, ok, error, data). It reconnects with backoff from 1 s to 30 s; a machine stays online 10 s after a drop.', 'machine token'),
 ('Daemon settings', 'Plain HTTPS JSON on the same listener: /v1/scores, /v1/picker, /v1/worker-floors and about 12 more read and write network settings.', 'machine token'),
 ('Local socket', 'Each daemon serves JSON lines on a Unix socket only its user can open. A client says who it is at hello: the TUI and CLI, an MCP server, the Pi or OpenCode plugin, or the CTO\'s private channel. Owner-only methods are allowed only to the CLI and TUI outside any agent session. An agent\'s calls carry the session the daemon verified, never a name the caller gives.', 'process checks'),
 ('MCP for agents', 'Claude Code, Codex and agy run one agent-net MCP server per session over stdio; it is a thin client of the local socket. Pi and OpenCode load a plugin that speaks the socket directly with the same tool list.', 'parent process check'),
 ('MCP for bots', 'The gateway serves /mcp on its apps listener (loopback, exposed by Tailscale Funnel). Only an app of kind bot may call it, with an OAuth token. It offers 11 tools plus the shared MCP servers granted to that bot, rate limited per app.', 'OAuth token'),
 ('Owner web console', 'The phone and desktop console (Desk, Sessions, Graph, Changes, Messages, Workers) is rendered by the gateway process, which reads through its own machine\'s daemon socket like the TUI. It refreshes by server-sent events and writes only through POST /action: answers, marks and owner messages.', 'pairing cookie'),
 ('Adapters to agent programs', 'Claude Code: session files plus inbox and reply sockets. Codex: JSON-RPC to codex app-server. OpenCode: HTTP and SSE to opencode serve. Pi and OpenCode: their plugins. agy: hooks plus MCP.', 'local user'),
 ('herdr', 'Sessions open in herdr. A session on another machine opens with herdr --remote, never ssh while a local herdr exists (decision 0046).', 'herdr'),
 ('Live screen refresh', 'The gateway samples topic versions every 250 ms and pushes topic.changed to subscribed daemons; the TUI re-reads only the screens whose topics moved. Topics: agents, nodes, workers, graph, inbox, changes, projects, learned, usage.', 'over the link'),
 ('Machine join', 'A new machine fetches the installer with a one-time code, calls POST /v1/enroll to trade the code for its machine token, then opens the link.', 'one-time code'),
]

# entity id, title, states, transitions [(from, to, when)], note
LIFE = [
 ('message', 'Message', ['held', 'stored', 'dispatched', 'delivered', 'replied', 'acknowledged', 'failed', 'withdrawn', 'expired'], [
   ('new', 'stored', 'the gateway saves it'),
   ('new', 'held', 'the owner\'s rule for these two projects is "ask first", or a CTO hold applies; a Desk ask opens'),
   ('held', 'stored', 'the owner (or the CTO hold) lets it go'),
   ('stored', 'dispatched', 'just before it goes to the receiver\'s machine'),
   ('dispatched', 'delivered / failed', 'the receiver\'s daemon reports back; a failure that may pass is retried'),
   ('delivered', 'replied', 'the reply arrives; a progress reply keeps the request open'),
   ('delivered', 'acknowledged', 'the receiver closes it with an ack and no body'),
   ('stored, held, failed', 'withdrawn', 'the sender takes it back before the agent saw it'),
   ('stored, failed', 'expired', 'nobody received it within its lifetime (24 h by default)'),
 ], 'A request to a busy session can wait at the gateway until the session\'s turn ends.'),
 ('ask', 'Ask', ['open', 'answered', 'dismissed', 'closed', 'replaced'], [
   ('new', 'open', 'an agent, the gateway or a step raises it; the route table picks who handles it next: the owner, the chief or the CTO'),
   ('open', 'open', 'the chief sorts it: answer, send on, merge into another, hide, or hand it to the owner; each sort can be undone'),
   ('open', 'answered', 'the owner, chief or CTO answers; an approval also runs what it approves'),
   ('open', 'dismissed', 'answered with "seen" or "dismiss"'),
   ('open', 'closed', 'it no longer needs anyone: withdrawn, the change finished, the session ended, the owner replied, and similar'),
   ('open', 'replaced', 'a newer ask about the same thing takes its place'),
 ], 'The Desk shows the same item as verifying, with CTO, ready, answered, carried out or closed.'),
 ('change', 'Change', ['open', 'merged', 'done', 'stopped'], [
   ('new', 'open', 'work is submitted; revision 1 is recorded and the first steps are queued'),
   ('open', 'open', 'a new head on the same branch adds a revision and restarts the steps'),
   ('open', 'merged', 'the merge step passes with the merged commit'),
   ('merged', 'done', 'the last after-merge step (verify) passes'),
   ('open, merged', 'stopped', 'a step reports close, or the change is given up'),
 ], 'A failed step does not stop a change; it waits for a fix.'),
 ('step', 'Step', ['skipped', 'waiting', 'ready', 'running', 'passed', 'failed', 'needs owner', 'closed'], [
   ('new', 'skipped', 'the pipeline has no owner for it on this project'),
   ('new', 'waiting', 'a step it needs has not passed yet'),
   ('waiting', 'ready', 'everything it needs passed; a task is created for its owner'),
   ('ready', 'running', 'one session claims it'),
   ('running', 'passed / failed / needs owner', 'the session reports, naming the exact commit it checked'),
   ('any', 'closed', 'a close report'),
 ], 'Order: accept, review, test, approve, merge, release, deploy, verify. Evidence is append-only; the newest report on the current revision counts.'),
 ('start', 'Worker start', ['open', 'starting', 'closed', 'not started', 'refused'], [
   ('new', 'open', 'a worker is asked for; the picker and the difficulty judge record their choice'),
   ('new', 'refused', 'the brief check refuses the brief (the third try starts anyway)'),
   ('open', 'starting', 'the task is sent to the machine'),
   ('starting', 'open', 'sending failed; it is tried again'),
   ('open, starting', 'closed', 'the worker runs and has its task; the start is bound to its session'),
   ('open', 'not started', 'no worker ever started, or the owner cancelled a queued start'),
 ], ''),
 ('run', 'Worker run', ['running', 'ended'], [
   ('new', 'running', 'the worker session registers with its run'),
   ('running', 'ended', 'the hand-back reports done, blocked or failed; or the daemon ends it as timed out or stopped'),
 ], 'The run keeps its time, tokens, cost, fixes and struggle counts; the hand-back submits the change when there is work.'),
 ('machine', 'Machine', ['invited', 'enrolled', 'online', 'offline', 'asleep or silent', 'removed'], [
   ('new', 'invited', 'the owner creates an invite: a one-time code, valid 10 minutes'),
   ('invited', 'enrolled', 'the new machine trades the code for its own token'),
   ('enrolled', 'online', 'its daemon opens the link and says hello'),
   ('online', 'offline', 'the link drops for more than 10 s'),
   ('offline', 'asleep or silent', 'it reported going to sleep, or stayed quiet 15 minutes (the CTO is told)'),
   ('online', 'online', 'the owner rotates its token; the next connection confirms it'),
   ('any', 'removed', 'the owner removes it; its token stops working at once'),
 ], ''),
 ('session', 'Session', ['online', 'idle / busy / dormant', 'offline', 'lost', 'ended', 'replaced', 'archived'], [
   ('new', 'online', 'the daemon discovers it'),
   ('online', 'idle / busy / dormant', 'its agent program reports its state'),
   ('online', 'offline', 'it is gone from discovery, or its machine went offline'),
   ('busy', 'lost', 'discovery loses it in the middle of a turn'),
   ('any', 'ended', 'a worker\'s run is over, or an OpenCode session closed'),
   ('any', 'replaced', 'a restart could not keep its ID'),
   ('any', 'archived', 'the owner archives it, or it was offline and unused for 7 days and not pinned'),
 ], 'Online is separate from its state: an online session can be idle, busy, waiting for its user, or limited.'),
 ('app', 'App sign-in', ['registered', 'pending', 'approved', 'refused', 'revoked'], [
   ('new', 'registered', 'a client registers itself (no tokens yet; forgotten after an hour if nobody approves)'),
   ('new', 'pending', 'a sign-in starts and shows a user code (10 minutes)'),
   ('pending', 'approved', 'the owner approves it in the TUI and names it as an owner app or a bot'),
   ('pending', 'refused', 'the owner refuses, or it expires'),
   ('approved', 'approved', 'tokens: a one-use code (2 min), access (1 h), refresh (90 days); reusing a refresh token cancels its family'),
   ('approved', 'revoked', 'the owner revokes it; every token and the phone cookie stop at once'),
 ], 'Phone pairing is the same path for a phone app, with a 90 day cookie.'),
]

d['model']['talk'] = {
 'nodes': {k: {'name': v[0], 'x': v[1], 'y': v[2]} for k, v in TALK_NODES.items()},
 'edges': [{'from': a, 'to': b, 'label': c} for a, b, c in TALK_EDGES],
 'channels': [{'name': a, 'how': b, 'auth': c} for a, b, c in CHANNELS],
}
ids = {e['id'] for e in d['model']['entities']}
for l in LIFE:
    assert l[0] in ids, l[0]
d['model']['lifecycles'] = [{'entity': a, 'name': b, 'states': s, 'transitions': [{'from': f, 'to': t, 'when': w} for f, t, w in tr], 'note': n} for a, b, s, tr, n in LIFE]
json.dump(d, open(H + 'final.json', 'w'))
print(len(CHANNELS), 'channels', len(LIFE), 'lifecycles')
