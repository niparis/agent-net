# TUI key audit

## Fit with the primitives (decision 0036)

This key audit uses sessions, workers, charters and the CTO. It records the existing and proposed interface, not a change to those primitives. No conflict with decision 0036 is known.

**Status:** The owner-approved map is implemented. The binding table is
[`internal/tui/keys.go`](../../internal/tui/keys.go). Sections 1 through 3
record the old keys and the reasons for changing them. The current keys are in
[the TUI keys reference](../reference.md#tui-keys).
**Date:** 2026-11-02.
**Scope:** every key handler and hint line under `internal/tui`: the five tabs,
the Needs you rows, Messages, the inbox, Quotas, Graph, Tools, help,
the forms and the CTO box.

The rule the owner asked for is simple: Enter means "go into it". This audit
lists what the keys did before the change, where one key did more than one job,
and the smaller map that follows that rule. Changes are marked **small**,
**medium** or **large**.

Sections 1 to 3 record the map before the approved changes. They are kept for
the reasons behind each change, not as a description of the current keys. The
current keys are in [the TUI keys reference](../reference.md#tui-keys) and in
[`internal/tui/keys.go`](../../internal/tui/keys.go).

## 1. Keys before the change

### Everywhere

| Key | Action |
|---|---|
| `1`-`5` | Open Sessions, Graph, Quotas, Messages or Tools. Not while filtering or in a text field. |
| `c`, `C` | Focus the CTO box. |
| `ctrl+]` | While the CTO box has focus, return to the tab. |
| `ctrl+c` | Quit at once. |
| `q` | Quit, except in a text field or a modal that uses `q` for typing. |

### Sessions (tab 1, including Needs you rows)

| Key | Action |
|---|---|
| `up`/`down`, `j`/`k` | Move the selection. |
| `pgup`/`pgdown` | Page the list. |
| `home`/`g`, `end` | First or last row. |
| `enter` | Enter the selected session, including a Needs you row. |
| `e` | Enter the selected session (alias of Enter). |
| `m` | Compose a message to the selected session. |
| `n` | Start a new session in the selected session's project. On the Graph card, open the same form filled from the node. |
| `s` | On an outcome row, "let stand". No action on a session row. |
| `o` | Toggle offline sessions. On an outcome row, "overrule". |
| `x` | Stop the selected worker. |
| `h` | Hide or unhide the selected session. |
| `H` | Show hidden sessions, dim. |
| `a` | Active only. On a learned suggestion, accept it. On a held request, accept it. |
| `r` | Refresh. On a learned suggestion, reject it. On a held request, refuse it. |
| `t` | Cycle the request rule: normal, ask, auto. |
| `p` | Delegation policy form. |
| `I` | Integrations form. |
| `N` | Rename the selected session's machine. |
| `L` | Learned notes overlay. |
| `M`, `G` | Open Graph. |
| `d` | Toggle the detail pane. |
| `l` | Cycle the event filter. |
| `/` | Filter by name, project, branch, model, and so on. |
| `esc` | Clear the filter. |
| `i` | Open the inbox. |
| `?` | Help. |
| `c`, `C` | Focus the CTO box. |

### Needs you rows

Needs you is a section of Sessions, not a separate screen. Enter enters the
session. The extra actions apply only when such a row is selected.

| Key | Action |
|---|---|
| `enter` | Enter the waiting session. |
| `m` | Send a message without entering. |
| `a`, `A` | Accept a held request. |
| `r`, `R` | Refuse a held request. |
| `a` | Accept a learned suggestion. |
| `r` | Reject a learned suggestion. |
| `s`, `o` | Let stand or overrule an outcome row. |

### Messages (tab 4)

| Key | Action |
|---|---|
| `up`/`down`, `j`/`k` | Select a conversation. |
| `left`/`right` | Select a message inside the conversation. |
| `enter` | Select the latest message in the thread. |
| `v` | Open the selected message body. |
| `f` | On a worker conversation: report a defect found later. A one-line note, a severity from 1 to 4, then the shared confirm. |
| `w` | Show or hide worker threads. |
| `e` | Show or hide system events. |
| `l` | Cycle the event filter. |
| `pgup`/`pgdown`, `home`/`g`, `end` | Move or page the conversation list. |
| `m`, `x`, `t`, `p`, `I`, `N`, `L`, `M`, `d`, `/`, `o`, `a`, `r`, `?`, `c`, `C`, `q` | Same as Sessions, except that `e` is events, not enter. |

### Message body overlay

| Key | Action |
|---|---|
| `esc`, `enter`, `backspace`, `m` | Close. |
| `q` | Quit. |
| `up`/`down`, `j`/`k` | Scroll one line. |
| `pgup`/`pgdown` | Scroll ten lines. |

### Inbox overlay

| Key | Action |
|---|---|
| `up`/`down`, `j`/`k` | Select a request. |
| `enter` | Open the details. |
| `esc`, `backspace` | Back from details, then close the inbox. |
| `a` | Approve. |
| `r` | Refuse. |
| `t` | Type an answer. In the text box, `enter` sends and `esc` cancels. |

### Quotas (tab 3)

| Key | Action |
|---|---|
| `up`/`down`, `j`/`k` | Select a subscription and scroll the panels. |
| `p` | Protect or unprotect, then `y`/`n`. |
| `s` | Show or hide subscriptions that are not set up. |
| `left`/`right` | Cycle the Quality panel agent: all agents, then each caller. |
| `d` | Show or hide the Quality panel detail: fixes and continues. |
| `r` | Refresh the readings. |
| `?` | Help. |
| `q` | Quit. |

`enter` has no action on this tab. The global tab and CTO keys still work.

### Tools (tab 5)

Three screens: Differences, All tools and By project. `!` means needs
attention.

| Mode | Keys |
|---|---|
| List | `j`/`k` or up/down move; Enter actions; `/` filter; `n` add; `R` rescan; Esc clear filter; `?` help; `q` quit. Digits and global keys stay. |
| Filter | Text combines words, `project:NAME`, `node:NAME`, `machine:NAME`; Enter keeps; Esc clears. |
| Menu | `j`/`k` or up/down move; Enter choose; Esc back. Only applicable actions appear. |
| Picker | `j`/`k` or up/down move; space ticks; `/` filters; Enter applies; Esc cancels. Empty selection explains what to tick. |
| Read | `j`/`k` or up/down scroll; Esc back. |
| Add | Tab/down next field; shift+tab/up previous; left/right choose machine; Enter add; Esc cancel. |
| Compare | Labelled letters keep a copy; `n`/`p` select file; `v` diff; `d` exact folders; up/down scroll; Esc back. |
| Plan | Enter or `y` applies; `d` exact folders; up/down scroll; Esc or `n` cancels. |

Project and node filters scope Turn on/Turn off directly, without a picker.
The bottom key bar follows the current mode. `[` and `]` switch the three
screens; left and right move between columns; `a` accepts a difference; `v`
selects the next machine on By project.

### Graph (tab 2)

| Key | Action |
|---|---|
| `+`/`=`, `-` | Zoom in or out. |
| arrows | Select a node in that direction. |
| shift+arrows | Pan. |
| `h`, `j`, `k`, `l` | Pan left, down, up, right. |
| `0` | Fit the graph. |
| `[`/`]` | Previous or next linked neighbour. |
| `t` | Open Tools filtered to `node:NAME` for the selected node. |
| `o` | Arrange nodes in rings (orbit). |
| `space` | Pause or resume traffic. |
| `/` | Find a node; Enter selects the first match, Esc cancels. |
| `enter` | Open the selected agent card. |
| `n` | Open the new session form filled from the selected project. |
| `?` | Graph help. `?` or `esc` closes it. |
| `q` | Quit. |
| `c`, `C` | Focus the CTO box. |

### Graph agent card

The detail includes `Tools: N skills, M MCP servers`. `t` opens that node's Tools filter.

| Key | Action |
|---|---|
| `enter` | Open Sessions filtered to the agent's project. |
| `n` | Start a new session filled from this project. |
| `esc` | Return to Graph with the same node selected. |
| `[`/`]`, `ctrl+u`/`ctrl+d` | Scroll the card body: legend, charter, learned changes and rules. |
| `U`/`u` | Select the next learned change / undo it. |
| `!` | Show or hide the legend (hidden by default). |
| `D` | Rename the project (empty clears). |
| `a` | Toggle autonomy between `off` and `wake`. |
| `h`/`H` | Hide or unhide the selected project / include hidden nodes. |
| `c` | Focus the CTO box. |
| `r` | Refresh. |
| `?` | Help. |
| `q` | Quit. |

`!` was free on the card and everywhere else: no other screen handles it, and
it does not collide with a digit, `tab`, `C` or a text field. It starts hidden
so the card keeps its rows; the key line says `! legend`. The legend is part of
the scrolling card body, so it never needs a second screen.

### Help overlay

| Key | Action |
|---|---|
| `down`/`j`, `up`/`k` | Scroll. |
| `pgdown`/`pgup` | Page. |
| `home`/`g`, `end`/`G` | First or last line. |
| `esc`, `?`, `enter`, `backspace` | Close. |
| `n` | Toggle desktop notifications. |
| `q` | Quit. |

### Forms and confirmations

| Form | Keys |
|---|---|
| Compose message | Text input, `enter` sends, `esc` cancels. |
| New session | `tab` completes a known project name, else next field; `down` next field; `shift+tab`/`up` previous; `left`/`right` change the focused switch (Harness, Model or Folder), and move the cursor in Project, Branch and First message; `enter` next field or starts on First message; `esc` cancels. Harness is a switch over the harnesses the target machine reports, in the order claude, codex, opencode, pi. Model follows the harness: `default` is first, then that harness's models on the target machine, and a harness change resets Model. The Branch field is shown only for a new worktree. The Project field also takes a path. Key line: `up/down field · left/right change · enter start · esc cancel`. |
| Create folder, Claude trust | The New session form's extra questions on the result line: `enter` or `y` says yes, `esc` or `n` says no, hint `enter: yes · esc: no`. |
| Stop worker | `y`/`Y` stops, `n`/`N`/`esc` cancels. |
| Protect quota | `y` confirms, `n`/`esc` cancels. |
| Peer trust | `y` turns on, `n`/`N`/`esc` cancels. |
| Delegation policy | `tab`/`down` next field, `shift+tab`/`up` previous, `enter` next or saves on Scope, `left`/`right` or `space` change a choice, `esc` cancels, text input for model and rule text. |
| Machine name | Text input, `enter` saves, `esc` cancels, empty resets. |
| Project name | Text input, `enter` saves, `esc` cancels, empty clears. |
| Integrations | `up`/`shift+tab` previous, `down`/`tab` next, `space` toggles, `enter` saves, `esc` cancels. |
| Inbox answer | Text input, `enter` sends, `esc` cancels. |
| Tools picker | `j`/`k` select, `space`/`x` tick, `/` filter, `enter` confirms, `esc` cancels. |

### CTO box

| Key | Action |
|---|---|
| `ctrl+]` | Return focus to the tab. `tab` is an unadvertised alias. |
| `esc` | Skip the current onboarding tip. |
| `enter` | Open the full CTO through `session_enter`; ctrl+] returns. |
| `pgup`, `ctrl+u` | Scroll up, and load an older page at the top. |
| `pgdown`, `ctrl+d` | Scroll down. |
| `m` | Harness and model form. |
| `n` | Ask to start a new conversation; `y` confirms. |
| `u` | Undo the latest graph change. |
| `w` | Toggle the 30-minute "I'm driving" graph window. Owner TUI only; the box shows time left. |

Inside the CTO harness (the full session), the harness owns the keys. The TUI
only supplies ctrl+] to come back.

