"""Draw the README SVG diagrams: python3 scripts/diagram.py docs/img

For PNG previews, run: rsvg-convert -o docs/img/primitives-light.png docs/img/primitives-light.svg
Repeat for the dark theme. This needs librsvg tools.
"""
import sys

THEMES = {
    "light": dict(card="#ffffff", net="#f6f8fa", module="#ffffff", border="#d0d7de",
                  text="#1f2328", muted="#656d76", accent="#0969da", soft="#ddf4ff",
                  live="#1a7f37", line="#8c959f"),
    "dark": dict(card="#161b22", net="#161b22", module="#0d1117", border="#30363d",
                 text="#e6edf3", muted="#8d96a0", accent="#4493f8", soft="#0f2a4a",
                 live="#3fb950", line="#6e7681"),
}
FONT = "-apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif"
W, H = 960, 538


def laptop_icon(x, y, c):
    return (f'<rect x="{x+3}" y="{y}" width="20" height="13" rx="2" fill="none" stroke="{c}" stroke-width="1.8"/>'
            f'<path d="M{x} {y+17} h26" stroke="{c}" stroke-width="1.8" stroke-linecap="round"/>')


def server_icon(x, y, c):
    out = ""
    for dy in (0, 9):
        out += (f'<rect x="{x+2}" y="{y+dy}" width="22" height="7" rx="1.5" fill="none" stroke="{c}" stroke-width="1.8"/>'
                f'<circle cx="{x+6.5}" cy="{y+dy+3.5}" r="1.2" fill="{c}"/>')
    return out


def chip(x, y, name, live, t):
    w, h = 172, 44
    if live:
        box = f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="9" fill="{t["soft"]}" stroke="{t["accent"]}" stroke-width="1.2"/>'
        tag = (f'<circle cx="{x+w-44}" cy="{y+h/2}" r="3.5" fill="{t["live"]}"/>'
               f'<text x="{x+w-14}" y="{y+h/2+4}" font-size="11.5" fill="{t["live"]}" text-anchor="end">live</text>')
        color = t["text"]
    else:
        box = f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="9" fill="none" stroke="{t["border"]}" stroke-width="1.2" stroke-dasharray="5 4"/>'
        tag = f'<text x="{x+w-14}" y="{y+h/2+4}" font-size="11.5" fill="{t["muted"]}" text-anchor="end">soon</text>'
        color = t["muted"]
    label = f'<text x="{x+16}" y="{y+h/2+5}" font-size="14" font-weight="600" fill="{color}">{name}</text>'
    return box + label + tag


