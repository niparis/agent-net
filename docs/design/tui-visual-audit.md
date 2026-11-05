# TUI visual audit (partial)

This audit is incomplete. The PNG rendering was started with `scripts/screens.sh` in a temporary directory, but the full set of screens and light backgrounds has not been inspected. Line numbers refer to `internal/tui/testdata` golden files. Tab bar, Graph footer, Sessions TASK column and Delivery are excluded.

| Screen | Golden | Line | Defect | Rank | Suggested fix |
| --- | --- | ---: | --- | ---: | --- |
| Quotas | quotas_80x24.golden | 8 | `1 other subscriptions` has the wrong number. | 1 | Use the singular for one subscription. |
| Quotas | quotas_80x24.golden | 11-17 | The T, S, N, H and V column headings are unexplained; the legend repeats the letters without naming them. | 2 | Name the categories in the legend or provide a visible key. |
| Tools | tools_list_80x24.golden | 23-24 | The shortcut line wraps onto a second line at 80 columns. | 1 | Shorten or prioritize shortcuts at narrow widths. |
| Tools | tools_list_80x24.golden | 14-15 | The same version mismatch appears twice in adjacent lines. | 1 | Combine the status and warning into one line. |
| Settings | settings_80x24.golden | 18-19 | Machines heading and explanation appear without any machine rows before the CTO panel. | 2 | Reserve room for a row or indicate that more content is below. |
| Messages | messages_80x24.golden | 16-18 | Three blank conversation lines consume space while the thread list shows only three threads. | 1 | Allocate space dynamically between thread list and conversation. |
| Workers log | workers_log_80x40.golden | 4-9 | Task titles are truncated even though their full text is useful to distinguish runs. | 2 | Offer a wider title or show the selected title in the details pane prominently. |

These seven findings are preliminary, not a ranked top ten. Counts: Quotas 2, Tools 2, Settings 1, Messages 1, Workers log 1. Other screens have not been fully audited. Light-theme rendering remains to be investigated.
