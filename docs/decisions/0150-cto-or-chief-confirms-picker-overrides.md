# 0150 - The CTO or the chief may confirm a picker override

Date: 2026-11-12
Status: Approved by the owner on 2026-11-12 (reshape brief v2). It is
already saved as a standing order. Code change not built: step (a) of
[the plan](../design/owner-front-door-plan.md).
Amends [0141](0141-picker-override-needs-a-human.md).

## Context

Decision 0141 made every agent start that sets a selection field (harness,
provider, model, effort, not_family, machine) wait for a human. The start
goes ahead with the owner's own words, or after a Desk ask that the owner
answers. The CTO could answer "picker" but not "confirm". Under
[0149](0149-owner-front-door.md) the picker, quotas and cost are the CTO's.
The owner does not want these asks.

## Decision

1. A held override ask goes to the CTO (route in
   [0151](0151-automatic-message-routes.md)), not to the owner.
2. The CTO may answer `confirm` or `picker`. The chief may also answer
   either, for example when the CTO is not available.
3. The owner's own words in the session (`owner_words`) still start the worker
   at once, as in 0141.
4. Every confirm is logged with who confirmed it and the caller's reason, and
   counts in the CTO's digest.

## Consequences

- The rule that an agent cannot pick its own model without someone else
  agreeing still holds; that someone is now the CTO or the chief.
- 0141's line "The CTO may answer picker but not confirm" no longer applies.
