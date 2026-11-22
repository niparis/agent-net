"""Last step: put the data into the page and write site/pages/architecture/index.html."""
import json
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
out = os.path.join(os.environ['SITE_ROOT'], 'site', 'pages', 'architecture', 'index.html')
d = json.load(open(H + 'final.json'))
t = open(H + 'tpl_model.html').read()
h = t.replace('/*DATA*/null', json.dumps(d, separators=(',', ':')).replace('</', '<\\/'))
h = h.replace('https://github.com/RamAltmann/agent-net/', 'https://github.com/niparis/agent-net/')
h = h.replace('<title>Gateway Data Model</title>', '<title>agent-net architecture</title>')
h = h.replace('<html', '<!-- Built by site/build.sh. Do not edit by hand; edit the sources and rebuild. -->\n<html', 1)
assert 'RamAltmann' not in h and '—' not in h
open(out, 'w').write(h)
print('wrote', out, len(h), 'bytes')