## 2. Earlier audit: where one key meant different things

| Key | Screen and meaning | Conflict |
|---|---|---|
| `enter` | Sessions: enter session. Messages: select latest. Card: open filtered sessions. Tools: expand, open fix, apply plan, or "details below". Graph: open its card. Quotas: nothing. Forms: send, save or next field. CTO: open full CTO. | The same key is "go into it", "send", "apply" and "do nothing". The intent is close, but the Tools All case says "details below" where every other list opens something. |
| `esc` | Card: return to Graph. Help and body: close. Filter or search: clear text. Tools: clear search. CTO: skip the current tip. | Usually back, but the card returns to its Graph node and the CTO skips a tip. |
| `h` | Sessions: hide a session. Card: hide a project. Tools All: move left a column. Graph: pan left. | Four meanings, two of them a column move. |
| `H` | Sessions and Card: show hidden items. | Consistent, but only for two of the four `h` meanings. |
| `x` | Sessions: stop a worker. Tools Differences: accept a difference as it is. Tools picker: tick an item. | Stop, accept and tick share one letter. |
| `[` `]` | Tools: switch screens. Graph: previous or next neighbour. Card: scroll the card body. | Screen switch, graph navigation and text scroll. |
| `a` | Sessions: active only, accept a learned suggestion, accept a held request. Graph card: toggle autonomy. Tools All: turn on. Inbox: approve. | Same letter for "show active", "accept", "toggle a rule" and "turn on". |
| `r` | Sessions: refresh, reject a learned suggestion, refuse a held request. Quotas, Graph card, Tools: refresh or rescan. Inbox: refuse. | Refresh and refuse are opposite actions. |
| `s` | Sessions outcome row: let stand. Quotas: show not set up. Sessions and Messages: no action. | Let stand and show. |
| `o` | Sessions: show offline, or overrule an outcome. Graph: orbit. | Filter, decision and layout. |
| `u` | CTO: undo a graph change. Card: undo a learned change. Tools All: turn off. Tools By project: explains where turn off lives. | Undo and turn off are close, but the By project case is a dead end. |
| `U` | Card: next learned change. | Only one meaning, but easy to confuse with `u`. |
| `e` | Sessions: enter the session. Messages: show events. | Enter and filter. |
| `m` | Sessions and Messages: compose. Tools: next machine. CTO: model form. Body overlay: close. | Compose, machine and close. |
| `p` | Sessions: policy. Quotas: protect. | Two settings actions. |
| `t` | Sessions: cycle request rule. Graph: traffic. Inbox: type an answer. | Rule, visibility and text. |
| `b` | Tools removal plan: ask the project agent. | Only used in the plan. |
| `d` | Sessions: detail. Tools plan: exact folders. | Both are details, so this one is acceptable. |
| `l` | Messages and Sessions feed: event filter. Tools All: move right a column. Graph: pan right. | Filter and movement. |
| `j`/`k` | Lists: move selection. Graph: pan. Modals: scroll. Picker: select. | Movement in all cases, but the axis and unit differ. |
| `g`/`end` | Sessions: first or last. Help also accepts `G` for last. | `G` opens Graph outside Help. |
| `space` | Graph: pause traffic. Policy and Integrations: toggle or cycle. Picker: tick. | Toggle and pause are close. |
| `y`/`n` | Confirmations: yes or no. Help: `n` toggles notifications. CTO: `n` starts a new conversation. | Confirmation letters leak into screens where they mean something else. |
| `q` | Everywhere: quit, except in text fields. | Fine, but `esc` is the safer back key and must never quit. |

