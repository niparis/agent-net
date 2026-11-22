"""Patch the guide template for the final-state view."""
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
s = open(H + 'tpl.html').read()

def rep(a, b, count=1):
    global s
    assert s.count(a) == count, (a[:80], s.count(a))
    s = s.replace(a, b)

# CSS
rep('.note{color:var(--muted);font-size:13px}',
    '.note{color:var(--muted);font-size:13px}\n'
    '.cya{border:1px solid var(--hot);background:var(--hot-soft);border-radius:8px;padding:12px 16px;margin-top:18px;max-width:90ch}\n'
    '.cya b{display:block;margin-bottom:4px}\n.cya ul{margin:0;padding-left:18px;display:grid;gap:4px;font-size:14px}\n'
    '.chip.chg{border-style:dashed;border-color:var(--hot)}\n.chip.chg.core{border-style:dashed;border-color:var(--hot)}\n'
    '.tag.wip{background:var(--hot);color:#fff}\n.tag.ok{background:var(--accent-soft);color:var(--accent)}\n'
    '.was{font-family:var(--mono);font-size:11px;color:var(--muted)}\n.wrap > *{min-width:0}\n.ledger td.mono{word-break:break-word}')

# Header: caveats box
rep('    <div class="stats" id="stats"></div>\n  </header>',
    '    <div class="stats" id="stats"></div>\n    <div class="cya" id="cya" role="note"></div>\n  </header>')
rep('<div class="label">agent-net gateway · gateway.db</div>',
    '<div class="label">agent-net gateway · gateway.db · live on v0.5.125</div>')
rep("Eleven areas. Each chip is a table; a bold outline marks the tables most of the system depends on.",
    "Eleven areas. Each chip is a table; a bold outline marks the tables most of the system depends on, and a dashed orange outline marks a table that today's reworks change.")

# Lede and stats
rep("$('#lede').textContent = `Every table the gateway keeps in SQLite, what it holds, who writes and reads it, and how everyday actions move through it. Counts are from the live database${DATA.meta ? ' (' + DATA.meta.generated + ', on the release candidate; recounted after install)' : ''}.`;",
    "$('#lede').textContent = `Every table the gateway will keep in SQLite once the reworks you approved on 2026-11-21 ship: what each holds, who writes and reads it, and how everyday actions move through it. Live today: ${DATA.meta.live_version}. ${DATA.meta.fts_note}`;\n"
    "$('#cya').innerHTML = `<b>Read this first: this page shows a planned state, not the live database</b><ul>${DATA.meta.caveats.map(c => `<li>${esc(c)}</li>`).join('')}</ul>`;")
rep("  [T.length, 'tables now'], [totalCols, 'columns'], [fmt(totalRows), 'rows'],\n  [removed, 'tables removed by decision 0179'],",
    "  [T.length, 'tables after the rework'], [DATA.meta.live_version.split(', ')[1].split(' ')[0], 'tables live today'], [(DATA.rework || []).length, 'tables removed today'], [totalCols, 'columns'], [fmt(totalRows), 'rows today'],")

# Area map chips: changed marker
rep("`<button class=\"chip${t.core ? ' core' : ''}\" data-t=",
    "`<button class=\"chip${t.core ? ' core' : ''}${(t.rework || []).length ? ' chg' : ''}\" data-t=")

# Journey steps: show the old table
rep("<span class=\"what\">${esc(s.what)}</span></li>`",
    "<span class=\"what\">${esc(s.what)}${s.was ? ` <span class=\"was\">was ${esc(s.was)}</span>` : ''}</span></li>`")

# Detail panel
rep("      <dt>Rows now</dt><dd class=\"num\">${fmt(t.rows_live)}</dd>",
    "      <dt>Rows today</dt><dd class=\"num\">${fmt(t.rows_live)}${(t.moved_in || []).length ? `<div class=\"note\">moved in by the rework: ${t.moved_in.map(m => esc(m.from) + ' (' + fmt(m.rows_today) + ' rows today)').join(', ')}</div>` : ''}</dd>\n"
    "      <dt>Rework</dt><dd>${(t.rework || []).length ? t.rework.map(r => `<div><span class=\"tag ${/accepted/.test(r.state) ? 'ok' : 'wip'}\">${esc(r.state)}</span> ${esc(r.label)}</div>`).join('') : '<span class=\"note\">unchanged</span>'}</dd>\n"
    "      ${t.note ? `<dt>Note</dt><dd class=\"note\">${esc(t.note)}</dd>` : ''}")
