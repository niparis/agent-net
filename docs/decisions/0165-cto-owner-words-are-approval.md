# 0165 - What the owner asks the CTO for is already approved

Date: 2026-11-14
Status: Approved by the owner on 2026-11-14 ("go T5").

## Problem

The owner asked the CTO for a change and then had to approve the same change
again on the Desk. He has complained about this many times. In his words on
2026-11-14: "If i ask for something to CTO it is already approved. The ONLY use
case of desk if an approval coming from the tree, and when reaching CoS it's
not willing or able to make a decision."

Five CTO actions waited on a Desk proposal even when he had just asked for
them: `node_remove`, `agent_tombstone`, `repo_archive`, `machine_remove` and
`machine_rotate`. A CTO permission proposal always opened an owner ask. A
session start, resume or wake the CTO did raised a trust ask. An app consent
the CTO opened waited for his click.

## Decision

The CTO can pass `owner_words`: a quote of what the owner typed in the CTO chat.
The daemon checks the quote against the owner's prompts in that chat in the last
3 hours (the check worker starts already use, `ownerWordsMatch`). The daemon
keeps the prompts from the CTO's `cto_send` and from the Claude Code prompt hook
of the CTO's own pane. The CTO cannot feed its own words into this memory: its
`cto_send` through the operator tool is not recorded.

- A quote that is there is verified. The change runs at once and no Desk item is
  filed. The words go into the Inbox line, with the undo where one exists.
- A quote that is not there is refused with one line. Nothing runs.
- A call with no words behaves as before.

What verified words do:

- Permissions: `permission_apply` applies at once. The gateway now refuses an
  apply whose words the daemon did not mark verified.
- The five actions run at once. They go through the same stored proposal as a
  Desk approval, decided in the same call, so the checks, the actor and the
  audit are the same and no ask ever opens.
- Folder trust: a session start trusts the folder first. No trust ask is
  raised. A resume does not check trust on the machine, so there is no ask to
  remove there.
- App consent: `app_ask` approves the consent it opens. The browser sign-in is
  still the owner's own step.

The Desk stays for approvals that come from the tree and reach the chief of
staff, and for the CTO's own actions that have no owner words behind them.

## Not covered

A wake is started by the gateway when a message arrives, not by a CTO tool, so
no words reach it. A trust ask from a wake still goes to the Desk.
`agent_permission_propose` is shared with every agent and has no `owner_words`
field; the CTO uses `permission_apply`.
