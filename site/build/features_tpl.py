"""Insert the feature table after 'Gateway and daemon'."""
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
s = open(H + 'tpl_model.html').read()


def rep(a, b):
    global s
    assert s.count(a) == 1, (a[:80], s.count(a))
    s = s.replace(a, b)


rep('.rel{font-size:13px;color:var(--muted)}', '.rel{font-size:13px;color:var(--muted)}\n'
    '.ftab{width:100%;border-collapse:collapse;font-size:14px}\n.ftab th,.ftab td{text-align:left;vertical-align:top;padding:7px 10px;border-bottom:1px solid var(--line)}\n'
    '.ftab thead th{font-size:12px;text-transform:uppercase;letter-spacing:.05em;color:var(--muted)}\n.ftab tr.farea td{background:var(--accent-soft);font-weight:600;padding-top:10px}\n'
    '.ftab tr.farea span{font-weight:400;color:var(--muted);margin-left:8px}\n.ftab td.dash{color:var(--muted)}\n'
    '.wh{display:inline-block;font-size:12px;font-weight:600;border-radius:999px;padding:1px 9px;white-space:nowrap}\n'
    '.wh.gateway{background:var(--accent);color:#fff}\n.wh.daemon{background:var(--fg);color:var(--bg)}\n.wh.mixed{background:var(--hot-soft);color:var(--fg);border:1px solid var(--hot)}\n'
    '.fsum{display:flex;gap:16px;flex-wrap:wrap;margin-bottom:12px;font-size:14px}\n.fscroll{overflow-x:auto}\n'
    '.ftab tr.frow td:first-child{min-width:210px}\n.ftab tr.frow.has{cursor:pointer}\n.ftab tr.frow.has:hover td{background:var(--accent-soft)}\n.fcar{display:inline-block;width:14px;color:var(--accent)}\n'
    '.ftab tr.fmore td{background:var(--panel);padding:10px 16px 12px 34px}\n.ftab tr.fmore ul{margin:0;padding-left:18px;display:grid;gap:3px}\n.fref{margin:8px 0 0;font-size:12.5px;color:var(--muted)}\n.fref code{font-size:12px}\n'
    '@media (max-width:700px){.ftab .gd{display:none}}')
rep('    <section aria-labelledby="p-h">',
    '    <section aria-labelledby="f-h"><div class="head"><div><h2 id="f-h">Features</h2><p class="lede">Everything agent-net does, in one list. Each feature says where it lives: only in the gateway, only in the daemon, or mixed (the gateway stores and decides, the daemon acts on its machine). Every tool call passes through the daemon; a feature counts as gateway when the daemon only forwards it.</p></div></div><div class="fsum" id="fsum"></div><div class="fscroll"><table class="ftab" id="ftab"></table></div></section>\n'
    '    <section aria-labelledby="p-h">')
rep('// Proposed execution drivers\n', r"""// Features
(function () {
  const F = M.features; if (!F) return;
  const n = w => F.rows.filter(r => r.where === w).length;
  $('#fsum').innerHTML = ['gateway', 'daemon', 'mixed'].map(w => `<span><span class="wh ${w}">${w}</span> ${n(w)}</span>`).join('') + `<span>${F.rows.length} features</span>`;
  const cell = t => t ? `<td class="gd">${esc(t)}</td>` : '<td class="gd dash">-</td>';
  let h = '<thead><tr><th>Feature</th><th>Where</th><th class="gd">Gateway part</th><th class="gd">Daemon part</th><th>Lifecycle</th></tr></thead><tbody>';
  for (const a in F.areas) {
    h += `<tr class="farea"><td colspan="5">${esc(a)}<span>${esc(F.areas[a])}</span></td></tr>`;
    for (const r of F.rows.filter(r => r.area === a))
      h += `<tr class="frow${(r.more || []).length ? ' has' : ''}" data-f="${esc(r.name)}" tabindex="${(r.more || []).length ? 0 : -1}" aria-expanded="false"><td><span class="fcar" aria-hidden="true">${(r.more || []).length ? '▸' : ''}</span>${esc(r.name)}</td><td><span class="wh ${r.where}">${r.where}</span></td>${cell(r.g)}${cell(r.d)}<td>${esc(r.life || '')}</td></tr>`;
  }
  $('#ftab').innerHTML = h + '</tbody>';
  const more = r => `<tr class="fmore"><td colspan="5"><ul>${r.more.map(x => `<li>${esc(x)}</li>`).join('')}</ul>${(r.files || []).length || (r.decisions || []).length ? `<p class="fref">${(r.files || []).map(f => `<code>${esc(f)}</code>`).join(' ')}${(r.decisions || []).length ? ` · decision ${r.decisions.map(esc).join(', ')}` : ''}</p>` : ''}</td></tr>`;
  const toggle = tr => {
    const r = F.rows.find(x => x.name === tr.dataset.f);
    if (!r || !(r.more || []).length) return;
    const open = tr.getAttribute('aria-expanded') === 'true';
    if (open) tr.nextElementSibling.remove(); else tr.insertAdjacentHTML('afterend', more(r));
    tr.setAttribute('aria-expanded', String(!open));
    tr.querySelector('.fcar').textContent = open ? '▸' : '▾';
  };
  $('#ftab').addEventListener('click', e => { const tr = e.target.closest('tr.frow.has'); if (tr) toggle(tr); });
  $('#ftab').addEventListener('keydown', e => { const tr = e.target.closest('tr.frow.has'); if (tr && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); toggle(tr); } });
})();
// Proposed execution drivers
""")
open(H + 'tpl_model.html', 'w').write(s)
print('features tpl ok')