## 3. Earlier audit: where one action had different keys

| Action | Keys today | Where |
|---|---|---|
| Enter or open | `enter`, `e` | Sessions; Enter is also used for forms and plan verbs. |
| Back or close | `esc`, `ctrl+]`, `tab`, `backspace`, `M`, `L`, `enter`, `m` | Tab, CTO box, body overlay, card, learned overlay, help. |
| Move selection | arrows, `j`/`k`, `h`/`l`, `tab`, `shift+tab` | Lists, Tools columns, forms. |
| Search or filter | `/`, then `esc` to clear | Sessions, Messages, Graph, Tools, picker. |
| Show keys | `?` | Most screens; Graph has a separate help panel reached by `?`. |
| Quit | `q`, `ctrl+c` | Everywhere. |
| Switch screens or tabs | digits, `[`/`]`, `left`/`right`, `M` | Title, Tools, Graph, card. |
| Previous or next | `[`/`]`, `left`/`right`, `up`/`down`, `j`/`k`, `tab` | Tools screens, Graph neighbours, list rows, form fields. |
| Accept or refuse | `a`/`r`, `y`/`n`, `a`/`u`, `space`/`x` | Inbox, learned, held requests, confirms, Tools, picker. |
| Show or hide | `h`/`H`, `w`, `e`, `t`, `o`, `a`, `z` | Sessions, Messages, Graph. |
| Refresh | `r` | Sessions, Quotas, Graph card, Tools. |
| Scroll text | `pgup`/`pgdown`, `ctrl+u`/`ctrl+d`, `j`/`k`, arrows | CTO, help, body, card charter, plans. |

