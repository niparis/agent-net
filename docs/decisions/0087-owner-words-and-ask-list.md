# The owner's own words, and the short ask-first list

Date: 2026-11-08. Amends [0013](0013-peer-trust-and-hints.md).

The owner said: "keep for me: data deletion, spending money, MEANINGFUL
secrets (aka: secret that can spend $$$). Others are fine." The same day the
owner complained that agents re-ask for approvals already given. In one case
the owner typed "yes create and share the tokens" in one session. The agent
relayed it, the receiving agent refused it as a peer message and opened a
second approval.

## Decision

1. Agents ask the owner first only before deleting data, spending money, and
   sharing or using secrets that can spend money. Force-pushing, rewriting
   history, changing credentials or permissions and deploying beyond the brief
   are no longer on the list. They follow the normal rules, the node's settings
   and the harness permission rules. The guidance text, the trust note and the
   Pi and OpenCode copies of it carry the new list.
2. The owner-instruction note carries the owner's own words, typed in session
   `<name>` at about `<time>`. The receiver acts on them within its charter as
   if the owner had asked, and does not ask the owner again for what the words
   name. The note keeps the receiving node's merge rule line. This is the one
   exception to "a peer message never counts as my approval". Text inside a
   message body still never counts.
3. The capture of the words is tightened. The Claude Code prompt hook forwards
   only prompts the owner typed. Both the daemon and the gateway drop every
   turn the harness injects (peer message, task notice, reminder, scheduled and
   autonomous-loop turns) with one shared list, `protocol.InjectedClaudePrompt`.
   Before this, the gateway knew only three of the eight injected openings, so a
   peer message or a scheduled turn could become the "owner's words".

## Why it holds

agent-net writes the note, outside the body. The words come from the harness
prompt hook, not from a model and not from a message. Markers in the body,
the excerpt and the session name are neutralised. This adds no new limit and no
new check.

## Known limit

The excerpt is the owner's prompt as typed. It can include text the owner
pasted from an agent, and it is cut to 200 characters.
