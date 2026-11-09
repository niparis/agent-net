# 0122 - The web console

Date: 2026-11-09
Status: Accepted by the owner (2026-11-09). Parts 1 to 4 built.

## The problem

The phone app of [0006](0006-phone-app.md) is a short page of lists. The owner
wants a real console in the browser that follows the screens of the TUI, with
the same pages on a desk and on a phone. The approved clickable design is
[the mockup](../design/web-console-mockup.html). It is a reference, not shipped
code.

## Decision

The owner's words, from his Mac session on 2026-11-09: "First version is read
and answer only, plus the CTO chat panel. No starting workers or editing
charters yet. Phone: keep it as designed. Serve it from the gateway over
Tailscale to start. Go ahead and build it."

1. **The console replaces the phone app in `internal/phone`.** It keeps the
   pairing, the cookie `__Host-agent_net_phone` and the same listener. Every
   new route sits behind the same `phoneRoute` check and `phoneAuth` in the
   gateway. OAuth, `/mcp` and the public host checks do not change.
2. **Stack as 0006 P7 says.** Server-rendered Go `html/template`, htmx and its
   SSE extension, `go:embed`. Hand-written JavaScript is only for the keys 1 to
   6, the CTO panel toggle, a click on a table row, and one guard that skips a
   live update while the owner is typing. No Node build. Nothing loads from
   outside the gateway: the font stacks fall back to the fonts of the device.
3. **Screens.** A left rail on a desk, a bottom bar under 760px:
   Desk (key 1), Sessions (2), Graph (3), Changes (4), Messages (5), Workers
   (6). The top bar has the title, a live dot and the CTO button.
   - Part 1 builds Desk and Sessions. Graph, Changes, Messages and Workers say
     "coming in the next update". The CTO button opens an empty side panel that
     says "CTO chat arrives in part 4" (part 4 replaces it, see items 16 and 17).
   - Part 2 is Changes and Messages (built, see items 10 to 12). Part 3 is
     Graph and Workers (built, see items 13 to 15). Part 4 is the CTO chat
     panel (built, see items 16 and 17).
