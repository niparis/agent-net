"""Use the 8 feature areas for entities, tables and lifecycles too."""
import json
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
d = json.load(open(H + 'final.json'))
M = d['model']
AREAS = M['features']['areas']
ORDER = list(AREAS)
ENT = {
 'Network': ['machine', 'transfer', 'state'],
 'Sessions': ['session'],
 'Communication': ['message', 'event', 'nudge'],
 'Organization': ['agent', 'repo', 'team', 'link', 'charter', 'scope', 'outcome', 'loop', 'setting'],
 'Owner': ['ask', 'sort', 'followup', 'note', 'suggestion'],
 'Workers': ['start', 'run', 'rating', 'quota', 'friction', 'playbook'],
 'Delivery': ['change', 'step', 'bar'],
 'Shared resources': ['skill', 'mcp', 'app', 'signin'],
}
area_of = {e: a for a, es in ENT.items() for e in es}
ids = {e['id'] for e in M['entities']}
assert ids == set(area_of), (ids ^ set(area_of))
key = {a: a.lower().replace(' ', '-') for a in ORDER}
for e in M['entities']:
    e['domain'] = key[area_of[e['id']]]
M['entities'].sort(key=lambda e: ORDER.index(area_of[e['id']]))
M['domains'] = [{'id': key[a], 'name': a, 'what': AREAS[a]} for a in ORDER]
# Tables follow the entity that stores them
tab_area = {}
for e in M['entities']:
    for t in e['tables']:
        tab_area.setdefault(t, key[area_of[e['id']]])
missing = [t['name'] for t in d['tables'] if t['name'] not in tab_area]
assert not missing, missing
for t in d['tables']:
    t['area'] = tab_area[t['name']]
d['area_names'] = {key[a]: a for a in ORDER}
# Lifecycles in area order
M['lifecycles'].sort(key=lambda l: ORDER.index(area_of.get(l['entity'], 'Shared resources')))
json.dump(d, open(H + 'final.json', 'w'))

s = open(H + 'tpl_model.html').read()
i = s.index('const AREAS = {')
j = s.index('};', i) + 2
s = s[:i] + 'const AREAS = DATA.area_names;' + s[j:]
a = 'Click a table to open it in the Tables tab.'
if a in s:
    s = s.replace(a, 'Click a table to open it further down this tab.')
s = s.replace('<p class="lede">Eleven areas. Each chip', '<p class="lede">The same eight areas as the Features tab. Each chip')
s = s.replace('<h2 id="d-h">Every entity, by domain</h2>', '<h2 id="d-h">Every entity, by area</h2>')
s = s.replace("[DATA.model.domains.length, 'domains']", "[DATA.model.domains.length, 'areas']")
s = s.replace('entities in ${M.domains.length} domains.', 'entities in ${M.domains.length} areas, the same as the Features tab.')
open(H + 'tpl_model.html', 'w').write(s)
print('regrouped')
