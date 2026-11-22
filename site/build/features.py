"""Feature list: one table, each feature tagged gateway, daemon or mixed."""
import json
from collections import Counter
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
d = json.load(open(H + 'final.json'))
G, D, X = 'gateway', 'daemon', 'mixed'
AREAS = {
 'Network': 'Which machines belong, and keeping them healthy.',
 'Sessions': 'Which agents are alive, and where.',
 'Communication': 'How agents talk to each other.',
 'Organization': 'Who is who, and what each may do.',
 'Owner': 'What reaches you, and what is decided without you.',
 'Workers': 'Short-lived sessions that do one task.',
 'Delivery': 'How a change ships.',
 'Shared resources': 'What agents and apps share.',
}
F = [
 ('Network', 'Machine membership', X, 'Invites; checks who may join; revokes a machine', 'Holds the machine keys; joins and stays connected'),
 ('Network', 'Install and upgrade', X, 'Serves the install script and binaries', 'Upgrades itself and restarts'),
 ('Network', 'Backups', G, 'Backs up its database before upgrades', ''),
 ('Network', 'Machine health and usage', X, 'Stores and shows them', 'Measures disk, processes, sleep and usage'),
 ('Network', 'Logs and errors', X, 'Stores machine logs and client errors', 'Writes logs; reports the errors screens show'),
 ('Network', 'Screen refresh topics', X, 'Bumps a topic when data a screen shows changes', 'Subscribes to the topics and passes them on to the TUI'),
 ('Sessions', 'Session directory', X, 'Keeps the list for all machines: rename, hide, archive', 'Finds the sessions running on its machine'),
 ('Sessions', 'Agent program adapters', D, '', 'Talks to Claude Code, Codex, OpenCode, Pi and agy, each in its own way'),
 ('Sessions', 'Start, stop and resume a session', X, 'Relays the request to the right machine', 'Starts, stops or resumes the process'),
 ('Sessions', 'Open a session in herdr', X, 'Relays the open request to the machine that holds the session', 'Builds the plan: a pane or tab, or herdr --remote for another machine'),
 ('Sessions', 'Stall and wake', X, 'Notices a stalled or due session', 'Wakes the session in its agent program'),
 ('Sessions', 'The CTO session', X, 'Its settings, title and memory notes', 'Runs it and gives it its operator tools'),
 ('Communication', 'MCP server for sessions', D, '', 'The agent-net tools every session calls'),
 ('Communication', 'Message routing', X, 'Stores, routes to a session or a role, holds while busy, retries', 'Delivers into the agent program and captures the reply'),
 ('Communication', 'Outbox', D, '', 'Keeps messages and hand-backs while the gateway is unreachable'),
 ('Communication', 'Owed replies', G, 'Tracks who still owes an answer', ''),
 ('Communication', 'Nudges and loops', X, 'Builds and queues the reminders to idle leads and members', 'Reports tool calls, so the gateway can tell whether work followed'),
 ('Communication', 'File sending', X, 'Records the transfer', 'Sends and receives the files'),
 ('Organization', 'Org graph', G, 'Teams, repos, agents, who reports to whom', ''),
 ('Organization', 'Charters', G, 'Purpose, rules and refuses, inherited down the tree', ''),
 ('Organization', 'Authority and permissions', X, 'Who may do what; permission proposals', 'Applies an approved permission to the session'),
 ('Organization', 'Settings', X, 'One store for network, team, repo and agent values', 'Receives and applies the values for its machine'),
 ('Owner', 'The Desk and the chief of staff', G, 'Collects and sorts every question for you', ''),
 ('Owner', 'Owner judgments', G, 'Your past decisions and the rules learned from them', ''),
 ('Workers', 'Worker starts', X, 'Records the start; makes sure it never vanishes', 'Makes the worktree, launches the worker, sends the notice'),
 ('Workers', 'Brief check and difficulty', X, 'Checks that a brief does not overlap running work', 'Runs the difficulty judge'),
 ('Workers', 'Model picker', G, 'Picks harness, model and machine by cost per accepted result', ''),
 ('Workers', 'Quotas', X, 'Stores and paces quota use', "Reads each provider's quota"),
 ('Workers', 'Ratings, costs and runs', X, 'Stores runs, ratings and defects; scores models', 'Reports run time, tokens and cost'),
 ('Workers', 'Worker sandbox', X, 'Stores the sandbox setting', 'Enforces it (bubblewrap on Linux, Seatbelt on macOS)'),
 ('Workers', 'Friction reports', X, 'Stores them and their read status (a weekly digest is planned, not built)', 'Collects them at hand-back'),
 ('Delivery', 'Changes and steps', G, 'Accept, review, test, merge, release, verify', ''),
 ('Delivery', 'Merge modules', X, 'Assigns the steps to their owners; stores the delivery settings', 'Starts the merger session when the CTO or the owner asks for one'),
 ('Delivery', 'The bar', G, "Each project's rules (B1, B2...) that every worker brief, accept and review checks a diff against; written by the lead, the owner or the CTO", ''),
 ('Delivery', 'Test slot', D, '', 'Runs one merger test suite at a time per machine, queues the next, and counts it as load so fewer workers start'),
 ('Shared resources', 'Connected apps and sign-in', X, 'Phone pairing, bots, OAuth, sign-in asks', 'Raises a sign-in ask when a provider login expires, and runs the login in a pane'),
 ('Shared resources', 'Shared MCP servers', X, 'The registry; checks access on every call', 'The machine that hosts a server runs it'),
 ('Shared resources', 'Notes (shared agent memory)', G, 'Memory for one agent, one project or every session; the same for every agent program, so it survives restarts, machines and a switch of program', ''),
 ('Shared resources', 'Skills', X, 'The skill archive and the lists per project', 'Builds the skill folder for a session and hands it to the agent program'),
]
LIFE = {'Machine membership': 'machine', 'Session directory': 'session', 'Message routing': 'message',
        'The Desk and the chief of staff': 'ask', 'Worker starts': 'start', 'Ratings, costs and runs': 'run',
        'Changes and steps': 'change, step', 'Connected apps and sign-in': 'app'}
d['model']['features'] = {'areas': AREAS, 'rows': [
    {'area': a, 'name': n, 'where': w, 'g': g, 'd': dd, 'life': LIFE.get(n, '')} for a, n, w, g, dd in F]}
import os
if os.path.exists(H + 'feature_details.json'):
    det = json.load(open(H + 'feature_details.json'))
    for r in d['model']['features']['rows']:
        x = det.get(r['name']) or {}
        r['more'] = x.get('lines', [])
        r['files'] = x.get('files', [])
        r['decisions'] = x.get('decisions', [])
json.dump(d, open(H + 'final.json', 'w'))
print(Counter(w for _, _, w, _, _ in F), len(F))
