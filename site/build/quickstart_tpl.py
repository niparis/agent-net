"""Add a Quick start tab, built from docs/quickstart.md in the quickstart branch."""
import html
import re
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
import subprocess
md = open(os.path.join(os.environ['SITE_ROOT'], 'docs/quickstart.md')).read()
md = md.replace('Your first day with agent-net, step by step.\n', '', 1)


import sys
sys.path.insert(0, H)
from md2html import render  # noqa: E402
body = render(md)
assert '—' not in body

s = open(H + 'tpl_model.html').read()


def rep(a, b):
    global s
    assert s.count(a) == 1, (a[:80], s.count(a))
    s = s.replace(a, b)


rep('    <button class="tab" role="tab" id="t-overview" aria-controls="tab-overview" aria-selected="true">Overview</button>',
    '    <button class="tab" role="tab" id="t-quick" aria-controls="tab-quick" aria-selected="true">Quick start</button>\n'
    '    <button class="tab" role="tab" id="t-overview" aria-controls="tab-overview" aria-selected="false">Overview</button>')
rep('  <div class="tabpane" id="tab-overview" role="tabpanel" aria-labelledby="t-overview">',
    '  <div class="tabpane" id="tab-quick" role="tabpanel" aria-labelledby="t-quick">\n'
    '    <section aria-labelledby="q-h"><div class="head"><div><h2 id="q-h">Quick start</h2><p class="lede">Your first day with agent-net, step by step.</p></div></div><div class="qs">' + body + '</div></section>\n  </div>\n'
    '  <div class="tabpane" id="tab-overview" role="tabpanel" aria-labelledby="t-overview" hidden>')
rep("const TABS = ['overview', 'features', 'model', 'proposals'];", "const TABS = ['quick', 'overview', 'features', 'model', 'proposals'];")
rep("if (!TABS.includes(which)) which = 'overview';", "if (!TABS.includes(which)) which = 'quick';")
rep("let firstTab = 'overview'; try { firstTab = localStorage.getItem('gt-tab2') || 'overview'; } catch (_) {}",
    "let firstTab = 'quick'; try { firstTab = localStorage.getItem('gt-tab3') || 'quick'; } catch (_) {}")
rep("try { localStorage.setItem('gt-tab2', which); } catch (_) {}", "try { localStorage.setItem('gt-tab3', which); } catch (_) {}")
rep('This page has four tabs: <b>Overview</b> (this one: the roles and the two programs)',
    'This page has five tabs: <b>Quick start</b> (your first day), <b>Overview</b> (this one: the roles and the two programs)')
rep('.rel{font-size:13px;color:var(--muted)}', '.rel{font-size:13px;color:var(--muted)}\n'
    '.qs{max-width:78ch;display:grid;gap:10px;font-size:15.5px;line-height:1.6}\n.qs h3{margin:18px 0 0;font-size:18px}\n.qs h4{margin:12px 0 0;font-size:16px}\n.qs p,.qs ul{margin:0}\n.qs ul{padding-left:20px;display:grid;gap:5px}\n'
    '.qs pre{margin:0;background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:10px 14px;overflow-x:auto}\n.qs code{font-family:var(--mono);font-size:.9em}\n.qs i{color:var(--accent);padding-right:2px}\n.qs a{color:var(--accent)}')
open(H + 'tpl_model.html', 'w').write(s)
print('quick start tab ok')
