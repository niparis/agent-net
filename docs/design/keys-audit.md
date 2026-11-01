# TUI key audit

## Fit with the primitives (decision 0036)

This key audit uses sessions, workers, charters and the CTO. It records the existing and proposed interface, not a change to those primitives. No conflict with decision 0036 is known.

**Status:** Proposal, except the CTO keys and Graph/card merge in [0038](../decisions/0038-map-merged-into-graph.md). The tables below include that merge.
**Date:** 2026-10-31.
**Scope:** every key handler and hint line under `internal/tui`: the five tabs,
the Needs you rows, Messages, the inbox, Quotas, Graph, Tools, help,
the forms and the CTO box.

The rule the owner asked for is simple: Enter means "go into it". This audit
lists what the keys do today, where one key does more than one job, and a
smaller map that follows that rule. Changes are marked **small**, **medium** or
**large**.

## 1. Keys by screen

### Everywhere

| Key | Action |
|---|---|
| `1`-`5` | Open Sessions, Graph, Quotas, Messages or Tools. Not while filtering or in a text field. |
| `c`, `C` | Focus the CTO box. |
| `esc` | While the CTO box has focus, return to Sessions. |
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
| `b` | Hand over the selected row to the CTO: a preview first, then Enter sends it. |
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
| `b` | Hand over the selected row to the CTO: a preview first, then Enter sends it. |
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
| `b` | Hand over the selected message to the CTO: a preview first, then Enter sends it. |
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
| `up`/`down`, `j`/`k` | Select a subscription. |
| `p` | Protect or unprotect, then `y`/`n`. |
| `s` | Show or hide subscriptions that are not set up. |
| `left`/`right` | Cycle the Quality panel agent: all agents, then each caller. |
| `d` | Show or hide the Quality panel detail: fixes and continues. |
| `r` | Refresh the readings. |
| `?` | Help. |
| `q` | Quit. |

`enter` has no action on this tab. The global tab and CTO keys still work.

### Tools (tab 5)

Global keys on the tab:

| Key | Action |
|---|---|
| `[`/`]` | Previous or next screen. |
| `left`/`right` | Previous or next screen, except in All tools where they move between columns. |
| `up`/`down`, `j`/`k` | Move the selection. |
| `l`/`h` | Move right or left between columns. |
| `m` | Next machine. |
| `/` | Search or filter. In Differences this switches to All tools. |
| `esc` | Clear the search. |
| `r` | Rescan. |
| `?` | Tools keys. |
| `q` | Quit. |
| `c`, `C` | Focus the CTO box. |
| `M`, `G` | Open Graph. |

Differences screen:

| Key | Action |
|---|---|
| `enter` | Expand a group, or open the fix for a difference. |
| `x` | Accept the difference as it is. |
| `.` | Show accepted differences. |

All tools screen:

| Key | Action |
|---|---|
| `enter` | Says that details are below the list. |
| `a` | Turn on, and open the plan. |
| `u` | Turn off, and open the plan. |
| `h`/`l` | Select a machine or the projects cell. |

By project screen:

| Key | Action |
|---|---|
| `j`/`k` | Select a project. |
| `m` | Change the machine. |
| `/` | Filter projects. |
| `a`/`u` | No action. `u` explains that turning off is done in All tools. |

Tools plan modal and picker:

| Key | Action |
|---|---|
| `enter` | Run the plan verb, or confirm the picker. |
| `d` | Show the exact folders. |
| `j`/`k`, `up`/`down` | Scroll or select. |
| `esc` | Cancel. |
| `space`/`x` | Tick a project in the picker. |
| `/` | Filter the picker. |
| `enter` when done | Close the finished plan. |

### Graph (tab 2)

| Key | Action |
|---|---|
| `+`/`=`, `-` | Zoom in or out. |
| arrows | Select a node in that direction. |
| shift+arrows | Pan. |
| `h`, `j`, `k`, `l` | Pan left, down, up, right. |
| `0` | Fit the graph. |
| `[`/`]` | Previous or next linked neighbour. |
| `t` | Show or hide traffic. |
| `o` | Arrange nodes in rings (orbit). |
| `space` | Pause or resume traffic. |
| `/` | Find a node; Enter selects the first match, Esc cancels. |
| `enter` | Open the selected agent card. |
| `n` | Open the new session form filled from the selected project. |
| `?` | Graph help. `?` or `esc` closes it. |
| `q` | Quit. |
| `c`, `C` | Focus the CTO box. |

