# 0194 - Two worker start tools: the picker, and the owner's exception

Date: 2026-11-26
Status: Approved by the owner on 2026-11-26. Supersedes
[0151](0151-cto-or-chief-confirms-a-picker-override.md) and the owner choice
of [0191](0191-picks-keep-their-quota-read-and-the-owners-choice-holds.md).

## Context

The owner said:

> the worker is chosen by the picker. So there should be 2 separate tools and 2
> only: 1. Standard worker initiation (by difficulty brief) 2. Exceptional
> route, with full freedom, but requiring owner approval

He rejected per-project worker settings.

One tool mixed both routes. It took the selection fields (harness, provider,
model, effort, not_family, machine), and an agent that set them without the
owner's words was held for a human to confirm. In 14 days there were 27 held
asks: 26 were confirmed in about 19 seconds, and 20 were only about
`machine=research`. That is a rubber stamp, not a decision. The `owner_choice`
table never held a row.

The brief overlap check refused 100 starts. No start needed a forced third
try, and 62 percent of briefs carried the `overlap ok` escape. That escape
shows the refusal was in the way of normal work.

Start fields were validated three times: in the daemon operator, in the
gateway and again in the starting daemon. A model with no harness silently
became an OpenCode start. `continue` did not accept the start ids a caller was
given.

## Decision

1. `agent_worker_start` is the standard route. It has no selection fields: the
   picker chooses the harness, model, effort and machine. The brief, `continue`,
   `base`, `isolation`, `fixes`, `title`, project and the other non-selection
   fields stay. The CTO's `worker_change` start has no selection fields either.
2. `agent_worker_start_override` is the exceptional route. It takes the
   selection fields with full freedom. It always needs the owner's approval:
   his own words (`owner_words`) found in what he typed in that session start
   the worker at once; otherwise one ask goes on his Desk, not to the chief.
3. The chief no longer confirms a start. Decision 0151's confirm rule and its
   chief sort actions are gone. The owner answers the one ask, or the CTO
   answers it on his instructions (his Desk answer records that the CTO
   answered for the owner); the chief and every other session are refused.
4. Overlap no longer refuses a start. A file a running worker or an open change
   holds becomes one advisory `brief_flags` entry. The phrase `overlap ok:
   reason` still silences it. Rejection counting and the third-try brief defect
   are gone.
5. Start fields are validated in one place, the gateway, which knows the
   machine's model list. A model with no harness is resolved to a harness that
   lists it, or refused with one line. No silent OpenCode default.
6. `continue` accepts the session id, the worker id, or the start id a caller
   was given.
7. The gateway keeps the `worker_starts.owner_choice` column because a shipped
   migration is never rewritten, but no code writes or reads it.

## Consequences

- An agent that needs a model the picker did not choose asks the owner once,
  on his Desk. A routine start no longer waits for a human.
- A brief never fails for a shared file. The caller sees the overlap in
  `brief_flags` and can decide.
- A project does not select a model. The picker does, at every start.
