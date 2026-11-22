"""Add the Data model tab to the guide template (tpl_final.html -> tpl_model.html)."""
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
s = open(H + 'tpl_final.html').read()

def rep(a, b):
    global s
    assert s.count(a) == 1, (a[:90], s.count(a))
    s = s.replace(a, b)

rep('<title>Gateway Tables</title>', '<title>Gateway Data Model</title>')
rep('<h1>Gateway Tables</h1>', '<h1>Gateway Data Model</h1>')
rep('.was{font-family:var(--mono);font-size:11px;color:var(--muted)}',
    '.was{font-family:var(--mono);font-size:11px;color:var(--muted)}\n'
    '.tabs{display:flex;gap:4px;border-bottom:1px solid var(--line);margin-top:-12px}\n'
    '.tab{all:unset;cursor:pointer;padding:10px 16px;font-weight:600;font-size:15px;color:var(--muted);border-bottom:3px solid transparent;margin-bottom:-1px}\n'
    '.tab[aria-selected="true"]{color:var(--fg);border-bottom-color:var(--accent)}\n'
    '.tab:focus-visible{outline:2px solid var(--accent);outline-offset:2px}\n'
    '.tabpane{display:grid;gap:40px;min-width:0}\n'
    '.coremap{width:100%;height:auto;background:var(--panel);border:1px solid var(--line);border-radius:8px}\n'
    '.coremap .edge{stroke:var(--line);stroke-width:2}\n'
    '.coremap .elabel{font:12px var(--body);fill:var(--muted)}\n'
    '.coremap .elabelbg{fill:var(--panel)}\n'
    '.coremap .ent rect{fill:var(--accent-soft);stroke:var(--accent);stroke-width:1.5}\n'
    '.coremap .ent text{font:600 15px var(--body);fill:var(--fg)}\n'
    '.coremap .ent{cursor:pointer}\n.coremap .ent:hover rect,.core .ent:focus rect{fill:var(--accent);}\n.coremap .ent:hover text,.core .ent:focus text{fill:#fff}\n'
    '.domain{display:grid;gap:12px}\n.domain h3{font-family:var(--display);font-size:19px;margin:0}\n'
    '.ents{display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:10px}\n'
    '.ent-card{background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:14px;display:grid;gap:8px;align-content:start;min-width:0;scroll-margin-top:16px}\n'
    '.ent-card h4{margin:0;font-size:16px}\n.ent-card p{margin:0;font-size:14px}\n'
    '.ent-card.flash{outline:2px solid var(--accent)}\n'
    '.life{display:grid;grid-template-columns:repeat(auto-fill,minmax(520px,1fr));gap:12px}\n@media (max-width:520px){.life{grid-template-columns:minmax(0,1fr)}}\n'
    '.life-card{background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:14px;display:grid;gap:10px;min-width:0;align-content:start}\n.life-card h4{margin:0;font-size:16px}\n'
    '.states{display:flex;flex-wrap:wrap;gap:4px}\n.state{font-family:var(--mono);font-size:12px;background:var(--accent-soft);color:var(--accent);border-radius:999px;padding:1px 9px}\n'
    '.tr{width:100%;border-collapse:collapse;font-size:13px}\n.tr td{padding:4px 6px;border-top:1px solid var(--line);vertical-align:top}\n.tr td.ft{font-family:var(--mono);font-size:12px;white-space:nowrap;color:var(--muted)}\n'
    '.chan{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:10px;margin-top:14px}\n.chan div{background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:12px;font-size:13.5px;display:grid;gap:4px;align-content:start}\n.chan b{font-size:14px}\n'
    '.rel{font-size:13px;color:var(--muted)}\n.rel a{color:var(--accent);text-decoration:none;font-weight:500}\n.rel a:hover{text-decoration:underline}')