def host(x, y, title, sub, icon, t):
    w, h = 400, 240
    s = f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="14" fill="{t["card"]}" stroke="{t["border"]}" stroke-width="1.2"/>'
    s += icon(x + 20, y + 22, t["text"])
    s += f'<text x="{x+56}" y="{y+37}" font-size="17" font-weight="700" fill="{t["text"]}">{title}</text>'
    s += f'<text x="{x+w-20}" y="{y+37}" font-size="12.5" fill="{t["muted"]}" text-anchor="end">{sub}</text>'
    agents = [("Claude Code", True), ("Pi", True), ("Codex", False), ("OpenCode", True)]
    for i, (name, live) in enumerate(agents):
        s += chip(x + 20 + (i % 2) * 188, y + 60 + (i // 2) * 56, name, live, t)
    dy = y + 180
    s += f'<rect x="{x+20}" y="{dy}" width="{w-40}" height="40" rx="9" fill="{t["module"]}" stroke="{t["text"]}" stroke-width="1.2"/>'
    s += f'<text x="{x+36}" y="{dy+25}" font-size="14" font-weight="600" fill="{t["text"]}">agent-net daemon</text>'
    s += f'<text x="{x+w-36}" y="{dy+25}" font-size="11.5" fill="{t["muted"]}" text-anchor="end">one per machine</text>'
    return s


def module(x, y, title, lines, live, t):
    w, h = 192, 96
    dash = '' if live else ' stroke-dasharray="5 4"'
    stroke = t["accent"] if live else t["border"]
    s = f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="10" fill="{t["module"]}" stroke="{stroke}" stroke-width="1.2"{dash}/>'
    tc = t["text"] if live else t["muted"]
    s += f'<text x="{x+16}" y="{y+28}" font-size="15" font-weight="700" fill="{tc}">{title}</text>'
    if not live:
        s += f'<text x="{x+w-14}" y="{y+28}" font-size="11.5" fill="{t["muted"]}" text-anchor="end">soon</text>'
    for i, ln in enumerate(lines):
        s += f'<text x="{x+16}" y="{y+52+i*18}" font-size="12.5" fill="{t["muted"]}">{ln}</text>'
    return s


def svg(t):
    s = f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}" font-family="{FONT}">'
    s += '<title>agent-net: agents on your laptop and your VM talk through one agent network</title>'
    s += host(24, 16, "Your laptop", "macOS", laptop_icon, t)
    s += host(536, 16, "Your VM", "Linux", server_icon, t)

    # Links from each daemon to the network.
    s += f'<path d="M224 256 C224 300, 300 300, 330 338" fill="none" stroke="{t["line"]}" stroke-width="1.8"/>'
    s += f'<path d="M736 256 C736 300, 660 300, 630 338" fill="none" stroke="{t["line"]}" stroke-width="1.8"/>'
    for cx in (224, 736):
        s += f'<circle cx="{cx}" cy="256" r="3.5" fill="{t["line"]}"/>'
    s += f'<text x="480" y="300" font-size="12.5" fill="{t["muted"]}" text-anchor="middle">TLS over your LAN or Tailscale</text>'

    # The network.
    nx, ny, nw, nh = 144, 338, 672, 184
    s += f'<rect x="{nx}" y="{ny}" width="{nw}" height="{nh}" rx="14" fill="{t["net"]}" stroke="{t["accent"]}" stroke-width="1.6"/>'
    s += f'<text x="{nx+24}" y="{ny+36}" font-size="17" font-weight="700" fill="{t["text"]}">Agent network</text>'
    s += f'<text x="{nx+nw-24}" y="{ny+36}" font-size="12.5" fill="{t["muted"]}" text-anchor="end">agent-net gateway, runs on one of your machines</text>'
    my = ny + 64
    s += module(nx + 24, my, "Directory", ["who is online,", "where, and doing what"], True, t)
    s += module(nx + 240, my, "Messages", ["kept until delivered,", "replies routed back"], True, t)
    s += module(nx + 456, my, "Registry", ["MCP servers today,", "shared skills next"], True, t)
    s += '</svg>\n'
    return s


def primitives(t):
    """Draw the conceptual model in separate lanes to keep links legible."""
    s = (f'<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="870" '
         f'viewBox="0 0 1200 870" font-family="{FONT}">')
    s += '<title>Agent network primitives and their relationships</title>'
    s += (f'<defs><marker id="arrow" markerWidth="8" markerHeight="8" refX="7" refY="4" '
          f'orient="auto"><path d="M0 0 L8 4 L0 8 Z" fill="{t["line"]}"/></marker></defs>')
    s += f'<rect width="1200" height="870" rx="14" fill="{t["net"]}"/>'

    def box(x, y, w, h, title, lines=(), accent=False):
        nonlocal s
        stroke = t["accent"] if accent else t["border"]
        fill = t["soft"] if accent else t["card"]
        s += f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="10" fill="{fill}" stroke="{stroke}" stroke-width="1.3"/>'
        s += f'<text x="{x+16}" y="{y+27}" font-size="15" font-weight="700" fill="{t["text"]}">{title}</text>'
        for i, line in enumerate(lines):
            s += f'<text x="{x+16}" y="{y+49+i*19}" font-size="13" fill="{t["muted"]}">{line}</text>'

    def link(path, label, x, y):
        nonlocal s
        s += f'<path d="{path}" fill="none" stroke="{t["line"]}" stroke-width="1.5" marker-end="url(#arrow)"/>'
        s += f'<text x="{x}" y="{y}" text-anchor="middle" font-size="12" fill="{t["muted"]}">{label}</text>'

    box(470, 20, 260, 62, 'OWNER', ['Source of authority'], True)
    for x, title, sub in [(30, 'CHARTER', 'goals and behavior'),
                          (320, 'AUTHORITY', 'actions without asking'),
                          (610, 'NEEDS YOU', 'attention queue'),
                          (900, 'BUDGET', 'quotas and providers')]:
        box(x, 145, 270, 68, title, [sub])
    # Owner links branch above the cards.
    for target, label, lx in [(165, 'sets', 280), (455, 'sets', 475),
                              (745, 'attends to', 725), (1035, 'limits', 930)]:
        link(f'M600 82 V110 H{target} V145', label, lx, 103)

    box(360, 275, 350, 105, 'AGENT', ['Graph node with a lasting identity',
                                      'project lead  /  advisor  /  worker  /  ghost'], True)
    box(850, 292, 260, 68, 'AGENT', ['another Graph node'], True)
    link('M710 325 H850', 'EDGE defines', 780, 315)
    link('M165 213 V250 H400 V275', 'defines', 270, 242)
    link('M455 213 V275', 'limits', 477, 248)

    # Three independent lanes: runtime, home, and delegated work.
    box(30, 465, 240, 75, 'SESSION', ['1 = agent; 2+ = clones'])
    box(30, 635, 240, 75, 'MACHINE / HARNESS / MODEL', ['session attributes'])
    link('M400 380 V420 H150 V465', 'lives as', 275, 412)
    link('M150 540 V635', 'runs on', 180, 590)

    box(340, 465, 220, 75, 'PROJECT', ['home of a lead'])
    box(340, 635, 220, 75, 'WORKSPACE', ['folder or worktree'])
    link('M500 380 V430 H450 V465', 'has home', 466, 422)
    link('M450 540 V635', 'contains', 480, 590)

    box(650, 465, 220, 75, 'WORKER', ['one-task agent'])
    box(650, 635, 220, 75, 'TASK', ['work to be done'])
    box(950, 635, 220, 75, 'REPORT', ['task result'])
    link('M650 380 V420 H760 V465', 'requests', 711, 412)
    link('M760 540 V635', 'does', 777, 590)
    link('M870 500 H920 V672 H950', 'returns', 951, 580)

    s += f'<text x="30" y="785" font-size="13" font-weight="700" fill="{t["muted"]}">SHARED ACROSS THE NETWORK</text>'
    for x, title, sub in [(30, 'TOOLS', 'skills and MCP'), (420, 'KNOWLEDGE', 'playbooks and findings'),
                          (810, 'MESSAGE', 'ask, reply, notification')]:
        box(x, 800, 360, 52, title)
        s += f'<text x="{x+344}" y="832" text-anchor="end" font-size="12" fill="{t["muted"]}">{sub}</text>'
    return s + '</svg>\n'


out = sys.argv[1]
for name, t in THEMES.items():
    with open(f"{out}/overview-{name}.svg", "w") as f:
        f.write(svg(t))
    with open(f"{out}/primitives-{name}.svg", "w") as f:
        f.write(primitives(t))
