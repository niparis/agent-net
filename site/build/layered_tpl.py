"""Patch tpl_model.html: org view, gateway vs daemon, two-layer core diagram, lifecycle flow diagrams."""
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
s = open(H + 'tpl_model.html').read()

def rep(a, b):
    global s
    assert s.count(a) == 1, (a[:90], s.count(a))
    s = s.replace(a, b)

# CSS
rep('.rel{font-size:13px;color:var(--muted)}',
    '.split{display:grid;grid-template-columns:repeat(auto-fit,minmax(320px,1fr));gap:14px}\n'
    '.side{background:var(--panel);border:1px solid var(--line);border-radius:10px;padding:16px;display:grid;gap:10px;align-content:start}\n'
    '.side h3{margin:0;font-family:var(--display);font-size:19px}\n.side .one{margin:0;font-size:15px}\n'
    '.side ul{margin:0;padding-left:18px;display:grid;gap:5px;font-size:14px}\n.side .never li{color:var(--muted)}\n'
    '.side.gw{border-top:4px solid var(--accent)}\n.side.dm{border-top:4px solid var(--hot)}\n'
    '.rule{font-family:var(--display);font-size:18px;margin:4px 0 0}\n'
    '.lane{width:100%;border-collapse:collapse;font-size:14px;table-layout:fixed}\n'
    '.lane th{font-size:12px;letter-spacing:.06em;text-transform:uppercase;color:var(--muted);font-weight:500;text-align:left;padding:6px 8px;border-bottom:1px solid var(--line)}\n'
    '.lane td{padding:6px 8px;border-bottom:1px solid var(--line);vertical-align:top}\n'
    '.lane td.on{background:var(--accent-soft);border-radius:6px}\n.lane td.on.c1{background:var(--hot-soft)}\n'
    '.lane .n{font-family:var(--mono);color:var(--muted);font-size:12px;width:28px}\n'
    '@media (max-width:640px){.lane{table-layout:auto;font-size:13px}}\n'
    '.flow{width:100%;height:auto;margin:2px 0}\n.flow .st rect{fill:var(--accent-soft);stroke:var(--accent);stroke-width:1.2}\n'
    '.flow .st.side rect{fill:var(--panel);stroke:var(--muted);stroke-dasharray:0}\n.flow .st text{font:12px var(--mono);fill:var(--fg)}\n'
    '.flow .ar{stroke:var(--muted);stroke-width:1.4;fill:none}\n.flow .ar.back{stroke-dasharray:4 3}\n.flow .dot{fill:var(--fg)}\n'
    '.flow .lp{font:11px var(--body);fill:var(--muted)}\n.flow .ah{fill:var(--muted)}\n'
    '.band{fill:var(--bg);stroke:var(--line);stroke-dasharray:5 4}\n.coremap .ent.opt rect{fill:var(--hot-soft);stroke:var(--hot);stroke-dasharray:6 4}\n.coremap .ent.opt:hover rect{fill:var(--hot)}\n.optchip rect{fill:var(--panel);stroke:var(--hot);stroke-width:1}\n.optchip text{font:12.5px var(--body);fill:var(--fg)}\n.optbox{fill:none;stroke:var(--hot);stroke-dasharray:4 4}\n.optlbl{font:600 11px var(--body);fill:var(--hot);letter-spacing:.04em}\n.legend{display:flex;gap:18px;flex-wrap:wrap;font-size:13px;color:var(--muted);margin-top:8px}\n.legend i{display:inline-block;width:22px;height:12px;border-radius:3px;margin-right:6px;vertical-align:-1px}\n.bandlbl{font:600 13px var(--body);fill:var(--muted);letter-spacing:.04em}\n'
    '.rel{font-size:13px;color:var(--muted)}')