# Tabs and the model pane right after the header; the old page becomes the Tables pane.
rep('  </header>\n',
    '  </header>\n'
    '  <div class="tabs" role="tablist" aria-label="View">\n'
    '    <button class="tab" role="tab" id="t-model" aria-controls="tab-model" aria-selected="true">Data model</button>\n'
    '    <button class="tab" role="tab" id="t-tables" aria-controls="tab-tables" aria-selected="false">Tables (the implementation)</button>\n'
    '  </div>\n'
    '  <div class="tabpane" id="tab-model" role="tabpanel" aria-labelledby="t-model">\n'
    '    <section aria-labelledby="m-h"><div class="head"><div><h2 id="m-h">The core of the model</h2><p class="lede">The entities most of the system hangs on. A session is one running agent on a machine; it acts as a lasting agent, which leads repos and belongs to a team. Sessions send messages, raise asks and make changes; a change moves through delivery steps. Click an entity to read it.</p></div></div>\n'
    '      <svg class="coremap" id="core" viewBox="0 0 1000 460" role="img" aria-label="Core entities and how they relate"></svg></section>\n'
    '    <section aria-labelledby="l-h"><div class="head"><div><h2 id="l-h">Lifecycles</h2><p class="lede">How the entities that change state move through their life, from the code of v0.5.125. Each card lists the states, then what moves an item from one state to the next.</p></div></div><div class="life" id="life"></div></section>\n'
    '    <section aria-labelledby="w-h"><div class="head"><div><h2 id="w-h">How the parts talk</h2><p class="lede">The processes that hold and move this model, and the channel between each pair. The gateway is the only place the data lives; every daemon and screen reaches it through these channels.</p></div></div><svg class="coremap" id="talkmap" viewBox="0 0 1000 420" role="img" aria-label="Processes and the channels between them"></svg><div class="chan" id="chan"></div></section>\n'
    '    <section aria-labelledby="d-h"><div class="head"><div><h2 id="d-h">Every entity, by domain</h2><p class="lede" id="dlede"></p></div></div><div id="domains" style="display:grid;gap:28px"></div></section>\n'
    '  </div>\n'
    '  <div class="tabpane" id="tab-tables" role="tabpanel" aria-labelledby="t-tables" hidden>\n')
rep('</div>\n\n<script>', '</div>\n</div>\n\n<script>')

# Lede and stats speak about the model first.
rep("$('#lede').textContent = `Every table the gateway will keep in SQLite once the reworks you approved on 2026-11-21 ship: what each holds, who writes and reads it, and how everyday actions move through it. Live today: ${DATA.meta.live_version}. ${DATA.meta.fts_note}`;",
    "$('#lede').textContent = `The application data model of the agent-net gateway: the things it knows about, what each one means and how they relate. The tables in the second tab are only how this model is stored in SQLite. ${DATA.meta.fts_note}`;")
rep("  [T.length, 'tables after the rework'], [DATA.meta.live_version.split(', ')[1].split(' ')[0], 'tables live today'], [(DATA.rework || []).length, 'tables removed today'], [totalCols, 'columns'], [fmt(totalRows), 'rows today'],",
    "  [DATA.model.entities.length, 'entities'], [DATA.model.domains.length, 'domains'], [T.length, 'tables'], [totalCols, 'columns'], [fmt(totalRows), 'rows today'],")
rep("<b>Read this first: this page shows a planned state, not the live database</b>",
    "<b>Live on v0.5.125. What this page can and cannot tell you</b>")

