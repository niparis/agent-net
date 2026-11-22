"""The Conductor architecture tab, written by a friend of the owner (data/conductor-tab.html, kept as published)."""
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
s = open(H + 'tpl_model.html').read()
pane = open(H + 'conductor-tab.html').read()


def rep(a, b):
    global s
    assert s.count(a) == 1, (a[:90], s.count(a))
    s = s.replace(a, b)


rep('    <button class="tab" role="tab" id="t-proposals" aria-controls="tab-proposals" aria-selected="false">Proposals</button>',
    '    <button class="tab" role="tab" id="t-proposals" aria-controls="tab-proposals" aria-selected="false">Proposals</button>\n'
    '    <button class="tab" role="tab" id="t-arch" aria-controls="tab-arch" aria-selected="false">Conductor arch</button>')
i = s.index('</div>\n\n<script>')
s = s[:i] + pane + s[i:]
rep("'model', 'proposals'];", "'model', 'proposals', 'arch'];")
rep("  try { localStorage.setItem('gt-tab4', which); } catch (_) {}\n}",
    "  if (which === 'arch') fitArch();\n  try { localStorage.setItem('gt-tab4', which); } catch (_) {}\n}\n"
    "function fitArch() {\n  const f = document.getElementById('arch-frame');\n"
    "  try { const d = f.contentDocument; if (d && d.documentElement) f.style.height = d.documentElement.scrollHeight + 'px'; } catch (_) {}\n}\n"
    "{ const f = document.getElementById('arch-frame'); f.addEventListener('load', fitArch); window.addEventListener('resize', fitArch); }")
open(H + 'tpl_model.html', 'w').write(s)
print('arch tab kept')
