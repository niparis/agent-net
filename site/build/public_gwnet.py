"""Public edition only: a 'Gateway network' tab that shows gateway-network/index.html (decision 0178 design page) inline."""
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
s = open(H + 'tpl_model.html').read()


def rep(a, b):
    global s
    assert s.count(a) == 1, (a[:90], s.count(a))
    s = s.replace(a, b)


rep('    <button class="tab" role="tab" id="t-proposals" aria-controls="tab-proposals" aria-selected="false">Proposals</button>',
    '    <button class="tab" role="tab" id="t-proposals" aria-controls="tab-proposals" aria-selected="false">Proposals</button>\n'
    '    <button class="tab" role="tab" id="t-gwnet" aria-controls="tab-gwnet" aria-selected="false">Gateway network</button>')
i = s.index('</div>\n\n<script>')
s = s[:i] + ('  <div class="tabpane" id="tab-gwnet" role="tabpanel" aria-labelledby="t-gwnet" hidden>\n'
             '    <section aria-labelledby="gw-h"><div class="head"><div><h2 id="gw-h">A network of gateways (proposed)</h2>'
             '<p class="lede">Decision 0178, proposed and not scheduled: how agent-net could go from one owner with one gateway to many owners whose gateways talk to each other. '
             '<a href="gateway-network/index.html" target="_blank" rel="noopener">Open it on its own page</a>.</p></div></div>'
             '<iframe id="gw-frame" src="gateway-network/index.html" title="A network of gateways" loading="lazy" style="width:100%;height:1200px;border:0;display:block"></iframe></section>\n'
             '  </div>\n') + s[i:]
s = s.replace("'model', 'proposals', 'arch'];", "'model', 'proposals', 'arch', 'gwnet'];") if "'proposals', 'arch'];" in s else s
if False: rep("x",
    "const TABS = ['overview', 'quick', 'runs', 'features', 'principles', 'model', 'proposals', 'gwnet'];")
rep("  try { localStorage.setItem('gt-tab4', which); } catch (_) {}\n}",
    "  if (which === 'gwnet') fitGw();\n  try { localStorage.setItem('gt-tab4', which); } catch (_) {}\n}\n"
    "function fitGw() {\n  const f = document.getElementById('gw-frame');\n"
    "  try { const d = f.contentDocument; if (d && d.documentElement && d.documentElement.scrollHeight > 200) f.style.height = d.documentElement.scrollHeight + 'px'; } catch (_) {}\n}\n"
    "{ const f = document.getElementById('gw-frame'); f.addEventListener('load', fitGw); window.addEventListener('resize', fitGw); }")
open(H + 'tpl_model.html', 'w').write(s)
print('gwnet tab')
