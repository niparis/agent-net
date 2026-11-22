"""Owner 2026-11-22: the best parts of README.md go into Overview; Supported agents and Security model go into How it runs.
Read from README.md on origin/main at build time, so the guide follows the README."""
import re
import subprocess
import sys
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
sys.path.insert(0, H)
from md2html import render  # noqa: E402

REPO = os.environ['SITE_ROOT']
BASE = 'https://github.com/RamAltmann/agent-net/blob/main/'
md = open(os.path.join(os.environ['SITE_ROOT'], 'README.md')).read()


def section(title, level):
    """The body of the README section with this exact heading, without the heading."""
    m = re.search(r'(?m)^' + '#' * level + ' ' + re.escape(title) + r'\s*$', md)
    assert m, title
    rest = md[m.end():]
    end = re.search(r'(?m)^#{1,%d} ' % level, rest)
    body = rest[:end.start()] if end else rest
    body = re.sub(r'(?s)<p align="center">.*?</p>', '', body)  # README images use repo-relative paths
    return body.strip()


def top_n(body, n):
    items = re.split(r'(?m)^(?=\d+\. )', body)
    head, items = items[0], items[1:]
    return head + ''.join(items[:n])


def block(id_, title, body_md, lede=''):
    html = render(body_md, base=BASE)
    return (f'    <section aria-labelledby="{id_}"><div class="head"><div><h2 id="{id_}">{title}</h2>'
            + (f'<p class="lede">{lede}</p>' if lede else '') + f'</div></div><div class="qs rd">{html}</div></section>\n')


different = top_n(section('What makes agent-net different', 3), 6).replace('Ranked by how much they set it apart:', 'The six things that set it apart most:')
overview_add = (
    block('rd-isnot', 'What it is, and what it is not', section('What it is, and what it is not', 2))
    + block('rd-diff', 'What makes it different', different)
    + block('rd-fragile', "Isn't a swarm fragile?", section("Isn't a swarm fragile?", 2))
    + '    <section aria-labelledby="rd-why"><div class="head"><div><h2 id="rd-why">How it compares to other tools</h2></div></div>'
    + '<details class="howto"><summary>Show the comparison with Claude Code teams, Conductor, OpenHands, hosted agents, swarm tools and agent frameworks</summary>'
    + '<div class="qs rd">' + render(section('Why not just …', 2), base=BASE) + '</div></details></section>\n'
)
runs_add = (
    block('rd-agents', 'Supported agents', section('Supported agents', 2))
    + block('rd-sec', 'Security model', section('Security model', 2))
)

s = open(H + 'tpl_model.html').read()


def rep(a, b):
    global s
    assert s.count(a) == 1, (a[:90], s.count(a))
    s = s.replace(a, b)


rep('    <section aria-labelledby="o-h">', overview_add + '    <section aria-labelledby="o-h">')
i = s.index('<div class="tabpane" id="tab-runs"')
j = s.index('\n  </div>', i)
s = s[:j] + '\n' + runs_add.rstrip('\n') + s[j:]
rep('.rel{font-size:13px;color:var(--muted)}', '.rel{font-size:13px;color:var(--muted)}\n'
    '.mdt{overflow-x:auto;border:1px solid var(--line);border-radius:8px;background:var(--panel)}\n'
    '.mdt table{border-collapse:collapse;width:100%;font-size:14px}\n.mdt th,.mdt td{text-align:left;vertical-align:top;padding:8px 12px;border-bottom:1px solid var(--line)}\n'
    '.mdt th{font-size:12px;text-transform:uppercase;letter-spacing:.05em;color:var(--muted)}\n.qs.rd{max-width:none}\n.qs.rd p,.qs.rd ul,.qs.rd ol{max-width:80ch}\n.qs.rd i{color:inherit}\n'
    'details.howto .qs{margin-top:10px}')
assert '—' not in overview_add + runs_add
open(H + 'tpl_model.html', 'w').write(s)
print('readme bits added')
