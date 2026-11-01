# 0042 - A kitty pixel renderer for the Graph tab

**Date:** 2026-11-01
**Status:** Accepted. Part 1 is the static picture; animation comes later.

The Graph tab already splits its state, a Scene and a renderer, so the drawing
can change without touching the graph logic. Braille works everywhere but is
coarse. The owner approved a mockup with real circles, glows, dashed edges and
anti-aliasing for terminals that can show images.

## Decision

The Graph tab can draw a Scene to an RGBA image with `golang.org/x/image/vector`
(pure Go, no cgo) and show it with the kitty graphics protocol. The image is
sized from the graph area in cells and the terminal's pixel cell size. Labels
stay ordinary terminal text at the cells the Braille renderer would use, so
they remain crisp and selectable.

Output uses Unicode placeholders: the image is transmitted once with `U=1` and
then shown through `U+10EEEE` cells with row and column diacritics. Placeholder
cells are normal text, so they survive Bubble Tea's line-based redraw. The
picture is hashed; it is transmitted again only when the hash changes, and the
old image is deleted by id first.

The renderer is chosen once at start. `AGENT_NET_GRAPH` is `auto` (default),
`pixel` or `braille`. Auto sends a tiny `a=q` image query and then a device
attributes query to bound the wait, and reads the pixel cell size from
`TIOCGWINSZ` or a CSI 16 t reply. It stays on Braille when there is no OK
answer, when the cell size is unknown, or inside a multiplexer that drops
images (`$TMUX`, or herdr, which sets `HERDR_ENV=1`). `pixel` overrides the
multiplexer veto; `braille` always falls back. Unknown or missing state falls
back to Braille, so the existing goldens and the Braille behaviour do not
change.

## Out of scope

Part 1 has no animation, no traffic comets and no zoom-in cards. It does not
re-detect on terminal resize or font change; that can come with the animated
part.