# New sections before the core model
rep('    <section aria-labelledby="m-h">',
    '    <section aria-labelledby="o-h"><div class="head"><div><h2 id="o-h">Who is who</h2><p class="lede">The roles in an agent-net network, and how they relate. Each box is an agent role; the running sessions play these roles. Dashed lines are optional: team leads are optional (a repo without a team reports straight to the CTO), and so is the merge module (orange). This is the intuition; the data model below is how agent-net records it.</p></div></div>'
    '<svg class="coremap" id="orgmap" viewBox="0 0 1100 470" role="img" aria-label="Roles and how they relate"></svg><div class="legend"><span><i style="background:var(--accent-soft);border:1px solid var(--accent)"></i>always there</span><span><i style="background:var(--hot-soft);border:1px dashed var(--hot)"></i>optional role or module</span><span>dashed line: optional link</span></div><div class="ents" id="orgroles" style="margin-top:12px"></div></section>\n'
    '    <section aria-labelledby="s-h"><div class="head"><div><h2 id="s-h">Gateway and daemon: who does what</h2><p class="lede">agent-net has two programs. Everything else on this page belongs to one of them.</p></div></div><div class="split" id="split"></div><p class="rule" id="splitrule"></p><div id="lanes" style="display:grid;gap:22px;margin-top:18px"></div></section>\n'
    '    <section aria-labelledby="m-h">')
rep('<h2 id="m-h">The core of the model</h2><p class="lede">The entities most of the system hangs on. A session is one running agent on a machine; it acts as a lasting agent, which leads repos and belongs to a team. Sessions send messages, raise asks and make changes; a change moves through delivery steps. Click an entity to read it.</p>',
    '<h2 id="m-h">The core of the model</h2><p class="lede">The entities most of the system hangs on, in two layers. The top layer is what the gateway coordinates: who works on what, the messages, the decisions and the work moving to release. The bottom layer is what runs on machines. A session is the bridge: it is one agent program running on a machine, and it acts as an agent of the top layer. Hover over an entity to see what it is; click it to read more.</p>')
rep('<svg class="coremap" id="core" viewBox="0 0 1000 460"', '<svg class="coremap" id="core" viewBox="0 0 1260 480"')

# Core diagram: draw the two bands first
rep("""  const edges = el('g', {}), labels = el('g', {}), boxes = el('g', {});
  for (const ed of M.core.edges) {""",
"""  for (const b of (M.core.bands || [])) {
    el('rect', {x: 6, y: b.y, width: (M.core.width || 1000) - 12, height: b.h, rx: 12, class: 'band'});
    const t = el('text', {x: 22, y: b.y + 20, class: 'bandlbl'}); t.textContent = b.label;
  }
  const edges = el('g', {}), labels = el('g', {}), boxes = el('g', {});
  for (const ed of M.core.edges) {""")

# Lifecycle cards: a flow diagram instead of the bare state pills
rep("""<div class="states">${l.states.map(x => `<span class="state">${esc(x)}</span>`).join('')}</div>""",
    """${l.flow ? flowSVG(l.flow) : `<div class="states">${l.states.map(x => `<span class="state">${esc(x)}</span>`).join('')}</div>`}""")

