"""Reorganize the page into four tabs: Overview, Features, Data model, Proposals. History is hidden."""
import re
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
s = open(H + 'tpl_model.html').read()


def rep(a, b, n=1):
    global s
    assert s.count(a) == n, (a[:90], s.count(a))
    s = s.replace(a, b)


def cut(start, end_marker='</section>'):
    """Remove one section that starts with `start` and return its HTML."""
    global s
    i = s.index(start)
    j = s.index(end_marker, i) + len(end_marker)
    block = s[i:j]
    s = s[:i] + s[j:]
    return block


# Sections of the old Data model tab
gloss = cut('    <section aria-labelledby="g-h">')
who = cut('    <section aria-labelledby="o-h">')
split = cut('    <section aria-labelledby="s-h">')
feat = cut('    <section aria-labelledby="f-h">')
prop = cut('    <section aria-labelledby="p-h">')
core = cut('    <section aria-labelledby="m-h">')
life = cut('    <section aria-labelledby="l-h">')
talk = cut('    <section aria-labelledby="w-h">')
ents = cut('    <section aria-labelledby="d-h">')
# Sections of the old Tables tab
tmap = cut('  <section aria-labelledby="map-h">')
jour = cut('  <section aria-labelledby="j-h">')
tools = cut('  <section aria-labelledby="t-h">')
every = cut('  <section aria-labelledby="x-h">')
hist = cut('  <section aria-labelledby="r-h">')
rest = s.index('<div class="tabpane" id="tab-tables"')

# Rewrite intros
gloss = gloss.replace('<h2 id="g-h">Key words</h2><p class="lede">New to agent-net? These words come up everywhere below.</p>',
    '<h2 id="g-h">Start here</h2><p class="lede">agent-net connects AI coding agents (Claude Code, Codex, OpenCode, Pi, agy) running on several machines, so they can work as one team for one person, the owner. One central program, the <b>gateway</b>, keeps all shared data and routes every message. A small program on each machine, the <b>daemon</b>, connects that machine\'s agent sessions to the gateway.</p><p class="lede">This page has four tabs: <b>Overview</b> (this one: the roles and the two programs), <b>Features</b> (everything agent-net does), <b>Data model</b> (how it is recorded) and <b>Proposals</b> (ideas not built yet). The roles (owner, CTO, leads, workers and the rest) are explained under Who is who. A few other words come up everywhere:</p>')
talk = talk.replace('<h2 id="w-h">How the parts talk</h2>', '<h2 id="w-h">The channels between them</h2>')
life = re.sub(r'(<h2 id="l-h">Lifecycles</h2><p class="lede">.*?</p><p class="lede">.*?</p>)<p class="lede">(How to read a card:.*?)</p>',
              r'\1<details class="howto"><summary>How to read a card</summary><p>\2</p></details>', life, flags=re.S)
assert 'class="howto"' in life

overview = '\n'.join([gloss, who, split, talk])
model = '\n'.join([core, life, ents, tmap, jour, tools, every])
tabs_html = f'''  <div class="tabpane" id="tab-overview" role="tabpanel" aria-labelledby="t-overview">
{overview}
  </div>
  <div class="tabpane" id="tab-features" role="tabpanel" aria-labelledby="t-features" hidden>
{feat}
  </div>
  <div class="tabpane" id="tab-model" role="tabpanel" aria-labelledby="t-model" hidden>
{model}
  </div>
  <div class="tabpane" id="tab-proposals" role="tabpanel" aria-labelledby="t-proposals" hidden>
{prop}
  </div>
  <div hidden>
{hist}
  </div>
'''
# Replace both old tab panes (now holding only whitespace) with the new ones
i = s.index('  <div class="tabpane" id="tab-model"')
j = s.index('</div>\n</div>\n\n<script>')
s = s[:i] + tabs_html + s[j + len('</div>\n'):]

