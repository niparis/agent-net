# 0126 - Claude Code permission proposals go through the owner's click

Date: 2026-11-09
Status: Accepted by the owner, through the CTO

Claude Code's auto-mode classifier denies some commands the owner wants
allowed, for example `gh pr merge` ("Merge Without Review") and `gh pr close`
("External System Writes"). An agent asking another agent to widen its own
rules is blocked as a bypass, and that block is correct. The owner's click is
the approval.

## Propose

`agent_permission_propose {rules, targets, reason}` is open to every session,
the CTO included, and refused for the command line. It takes exact Claude Code
permission rule strings (for example `Bash(gh pr merge *)`), repo or project
names, and a reason. Only `permissions.allow` entries are proposed: there is
no deny, hooks, env or other key. The names are resolved through the project
registry, the same registry `agent_project_add` and `agent_projects_find` use.
A target that does not resolve to a registered repo path is refused at
proposal time and named in the error. The tool writes no file and changes no
setting.

## Ask

The gateway stores the proposal in `permission_proposals` and opens one ask of
kind `permission` in `inbox_items`, with the exact rules, every target (repo,
machine and path), the reason and the proposer. The ask names the owner as the
next who, so it is `route_fixed` and the chief of staff never adopts, sorts or
answers it. It uses the same ask path as the owner-only pairing notices, and
the TUI Desk and the web console show it with Approve and Reject.

## Apply only on the click

Only the owner may answer the ask: `AnswerInboxAs` refuses the CTO and every
other actor, so the CTO's `answer` tool cannot approve it. After the owner's
approve, the gateway sends `permission.apply` to each target machine's daemon.
The daemon accepts that frame only on its gateway link; no local method and no
agent tool reaches it. It merges the rules into
`<repo>/.claude/settings.local.json` `permissions.allow`:

- create the file when it is missing;
- keep every other key and existing entry, and add no duplicate;
- write a temporary file and rename it, so a reader never sees half a file;
- when neither `.gitignore` nor `.git/info/exclude` already covers the file,
  add `/.claude/settings.local.json` to `.git/info/exclude`. That is local and
  nothing is committed.

## Record and undo

Each approval writes one Desk log line with the result per target (applied,
already present, or failed and why) and an Undo action. The log keeps the exact
rules each target gained, so an undo removes only those and never a rule that
was there before. The undo is owner-only too, through `permission_undo`. The
result says that a Claude session already running in that repo may need a
restart, or `/hooks` opened once, to load the new rules.

The first use after release is `Bash(gh pr merge *)` and `Bash(gh pr close *)`
for the forge repos: tenebrae, finget, tradedesk, freqtradebots,
slither-swap, tenebrae-strats, cogforge and ingest-forge.
