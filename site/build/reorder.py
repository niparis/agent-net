"""Final tab layout: Overview (what it is + who is who), Quick start, How it runs,
Features, Principles, Data model, Proposals."""
import re
import os
H = os.environ['SITE_WORK'].rstrip('/') + '/'
s = open(H + 'tpl_model.html').read()


def rep(a, b):
    global s
    assert s.count(a) == 1, (a[:90], s.count(a))
    s = s.replace(a, b)


# 1. Take "Who is who" out of the old overview pane
i = s.index('    <section aria-labelledby="o-h">')
j = s.index('</section>', i) + len('</section>')
who = s[i:j]
s = s[:i] + s[j:]

# 2. The old overview pane becomes "How it runs"; its first section becomes the key words
rep('<div class="tabpane" id="tab-overview" role="tabpanel" aria-labelledby="t-overview" hidden>',
    '<div class="tabpane" id="tab-runs" role="tabpanel" aria-labelledby="t-runs" hidden>')
s, n = re.subn(r'<h2 id="g-h">Start here</h2>.*?</p><p class="lede">.*?</p>',
               '<h2 id="g-h">Key words</h2><p class="lede">One central program, the <b>gateway</b>, keeps all shared data and routes every message. A small program on each machine, the <b>daemon</b>, connects that machine\'s agent sessions to the gateway. These words come up everywhere below:</p>',
               s, count=1, flags=re.S)
assert n == 1

# 3. New Overview pane: what agent-net is, then who is who
overview = ('  <div class="tabpane" id="tab-overview" role="tabpanel" aria-labelledby="t-overview">\n'
            '    <section aria-labelledby="ov-h"><div class="head"><div><h2 id="ov-h">What agent-net is</h2>'
            '<p class="lede">agent-net connects AI coding agents (Claude Code, Codex, OpenCode, Pi, agy) running on several machines, so they work as one team for one person, the owner. You talk to one assistant, the CTO, and it runs the network for you: machines, projects, leads, workers and settings. The agents plan, build, review and ship; you decide what only you can decide.</p>'
            '<p class="lede">This page has seven tabs: <b>Overview</b> (this one: what it is and who does the work), <b>Quick start</b> (your first day), <b>How it runs</b> (the programs and how they talk), <b>Features</b> (everything it does), <b>Principles</b> (the rules it is built on), <b>Data model</b> (how it is recorded) and <b>Proposals</b> (ideas not built yet).</p></div></div></section>\n'
            + who + '\n  </div>\n')
i = s.index('  <div class="tabpane" id="tab-quick"')
s = s[:i] + overview + s[i:]
rep('<div class="tabpane" id="tab-quick" role="tabpanel" aria-labelledby="t-quick">',
    '<div class="tabpane" id="tab-quick" role="tabpanel" aria-labelledby="t-quick" hidden>')

# 4. Tab buttons in the new order
i = s.index('  <div class="tabs" role="tablist" aria-label="View">')
j = s.index('</div>', i) + len('</div>')
names = [('overview', 'Overview'), ('quick', 'Quick start'), ('runs', 'How it runs'), ('features', 'Features'),
         ('principles', 'Principles'), ('model', 'Data model'), ('proposals', 'Proposals')]
buttons = '\n'.join(f'    <button class="tab" role="tab" id="t-{k}" aria-controls="tab-{k}" aria-selected="{str(k == "overview").lower()}">{v}</button>' for k, v in names)
s = s[:i] + '  <div class="tabs" role="tablist" aria-label="View">\n' + buttons + '\n  </div>' + s[j:]

rep("const TABS = ['quick', 'overview', 'features', 'model', 'principles', 'proposals'];",
    "const TABS = ['overview', 'quick', 'runs', 'features', 'principles', 'model', 'proposals'];")
rep("if (!TABS.includes(which)) which = 'quick';", "if (!TABS.includes(which)) which = 'overview';")
rep("let firstTab = 'quick'; try { firstTab = localStorage.getItem('gt-tab3') || 'quick'; } catch (_) {}",
    "let firstTab = 'overview'; try { firstTab = localStorage.getItem('gt-tab4') || 'overview'; } catch (_) {}")
rep("try { localStorage.setItem('gt-tab3', which); } catch (_) {}", "try { localStorage.setItem('gt-tab4', which); } catch (_) {}")
open(H + 'tpl_model.html', 'w').write(s)
print('reordered')
