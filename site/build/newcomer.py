"""Newcomer layer for the data model: simple definitions, why each lifecycle exists, and a glossary."""
import json
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
d = json.load(open(H + 'final.json'))

SHORT = {
 'team': 'A group of agents working on related projects, with one lead.',
 'agent': 'A named role, like "lead of the agent-net repo". It stays the same while the sessions that play it come and go.',
 'repo': 'A project: one code repository that agents work on.',
 'setting': 'A configured value, for the whole network or for one team, repo or agent.',
 'machine': 'A computer in the network that runs agent-net.',
 'session': 'One AI coding agent running right now on a machine: Claude Code, Codex, a worker, the CTO and so on.',
 'change': 'A finished piece of work on a branch, on its way to being merged and released.',
 'step': 'One check a change must pass before it ships: accept, review, test, merge, release, verify.',
 'app': 'A phone, a bot or another outside program that you allowed to connect.',
 'message': 'A note one session sends another: a request, a reply or a heads-up.',
 'ask': 'A question or decision waiting for someone: you, the chief of staff or the CTO.',
 'run': 'One job done by a worker: which model did it, how long it took, what it cost.',
}
for e in d['model']['entities']:
    if e['id'] in SHORT:
        e['short'] = SHORT[e['id']]

WHY = {
 'message': 'So a sender knows whether its request arrived, was answered or got lost, and so the gateway knows when to hold, retry or give up.',
 'ask': 'So nothing that needs a decision is forgotten, and it is always clear who must decide next. The Desk is built from these states.',
 'change': 'So anyone can see where a piece of work stands, from "submitted" to "shipped and checked".',
 'step': 'So every check before a release is explicit, done by someone named, and recorded for good. Nothing ships with a step missing.',
 'start': 'So a request for a worker never vanishes: it either runs, or says why it did not.',
 'run': 'So each worker job leaves a record of how it ended and what it cost. The picker learns from these records.',
 'machine': 'So only machines you invited can join, it is clear which ones can work right now, and a removed one is locked out at once.',
 'session': 'So agent-net knows which agents are alive and able to take a message or a task.',
 'app': 'So only apps you approved can connect, and you can cut one off at once.',
}
for l in d['model']['lifecycles']:
    l['why'] = WHY.get(l['entity'], '')

d['model']['glossary'] = [
 ['Gateway', 'The one central server. It stores all shared data and routes every message.'],
 ['Daemon', "The agent-net program on each machine. It connects that machine's sessions to the gateway and acts for it there."],
 ['Session', 'One running AI coding agent (Claude Code, Codex, OpenCode, Pi, agy). The roles in the Overview tab are played by sessions.'],
 ['MCP tool', 'An action a session can call, like "send a message" or "start a worker".'],
 ['Change', 'A finished piece of work on a branch, on its way to being merged and released.'],
 ['Ask', 'A question or decision waiting for someone: you, the chief of staff or the CTO.'],
]
json.dump(d, open(H + 'final.json', 'w'))
print('newcomer layer added')
