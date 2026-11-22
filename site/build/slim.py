"""Owner 2026-11-22: the tool map and table details live in docs/data-model/*.md now; drop them from the artifact.
Runs after the other steps: slims final.json and patches tpl_model.html."""
import json
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
DOCS = 'https://github.com/RamAltmann/agent-net/blob/main/docs/data-model/'

d = json.load(open(H + 'final.json'))
d['tools'] = {'tools': [], 'common_reads': [], 'note': ''}
keep = ('name', 'area', 'holds', 'rows_live', 'core')
d['tables'] = [{k: t[k] for k in keep if k in t} for t in d['tables']]
d['reduction'] = {}
d['rework'] = []
d['reworks'] = []
json.dump(d, open(H + 'final.json', 'w'))

s = open(H + 'tpl_model.html').read()


def rep(a, b):
    global s
    assert s.count(a) == 1, (a[:90], s.count(a))
    s = s.replace(a, b)


# Hide the two explorer sections (their scripts still run against empty data) and point to the docs instead
rep('  <section aria-labelledby="t-h">', '  <section aria-labelledby="t-h" hidden>')
rep('  <section aria-labelledby="x-h">', '  <section aria-labelledby="x-h" hidden>')
rep('  <section aria-labelledby="j-h">',
    f'  <section aria-labelledby="dd-h"><div class="head"><div><h2 id="dd-h">Table and tool details</h2>'
    f'<p class="lede">The columns, writers, readers and retention of every table, and the tables each MCP tool reads and writes, are in the repo: '
    f'<a href="{DOCS}tables.md" target="_blank" rel="noopener">docs/data-model/tables.md</a> and '
    f'<a href="{DOCS}tools.md" target="_blank" rel="noopener">docs/data-model/tools.md</a>. Click a table name on this page to open its entry there.</p></div></div></section>\n'
    '  <section aria-labelledby="j-h">')
rep("[(DATA.tools && DATA.tools.tools || []).length, 'MCP tools mapped']", "")
rep("[totalCols, 'columns'], ", "")
s = s.replace("[T.filter(t => t.core).length, 'core tables'], ", "[T.filter(t => t.core).length, 'core tables']")
rep('the core entities, their lifecycles, then every entity, table and MCP tool. Click any entity or table name to jump to it.',
    'the core entities, their lifecycles, every entity, where each one is stored, and everyday journeys. Click a table name to open its details.')
s = s.replace('a bold outline marks the tables most of the system depends on, and a dashed orange outline marks a table that today\'s reworks change.',
              'a bold outline marks the tables most of the system depends on.')
# After all start-up code: a table name opens its entry in tables.md
i = s.rindex('</script>')
s = s[:i] + f"show = function (name) {{ window.open('{DOCS}tables.md#' + String(name).toLowerCase(), '_blank', 'noopener'); }};\n" + s[i:]
open(H + 'tpl_model.html', 'w').write(s)
print('slimmed')
