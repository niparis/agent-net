# 0147 - Each project has a bar of rules

Date: 2026-11-12
Status: Accepted by the owner ("ok so it's a self evolving list of project rules, stored in the gateway? ok good", then "lets go")

## Problem

Reviewers fail changes on rules the worker never saw: performance first,
append-only migrations, no scratch files, taken decision numbers. The rules
live in the reviewer's head or in memory files. About half of the accept
failures in agent-net broke a rule the caller knew and the brief did not say.

## Decision

Each project has a **bar**: an ordered list of short rules. This is the first
part. It stores the list and delivers it. Growing the list from review
findings is a later part.

- **Store.** The gateway keeps the rules per project in `project_bar_rules`
  (project, number, text, source, created time). IDs are B1, B2, ... and the
  number is never reused after a rule is removed (`project_bar_next` holds the
  next one). A rule is one line, at most 400 characters. The source is
  `seeded` (a lead filled an empty bar), `lead` or `owner` (the owner, and the
  CTO for the owner).
- **Not a charter field.** A charter field is one text value with no ID, no
  source and no per-rule history. The bar needs all three, so it has its own
  table. It reads through the graph: `agent_graph_read` shows `bar` on the
  repo node, and the TUI card shows a "Project rules" section.
- **Write.** `agent_bar_edit {project, add, edit, remove}` is one call that may
  add, edit and remove rules, so a lead seeds a whole bar at once. The
  project's lead, the owner session and the CTO may write; the owner and the
  CTO name the project. Every other session, and every worker (even a worker
  of the lead), only reads. The command line is refused.
- **Brief.** The worker brief header lists the rules after the shared rules:
  "Project rules (check your diff against each before you hand back; list each
  as met or not applicable in your report):". No rules, no section.
- **Steps.** The accept and review task texts list the same rules ("check the
  diff against each"). Other steps do not.
- **Seeding.** A project with no bar asks its lead once per version of
  AGENTS.md, the same way charter drafts ask (decision 0088): the text rides
  on the lead's turn, `charter_draft_asks` holds the hash with level `bar`.
  The lead drafts 5 to 15 rules from AGENTS.md and README.md and sends them
  with one `agent_bar_edit` call. The ask is not repeated until AGENTS.md
  changes, and never once the project has a rule.

## Not in this part

No finding tags, no counting of failures per rule, no link to the merger.
There is no cap on how many rules a bar holds; the brief and the steps list
all of them, so a lead keeps the list short.