## 4. Implemented key map

Rules:

1. **Enter goes into the selected thing.** It enters a session, opens a
   project, opens a message body, opens a plan or fix, applies the focused plan
   verb, confirms a search, or sends a composed message. On a list row it never
   means "do nothing".
2. **Esc goes back one level.** It closes an overlay, clears a non-empty
   filter, or leaves the current screen. It never quits. `q` and `ctrl+c` quit.
3. **Arrows move.** `j`/`k` are list aliases for up and down. No letter moves
   the view. Panning is shift+arrows. Columns are left and right arrows.
4. **`/` searches or filters** on every screen that has a list. `esc` clears
   the text first, then a second `esc` goes back.
5. **`?` shows the keys** and closes them again. `esc` also closes.
6. **Letters are actions, and the same letter is the same action everywhere.**
   The proposed letters:
   - `a` accept, approve or turn on.
   - `r` refuse or reject. Refresh moves to `R`.
   - `x` stop or close. Accept on Tools moves to `a`.
   - `u` undo or turn off.
   - `m` message or compose. Machine choice moves to `v`.
   - `s` has no action on Sessions or Messages. It remains "let stand" on an outcome row and show not set up in Quotas.
   - `o` offline filter. Overrule moves to `O`; orbit stays `o` only on Graph.
   - `h`/`H` hide and show hidden. Panning and column movement stop using them.
   - `[`/`]` switch screens or tabs. Graph neighbours move to `,`/`.`; charter
     scroll stays on `ctrl+u`/`ctrl+d`.
   - `space` toggles. Pause on Graph stays `space`; it is the same idea.
   - `y`/`n` answer a confirmation inside a modal only. They do no action on a
     list or overlay.
