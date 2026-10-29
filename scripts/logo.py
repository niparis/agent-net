"""Draw the agent-net logo (python3 scripts/logo.py docs/img): three agents, one message in flight."""
import sys

THEMES = {
    "light": dict(fg="#57606a", accent="#0969da", soft="#ddf4ff", ring="#d0d7de"),
    "dark": dict(fg="#6e7681", accent="#4493f8", soft="#0f2a4a", ring="#30363d"),
}


def svg(t):
    # Nodes on a triangle; edges between them; a small message dot on one edge.
    a, b, c = (60, 20), (20, 92), (100, 92)
    s = '<svg xmlns="http://www.w3.org/2000/svg" width="120" height="120" viewBox="0 0 120 120">'
    s += '<title>agent-net</title>'
    for p, q in ((a, b), (b, c), (c, a)):
        s += f'<line x1="{p[0]}" y1="{p[1]}" x2="{q[0]}" y2="{q[1]}" stroke="{t["fg"]}" stroke-width="3.5" stroke-linecap="round"/>'
    # The highlighted edge carries a message from b to a.
    s += f'<line x1="{b[0]}" y1="{b[1]}" x2="{a[0]}" y2="{a[1]}" stroke="{t["accent"]}" stroke-width="4.5" stroke-linecap="round"/>'
    for (x, y) in (a, b, c):
        s += f'<circle cx="{x}" cy="{y}" r="13" fill="{t["soft"]}" stroke="{t["accent"]}" stroke-width="3.5"/>'
        s += f'<circle cx="{x}" cy="{y}" r="4.5" fill="{t["accent"]}"/>'
    mx, my = (b[0] + a[0]) / 2, (b[1] + a[1]) / 2
    s += f'<circle cx="{mx}" cy="{my}" r="6.5" fill="{t["accent"]}"/>'
    s += '</svg>\n'
    return s


out = sys.argv[1]
for name, t in THEMES.items():
    with open(f"{out}/logo-{name}.svg", "w") as f:
        f.write(svg(t))
