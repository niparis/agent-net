"""Patch tpl_model.html (output of model_tpl.py) with the newcomer layer: glossary, hover definitions, lifecycle intro."""
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
s = open(H + 'tpl_model.html').read()

def rep(a, b):
    global s
    assert s.count(a) == 1, (a[:80], s.count(a))
    s = s.replace(a, b)

rep('.rel{font-size:13px;color:var(--muted)}',
    '.gloss{display:grid;grid-template-columns:repeat(auto-fill,minmax(340px,1fr));gap:6px 18px;margin:0;font-size:14px}\n'
    '.gloss div{display:flex;gap:8px}\n.gloss dt{font-weight:600;min-width:104px}\n.gloss dd{margin:0;color:var(--muted)}\n'
    '.coredef{min-height:48px;margin-top:10px;padding:10px 14px;border-radius:8px;background:var(--accent-soft);font-size:15px}\n.coredef b{margin-right:6px}\n'
    '.why{margin:0;font-size:14px;border-left:3px solid var(--accent);padding-left:10px}\n'
    '.rel{font-size:13px;color:var(--muted)}')
rep('    <section aria-labelledby="m-h">',
    '    <section aria-labelledby="g-h"><div class="head"><div><h2 id="g-h">Key words</h2><p class="lede">New to agent-net? These words come up everywhere below.</p></div></div><dl class="gloss" id="gloss"></dl></section>\n'
    '    <section aria-labelledby="m-h">')
rep('role="img" aria-label="Core entities and how they relate"></svg></section>',
    'role="img" aria-label="Core entities and how they relate"></svg><div class="coredef" id="coredef" aria-live="polite"><span class="note">Hover over or tap an entity to see what it is. Click it to read more below.</span></div></section>')
rep('<p class="lede">How the entities that change state move through their life, from the code of v0.5.125. Each card lists the states, then what moves an item from one state to the next.</p>',
    '<p class="lede">A lifecycle is the list of states something can be in, and the events that move it from one state to the next. A message, for example, is first stored, then delivered, then replied to.</p>'
    '<p class="lede">agent-net uses lifecycles to answer three everyday questions about any piece of work: <b>where is it now</b>, <b>who has to act next</b>, and <b>what happens if nobody does</b>. The screens you use (the Desk, Sessions, Workers), the reminders sent to agents and the alerts to the CTO all read these states. When something is stuck, its state tells you where.</p>'
    '<p class="lede">How to read a card: the first line says why the lifecycle exists. The diagram starts at the black dot, when the item is created. The normal path runs left to right in green; other outcomes hang below it; dashed arrows go back to an earlier state or loop on the same one. The table under it says what makes each move happen ("new" means the item does not exist yet). The states come from the code of v0.5.125.</p>')
rep("    g.addEventListener('click', () => goEntity(id));",
    "    const tip = el('title', {}, g); tip.textContent = EN[id].name + ': ' + (EN[id].short || EN[id].what);\n"
    "    const showDef = () => { $('#coredef').innerHTML = `<b>${esc(EN[id].name)}</b>${esc(EN[id].short || EN[id].what)}`; };\n"
    "    g.addEventListener('mouseenter', showDef); g.addEventListener('focus', showDef);\n"
    "    g.addEventListener('click', () => { showDef(); goEntity(id); });")
rep('// How the parts talk',
    "// Key words\n$('#gloss').innerHTML = (M.glossary || []).map(([k, v]) => `<div><dt>${esc(k)}</dt><dd>${esc(v)}</dd></div>`).join('');\n// How the parts talk")
rep('<div class="states">${l.states',
    '${l.why ? `<p class="why">${esc(l.why)}</p>` : \'\'}<div class="states">${l.states')
open(H + 'tpl_model.html', 'w').write(s)
print('tpl ok')