rep("      <dt>Written by</dt><dd>${li(t.writers)}</dd>",
    "      <dt>Written by</dt><dd>${li(t.writers)}${t.code_level === 'files' ? '<div class=\"note\">files, from the rework\\'s docs</div>' : ''}</dd>")

# Ledgers
rep('<h2 id="r-h">What the reduction removed</h2><p class="lede" id="rlede"></p></div></div>\n    <div class="ledger-wrap"><table class="ledger"><thead><tr><th>Table</th><th>Part</th><th>What happened</th><th>Where the data went</th></tr></thead><tbody id="ledger"></tbody></table></div>',
    '<h2 id="r-h">What today\'s rework removes</h2><p class="lede" id="wlede"></p></div></div>\n'
    '    <div class="jtabs" id="wtabs" role="group" aria-label="Rework" style="margin-bottom:10px"></div>\n'
    '    <div class="ledger-wrap"><table class="ledger"><thead><tr><th>Table</th><th>Rework</th><th>State</th><th>What happens</th><th>Where the data goes</th><th>Rows today</th></tr></thead><tbody id="wledger"></tbody></table></div>\n'
    '    <div class="head" style="margin-top:28px"><div><h2>What the first reduction removed</h2><p class="lede" id="rlede"></p></div></div>\n'
    '    <div class="ledger-wrap"><table class="ledger"><thead><tr><th>Table</th><th>Part</th><th>What happened</th><th>Where the data went</th></tr></thead><tbody id="ledger"></tbody></table></div>')
rep("$('#rlede').textContent = `Decision 0179 removed ${removed} tables,",
    "$('#rlede').textContent = `Shipped in v0.5.116 to v0.5.118 (decision 0179, 2026-11-20). Decision 0179 removed ${removed} tables,")
s = s.replace('</script>\n\n</body>', r"""
// Today's rework ledger
const W = DATA.rework || [], RW = DATA.reworks || [];
let wsel = 'all';
function wledger() {
  const rows = W.filter(r => wsel === 'all' || r.rework === wsel);
  $('#wledger').innerHTML = rows.map(r => `<tr><td class="mono">${byName[r.into] ? '' : ''}${esc(r.name)}</td><td>${esc(r.rework)}</td><td><span class="tag ${/accepted/.test(r.state) ? 'ok' : 'wip'}">${esc(r.state.split(';')[0])}</span></td><td><span class="tag ${r.how === 'dropped' ? 'drop' : 'merge'}">${esc(r.how)}</span>${r.into ? ' <button class="chip" data-t="' + esc(r.into) + '">' + esc(r.into) + '</button>' : ''}</td><td>${esc(r.where_data_went)}</td><td class="num">${fmt(r.rows_today)}</td></tr>`).join('');
}
$('#wlede').textContent = `${W.length} tables go and one is new (skill_changes), so ${DATA.meta.live_version.split(', ')[1]} become ${T.length}. Folded means the data moves into the named table; dropped means it is removed or computed when needed. Pick a rework to filter.`;
$('#wtabs').innerHTML = [`<button class="jtab" aria-pressed="true" data-w="all">All <span class="num">${W.length}</span></button>`, ...RW.map(r => `<button class="jtab" aria-pressed="false" data-w="${esc(r.label)}" title="${esc(r.state)}${r.change ? ' · ' + esc(r.change) : ''}${r.decision ? ' · decision ' + esc(r.decision) : ''}">${esc(r.label)} <span class="num">${r.drops}</span></button>`)].join('');
$('#wtabs').addEventListener('click', e => { const b = e.target.closest('.jtab'); if (!b) return; wsel = b.dataset.w; document.querySelectorAll('#wtabs .jtab').forEach(x => x.setAttribute('aria-pressed', String(x === b))); wledger(); });
$('#wledger').addEventListener('click', e => { const b = e.target.closest('.chip'); if (b) { show(b.dataset.t); $('#detail').scrollIntoView({behavior: 'smooth', block: 'nearest'}); } });
wledger();
// Open on the first journey, not on the tool view, so the steps show.
pick(J[start] ? start : 0);
</script>

</body>""", 1)
open(H + 'tpl_final.html', 'w').write(s)
print('ok', len(s))