### Graph agent card

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
| `esc` | Return focus to Sessions. `tab` is an unadvertised alias. |
| `>` | Skip the current onboarding tip. |
| `enter` | Open the full CTO through `cto_enter`; ctrl+] returns. |
| `pgup`, `ctrl+u` | Scroll up, and load an older page at the top. |
| `pgdown`, `ctrl+d` | Scroll down. |
| `m` | Harness and model form. |
| `n` | Ask to start a new conversation; `y` confirms. |
| `u` | Undo the latest graph change. |
| `w` | Toggle the 30-minute "I'm driving" graph window. Owner TUI only; the box shows time left. |

Inside the CTO harness (the full session), the harness owns the keys. The TUI
only supplies ctrl+] to come back.

## 2. Where one key means different things

| Key | Screen and meaning | Conflict |
|---|---|---|
| `enter` | Sessions: enter session. Messages: select latest. Card: open filtered sessions. Tools: expand, open fix, apply plan, or "details below". Graph: open its card. Quotas: nothing. Forms: send, save or next field. CTO: open full CTO. | The same key is "go into it", "send", "apply" and "do nothing". The intent is close, but the Tools All case says "details below" where every other list opens something. |
| `esc` | Everywhere: back or cancel. Card: return to Graph. Help and body: close. Filter or search: clear text. Tools: clear search. CTO: return to the tab. | Usually back, but the card returns to its Graph node. |
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
| `b` | Sessions and Messages: hand the selected item to the CTO. Tools removal plan: ask the project agent. | Both actions send an item to an agent, on separate screens. |
| `d` | Sessions: detail. Tools plan: exact folders. | Both are details, so this one is acceptable. |
| `l` | Messages and Sessions feed: event filter. Tools All: move right a column. Graph: pan right. | Filter and movement. |
| `j`/`k` | Lists: move selection. Graph: pan. Modals: scroll. Picker: select. | Movement in all cases, but the axis and unit differ. |
| `g`/`end` | Sessions: first or last. Help also accepts `G` for last. | `G` opens Graph outside Help. |
| `space` | Graph: pause traffic. Policy and Integrations: toggle or cycle. Picker: tick. | Toggle and pause are close. |
| `y`/`n` | Confirmations: yes or no. Help: `n` toggles notifications. CTO: `n` starts a new conversation. | Confirmation letters leak into screens where they mean something else. |
| `q` | Everywhere: quit, except in text fields. | Fine, but `esc` is the safer back key and must never quit. |

## 3. Where one action has different keys

| Action | Keys today | Where |
|---|---|---|
| Enter or open | `enter`, `e` | Sessions; Enter is also used for forms and plan verbs. |
| Back or close | `esc`, `tab`, `backspace`, `M`, `L`, `enter`, `m` | Tab, CTO box, body overlay, card, learned overlay, help. |
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

## 4. Proposed key map

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
7. **Digits stay global tabs.** `c` and `C` focus the CTO box; `esc` returns
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

### Hand-over key

Part 5 of [the CTO design](cto-as-agent.md#6-reads-the-gap-option-a-leaves-and-the-rule)
uses `b` on a Needs you row, a Sessions row and a message in Messages. It
opens a preview of the exact text that Enter sends to the CTO. `b` was free
on Sessions and Messages; `h` was already used to hide a session. `c` and
`C` focus the CTO box on every tab, including the Graph card. The text is capped
at 4,000 characters; a longer item ends with a line that says it was cut.

Items 8, 9, 10, 11 and 12 change everyday keys. They should be done together,
one commit, after the owner approves the map. The implemented changes are the Graph/card merge in decision 0038 and
the CTO box in decision
[0034](../decisions/0034-cto-box-is-read-only.md): Enter opens the CTO and `o`
no longer does anything there.

The focused CTO box reserves `w` for the graph window (decision 0040). This key
was free there; the Messages worker-thread filter keeps its existing `w`.
