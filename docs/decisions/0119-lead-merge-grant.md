# 0119 - The owner can let a team lead merge its members' work

Date: 2026-11-09
Status: Approved by the owner on 2026-11-09 ("Lead merge grants: yes we need
that"). Built. Builds on [0100](0100-team-questions-go-to-the-lead.md) and
[0111](0111-blocked-members-reach-the-lead.md).

## The problem

The owner granted the forge lead (tenebrae) the right to merge its members'
work. A member (tenebrae-strats) still declined to hand its denied merge of
PR #136 to the lead. It answered that the merge was for the owner and the lead
under its own rules, and it escalated to the owner. The member could not see
the grant anywhere in agent-net, so it had no reason to hand the merge over.

## Decision

1. **A Team setting `lead_merges`.** `on` or `off`. The default is `off`. It is
   a column on `teams`, like `asks_to`. The owner or the CTO sets it with the
   team charter: `team_charter` with `attr: lead_merges` and `value: on` or
   `off` (empty means `off`). The CTO tool and `graph_change` accept it. It is
   recorded in the graph history and can be undone. `node_settings_get` on the
   Team name shows it.
2. **The grant is visible, never a gate.** A member of a team with
   `lead_merges=on` reads one line in its place block, with the real lead name:
   `Merges: your lead <lead> may merge this team's work on the owner's grant;
   hand a merge you cannot do to the lead.` The lead does not read this line
   about itself.
3. **The pre-call hint states the grant as a fact.** Before `gh pr merge`, the
   Claude Code hint hook says that the owner granted the lead merge rights for
   the team, and to hand the change over to the lead with `agent_message_send`.
   The hint is silent for a grant on a close or a workflow run. The
   `asks_to=lead` line is unchanged.
4. **No new limit.** Nothing is blocked by the grant. The member's own charter
   and the merge rules of the node still apply.

## Consequences

- A member no longer needs to escalate a merge its lead can do. It hands the
  merge to the lead and says so in its report.
- The grant is a rule, so only the owner or the CTO can change it. A member
  cannot set it.
- The owner's TUI does not set `lead_merges` yet. The CTO sets it.
