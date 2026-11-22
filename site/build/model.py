"""Add the application data model to final.json: domains, entities, the tables that store each, and relations."""
import json

import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
d = json.load(open(H + 'final.json'))

DOMAINS = [
 ('org', 'People and structure', 'Who does the work and how they are organised: agents, repos, teams, the links between them and the charters that guide them.'),
 ('run', 'Machines and sessions', 'Where work runs: the machines in the network and the agent sessions on them.'),
 ('talk', 'Messages', 'How sessions, the owner and bots talk to each other, and the audit trail of it.'),
 ('desk', 'The Desk', 'Questions and decisions that wait for the owner, the chief of staff or the CTO.'),
 ('ship', 'Delivery', 'How a piece of work moves from a branch to a release, step by step.'),
 ('workers', 'Workers and the picker', 'How workers are chosen, what each run cost and how good its result was.'),
 ('memory', 'Learning and memory', 'What the system and the owner remember: notes, judgments, friction and lessons per model.'),
 ('settings', 'Settings', 'How the network is configured, with history and undo.'),
 ('skills', 'Skills and MCP', 'Shared skills and shared MCP servers that agents use.'),
 ('apps', 'Apps and sign-in', 'Phones, bots and other clients that connect from outside, and how they sign in.'),
]

# id, domain, name, what it is, tables, relations [(verb, target id)]
E = [
 ('agent', 'org', 'Agent', 'A lasting identity in the org tree, such as a repo lead, the chief of staff or a bot. Sessions come and go; the agent stays.', ['agents'], [('belongs to', 'team'), ('leads', 'repo'), ('pins a lead', 'session'), ('is guided by', 'charter')]),
 ('repo', 'org', 'Repo', 'A project as the org tree sees it: one lead agent, one team, one merger, its quality bar. A project folder is where a machine keeps its checkout.', ['repos', 'projects'], [('is led by', 'agent'), ('belongs to', 'team'), ('has', 'bar')]),
 ('team', 'org', 'Team', 'A group of agents and repos with a lead, a purpose and an authority mode.', ['teams'], [('is led by', 'agent')]),
 ('link', 'org', 'Link', 'A typed relation between two nodes: depends, deploys, or who owns a delivery step. Who reports to whom is computed from leads, not stored.', ['graph_edges', 'graph_history'], [('connects', 'agent'), ('connects', 'repo')]),
 ('charter', 'org', 'Charter', 'The rules and text that guide a node, at global, team, repo or agent level. A session can draft one from a repo\'s AGENTS.md for the owner to accept.', ['charter_values', 'charter_drafts'], [('applies to', 'agent'), ('applies to', 'repo'), ('applies to', 'team')]),
 ('scope', 'org', 'Permission', 'What a role may do without asking, and the proposals to change that.', ['node_scopes', 'permission_proposals'], [('is held by', 'agent')]),
 ('outcome', 'org', 'Request outcome', 'How a request between two nodes ended: accepted, rejected, escalated or redirected.', ['graph_outcomes'], [('closes', 'message')]),
 ('loop', 'org', 'Standing loop', 'A node that ticks on its own schedule, with its daily count and progress.', ['agent_loops'], [('runs for', 'agent')]),

 ('machine', 'run', 'Machine', 'A computer that runs the agent-net daemon. An invite is a machine row that has not joined yet.', ['nodes'], [('runs', 'session'), ('reports', 'quota')]),
 ('session', 'run', 'Session', 'One running agent: a coding agent, a worker, the CTO, the chief or a bot. It holds what it announced, its idle check and its wake state.', ['sessions', 'session_dues', 'member_blocked_notices'], [('runs on', 'machine'), ('acts as', 'agent'), ('works in', 'repo')]),
 ('transfer', 'run', 'File transfer', 'A file sent from one machine to another, in parts.', ['transfers'], [('goes between', 'machine')]),

 ('message', 'talk', 'Message', 'One message between sessions, the owner and bots, with its delivery history and retries.', ['messages', 'deliveries', 'delivery_retries'], [('is sent by', 'session'), ('is sent to', 'session')]),
 ('event', 'talk', 'Event', 'One line of the audit log: what happened, where and when.', ['events'], [('is about', 'session')]),
 ('nudge', 'talk', 'Nudge', 'An automatic reminder to an agent, and the pauses the owner set on them.', ['nudge_fires', 'nudge_pauses'], [('goes to', 'agent')]),

 ('ask', 'desk', 'Ask', 'Anything that waits for a decision: a question, an approval, a step approval, or a problem for the CTO. Each has one owner: the owner, the chief or the CTO.', ['inbox_items'], [('is raised by', 'session'), ('can be about', 'step'), ('can be about', 'machine')]),
 ('sort', 'desk', 'Sort and log', 'What the chief of staff did with an ask (answer, route, merge, hide), and the Desk log with how to undo it.', ['ask_sorts', 'desk_log'], [('acts on', 'ask')]),
 ('followup', 'desk', 'Follow-up', 'A check that an expected event happened in time: a reply, a change step, a session.', ['follow_ups'], [('watches', 'message')]),

 ('change', 'ship', 'Change', 'A branch of work submitted for delivery, with each revision pushed.', ['delivery_changes', 'delivery_revisions'], [('targets', 'repo'), ('is made by', 'session')]),
 ('step', 'ship', 'Step', 'One delivery step of a change (accept, review, test, merge, release, verify): the open task, and the evidence once someone reports it. Evidence is never changed.', ['delivery_step_tasks', 'delivery_evidence'], [('belongs to', 'change'), ('is done by', 'session')]),
 ('bar', 'ship', 'Bar rule', 'A numbered quality rule of a project that workers and reviewers check against.', ['project_bar_rules'], [('belongs to', 'repo')]),

 ('start', 'workers', 'Worker start', 'A request to start a worker: the brief, the difficulty verdict and every candidate the picker weighed.', ['worker_starts', 'worker_assessments', 'pick_candidates'], [('is asked by', 'session'), ('creates', 'session')]),
 ('run', 'workers', 'Worker run', 'One run of a worker: model, harness, time, tokens and cost.', ['worker_runs'], [('is a run of', 'session'), ('comes from', 'start')]),
 ('rating', 'workers', 'Rating and defect', 'How good a run\'s result was, and defects found in it later.', ['worker_ratings', 'worker_defects'], [('judges', 'run')]),
 ('quota', 'workers', 'Model knowledge', 'What the picker knows about models: prices, recent failures and used-up quota windows.', ['model_prices', 'model_failures', 'quota_blocks'], [('informs', 'start')]),

 ('note', 'memory', 'Note and judgment', 'Lasting text: the owner\'s standing orders and judgments, and the notes agents write. Searchable, with history.', ['standing_orders', 'standing_order_history', 'standing_orders_fts'], [('is written by', 'agent')]),
 ('friction', 'memory', 'Friction report', 'What slowed a worker or agent down, with its read status.', ['friction'], [('is reported by', 'session'), ('can be about', 'change')]),
 ('suggestion', 'memory', 'Learned suggestion', 'A change to a charter or a link that the system learned from traffic, for the owner to accept.', ['learned_suggestions'], [('proposes', 'charter'), ('proposes', 'link')]),
 ('playbook', 'memory', 'Model lesson', 'Findings about a model or harness, and the playbook notes they promote for workers.', ['findings', 'playbook'], [('guides', 'run')]),

 ('setting', 'settings', 'Setting', 'One configured value at network, team, repo or agent level, with full history and undo.', ['setting_values', 'setting_history'], [('is scoped to', 'repo'), ('is scoped to', 'team'), ('is scoped to', 'agent')]),
 ('state', 'settings', 'Gateway state', 'What the gateway remembers about itself: markers, counters and the applied migrations. Not shown to the owner.', ['gateway_state', 'schema_version'], []),

 ('skill', 'skills', 'Skill', 'Every version of every shared skill, which one is in use, which are blocked, where each is on or off, and the undo log.', ['skill_archive', 'skill_archive_blocks', 'skill_node_lists', 'skill_changes'], [('is enabled for', 'repo'), ('is enabled for', 'agent')]),
 ('mcp', 'skills', 'Shared MCP server', 'An MCP server one machine runs and every session can use.', ['mcp_servers'], [('runs on', 'machine')]),

 ('app', 'apps', 'App', 'A connected client: a phone, a bot or another app, or a sign-in client waiting for the owner\'s approval.', ['apps'], [('as a bot, is a', 'session'), ('holds', 'signin')]),
 ('signin', 'apps', 'Sign-in', 'The sign-in flow and its credentials: pending approvals, one-time codes, failed attempts and hashed tokens.', ['oauth_pending', 'oauth_codes', 'oauth_attempts', 'app_tokens'], [('belongs to', 'app')]),
]

