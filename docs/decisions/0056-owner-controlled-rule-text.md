# 0056 - Owner-controlled rule text

**Date:** 2026-11-04
**Status:** Accepted.

## Context

Worker briefs contain instructions that the hand-back protocol and approval
boundary depend on. The settings layer allowed the CTO to rewrite these as
advice. They must be rules, while the owner must still be able to choose their
wording for a repo. Arbitrary prose has no reliable strictness order.

## Decision

Mark the protected worker brief texts as rules. The owner controls network and
repo values; the CTO proposes changes or undo for owner approval. A rule text
with `stricter: owner` takes the owner's repo value in preference to the network
value. It cannot use session or run levels. A network wording change does not
rewrite an owner-approved repo wording.

The hand-back tool name and completion tokens are code-owned framing. Text
settings declare required slots, and the renderer inserts the fixed tokens.
Values missing a required slot are ignored or refused. Default worker briefs
remain byte-identical.

This is a narrow exception to the ordered rule comparison in the settings
layer design. It does not give an agent authority to change rule text.
