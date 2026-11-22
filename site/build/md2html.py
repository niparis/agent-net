"""A small Markdown to HTML converter for the repo docs shown in the guide:
headings, paragraphs, numbered and bulleted lists with wrapped lines and one
level of nested bullets, code fences, links, bold, italics and inline code."""
import html
import re

REPO = 'https://github.com/RamAltmann/agent-net/blob/main/docs/'


BASE = [REPO]


def inline(t):
    t = html.escape(t, quote=False)

    def link(m):
        url = m.group(2)
        if not re.match(r'https?://', url):
            url = BASE[0] + url
        return f'<a href="{url}" target="_blank" rel="noopener">{m.group(1)}</a>'
    t = re.sub(r'\[([^\]]+)\]\(([^)]+)\)', link, t)
    t = re.sub(r'`([^`]+)`', r'<code>\1</code>', t)
    t = re.sub(r'\*\*([^*]+)\*\*', r'<b>\1</b>', t)
    t = re.sub(r'(?<![\w*])\*([^*]+)\*(?![\w*])', r'<i>\1</i>', t)
    return t


def render(md, skip_title=True, base=None):
    BASE[0] = base or REPO
    out = []
    para = []
    lst = None          # 'ol' or 'ul'
    item = None         # current item: {'text': [...], 'subs': [[...], ...]}
    code = None

    def flush_para():
        nonlocal para
        if para:
            out.append('<p>' + inline(' '.join(para)) + '</p>')
            para = []

    def flush_item():
        nonlocal item
        if item is None:
            return
        h = inline(' '.join(item['text']))
        if item['subs']:
            h += '<ul>' + ''.join('<li>' + inline(' '.join(s)) + '</li>' for s in item['subs']) + '</ul>'
        if item['after']:
            h += '<p style="margin-top:6px">' + inline(' '.join(item['after'])) + '</p>'
        out.append('<li>' + h + '</li>')
        item = None

    def close_list():
        nonlocal lst
        flush_item()
        if lst:
            out.append(f'</{lst}>')
            lst = None

    for line in md.splitlines():
        if code is not None:
            if line.strip().startswith('```'):
                out.append('<pre><code>' + html.escape('\n'.join(code)) + '</code></pre>')
                code = None
            else:
                code.append(line)
            continue
        if line.strip().startswith('```'):
            flush_para()
            close_list()
            code = []
            continue
        if line.startswith('|'):
            flush_para()
            close_list()
            cells = [c.strip() for c in line.strip().strip('|').split('|')]
            if all(re.fullmatch(r':?-{3,}:?', c) for c in cells if c):
                continue
            if not out or not out[-1].startswith('<tr') and not out[-1].startswith('<div class="mdt"'):
                out.append('<div class="mdt"><table>')
                out.append('<tr>' + ''.join('<th>' + inline(c) + '</th>' for c in cells) + '</tr>')
            else:
                out.append('<tr>' + ''.join('<td>' + inline(c) + '</td>' for c in cells) + '</tr>')
            continue
        if out and out[-1].startswith('<tr') and not line.startswith('|'):
            out.append('</table></div>')
        m_ol = re.match(r'^(\d+)\. (.*)', line)
        m_ul = re.match(r'^- (.*)', line)
        m_sub = re.match(r'^\s+- (.*)', line)
        if m_ol or m_ul:
            flush_para()
            kind = 'ol' if m_ol else 'ul'
            if lst != kind:
                close_list()
                out.append(f'<ol start="{m_ol.group(1)}">' if m_ol else '<ul>')
                lst = kind
            flush_item()
            item = {'text': [(m_ol or m_ul).group(2 if m_ol else 1)], 'subs': [], 'after': []}
            continue
        if item is not None and m_sub:
            item['subs'].append([m_sub.group(1)])
            continue
        if item is not None and line.startswith(' ') and line.strip():
            if item['subs'] and line.startswith('     '):
                item['subs'][-1].append(line.strip())
            elif item['subs']:
                item['after'].append(line.strip())
            else:
                item['text'].append(line.strip())
            continue
        if not line.strip():
            flush_para()
            continue
        # a non-indented line ends any open list
        close_list()
        if line.startswith('# '):
            if not skip_title:
                out.append('<h2>' + inline(line[2:]) + '</h2>')
            continue
        if line.startswith('### '):
            flush_para()
            out.append('<h4>' + inline(line[4:]) + '</h4>')
            continue
        if line.startswith('## '):
            flush_para()
            out.append('<h3>' + inline(line[3:]) + '</h3>')
            continue
        para.append(line.strip())
    flush_para()
    close_list()
    if out and out[-1].startswith('<tr'):
        out.append('</table></div>')
    body = '\n'.join(out)
    assert '\u2014' not in body
    return body