# JS: org view, split, lanes, flow renderer (placed before the lifecycle cards are rendered)
rep('// Lifecycles\n', r"""// Who is who
(function () {
  const NS = 'http://www.w3.org/2000/svg', svg = $('#orgmap'), O = M.org, W = 200, Hh = 50;
  const el = (n, a, parent) => { const e = document.createElementNS(NS, n); for (const k in a) e.setAttribute(k, a[k]); (parent || svg).appendChild(e); return e; };
  const edges = el('g', {}), labels = el('g', {}), boxes = el('g', {});
  for (const [a, b, label, dashed] of O.edges) {
    const A = O.nodes[a], B = O.nodes[b];
    el('line', {x1: A[1], y1: A[2], x2: B[1], y2: B[2], class: 'edge', 'stroke-dasharray': dashed ? '6 5' : ''}, edges);
    const mx = (A[1] + B[1]) / 2, my = (A[2] + B[2]) / 2, w = label.length * 6.6 + 12;
    el('rect', {x: mx - w / 2, y: my - 10, width: w, height: 20, rx: 4, class: 'elabelbg'}, labels);
    const t = el('text', {x: mx, y: my + 4, 'text-anchor': 'middle', class: 'elabel'}, labels); t.textContent = label;
  }
  for (const id in O.nodes) {
    const [name, x, y, def] = O.nodes[id], g = el('g', {class: 'ent' + ((O.optional || []).includes(id) ? ' opt' : ''), tabindex: 0, role: 'button', 'aria-label': name}, boxes);
    el('rect', {x: x - W / 2, y: y - Hh / 2, width: W, height: Hh, rx: 10}, g);
    const t = el('text', {x, y: y + 5, 'text-anchor': 'middle'}, g); t.textContent = name;
    const tip = el('title', {}, g); tip.textContent = name + ': ' + def;
    const go = () => { const c = document.getElementById('role-' + id); if (c) { c.scrollIntoView({behavior: 'smooth', block: 'center'}); c.classList.add('flash'); setTimeout(() => c.classList.remove('flash'), 1400); } };
    g.addEventListener('click', go); g.addEventListener('keydown', e => { if (e.key === 'Enter') go(); });
  }
  for (const id in (O.options || {})) {
    const [, x, y] = O.nodes[id], opts = O.options[id], cw = 210, ch = 26, top = y + 38;
    el('rect', {x: x - cw / 2 - 8, y: top - 16, width: cw + 16, height: opts.length * (ch + 6) + 22, rx: 8, class: 'optbox'}, boxes);
    const lb = el('text', {x: x - cw / 2, y: top - 4, class: 'optlbl'}, boxes); lb.textContent = 'PICK ONE';
    opts.forEach((o, i) => {
      const g = el('g', {class: 'optchip'}, boxes), yy = top + 4 + i * (ch + 6);
      el('rect', {x: x - cw / 2, y: yy, width: cw, height: ch, rx: 13}, g);
      const t = el('text', {x, y: yy + 17, 'text-anchor': 'middle'}, g); t.textContent = o;
    });
  }
  $('#orgroles').innerHTML = Object.entries(O.nodes).map(([id, [name, , , def]]) => `<article class="ent-card" id="role-${esc(id)}"><h4>${esc(name)}</h4><p>${esc(def)}</p></article>`).join('');
})();
// Gateway and daemon
const SP = M.split;
$('#split').innerHTML = [['gw', SP.gateway], ['dm', SP.daemon]].map(([k, x]) => `<div class="side ${k}"><h3>${esc(x.title)}</h3><p class="one">${esc(x.one)}</p><div><span class="label">It does</span><ul>${x.does.map(i => `<li>${esc(i)}</li>`).join('')}</ul></div><div><span class="label">It never</span><ul class="never">${x.never.map(i => `<li>${esc(i)}</li>`).join('')}</ul></div></div>`).join('');
$('#splitrule').textContent = SP.rule;
$('#lanes').innerHTML = SP.flows.map(f => `<div><h3 style="margin:0 0 8px;font-size:16px">${esc(f.title)}</h3><div class="ledger-wrap"><table class="lane"><thead><tr><th class="n"></th>${f.cols.map(c => `<th>${esc(c)}</th>`).join('')}</tr></thead><tbody>${
  f.rows.map(([c, text], i) => `<tr><td class="n">${i + 1}</td>${f.cols.map((_, j) => j === c ? `<td class="on c${j === 1 ? 0 : 1}">${esc(text)}</td>` : '<td></td>').join('')}</tr>`).join('')
}</tbody></table></div></div>`).join('');
// Lifecycle flow diagrams: normal path left to right, other outcomes below, returns dashed.
let flowN = 0;
function flowSVG(F) {
  const names = [...F.main, ...(F.sides || []).map(x => x[0])], BW = Math.max(104, Math.max(...names.map(n => n.length)) * 7.6 + 18), CW = BW + 20, RH = 78, BH = 30, X0 = BW / 2 + 12, Y0 = 62;
  const pos = {start: [0, 0]}; F.main.forEach((n, i) => pos[n] = [i + 1, 0]);
  const taken = new Set(Object.values(pos).map(p => p.join(',')));
  for (const sd of (F.sides || [])) {
    const [name, from, , c, r] = sd;
    if (c !== undefined && c !== null) { pos[name] = [c, r]; taken.add(c + ',' + r); continue; }
    const fc = pos[from][0];
    let placed = false;
    for (const row of [1, 2]) { for (const dc of [0, 1, -1, 2, -2, 3]) { const cc = fc + dc; if (cc < 0 || taken.has(cc + ',' + row)) continue; pos[name] = [cc, row]; taken.add(cc + ',' + row); placed = true; break; } if (placed) break; }
  }
  const cols = Math.max(...Object.values(pos).map(p => p[0])) + 1, rows = Math.max(...Object.values(pos).map(p => p[1])) + 1;
  const Wd = X0 + (cols - 1) * CW + BW / 2 + 12, Ht = Y0 + (rows - 1) * RH + BH;
  const xy = n => [X0 + pos[n][0] * CW, Y0 + pos[n][1] * RH];
  const id = 'ah' + (flowN++);
  let out = `<svg class="flow" viewBox="0 0 ${Wd} ${Ht}" style="max-width:${Wd}px" role="img" aria-label="State diagram"><defs><marker id="${id}" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto"><path d="M0,0 L10,5 L0,10 z" class="ah"/></marker></defs>`;
  const edge = (a, b, cls) => {
    const [x1, y1] = xy(a), [x2, y2] = xy(b), sa = a === 'start' ? 6 : 0;
    let sx = x1, sy = y1, ex = x2, ey = y2;
    if (y1 === y2) { sx = x1 + (a === 'start' ? 6 : BW / 2); ex = x2 - BW / 2 - 2; }
    else if (y2 > y1) { sy = y1 + (a === 'start' ? 6 : BH / 2); ey = y2 - BH / 2 - 2; }
    else { sy = y1 - BH / 2; ey = y2 + BH / 2 + 2; }
    out += `<line x1="${sx}" y1="${sy}" x2="${ex}" y2="${ey}" class="ar ${cls || ''}" marker-end="url(#${id})"/>`;
  };
  edge('start', F.main[0]);
  F.main.slice(1).forEach((n, i) => edge(F.main[i], n));
  for (const sd of (F.sides || [])) { edge(sd[1], sd[0]); if (sd[2]) edge(sd[0], sd[2], 'back'); }
  for (const [a, b] of (F.extra || [])) edge(a, b);
  for (const [a, b] of (F.back || [])) {
    const [x1, y1] = xy(a), [x2, y2] = xy(b), top = y1 - BH / 2;
    out += `<path d="M${x1},${top} C${x1},${top - 30} ${x2},${top - 30} ${x2},${top - 2}" class="ar back" marker-end="url(#${id})"/>`;
  }
  for (const [n, label] of (F.loops || [])) {
    const [x, y] = xy(n), top = y - BH / 2;
    out += `<path d="M${x - 22},${top} C${x - 30},${top - 34} ${x + 30},${top - 34} ${x + 22},${top - 2}" class="ar back" marker-end="url(#${id})"/><text x="${x}" y="${top - 30}" text-anchor="middle" class="lp">${esc(label)}</text>`;
  }
  out += `<circle cx="${X0}" cy="${Y0}" r="6" class="dot"/>`;
  const mains = new Set(F.main);
  for (const n in pos) {
    if (n === 'start') continue;
    const [x, y] = xy(n);
    out += `<g class="st ${mains.has(n) ? '' : 'side'}"><rect x="${x - BW / 2}" y="${y - BH / 2}" width="${BW}" height="${BH}" rx="15"/><text x="${x}" y="${y + 4}" text-anchor="middle">${esc(n)}</text></g>`;
  }
  return out + '</svg><p class="note" style="margin:0">Normal path left to right; other outcomes below; dashed arrows go back. Details in the table.</p>';
}
// Lifecycles
""")
open(H + 'tpl_model.html', 'w').write(s)
print('layered tpl ok')
