"""Org view, gateway vs daemon, two-layer core model and lifecycle flows, added to final.json."""
import json
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
d = json.load(open(H + 'final.json'))
M = d['model']

# Org view: who is who. Each box is an agent role; sessions play them.
M['org'] = {
 'nodes': {
  'owner': ['Owner (you)', 550, 50, 'You. You set the direction and decide what only you can decide, mostly by talking to the CTO.'],
  'cto': ['CTO', 280, 165, 'Your main assistant and the operator of the network. It sets up and changes projects, leads, teams, charters, settings and optional modules, and raises what needs you.'],
  'chief': ['Chief of staff', 820, 165, 'Sorts every question on its way to you. It answers from your past decisions when it can and sends the rest to the agent that should answer.'],
  'teamlead': ['Team lead (optional)', 140, 290, 'Optional. Coordinates a team of related repos; their repo leads report to it.'],
  'repolead': ['Repo lead', 440, 290, 'The agent in charge of one repo. It plans with you, starts workers, checks their results and answers for the repo.'],
  'merger': ['Merge module (optional)', 910, 290, 'Optional. How changes get merged: by the lead itself, by a merger session that tests, merges and releases, or by an outside CI/CD (untested).'],
  'members': ['Repo members', 300, 405, 'Other long-lived sessions working on the same repo, for example on another agent program. They report to the repo lead.'],
  'workers': ['Workers', 580, 405, 'Short-lived sessions that do one task in their own branch and sandbox, hand back the result and end.'],
 },
 'edges': [
  ['owner', 'cto', 'works with'], ['chief', 'owner', 'filters asks for'], ['teamlead', 'cto', 'reports to'],
  ['repolead', 'teamlead', 'reports to'], ['repolead', 'cto', 'or, with no team, to', True], ['repolead', 'workers', 'starts'], ['merger', 'repolead', 'can ship changes of', True],
  ['repolead', 'chief', 'asks go through'], ['members', 'repolead', 'report to'],
 ],
 'optional': ['teamlead', 'merger'],
 'options': {'merger': ['None: the lead merges', 'Merge queue: a merger session', 'Outside CI/CD (untested)']},
}

# Gateway vs daemon: what each one is for, does and never does.
M['split'] = {
 'gateway': {
  'title': 'Gateway: the one central place',
  'one': 'One server for the whole network. It holds all the data on this page, decides, and routes. It never runs an agent itself.',
  'does': [
   'Keeps the data model: agents, repos, teams, messages, asks, changes, settings, worker records (the 62 tables).',
   'Routes every message between sessions, wherever they run. It holds, retries or expires a message when needed.',
   'Applies the rules of the network: charters, who may ask whom, permissions, rate limits, and your approvals.',
   'Runs the Desk: the asks waiting for you, the chief of staff\'s sorting, the CTO\'s list of problems.',
   'Runs delivery: the steps each change goes through, who owns each step, and the evidence.',
   'Chooses workers: the picker and the difficulty judge decide which agent program and model do a task, then it asks a machine to start it.',
   'Knows every machine: whether it is online, its health, quotas and usage, as the machines report them.',
   'Serves outside clients: bots (through MCP), the phone and web console, and sign-in.',
  ],
  'never': ['Runs an agent or a worker.', 'Touches a repo or any file on a machine.', 'Talks to an agent program directly. It always goes through that machine\'s daemon.'],
 },
 'daemon': {
  'title': 'Daemon: the local relay on each machine',
  'one': 'One small program on every machine. It connects that machine\'s agent sessions to the gateway and does the local work the gateway asks for.',
  'does': [
   'Finds the agent sessions running on its machine (Claude Code, Codex, OpenCode, Pi, agy) and tells the gateway about them.',
   'Delivers messages into those sessions, and reports back whether they arrived. It carries their replies out.',
   'Starts, watches and stops workers on its machine when the gateway asks: a git worktree, a sandbox, the agent program. It sends their results back.',
   'Serves local clients: the TUI, the CTO and the tools agents use (the MCP server and plugins). It checks which process is calling.',
   'Reports machine facts: health, quotas, usage, logs and running processes.',
   'Receives settings, charters and shared skills from the gateway and applies them on its machine.',
   'Opens sessions for you in herdr.',
   'Keeps outgoing messages while the gateway is unreachable, and reconnects by itself.',
  ],
  'never': ['Decides routing, rules or who does a task.', 'Keeps the network\'s data. It only keeps local working state.', 'Talks to another machine directly. Everything goes through the gateway.'],
 },
 'rule': 'In one line: the gateway decides and records; the daemon carries it out on one machine and reports back.',
 'flows': [
  {'title': 'One agent sends a message to another', 'cols': ['Sender\'s machine (daemon)', 'Gateway', 'Receiver\'s machine (daemon)'], 'rows': [
    [0, 'The agent calls the send tool. Its daemon checks which session is calling and passes the message on.'],
    [1, 'Checks the rules (may this agent ask that one?), stores the message and finds the receiver\'s machine.'],
    [2, 'Puts the message into the receiver\'s agent program, then reports "delivered".'],
    [1, 'Marks the message delivered. If the delivery failed, it retries later.'],
    [2, 'The receiver replies; its daemon passes the reply on.'],
    [1, 'Stores the reply, closes the request, and routes the reply back the same way.'],
  ]},
  {'title': 'A lead starts a worker, and the worker hands back', 'cols': ['Lead\'s machine (daemon)', 'Gateway', 'Worker\'s machine (daemon)'], 'rows': [
    [0, 'The lead calls the worker start tool with a brief. Its daemon passes it on.'],
    [1, 'Records the start, asks the difficulty judge, and the picker chooses the agent program, model and machine.'],
    [2, 'Makes a git worktree, starts the agent program in a sandbox and gives it the task.'],
    [2, 'When the worker finishes, it collects the result and submits the change.'],
    [1, 'Records the change and its first steps (accept, review), and sends the notice to the lead.'],
    [0, 'The daemon delivers the notice to the lead, who reviews and accepts.'],
  ]},
 ],
}

