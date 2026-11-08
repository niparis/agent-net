# 0091 - CTO folder trust and standing orders

Date: 2026-11-08
Status: Accepted by the owner, through the CTO.

## The problem

The owner said: "I'm not the errand boy. When I speak you must do. When you
can't do, you ask agent-net to build." Two gaps stopped the CTO from doing what
the owner asked.

1. The CTO could not trust a project folder for Claude Code. `session_change`
   action `trust` only sets peer trust for a session, and a CTO proposal cannot
   set `trust_claude`. The owner had to type the trust answer in the TUI.
2. The CTO had no place to keep the owner's lasting instructions. Each new
   conversation started without them.

## The decision

**Folder trust by the CTO.** `session_change` action `trust` with `project` or
`path` (and `node_id`) trusts that folder for Claude Code on that machine.
`session_start` with `trust: true` trusts and starts. The CTO sets trust only
through these two explicit forms. The proposal rule stays: a proposal still
cannot set `trust_claude`. A project must be registered on the machine and a
path must be an existing folder under the owner's home (the rule a typed path
already has). The request to another machine uses new owner frames, checked at
the gateway and again at the machine.

Each use is audited with an Inbox notice. The notice has an undo only when
agent-net added the trust, so the undo never removes trust the owner set.

This changes who can set Claude Code trust. It was only the owner at the
keyboard. It is now also the CTO, on the owner's instruction, audited and
undoable. No approval gate was added, because the owner did not ask for one.

**Standing orders.** The gateway keeps the owner's standing orders. The CTO
tool `cto_standing_orders` saves, reads, lists and removes them. They go into
the CTO's first turn of every conversation. The owner sees, adds and removes
them in Settings, with an undo for a removal. Each order has text, a time and a
source, `owner` or `cto`.

## What we gave up

Trust is a security setting and the CTO can now set it for any registered
project or home folder on any enrolled machine. We accept this because the
owner asked for it, the use is audited and the undo is exact.
