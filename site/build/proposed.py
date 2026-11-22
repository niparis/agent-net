"""Proposed section: split the daemon into a relay core and execution drivers (owner chose 'proposed section', 2026-11-22)."""
import json
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
d = json.load(open(H + 'final.json'))
d['model']['proposed'] = {
 'status': 'Proposed, not built. For discussion.',
 'problem': 'Today the daemon does two jobs at once. It is the machine\'s relay to the gateway, and it also holds built-in code for each agent program (Claude Code, Codex, OpenCode, Pi, agy): finding their sessions, putting messages into them, starting workers with the program and model the picker chose. So a new way of doing the work, such as an OpenRig team, means changing the daemon itself.',
 'idea': 'Split the daemon in two. A relay core that is the same for every engine, and one execution driver per engine behind a single small contract. The gateway keeps deciding which engine and mode do a task; how the engine does it stays inside the machine.',
 'core': ['Link to the gateway', 'Local socket for the TUI, the CTO and agents\' tools', 'Process checks', 'Machine facts: health, quotas, usage, logs', 'Outbox while the gateway is away', 'Settings and shared skills'],
 'drivers': ['Claude Code', 'Codex', 'OpenCode', 'Pi', 'agy', 'OpenRig (external)'],
 'verbs': [
  ['register', 'I am engine X on this machine; I can run these modes and models.'],
  ['start', 'Take a task (brief, repo, base commit, done list) and return an execution ID.'],
  ['deliver', 'Pass a message to a running execution, and say whether it arrived.'],
  ['report', 'Running, waiting for input, or ended; tokens and cost.'],
  ['stop', 'End an execution.'],
  ['hand back', 'The results: a change (branch or PR), an escalation (it becomes an ask), or proof of done (it becomes step evidence).'],
 ],
 'openrig': [
  ['Black box (default)', 'The whole OpenRig team is one execution. A lead starts "openrig team X" with a brief; the driver runs it; the team\'s own conversation stays inside OpenRig. agent-net sees one execution, its state and cost, and its results.'],
  ['Glass box (optional, per engine)', 'The driver also registers team members as addressable endpoints, so agent-net agents can message them. More visibility, more coupling.'],
  ['Outside the binary', 'A driver can be a separate program that connects to the daemon\'s local socket, says it is engine "openrig", and speaks the contract. The Pi and OpenCode plugins already connect this way. Adding an engine then needs no change to agent-net.'],
 ],
 'model': [
  'A first-class Execution entity: engine, mode, who asked, brief, state, results. It would take over today\'s worker start, worker run and the worker kind of session.',
  '"Session" narrows to an endpoint: something you can message, like the CTO chat, a lead, or a glass-box team member.',
  'The picker chooses among the engines each machine registers, not only among agent programs and models.',
 ],
 'path': [
  'Write the execution contract down as a decision: the verbs, the states, the results.',
  'Move one built-in program (OpenCode) onto it, to prove the contract in real use.',
  'Open it to external drivers over the local socket.',
  'Build OpenRig as the first outside engine.',
 ],
 'open': ['What OpenRig exposes: a CLI, an API, or a process to supervise.', 'Whether glass-box members should count as agents in the org view.', 'Which execution modes the group wants first (the four modes discussed).'],
}
json.dump(d, open(H + 'final.json', 'w'))
print('proposed section data added')