names = {t['name'] for t in d['tables']}
used = [t for e in E for t in e[4]]
assert sorted(used) == sorted(names), (set(names) - set(used), set(used) - names, [x for x in used if used.count(x) > 1])
ids = {e[0] for e in E}
for e in E:
    for _, tgt in e[5]:
        assert tgt in ids, (e[0], tgt)

# Core diagram: the entities most of the system hangs on, laid out by hand.
CORE = {
 'team': (130, 70), 'agent': (380, 70), 'repo': (630, 70), 'setting': (870, 70),
 'machine': (130, 230), 'session': (380, 230), 'change': (630, 230), 'step': (870, 230),
 'app': (130, 390), 'message': (380, 390), 'run': (630, 390), 'ask': (870, 390),
}
CORE_EDGES = [
 ('agent', 'team', 'member of'), ('agent', 'repo', 'leads'), ('repo', 'team', 'in'), ('setting', 'repo', 'scoped to'),
 ('session', 'machine', 'runs on'), ('session', 'agent', 'acts as'), ('change', 'repo', 'targets'), ('change', 'session', 'made by'),
 ('step', 'change', 'step of'), ('message', 'session', 'between'), ('ask', 'session', 'raised by'), ('ask', 'step', 'approves'),
 ('run', 'step', ''), ('app', 'session', 'bot is a'),
]
CORE_EDGES = [e for e in CORE_EDGES if e[2]] + [('run', 'session', 'run of')]