7. **Digits stay global tabs.** `c` and `C` focus the CTO box; `ctrl+]` returns
   from it. Text fields keep their typed letters and form-specific Tab actions.

### Proposed changes

| # | Change | Size | Why |
|---|---|---|---|
| 1 | Keep Enter as "enter or open" and remove the Tools All "details below" result. Enter on All tools opens the item details or the plan. | Medium | One meaning for Enter on every list. |
| 2 | Graph: remove the `h`/`j`/`k`/`l` pan aliases; keep shift+arrows. | Small | Frees `h` for hide and `j`/`k`/`l` for list movement. |
| 3 | Tools: remove `h`/`l` column aliases; use left and right arrows for columns and `[`/`]` for screens. | Medium | Same arrows-move rule; ends the `h` conflict. |
| 4 | Tools: remove left/right screen switching; keep `[`/`]`. | Small | Left and right then always move. |
| 5 | Graph: move previous and next neighbour from `[`/`]` to `,`/`.`. | Small | `[`/`]` then always switch screens. |
| 6 | Card: keep charter scroll on `ctrl+u`/`ctrl+d`; drop `[`/`]` for charter. | Small | Same `[`/`]` rule. |
| 7 | Tools: accept a difference with `a`, not `x`. Keep `.` for accepted. | Small | `a` is accept everywhere; `x` is stop or close. |
| 8 | Refresh moves from `r` to `R` on Sessions, Messages, Quotas, Graph card and Tools. `r` becomes refuse or reject. | Large | `r` currently means refresh and refuse on the same Sessions screen. This is the biggest change and needs the owner's approval. |
| 9 | Sessions: move active-only from `a` to `A`. `a` stays accept or approve. | Small | `a` then means accept everywhere. `A` is free on Sessions. |
| 10 | Keep `s` for "let stand" on an outcome row and show not set up in Quotas. It does nothing on a session row. | None | Preserve the two existing actions. |
| 11 | Sessions: move "overrule" from `o` to `O`; keep `o` for offline. Graph orbit keeps `o`, which is a layout action on a graph, not a list action. | Small | Ends the two meanings of `o` on Sessions. |
| 12 | Tools: move machine choice from `m` to `v`; keep `m` for message and compose. | Medium | `m` then means message everywhere. |
| 13 | Body overlay: close on `esc` and Enter only; remove `m` and `backspace` as close keys. | Small | `m` is message; backspace is text editing. |
| 14 | Help: notifications move from `n` to `N`; `n` inside a confirmation stays "no". | Small | `n` stops doing an action on the help list. |
| 15 | CTO box: keep `n` for a new conversation because it is inside a modal with `y`/`n`; no change. | None | The confirmation convention already covers it. |
| 16 | Esc first clears a non-empty filter or search, and a second Esc goes back. Today one Esc does both on most screens. | Small | No lost text by accident. |
| 17 | Quotas: give Enter a meaning, "open the provider details", or state that the tab has no Enter action in its key line. | Small | Every other tab has Enter. |
| 18 | Card: Esc returns to the selected Graph node (implemented in decision 0038). | Medium | Esc must go back one level, not jump to Sessions. |
| 19 | Make `?` open the same help overlay on Graph, with a Graph section, instead of the separate `graphHelp` panel. | Medium | One key, one help. |
| 20 | Unify the confirmation keys on `y`/`n` and show them in every modal footer. They already agree except for `Y`/`N` aliases, which can stay. | Small | One confirmation convention. |