4. **Read and answer only.** The console answers what waits for the owner and
   reads the rest. Its only `POST` route is `/action`, with these kinds and no
   others: `held` (`AnswerHeld`), `outcome` (`ResolveOutcome`), `inbox`
   (`inbox_answer`: approve, refuse, dismiss, an option or typed text), `mark`
   (`ask_mark`, "didn't need me"), `send` (a message as the owner to a
   session that waits), `reply` (the owner's words in a conversation of the
   Messages screen), `cto_send` (the owner's words to the CTO, `text`) and
   `proposal` (Apply or Decline one open CTO proposal: `id`, an encoded `node`
   target and `action` `a` or `x`). There is no route that starts or stops a
   session or a worker, changes a charter, a team, an edge or a setting. A test
   holds this exact list.
5. **Asks the console does not answer.** A learned suggestion, a charter draft
   and a delivery step say "Answer from the TUI": only the CTO panel answers a
   proposal, with Apply and Decline, and only with the same owner action the TUI
   sends. An approval that needs the owner's consent page (internet access) and
   an approval that needs evidence fields can be dismissed on the web but not
   approved. The TUI opens the consent page and takes the evidence.
6. **Desk.** Needs you reads `asks_list`, the same list as the TUI Desk, in the
   TUI order, with the question, who asks, the kind, the age, the
   recommendation and the "why you" line. In flight lists the open changes that
   moved in the last day or need the owner (at most 12, read only). Handled for
   you reads `chief_sorts`. Log reads `desk_log`.
7. **Sessions.** A strip of machines (online, version, cpu, memory from the
   usage report) and the sessions grouped as needs you, working, idle and
   offline. A detail panel shows the task, machine, project, model, place in
   the team, the charter purpose and the recent conversations. It is read only.
   The web never opens a terminal: the panel says "Open from the TUI".
8. **Live updates.** The page keeps the `/events` stream. It names its screen
   (`/events?screen=sessions&s=<session>`) and the server sends that screen
   again when it changed, with the Desk count and the live label as
   out-of-band parts. There are no topic subscriptions here: another change
   adds them.
9. **No secrets on a page.** No token, node ID or join string is rendered. A
   session ID appears only in a link, as it did before.

10. **Changes.** A list and a detail, read with `changes_owner_find`, the same
    read the TUI Delivery tab uses. Filters are open, mine to do and all. A row
    has the title, id, revision, risk, the step strip, the status line and the
    age. The detail has each step with its state, owner and evidence note. It
    has no step action: no approve, claim or report from the web. A test holds
    that no such route or action kind exists. The change read carries no diff
    stat, so the detail shows one only when a step's evidence has it.
11. **Messages.** The conversation list (`conversations`) and the thread of the
    selected one (`conversation_messages`), as the TUI Messages tab reads them.
    System events and worker notices are dimmed. The composer uses the owner
    send path of the TUI (`owner_send`), the one that part 1's `send` action
    uses, so the message arrives marked as the owner in person. `reply` is the
    only new action kind. It takes the conversation id, the recipient session
    and the text. The recipient must be a side of that conversation, so a reply
    cannot be a message to any other session. **Decided: replies stay in the
    thread.** `owner_send` takes an optional `conversation_id`, which the daemon
    carries unchanged in `msg.send`. When an owner send has one, the gateway
    stores the message as a reply in that conversation, with no parent, only if
    the conversation exists and the recipient is one of its sides. Otherwise it
    refuses with the reason. A send without the field opens a new conversation as
    before. The page goes back to the same conversation. A worker row groups
    conversations, so the reply goes to the newest conversation of that group
    that has the chosen session as a side.
12. **Live.** `/events` carries the row (`s`) and, for Changes, the filter (`f`).
    Each screen reads only what it shows: Changes reads the change list and the
    selected change, Messages the conversation list and the selected thread. A
    live update does not wipe the composer while the owner writes. The thread
    keeps its scroll place across an update.

13. **Graph.** The screen draws the read the TUI Graph tab uses: `graph_view`
    with `places` and `all`, so team records and each record's place arrive in
    the same answer. It is one inline SVG built on the server. Team boxes carry
    the team name, its lead and the team mode. The owner, the CTO and the chief
    of staff (`chief_get`) sit above them, and records with no team sit in an
    "answers to you" box. A node shows needs you, busy, idle or offline from its
    sessions, and a team shows the highest state of its members. Depends edges
    are solid. Dashed edges come from the newest message of each conversation in
    the last hour (`conversations`), mapped from the two session IDs to their
    graph records. A click is a plain link (`?s=<node>`), so it works without
    JavaScript, and the detail card shows the name, harness and model, place,
    machine, step owners (the delivery assignment edges of the record or its
    repo) and charter purpose. The layout is deterministic: teams in columns,
    members in a grid of fixed cells, so labels do not overlap. At a phone width
    the SVG keeps its pixel width and scrolls inside `.graphwrap`; the page
    never scrolls sideways. The legend below the graph names the four states,
    the message edge and the depends edge. There is no edit action and no "Edit
    place" button: places and charters stay in the TUI.
14. **Workers.** Subscriptions read `usage` and merge the same provider, plan and
    window from every machine, newest reading first. Each row shows the percent
    left as a bar, the pace, the reset time and a protected chip from
    `protected_providers_get`. Model picks come from the registered profiles,
    `scores_get` and `worker_rotation_get`: model, harness, score, the T and S to
    V columns the model is in rotation for, and the used and done counts from the
    machines' track records. Running workers come from `worker_log` entries with
    status running, with their name, model, task, run time and tokens. The cost
    shows a dash because the worker log carries no cost for a run in progress;
    the screen does not invent one. The screen is read only: protecting a
    provider and starting or stopping a worker stay in the TUI.
15. **Live for both.** The same `/events` stream re-renders Graph and Workers.
    The Graph screen reads the graph, `chief_get` and the conversations of the
    last hour. The Workers screen reads `usage` and the worker snapshot. Neither
    screen reads the change list, the message text or the Desk lists.

16. **CTO panel.** The CTO button and the key `c` open a side panel on every
    screen. At 1180px and wider it is a third column; below that it is an
    overlay, and on a phone it is a full-height sheet. It stays open across
    screen changes, remembered in `localStorage` inside a try/catch and in a
    cookie of the same name so a new page arrives already open. The header shows
    the CTO's title from `cto_settings`, its harness and its model. The
    conversation is the TUI CTO pane's timeline, read with `cto_events`; an
    Older messages link asks for the page before the current offset, and Latest
    returns to the newest page. Owner lines say "you", CTO lines are styled as
    the CTO and harness actions are dimmed. The model's text is text: no HTML
    from it, markdown is not rendered, and line breaks stay. The composer sends
    with `cto_send`, the same call the TUI makes, so the words arrive from the
    owner in person; the daemon starts or wakes the CTO harness when it is not
    running. An open proposal from `proposal_list` is a card with Apply and
    Decline, answered with the TUI's owner action (`proposal_answer`, `a` or
    `x`). The card carries the proposal's machine as an encoded value, never a
    printed node ID. The panel does not start workers or change charters or
    settings; the CTO may do those when the owner asks it in the chat, exactly
    as in the TUI.
17. **Live for the panel.** `/events?section=cto&screen=<screen>&h=<offset>`
    is the panel's own section of the same stream. It sends the panel body as
    `event: cto` to its own id, so the screen and the panel update
    independently. A screen stream never reads the CTO timeline. An update is
    skipped while the owner is typing in the composer, and the panel keeps its
    scroll place.

## Not decided here

Starting workers, editing charters and the graph from the web, and push
notifications. Each needs its own decision.
