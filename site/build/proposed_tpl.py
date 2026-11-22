"""Patch tpl_model.html with the proposed execution-driver section (after Gateway and daemon)."""
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
s = open(H + 'tpl_model.html').read()

def rep(a, b):
    global s
    assert s.count(a) == 1, (a[:90], s.count(a))
    s = s.replace(a, b)

rep('.rel{font-size:13px;color:var(--muted)}',
    '.prop{border:2px dashed var(--hot);border-radius:12px;padding:18px;display:grid;gap:14px;background:var(--panel)}\n'
    '.prop .badge{display:inline-block;font-size:12px;font-weight:600;letter-spacing:.05em;text-transform:uppercase;color:#fff;background:var(--hot);border-radius:4px;padding:2px 8px}\n'
    '.prop h3{margin:0;font-size:16px}\n.prop p{margin:0}\n.prop ul,.prop ol{margin:0;padding-left:20px;display:grid;gap:4px;font-size:14px}\n'
    '.verbs{display:grid;grid-template-columns:repeat(auto-fill,minmax(250px,1fr));gap:8px}\n.verbs div{border:1px solid var(--line);border-radius:8px;padding:8px 10px;font-size:14px}\n.verbs b{font-family:var(--mono);font-size:13px;color:var(--accent);display:block}\n'
    '.drv .relay rect{fill:var(--accent-soft);stroke:var(--accent)}\n.drv .driver rect{fill:var(--panel);stroke:var(--accent)}\n.drv .driver.ext rect{stroke:var(--hot);stroke-dasharray:5 4}\n'
    '.drv text{font:600 14px var(--body);fill:var(--fg)}\n.drv .small{font:12px var(--body);fill:var(--muted)}\n'
    '.rel{font-size:13px;color:var(--muted)}')
rep('    <section aria-labelledby="m-h">',
    '    <section aria-labelledby="p-h"><div class="head"><div><h2 id="p-h">Proposed: execution drivers</h2><p class="lede">A proposal to separate the daemon from the execution layer, so other engines (for example an OpenRig team) can plug in. Nothing here is built.</p></div></div><div class="prop" id="prop"></div></section>\n'
    '    <section aria-labelledby="m-h">')
rep('// Lifecycles\n', r"""// Proposed execution drivers
(function () {
  const P = M.proposed; if (!P) return;
  const ul = a => `<ul>${a.map(x => `<li>${esc(x)}</li>`).join('')}</ul>`;
  const dW = 1100, n = P.drivers.length, gap = 14, bw = (dW - 40 - gap * (n - 1)) / n;
  let svg = `<svg class="coremap drv" viewBox="0 0 ${dW} 270" role="img" aria-label="Daemon split into a relay core and drivers">`;
  svg += `<g class="relay"><rect x="20" y="96" width="${dW - 40}" height="64" rx="10"/><text x="${dW / 2}" y="124" text-anchor="middle">Daemon relay core (same for every engine)</text><text class="small" x="${dW / 2}" y="146" text-anchor="middle">${esc(P.core.join(' · '))}</text></g>`;
  svg += `<g class="relay"><rect x="${dW / 2 - 130}" y="14" width="260" height="46" rx="10"/><text x="${dW / 2}" y="43" text-anchor="middle">Gateway</text></g>`;
  svg += `<line x1="${dW / 2}" y1="60" x2="${dW / 2}" y2="96" class="edge"/><text class="small" x="${dW / 2 + 10}" y="84">link</text>`;
  P.drivers.forEach((name, i) => {
    const x = 20 + i * (bw + gap), ext = /external/.test(name);
    svg += `<line x1="${x + bw / 2}" y1="160" x2="${x + bw / 2}" y2="196" class="edge"/>`;
    svg += `<g class="driver${ext ? ' ext' : ''}"><rect x="${x}" y="196" width="${bw}" height="54" rx="10"/><text x="${x + bw / 2}" y="${ext ? 220 : 228}" text-anchor="middle">${esc(name.replace(' (external)', ''))}</text>${ext ? `<text class="small" x="${x + bw / 2}" y="238" text-anchor="middle">external driver</text>` : ''}</g>`;
  });
  svg += `<text class="small" x="20" y="188">one contract</text></svg>`;
  $('#prop').innerHTML = `<div><span class="badge">${esc(P.status)}</span></div>
    <div><h3>The problem</h3><p>${esc(P.problem)}</p></div>
    <div><h3>The idea</h3><p>${esc(P.idea)}</p></div>${svg}
    <div><h3>The contract every driver speaks</h3><div class="verbs" style="margin-top:6px">${P.verbs.map(([k, v]) => `<div><b>${esc(k)}</b>${esc(v)}</div>`).join('')}</div></div>
    <div><h3>An OpenRig team under this split</h3>${ul(P.openrig.map(([k, v]) => k + ': ' + v))}</div>
    <div><h3>What it would change in the data model</h3>${ul(P.model)}</div>
    <div><h3>A path that does not break today's setup</h3><ol>${P.path.map(x => `<li>${esc(x)}</li>`).join('')}</ol></div>
    <div><h3>Open questions</h3>${ul(P.open)}</div>`;
})();
// Lifecycles
""")
open(H + 'tpl_model.html', 'w').write(s)
print('proposed tpl ok')
