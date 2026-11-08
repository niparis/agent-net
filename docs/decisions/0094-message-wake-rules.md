# 0094 - Message wake rules: who is asked, and what a refused wake leaves

Date: 2026-11-08
Status: Built. Not yet reviewed by the owner. Amends the message part of
[0065](0065-wake-retired-claude.md) and the wake rules in
[0036](0036-primitives.md).

A message to an offline session should either wake it or tell the sender why
it did not. Two faults broke that:

- A queued message to an interactive Claude session with autonomy `off`, or to
  a Claude session whose mode is not known, fell back to the Claude background
  wake. That wake needs a background record, which a terminal session does not
  have. The machine refused with "record is missing", the gateway logged an
  error, and it tried again on every gateway restart.
- A finished background run of the same session left a second record behind.
  The interactive wake then refused the session, although nothing was live.

The rules now are:

- A queued message asks the machine only when the session is a Claude
  background session (its mode says so, decision 0065), or the agent's
  autonomy is `wake`. Ready step tasks keep their own rule, unchanged.
- A session that is not asked gets no attempt and no error. Its sender's
  receipt says `queued; <name> is offline and wake is off for this agent`.
- The interactive Claude wake refuses a record only when it is live, names
  another session, has no kind, or is a background record that did not finish
  or is in another folder. A finished background record in the session's folder
  does not refuse it.
- A wake that the machine answered with an error is recorded with its reason.
  A gateway restart does not retry it. The next message asks again, after the
  10-minute limit. Each sender with a queued message gets one agent-net notice
  with the reason, and later receipts say the last wake failed.
- A wake that never got an answer, because the machine's connection dropped,
  is not recorded. The reconnect asks again as before.

Why: a sender needs a plain answer to "why is my message still queued", and an
automatic retry of a refused wake only repeats the error. The owner's own resume
never checks records; the autonomy wake keeps the checks that stop a live
session from being started twice.