The owner approved the key changes together. The binding table records their
action IDs, contexts, help labels and key-line priorities. The Graph/card merge
is in decision 0038 and the CTO box is in decision
[0034](../decisions/0034-cto-box-is-read-only.md): Enter opens the CTO and `o`
does nothing there.

The focused CTO box reserves `w` for the graph window (decision 0040). This key
was free there; the Messages worker-thread filter moved to the `l` cycle in
section 5.

## 5. Keys cut on the owner's request

On 2026-11-02 the owner approved a smaller map and a help screen generated
from the binding table. These direct keys were cut:

- Sessions: `t`, `p`, `I` and `N` now live in one `S` settings menu. `L` was
  removed; learned notes stay on the Graph agent card. `o`, `A` and `H` were
  replaced by one `v` view cycle: active only, all, all with offline, all with
  offline and hidden.
- Messages: `w` and `e` were replaced by one `l` filter cycle: all, without
  worker threads, with system events. The old event-type cycle was dropped
  because it did not fit that cycle.
- Graph: `o` (orbit), Space (pause), `t` (Tools filtered to the node) and `0`
  (fit) were removed. The graph fits itself when the tab opens.
- Agent card: `!` (legend) was removed. The legend text now sits in the help
  overlay under Graph.

`helpContent` is now built from `keyBindings`. It lists Global keys first, then
the current tab, then the other tabs in tab order, then In overlays. A global
key is not repeated in a tab section. No key row is written by hand.
