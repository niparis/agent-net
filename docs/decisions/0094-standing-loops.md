# 0094 - Standing loops

Date: 2026-11-08
Status: Accepted by the owner. Built.

## The problem

Nothing in agent-net runs on a clock. An agent works only when a person, a
peer or an idle check gives it a reason. The owner wants to leave an agent on a
long goal: wake it every few hours and ask it to keep working toward its
purpose.

## Decision

1. **Two settings on the node.** `loop_every` is a duration such as `4h`.
   Empty means no loop. `loop_prompt` is text; empty means the default. They
   are repo settings in the settings store (`loop.every`, `loop.prompt`), kind
   advice, so the owner and the CTO both set them with `node_settings_set`.
   There is no approval step.
2. **The prompt comes from the charter.** The default is
   `[agent-net] Loop: <what changed>. Your purpose is <purpose>. Check where you stand against it, do the next step, then announce what you did or what blocks you. If you need the owner, ask once with agent_owner_ask and carry on with work that does not depend on the answer; do not wait.`
   A set `loop_prompt` replaces the middle sentence. `<what changed>` counts,
   with ids, the new messages to the session, worker hand-backs to it, step
   tasks for it and its own Desk asks that opened or closed since the last
   sent tick, from tables the gateway already keeps. `loop_plan` (a relative
   path) adds `Read <plan>, do the next step, then update <plan>.`
   `loop_mode` is `always` (the default) or `on_change`, where a tick with
   nothing new is skipped and logged. The check is a few queries, not a model
   call.
3. **The expiry loop is the clock.** Once a minute it runs one indexed query
   for the nodes that have a loop. With none, it does nothing else. There is
   no goroutine per agent. A loop starts its clock when it is first seen, and
   the first tick comes one interval later.
4. **A tick goes to one session.** It is the repo lead, else the most recent
   live non-worker session. A busy session, a session waiting for its owner and
   a session under a usage limit are skipped. An offline session is woken
   through the wake-on-message path, which resumes the same session and never
   starts a new one. The loop is the owner's consent to that wake, so the
   autonomy rule is not needed. Pi cannot be woken: the tick is skipped and the
   log says so. A wake never opens a view (the herdr rule).
5. **Two safety stops reuse the wake rules.** The daily cap is the wake
   rule's 6 a day per session, counted with the idle-check wakes in
   `session_wakes`. Two ticks in a row with no progress pause the loop.
   Progress is a new announce, a new message, or a change in open work: the
   markers the wake rule uses, plus the announce. A pause or the cap opens one
   Desk ask with the options `resume` and `stop`. `resume` restarts the clock
   at once and clears the streak. `stop` clears `loop_every`. A cap pause also
   ends by itself on the next UTC day. The ask closes when the loop resumes
   or stops.
6. **Observability uses what exists.** Each tick writes one `desk_log` line
   (`sent`, `woke`, `skipped busy`, `skipped, cannot be woken`,
   `skipped, no session`, `skipped, nothing new`, `paused`). When the agent next announces, a
   following line `Loop <node> result: <announce>` is added. The log is append
   only, so the result is a following line, not an edit of the tick line. The
   Graph card shows one block, the Sessions tab marks the agent with `↻`, and
   the graph read and session views carry a `loop` object for the CTO.

## What it does not do

- It adds no dashboard, tab or per-tick notice to the owner.
- Tokens are shown only when known. agent-net reads token use for workers, not
  for ordinary sessions, so the card leaves them out today.
- A loop belongs to a repo node. It does not start a session, and it has no
  external triggers (feed cursors, systemd units): other agents wake the
  session by messaging it.

## Cost

A loop spends tokens on a clock. The cap of 6 wakes a day and the pause after
two idle ticks bound it. The owner can stop it from the Graph card or the Desk.