rep('''    <button class="tab" role="tab" id="t-model" aria-controls="tab-model" aria-selected="true">Data model</button>
    <button class="tab" role="tab" id="t-tables" aria-controls="tab-tables" aria-selected="false">Tables (the implementation)</button>''',
    '''    <button class="tab" role="tab" id="t-overview" aria-controls="tab-overview" aria-selected="true">Overview</button>
    <button class="tab" role="tab" id="t-features" aria-controls="tab-features" aria-selected="false">Features</button>
    <button class="tab" role="tab" id="t-model" aria-controls="tab-model" aria-selected="false">Data model</button>
    <button class="tab" role="tab" id="t-proposals" aria-controls="tab-proposals" aria-selected="false">Proposals</button>''')

# Tab switching for four tabs
i = s.index('function setTab(which) {')
j = s.index('setTab(firstTab);') + len('setTab(firstTab);')
s = s[:i] + '''const TABS = ['overview', 'features', 'model', 'proposals'];
function setTab(which) {
  if (which === 'tables') which = 'model';
  if (!TABS.includes(which)) which = 'overview';
  for (const t of TABS) {
    $('#t-' + t).setAttribute('aria-selected', String(t === which));
    $('#tab-' + t).hidden = t !== which;
  }
  try { localStorage.setItem('gt-tab2', which); } catch (_) {}
}
for (const t of TABS) $('#t-' + t).addEventListener('click', () => setTab(t));
let firstTab = 'overview'; try { firstTab = localStorage.getItem('gt-tab2') || 'overview'; } catch (_) {}
setTab(firstTab);''' + s[j:]

# Header
rep('<div class="label">agent-net gateway · gateway.db · live on v0.5.125</div>', '<div class="label">agent-net · valid for v0.5.126</div>')
rep('<h1>Gateway Data Model</h1>', '<h1>agent-net architecture</h1>')
rep('<div class="cya" id="cya" role="note"></div>', '<details class="cya"><summary>What this page can and cannot tell you</summary><div id="cya"></div></details>')
s = re.sub(r"\$\('#lede'\)\.textContent = `.*?`;",
           "$('#lede').textContent = `How agent-net is built: who does what, everything it does, and how the gateway records it.`;", s, count=1)
s = re.sub(r"\$\('#cya'\)\.innerHTML = `<b>.*?</b><ul>", "$('#cya').innerHTML = `<ul>", s, count=1)
rep('.rel{font-size:13px;color:var(--muted)}', '.rel{font-size:13px;color:var(--muted)}\n'
    'details.cya summary,details.howto summary{cursor:pointer;font-weight:600;font-size:14px;color:var(--accent)}\n'
    'details.howto{margin-top:6px;max-width:80ch}\ndetails.howto p{margin:6px 0 0;color:var(--muted);font-size:14px}')
rep('    <div class="stats" id="stats"></div>\n', '')
rep('<details class="cya"><summary>What this page can and cannot tell you</summary><div id="cya"></div></details>', '')
rep('  <div class="tabpane" id="tab-model" role="tabpanel" aria-labelledby="t-model" hidden>\n',
    '  <div class="tabpane" id="tab-model" role="tabpanel" aria-labelledby="t-model" hidden>\n    <section><p class="lede">How the gateway records everything: the core entities, their lifecycles, then every entity, table and MCP tool. Click any entity or table name to jump to it.</p><div class="stats" id="stats" style="margin-top:12px"></div><details class="cya"><summary>What this page can and cannot tell you</summary><div id="cya"></div></details></section>\n')
rep('This is the intuition; the data model below is how agent-net records it.', 'The Data model tab shows how agent-net records them.')
# Lifecycle cards: the transition table opens on demand
rep('<table class="tr"><tbody>${l.transitions.map(', '<details class="howto"><summary>What moves it (${l.transitions.length} moves)</summary><table class="tr"><tbody>${l.transitions.map(')
i = s.index('<details class="howto"><summary>What moves it')
j = s.index('</table>', i) + len('</table>')
s = s[:j] + '</details>' + s[j:]
rep('dashed arrows go back. Details in the table.</p>', 'dashed arrows go back.</p>')
# The caveat box is a note, not a warning: orange stays for optional and proposed things
rep('.cya{border:1px solid var(--hot);background:var(--hot-soft);', '.cya{border:1px solid var(--line);background:var(--panel);')
open(H + 'tpl_model.html', 'w').write(s)
print('restructured')
