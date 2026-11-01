# 0042 - A kitty pixel renderer for the Graph tab

**Date:** 2026-11-01
**Status:** Accepted. Part 1 is the static picture; part 2 adds motion and
detail in pixel mode only.

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

## Motion and detail (part 2)

All of this is pixel only. The Braille picture, its colours, its labels and its
goldens stay as they were. The scene carries the extra fields; Braille ignores
them.

- **Message traffic.** Every message that Braille animates is a small glowing
  dot with a short fading tail. It follows the quadratic curve of its edge, or
  a straight line when the two nodes have no edge. Requests are the accent
  colour `#5fc4c9`, replies `#7ccf8a`, deploys `#f0b35a`, and the owner's words
  stay gold. When the message arrives, a ring expands at the target and fades.
- **Busy nodes.** The green glow breathes slowly with a 2.2 second period. The
  phase comes from the scene time, so `space` freezes it with the traffic.
- **Smooth camera.** `+`, `-`, `0` and selecting a node ease the camera over
  300 ms with a smoothstep. The target is kept in `State.Camera`; a separate
  tween holds the picture the scene is drawn with. The ease runs only in pixel
  mode, so Braille still jumps.
- **Zoom-in cards.** Past 1.9 times the fitted zoom, every node with a charter
  gets a rounded card next to it. The first line is the maturity in caps and
  the session count; the purpose follows in at most two lines. The box is drawn
  in the image; the text is ordinary terminal cells, laid out by `PixelLabels`
  beside the node and cleared of any label it covers.

## Frame budget

A pixel frame is not free. The benchmark `BenchmarkRasterizeFrame120x34` draws
120 by 34 cells with 20 nodes, edges, a busy node, a comet and a ripple. On the
development machine it takes about 90 ms. A 116 by 20 cell area with 18 by 38
pixel cells takes about 150 ms to rasterize and 26 ms to PNG-encode; 200 by 45
cells takes about 180 ms and 40 ms after the cap below. Pixel frames are
therefore requested at 8 a second (125 ms), not 15, and idle ticks stay at
250 ms.

None of that work happens in `View` or `Update`. The TUI builds the cheap Scene,
hashes it, and starts at most one `tea.Cmd` goroutine to rasterize, PNG-encode
and lay out the placeholder rows. The finished frame is stored on the model and
`View` only copies the cached rows and prepends the transmission that has not
been sent yet. While a frame is in flight a new scene key is dropped; when the
frame finishes, the next update starts another if the scene moved on. So the
Bubble Tea loop never waits for a rasterizer, even on a slow machine.

Two hashes keep the work down. `HashScene` covers everything the picture draws
and lets the host reuse the last placeholder rows without rasterizing an
unchanged scene. `HashImage` covers the RGBA pixels and stops a retransmission
when a rasterized frame happens to be identical. A tick is scheduled only while
the Graph tab is shown, and `State.Moving` reports traffic in flight, a busy
pulse, or a running camera ease. When nothing moves the host stays on the idle
tick, sends no image and starts no render.

The image is capped at 1600 by 900 pixels. A larger cell area is rasterized
smaller with scaled cell metrics, and the kitty placement still names the full
cell area, so the terminal scales the image up. Large frames therefore cost
roughly the same as the cap instead of growing with the terminal.

## Out of scope

The renderer is still chosen once at start. It does not re-detect on terminal
resize or font change; a resize is handled by drawing a new image at the new
cell size, not by probing the terminal again.