s = s.replace('</script>\n\n</body>', r"""
// Data model tab
const M = DATA.model, EN = Object.fromEntries(M.entities.map(e => [e.id, e]));
function setTab(which) {
  const model = which !== 'tables';
  $('#t-model').setAttribute('aria-selected', String(model));
  $('#t-tables').setAttribute('aria-selected', String(!model));
  $('#tab-model').hidden = !model; $('#tab-tables').hidden = model;
  try { localStorage.setItem('gt-tab', model ? 'model' : 'tables'); } catch (_) {}
}
$('#t-model').addEventListener('click', () => setTab('model'));
$('#t-tables').addEventListener('click', () => setTab('tables'));
let firstTab = 'model'; try { firstTab = localStorage.getItem('gt-tab') || 'model'; } catch (_) {}
setTab(firstTab);
function goEntity(id) {
  const el = document.getElementById('ent-' + id); if (!el) return;
  setTab('model'); el.scrollIntoView({behavior: 'smooth', block: 'center'});
  el.classList.add('flash'); setTimeout(() => el.classList.remove('flash'), 1400);
}
function goTable(name) { setTab('tables'); show(name); $('#detail').scrollIntoView({behavior: 'smooth', block: 'start'}); }
// Core diagram
(function () {
  const NS = 'http://www.w3.org/2000/svg', svg = $('#core'), W = 180, Hh = 52, P = M.core.pos;
  const el = (n, a, parent) => { const e = document.createElementNS(NS, n); for (const k in a) e.setAttribute(k, a[k]); (parent || svg).appendChild(e); return e; };
  const edges = el('g', {}), labels = el('g', {}), boxes = el('g', {});
  for (const ed of M.core.edges) {
    const [x1, y1] = P[ed.from], [x2, y2] = P[ed.to];
    el('line', {x1, y1, x2, y2, class: 'edge'}, edges);
    const mx = (x1 + x2) / 2, my = (y1 + y2) / 2, w = ed.label.length * 6.6 + 12;
    el('rect', {x: mx - w / 2, y: my - 10, width: w, height: 20, rx: 4, class: 'elabelbg'}, labels);
    const t = el('text', {x: mx, y: my + 4, 'text-anchor': 'middle', class: 'elabel'}, labels); t.textContent = ed.label;
  }
  for (const id in P) {
    const [x, y] = P[id], g = el('g', {class: 'ent', tabindex: 0, role: 'button', 'aria-label': EN[id].name}, boxes);
    el('rect', {x: x - W / 2, y: y - Hh / 2, width: W, height: Hh, rx: 8}, g);
    const t = el('text', {x, y: y + 5, 'text-anchor': 'middle'}, g); t.textContent = EN[id].name;
    g.addEventListener('click', () => goEntity(id));
    g.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); goEntity(id); } });
  }
})();
// How the parts talk
(function () {
  const NS = 'http://www.w3.org/2000/svg', svg = $('#talkmap'), T = M.talk, W = 184, Hh = 52;
  const el = (n, a, parent) => { const e = document.createElementNS(NS, n); for (const k in a) e.setAttribute(k, a[k]); (parent || svg).appendChild(e); return e; };
  const edges = el('g', {}), labels = el('g', {}), boxes = el('g', {});
  for (const ed of T.edges) {
    const a = T.nodes[ed.from], b = T.nodes[ed.to];
    el('line', {x1: a.x, y1: a.y, x2: b.x, y2: b.y, class: 'edge'}, edges);
    const mx = (a.x + b.x) / 2, my = (a.y + b.y) / 2, w = ed.label.length * 6.6 + 12;
    el('rect', {x: mx - w / 2, y: my - 10, width: w, height: 20, rx: 4, class: 'elabelbg'}, labels);
    const t = el('text', {x: mx, y: my + 4, 'text-anchor': 'middle', class: 'elabel'}, labels); t.textContent = ed.label;
  }
  for (const id in T.nodes) {
    const n = T.nodes[id], g = el('g', {class: 'ent'}, boxes);
    el('rect', {x: n.x - W / 2, y: n.y - Hh / 2, width: W, height: Hh, rx: 8}, g);
    const t = el('text', {x: n.x, y: n.y + 5, 'text-anchor': 'middle'}, g); t.textContent = n.name;
  }
})();
$('#chan').innerHTML = M.talk.channels.map(c => `<div><b>${esc(c.name)}</b><span>${esc(c.how)}</span><span class="note">Sign-in: ${esc(c.auth)}</span></div>`).join('');
// Lifecycles
$('#life').innerHTML = M.lifecycles.map(l => `<article class="life-card"><h4><a href="#ent-${esc(l.entity)}" data-ent="${esc(l.entity)}" style="color:inherit">${esc(l.name)}</a></h4><div class="states">${l.states.map(x => `<span class="state">${esc(x)}</span>`).join('')}</div><table class="tr"><tbody>${l.transitions.map(t => `<tr><td class="ft">${esc(t.from)} → ${esc(t.to)}</td><td>${esc(t.when)}</td></tr>`).join('')}</tbody></table>${l.note ? `<p class="note" style="margin:0">${esc(l.note)}</p>` : ''}</article>`).join('');
$('#life').addEventListener('click', e => { const a = e.target.closest('[data-ent]'); if (a) { e.preventDefault(); goEntity(a.dataset.ent); } });
// Domains and entity cards
$('#dlede').textContent = `${M.entities.length} entities in ${M.domains.length} domains. Each card says what the entity means, how it relates to the others, and which tables store it. Click a table to open it in the Tables tab.`;
$('#domains').innerHTML = M.domains.map(dm => `<div class="domain"><div><h3>${esc(dm.name)}</h3><p class="lede" style="margin-top:2px">${esc(dm.what)}</p></div><div class="ents">${
  M.entities.filter(e => e.domain === dm.id).map(e => `<article class="ent-card" id="ent-${esc(e.id)}"><h4>${esc(e.name)}</h4><p>${esc(e.what)}</p>${
    e.relations.length ? `<div class="rel">${e.relations.map(r => `${esc(r.verb)} <a href="#ent-${esc(r.to)}" data-ent="${esc(r.to)}">${esc(EN[r.to].name)}</a>`).join(' · ')}</div>` : ''
  }<div><span class="label">Stored in</span><div class="chips" style="margin-top:4px">${e.tables.map(n => `<button class="chip${(byName[n] || {}).core ? ' core' : ''}" data-tab-t="${esc(n)}">${esc(n)}</button>`).join('')}</div></div></article>`).join('')
}</div></div>`).join('');
$('#domains').addEventListener('click', e => {
  const a = e.target.closest('[data-ent]'); if (a) { e.preventDefault(); goEntity(a.dataset.ent); return; }
  const c = e.target.closest('[data-tab-t]'); if (c) goTable(c.dataset.tabT);
});
</script>

</body>""", 1)
open(H + 'tpl_model.html', 'w').write(s)
print('ok', len(s))