d['model'] = {
 'domains': [{'id': a, 'name': b, 'what': c} for a, b, c in DOMAINS],
 'entities': [{'id': a, 'domain': b, 'name': c, 'what': w, 'tables': t, 'relations': [{'verb': v, 'to': x} for v, x in r]} for a, b, c, w, t, r in E],
 'core': {'pos': CORE, 'edges': [{'from': a, 'to': b, 'label': c} for a, b, c in CORE_EDGES]},
}
d['meta']['caveats'] = [
 'Live since 2026-11-22 04:13 UTC: release v0.5.125 shipped all ten table reworks. The gateway database has 62 tables (plus the storage tables of one text index), down from 123 on 2026-11-21.',
 'Row counts are live counts read on 2026-11-22, read only.',
 'For tables a rework changed, writers, readers, screens and retention come from that rework\'s docs at file level. Unchanged tables keep the earlier function-level scan.',
 'The tool-to-table map is the earlier trace with removed tables swapped for the table that took their data. It was not traced again on the new code, and the MCP tool cut (parts 1 to 3) is not in it yet.',
]
d['meta']['live_version'] = 'v0.5.125 (schema 247), 62 tables'
json.dump(d, open(H + 'final.json', 'w'))
print(len(E), 'entities', len(DOMAINS), 'domains', len(CORE_EDGES), 'core edges')