# Core model in two layers: what the gateway coordinates, and what runs on machines.
M['core'] = {
 'width': 1260, 'height': 480,
 'bands': [
  {'y': 18, 'h': 248, 'label': 'Gateway layer: coordination, stored and decided in one place'},
  {'y': 330, 'h': 132, 'label': 'Execution layer: daemons and agent programs on each machine'},
 ],
 'pos': {
  'team': (120, 90), 'agent': (370, 90), 'repo': (630, 90), 'setting': (890, 90), 'app': (1140, 90),
  'message': (120, 210), 'change': (630, 210), 'step': (890, 210), 'ask': (1140, 210),
  'machine': (370, 410), 'session': (630, 410), 'run': (890, 410),
 },
 'edges': [
  {'from': 'agent', 'to': 'team', 'label': 'member of'}, {'from': 'agent', 'to': 'repo', 'label': 'leads'},
  {'from': 'setting', 'to': 'repo', 'label': 'scoped to'}, {'from': 'change', 'to': 'repo', 'label': 'targets'},
  {'from': 'step', 'to': 'change', 'label': 'step of'}, {'from': 'ask', 'to': 'step', 'label': 'approves'},
  {'from': 'session', 'to': 'machine', 'label': 'runs on'}, {'from': 'run', 'to': 'session', 'label': 'run of'},
  {'from': 'session', 'to': 'agent', 'label': 'acts as'}, {'from': 'session', 'to': 'message', 'label': 'sends'},
  {'from': 'session', 'to': 'change', 'label': 'makes'}, {'from': 'session', 'to': 'ask', 'label': 'raises'},
 ],
}

# Lifecycle flows: the normal path left to right, other outcomes below, returns as dashed arrows.
FLOWS = {
 'message': {'main': ['stored', 'dispatched', 'delivered', 'replied'], 'sides': [['held', 'start', 'stored'], ['withdrawn', 'stored'], ['expired', 'stored'], ['failed', 'dispatched'], ['acknowledged', 'delivered']]},
 'ask': {'main': ['open', 'answered'], 'sides': [['replaced', 'open'], ['dismissed', 'open'], ['closed', 'open']], 'loops': [['open', 'chief sorts']]},
 'change': {'main': ['open', 'merged', 'done'], 'sides': [['stopped', 'open']], 'extra': [['merged', 'stopped']], 'loops': [['open', 'new revision']]},
 'step': {'main': ['waiting', 'ready', 'running', 'passed'], 'sides': [['skipped', 'start'], ['failed', 'running'], ['needs owner', 'running']]},
 'start': {'main': ['open', 'starting', 'closed'], 'sides': [['refused', 'start'], ['not started', 'open']], 'back': [['starting', 'open']]},
 'run': {'main': ['running', 'ended']},
 'machine': {'main': ['invited', 'enrolled', 'online', 'offline', 'silent'], 'sides': [['removed', 'online']], 'back': [['offline', 'online']]},
 'session': {'main': ['online', 'offline'], 'sides': [['replaced', 'online', None, 0, 1], ['lost', 'online', None, 1, 1], ['ended', 'online', None, 2, 1], ['archived', 'offline', None, 3, 1]], 'back': [['offline', 'online']]},
 'app': {'main': ['registered', 'pending', 'approved', 'revoked'], 'sides': [['refused', 'pending']]},
}
for l in M['lifecycles']:
    l['flow'] = FLOWS.get(l['entity'])

json.dump(d, open(H + 'final.json', 'w'))
print('layered model added')
