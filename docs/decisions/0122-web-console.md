# 0122 - The web console

Date: 2026-11-09
Status: Accepted by the owner (2026-11-09). Part 1 of 4 built.

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
     says "CTO chat arrives in part 4".
   - Part 2 is Changes and Messages (built, see items 10 to 12). Part 3 is Graph and Workers. Part 4 is
     the CTO chat panel.
4. **Read and answer only.** The console answers what waits for the owner and
   reads the rest. Its only `POST` route is `/action`, with these kinds and no
   others: `held` (`AnswerHeld`), `outcome` (`ResolveOutcome`), `inbox`
   (`inbox_answer`: approve, refuse, dismiss, an option or typed text), `mark`
   (`ask_mark`, "didn't need me"), `send` (a message as the owner to a
   session that waits) and `reply` (the owner's words in a conversation of the
   Messages screen). There is no route that starts or stops a session or a
   worker, changes a charter, a team, an edge or a setting. A test holds this.
5. **Asks the console does not answer.** A CTO proposal, a learned suggestion,
   a charter draft and a delivery step say "Answer from the TUI". An approval
   that needs the owner's consent page (internet access) and an approval that
   needs evidence fields can be dismissed on the web but not approved. The TUI
   opens the consent page and takes the evidence.
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

## Not decided here

Starting workers, editing charters and the graph from the web, and push
notifications. Each needs its own decision.
