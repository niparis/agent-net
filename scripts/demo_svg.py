#!/usr/bin/env python3
"""Combine the demo scene frames into one animated SVG.

The TUI test writes one .ansi file per frame and a demo_timeline.txt that says
how long each frame holds. scripts/demo.sh renders each .ansi to SVG with the
pinned Freeze, then calls this script.

Every Freeze SVG carries a copy of the embedded JetBrains Mono font, about
390 KB. This script keeps the first copy and drops the rest, then shows one
frame at a time with a CSS step animation. It also honours
prefers-reduced-motion by showing only the last frame.
"""

import re
import sys
from pathlib import Path


def fail(message):
    sys.exit(f"demo_svg: {message}")


def parse_frame(path, index):
    """Return the font style, canvas size, background rect and group of one frame."""
    text = path.read_text(encoding="utf-8")
    root = re.search(r'<svg width="([\d.]+)" height="([\d.]+)"', text)
    style = re.search(r"<style>(.*?)</style>", text, re.S)
    group = re.search(r"(<g\b.*?</g>)", text, re.S)
    rect = re.search(r"<rect\b[^>]*/>", text[style.end():] if style else text)
    if not root or not style or not group or not rect:
        fail(f"{path.name}: not a Freeze SVG")
    width, height = root.group(1), root.group(2)
    group = group.group(1).replace("<g ", f'<g class="frame f{index}" ', 1)
    return style.group(1), width, height, rect.group(0), group


def percent(ms, total):
    value = ms * 100.0 / total
    text = f"{value:.4f}".rstrip("0").rstrip(".")
    return text or "0"


def keyframes(index, start, end, total):
    """One frame's step animation: visible from start to end, hidden otherwise."""
    at_start = percent(start, total)
    at_end = percent(end, total)
    if index == 0:
        frames = ["0%{opacity:1}"]
        if end < total:
            frames.append(f"{at_end}%{{opacity:0}}")
        frames.append("100%{opacity:0}")
    elif end >= total:
        frames = ["0%{opacity:0}", f"{at_start}%{{opacity:1}}", "100%{opacity:1}"]
    else:
        frames = ["0%{opacity:0}", f"{at_start}%{{opacity:1}}", f"{at_end}%{{opacity:0}}", "100%{opacity:0}"]
    return f"@keyframes f{index}{{" + "".join(frames) + "}"


def main():
    if len(sys.argv) != 3:
        fail("usage: demo_svg.py SVG_DIR OUTPUT.svg")
    svg_dir, output = Path(sys.argv[1]), Path(sys.argv[2])
    timeline = svg_dir / "demo_timeline.txt"
    if not timeline.exists():
        fail(f"missing {timeline}")
    rows = []
    for line in timeline.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        name, hold = line.split()
        rows.append((name, int(hold)))
    if not rows:
        fail("the timeline is empty")
    total = sum(hold for _, hold in rows)

    styles, sizes, rects, groups = [], set(), [], []
    for index, (name, _) in enumerate(rows):
        path = svg_dir / f"{name}.svg"
        if not path.exists():
            fail(f"missing {path}")
        style, width, height, rect, group = parse_frame(path, index)
        styles.append(style)
        sizes.add((width, height))
        rects.append(rect)
        groups.append(group)
    if len(sizes) != 1:
        fail(f"frames have different sizes: {sorted(sizes)}")
    width, height = sizes.pop()
    if len(set(styles)) != 1:
        fail("frames carry different styles; the shared font would be lost")
    if len(set(rects)) != 1:
        fail("frames carry different backgrounds")

    css = [styles[0], ".frame{opacity:0}", f".f{len(rows) - 1}{{opacity:1}}"]
    css.append(".frame{animation-duration:%ss;animation-iteration-count:infinite;animation-timing-function:step-end}"
               % (total / 1000))
    for index in range(len(rows)):
        css.append(f".f{index}{{animation-name:f{index}}}")
    start = 0
    for index, (_, hold) in enumerate(rows):
        css.append(keyframes(index, start, start + hold, total))
        start += hold
    css.append("@media (prefers-reduced-motion: reduce){.frame{animation:none}}")

    body = [
        '<?xml version="1.0" encoding="UTF-8"?>',
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" '
        f'viewBox="0 0 {width} {height}">',
        "<style>" + "\n".join(css) + "</style>",
    ]
    body.append(rects[0])
    body.extend(groups)
    body.append("</svg>")
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text("\n".join(body) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
