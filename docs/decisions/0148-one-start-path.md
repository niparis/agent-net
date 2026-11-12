# 0148 - One start path: the daemon owns every session

Date: 2026-11-12
Status: Accepted by the owner, through the CTO ("Parts 2 and 3 of the approved session redesign as one brief: one start path ... then delete the old start paths")

## Problem

Each harness started its session its own way. Claude, OpenCode and Codex each
had a different answer to "who hosts the session", and a start often needed a
terminal: a herdr pane, or an approving TUI that ran the command for the
daemon. The owner's words: "each session is a special snowflake".

## Decision

The daemon owns every session as a background process, and a start needs no
terminal. Viewing is a separate, optional step: the view plan (decision 0142).

| Harness | Who hosts the session | Start | View |
|---|---|---|---|
| Claude | Claude's own background service | `claude --bg` through `startBackground` | `claude attach <job>` |
| OpenCode | the owner's `opencode service`, or a worker's private `opencode serve` | create the session over HTTP | `opencode --server <url> --session <id>` |
| Codex | the shared app server | `thread/start`, first message through the queue | `codex resume --remote unix:// <thread>` |
| Pi | its own headless herdr server | unchanged (decision 0140) | its herdr pane; Pi has no attach client |
| Antigravity | the `agy` process in its herdr pane | unchanged; the daemon types into the pane | its herdr pane |

Pi and Antigravity are the two allowed exceptions. Neither has a client that
can attach to a session it does not host.

### Move to background

A Claude session the owner started by hand in a terminal has no background
job. Enter on it no longer runs a second `claude --resume` client next to the
first. The plan has no command and sets `move_to_background`. The TUI asks
`Move <name> to the background?`. On yes the daemon ends the terminal client
and resumes the conversation with `claude --bg --resume <id>`. The agent-net
session ID stays (`rebindResumedSession`, as for any resume), so its workers,
changes and messages stay with it. On no nothing changes: the move is offered,
never forced. It works for a session on this machine only; for another machine
the note tells the owner to press Enter in agent-net there. The call is
`session_resume` with `move_to_background: true`.

### OpenCode

Enter on an owner OpenCode session without a running service used to run
`opencode --session <id>`, which hosts the session in a terminal of its own,
outside the daemon. It now gives a note (`opencode service start`). A test
fails if any plan runs `--session` without `--server`.

### Codex

The start creates the thread on the app server and sends the first message
itself. The refusal `no herdr pane and no approving TUI available to confirm
the start` is gone with the code that needed it.

### What was deleted

- the Codex start plan that asked the approving TUI to run `codex`;
- the `handed_to_tui` proposal state, `proposal_start_ack` and its operator
  method, `CTOStartAck`, the 60 second timeout, and the `start_plan`,
  `start_name` and `start_proposal_id` fields of the owner UI request;
- `notifyApprovedSessionStart`, the approver connection in the proposal
  location, `ptyproxy.Proxy.OnStart`;
- the three copies of the "show the new session" step of the Claude, OpenCode
  and Codex starts: they share `showStart`;

The daemon still closes a `handed_to_tui` proposal left by an older version at
startup, so none stays open for ever. That one statement can go once no such
row can exist.

## Not changed

Resume of an offline session already used the same launchers. The wake
fallback and workers already ran through `startBackground` (decision 0142,
part 2a). View tab reuse is a separate change.
