"""Add a Principles tab, built from docs/principles.md on main."""
import subprocess
import sys
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
sys.path.insert(0, H)
from md2html import render  # noqa: E402

REPO = os.environ['SITE_ROOT']
md = open(os.path.join(os.environ['SITE_ROOT'], 'docs/principles.md')).read()
body = render(md)

s = open(H + 'tpl_model.html').read()


def rep(a, b):
    global s
    assert s.count(a) == 1, (a[:80], s.count(a))
    s = s.replace(a, b)


rep('    <button class="tab" role="tab" id="t-proposals" aria-controls="tab-proposals" aria-selected="false">Proposals</button>',
    '    <button class="tab" role="tab" id="t-principles" aria-controls="tab-principles" aria-selected="false">Principles</button>\n'
    '    <button class="tab" role="tab" id="t-proposals" aria-controls="tab-proposals" aria-selected="false">Proposals</button>')
rep('  <div class="tabpane" id="tab-proposals" role="tabpanel" aria-labelledby="t-proposals" hidden>',
    '  <div class="tabpane" id="tab-principles" role="tabpanel" aria-labelledby="t-principles" hidden>\n'
    '    <section aria-labelledby="pr-h"><div class="head"><div><h2 id="pr-h">Principles</h2></div></div><div class="qs pr">' + body + '</div></section>\n  </div>\n'
    '  <div class="tabpane" id="tab-proposals" role="tabpanel" aria-labelledby="t-proposals" hidden>')
rep("const TABS = ['quick', 'overview', 'features', 'model', 'proposals'];",
    "const TABS = ['quick', 'overview', 'features', 'model', 'principles', 'proposals'];")
rep('This page has five tabs:', 'This page has six tabs:')
rep('<b>Data model</b> (how it is recorded) and <b>Proposals</b>', '<b>Data model</b> (how it is recorded), <b>Principles</b> (the rules it is built on) and <b>Proposals</b>')
rep('.qs i{color:var(--accent);padding-right:2px}',
    '.qs i{color:var(--accent);padding-right:2px}\n.qs.pr i{color:inherit}\n.qs ol{padding-left:26px;display:grid;gap:12px;margin:0}\n.qs ol>li::marker{font-weight:700;color:var(--accent)}\n.qs li ul{margin-top:6px}')
open(H + 'tpl_model.html', 'w').write(s)
print('principles tab ok')
