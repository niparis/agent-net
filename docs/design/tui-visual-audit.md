# TUI visual audit

Rank 1 is cosmetic, 2 makes information harder to read, and 3 is misleading or obscures an important decision. Lines refer to `internal/tui/testdata/*.golden`. The tab bar, Graph footer, Sessions TASK column and Delivery were excluded as requested.

`scripts/screens.sh` generated 159 ANSI scenes in a temporary directory. Its `TestScreens` harness forces the dark palette in `initScreenColors` (`internal/tui/screens_test.go`), and there are no light-theme goldens. To inspect light colours, make a temporary copy of the checkout, change that test helper to `palette.SetDark(false)`, render in a temporary directory, and use a light Freeze background. A light terminal run is still needed to verify contrast. Some goldens, including Inbox and Settings, are not emitted by `TestScreens`; their text was inspected directly. The pinned Freeze preview shows missing glyph boxes for Graph braille, so check the owner's terminal font before treating those boxes as a product defect.

| Screen | Golden | Line | Defect | Rank | Suggested fix |
| --- | --- | ---: | --- | ---: | --- |
| Sessions | sessions_active_80x24.golden | 4 | Machine capabilities shown at 120 columns disappear at 80 without a cue. | 2 | Expose missing harnesses in machine details or a compact indicator. |
| Graph card | map_card_80x24.golden | 18-19 | The refusal ends at `client` and the next line is RECENT, with no ellipsis at the cutoff. | 3 | Indicate clipped charter text and make the continuation discoverable. |
| Quotas | quotas_80x24.golden | 8 | `1 other subscriptions` uses a plural for one. | 1 | Use the singular. |
| Quotas | quotas_80x24.golden | 11-19 | T, S, N, H and V headings are not expanded in the legend. | 2 | Name the difficulty categories in the legend or a visible key. |
| Quotas | quotas_protect_confirm_80x24.golden | 7, 24 | The selected subscription is `claude` but the confirmation asks to protect `anthropic`. | 3 | Show both the selected display name and provider in the confirmation. |
| Workers log | workers_log_80x40.golden | 4-9 | Similar task titles are truncated in every row, making runs difficult to distinguish. | 2 | Give titles more space or show the selected full title prominently. |
| Messages | messages_120x34.golden | 4-6 | The wider layout truncates every message preview, while the 80-column stacked layout shows the full previews. | 2 | Allocate more width to the list or abbreviate counts and timestamps. |
| Messages | messages_80x24.golden | 16-18 | Three blank conversation lines take space while only three threads are visible. | 1 | Allocate space dynamically between list and conversation. |
| Tools | tools_list_80x24.golden | 23-24 | The shortcut line wraps to a second line. | 1 | Shorten or prioritize shortcuts at narrow widths. |
| Tools | tools_list_80x24.golden | 14-15 | The same version mismatch is repeated in adjacent lines. | 1 | Combine status and warning. |
| Tools | tools_owner_picker_80x24.golden | 4, 12 | `15 of 15 targets shown` contradicts `and 8 more`. | 3 | Report the visible range separately from the filtered total. |
| Tools | tools_compare_80x24.golden | 4-6 | Both alternatives are called `research's copy`, and both say `unknown`, so the labels do not distinguish the choice. | 3 | Identify each copy by harness or path before the keep action. |
| Settings | settings_80x24.golden | 18-19 | The Machines heading and explanation appear without any machine rows or a cue that more content follows. | 2 | Reserve room for a row or indicate more content below. |
| Inbox | inbox_empty_80x24.golden | 24 | The empty state still offers `up/down select` when nothing can be selected. | 1 | Hide the selection hint in the empty state. |
| CTO box | cto_long_error_80x24.golden | 21 | The error is cut after `insufficient` even though the panel has empty horizontal space. | 2 | Use the available width and offer a way to read the full error. |
| Help | help_80x24.golden | 1-3 | The page title and section heading both say Sessions, separated by a blank line, consuming two rows on a short screen. | 1 | Omit the duplicate heading or blank line. |
| Forms | view_80x24_compose.golden | 7-19 | The compose prompt occupies only the bottom line while the body is entirely blank. | 2 | Give the message editor multiple visible lines. |
| Details | detail_80x24.golden | 17-19 | Remote, source and message values are truncated, including the message status, with no visible way to expand them. | 2 | Allow a detail view or wrapping for the selected value. |

## Ten fixes to prioritize

1. Show the complete refusal in the Graph card or clearly indicate its continuation.
2. Make the quota protection confirmation name the selected subscription and provider together.
3. Correct the Tools picker visible count.
4. Distinguish the two copies in the Tools comparison before asking which to keep.
5. Give the compose editor room for more than one line.
6. Make the full CTO error accessible.
7. Let session details reveal truncated remote, source and message status values.
8. Expand the quota difficulty headings.
9. Restore useful message previews in the 120-column split layout.
10. Make machine capability information discoverable at 80 columns.

Counts by screen: Sessions 1, Graph card 1, Quotas 3, Workers log 1, Messages 2, Tools 4, Settings 1, Inbox 1, CTO box 1, Help 1, Forms 1, Details 1. Confirms are counted with their owning screen.
