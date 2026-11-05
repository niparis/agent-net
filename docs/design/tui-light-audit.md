# TUI light-theme audit

Rank 1 is cosmetic, 2 makes information harder to read, and 3 is misleading or obscures an important decision. Scene names refer to `internal/tui/testdata/*.golden`.

In a temporary checkout, `initScreenColors` was switched to `palette.SetDark(false)`. `scripts/screens.sh` generated 159 scenes using Freeze 0.2.2 with the Catppuccin Latte light theme and a white outer background. The actual terminal canvas in the PNGs is the theme's light gray, not pure white. The PNGs at their available widths, including 80 and 120 columns, were reviewed as contact sheets, with representative scenes opened at full size. Four Inbox scenes were rendered separately using the same light setup because `TestScreens` omits the Inbox tests. No product code or goldens were changed.

| Screen | Golden scene | Defect | Rank | Palette entry | Suggested light value |
| --- | --- | --- | ---: | --- | --- |
| Graph | `graph_80x24`, `graph_120x34` | Unselected dim edge glyphs are very pale against the canvas, making the links between nodes hard to follow. The thin braille strokes make the low contrast more apparent. | 2 | `edgeDim` in `internal/tui/graphview/braille.go`, `#a7adb8` | `#596171` |

## Fixes to prioritize

1. Darken Graph's `edgeDim` light color, then check unselected edges in a terminal with a braille-capable font. This is the only confirmed contrast defect in the reviewed scenes; there are not ten independent fixes to rank.

The Freeze font displays missing-glyph boxes for Graph braille, as in the dark audit. Those boxes are a preview limitation and make the shape of the edges impossible to assess reliably here. Check Graph's braille, status colors, and the proposed edge color in the owner's light terminal. Terminal palettes and fonts vary, and this preview uses true color rather than 16 or 256 colors.

Delivery's failed and waiting states, Sessions' busy and idle states and tree glyphs, quota bars, Inbox selection and borders, the CTO box, help, and forms were also checked. Their sampled colors and selection text remained distinguishable in this preview; no additional contrast defect was confirmed. This does not establish that every state or terminal theme is safe.

Counts by screen: Graph 1; Sessions 0; Delivery 0; Quotas 0; Inbox 0; CTO box 0; Help 0; Forms 0; Messages 0; Tools 0; Settings 0; Workers log 0; Details 0.
