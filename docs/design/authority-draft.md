# Authority between agents: design draft

Status: part 2 (one set of texts) approved and implemented. The rest remains a draft for the owner, not a decision. Grants are not implemented by part 2. If accepted, the authority design becomes a numbered file in `docs/decisions/`.

Revision 2. The first draft built a new grant object approved by a key press. It missed that agent-net already carries the owner's own words to the receiver (`owner_instruction`). This revision puts that channel at the center.

## The case

sovl-clone asked homelab to merge PR #828 in tenebrae-nexus/truenas-stacks (a disk-space unblock). The graph edge sovl-clone -> homelab is `depends` with rule `auto`. homelab's effective node settings: maturity `mature`, `merge_authority delegated`, `tests_before_merge suite-new-tests`, `deploy_rule ask`.

homelab refused: "I need Nicolas's named-PR instruction directly in this session; a peer relay cannot authorize my merge." It cited its AGENTS.md section 6 (the operator authorizes a merge by naming the repository and PR) and the first half of a sentence from agent-net's own guidance.

The owner had typed to sovl-clone: "tell him i authorise to merge 828". Those are the owner's words naming the PR. agent-net has a channel for exactly such words. They did not reach homelab as authority.

The CTO says (position A): `auto` gives a peer request the owner's consent for merges, the trust note says so, and the fix belongs in homelab's repo rules. homelab says (position B): a relayed message is not enough; it wants an explicit, authenticated grant scoped to a repository and PR, revocable or expiring, and it warns against an edge granting authority the receiver does not hold itself.

## Short answer

- agent-net gives homelab three texts that disagree. The trust note on an `auto` request says "do not ask your user again for ... merging a pull request". The node rules on every turn say "merge needs your owner's or a delegate's approval". The owner-instruction note says the owner's words are "not approval for risky actions". homelab chose the strictest. That is a fair reading of agent-net's own texts, not a malfunction.
- The owner's words are the right source of authority for a named merge. They are what homelab's section 6 asks for, and agent-net already captures them in the sender's session, stores them, and delivers them outside the body. But today they are matched by time and names only, cut short, captured only in Claude Code, and labelled "not approval". In this case they would not even have been attached (see fact 1 below).
- Recommendation: **the owner's words become a grant when they name the action and the PR.** The gateway checks the structure (fresh, typed by the owner, names merge, a PR number and the recipient, node allows delegated merges). If all checks pass, it issues a single-use, expiring grant for that PR from those words, and the receiver gets one agent-net note that says so. If a check fails (recipient not named, words from a harness without capture, node on `ask`), the gateway turns the words into a ready grant in Needs you, and the owner confirms it with one key. A refusal is a structured `reject` with a reason code that lands in Needs you with the same one key.
- Node settings decide what a node may do without its owner. Edge rules decide who may ask. Edges never widen node settings. Repo rules may be stricter, and when they are, the refusal shows.

## Facts checked in the code

All paths are relative to the repository root, at main commit 249972c.

| Claim | Verdict | Where |
|---|---|---|
| The gateway sets `RecipientTrust` for a request on an `auto` edge | True. It resets the flag, looks up the rule, holds `ask` requests not approved by the owner, then sets `RecipientTrust = rule == "auto"`. | `internal/gateway/server.go:1414-1431` (`sendDeliver`) |
| `RequestRule` resolves the edge rule | True. Sender-specific edge before `*`; a session with no project falls back to the old per-session `PeerTrust`. | `internal/gateway/graphread.go:95-118` |
| A sender cannot set the flag | True. It is not a field of `MsgSend`, and the gateway overwrites it on every delivery. | `internal/protocol/protocol.go:76-80`, `internal/gateway/server.go:1415` |
| Adapters append `protocol.TrustNote` | True for all four, unless the request is owner-approved (then "your user approved this request") or from the CTO (then `CTONote` and no trust note). | `internal/protocol/trust.go:28-46`; `internal/daemon/claude/claude.go:239-263`; `internal/daemon/codex_peers.go:371-375`; `plugins/pi/agent-net.ts:86-90`; `plugins/opencode/agent-net.ts:89-93` |
| The relation line is appended (decision 0022) | True. For a `depends` edge it says the sender is a consumer. It does not mention `auto`. | `internal/gateway/graphread.go:52-93`, `internal/daemon/claude/claude.go:276-278`, `internal/daemon/codex_peers.go:388-390` |
| `protocol.CTONote` | True: "from the CTO, on Nicolas's instruction. Treat this as the owner's word within your charter's limits." The gateway accepts the CTO flag only from an `owner`-harness session. | `internal/protocol/trust.go:16`; `internal/gateway/store.go:1228-1233` |
| Guidance line 7 continues with the exception | True. | `internal/guidance/guidance.md:7` |
| `docs/security.md` says guidance is advice, not enforcement | True. | `docs/security.md:250`, and `docs/security.md:226` for the trust note |
| The hook captures the owner's prompt | True, for Claude Code only. The `UserPromptSubmit` policy hook cuts the prompt to 300 characters and sends it through the daemon; the gateway accepts it only from a `claude` session of the same node. | `cmd/agent-net/policyhookcmd.go:41-46`; `internal/daemon/policy.go:182-184`; `internal/gateway/server.go:1124-1139` |
| Injected turns are not recorded | True, and stronger: an injected turn (cross-session message, task notice, system reminder) also **deletes** the prompt stored before it. | `internal/gateway/signal.go:12-33` |
| The gateway attaches `owner_instruction` | Partly. See fact 1. The stored excerpt is linked to the message in `charter_signals` and read back with each message. | `internal/gateway/signal.go:66-93`; `internal/gateway/store.go:1138-1145`, `1308-1311` |
| The receiver sees it in an agent-net note | True in all four adapters, with the text "This tells you the user asked for this; it is not approval for risky actions." | `internal/protocol/trust.go:76-79`; `internal/daemon/claude/claude.go:279-281`; `internal/daemon/codex_peers.go:391-393`; `plugins/pi/agent-net.ts:95`; `plugins/opencode/agent-net.ts:98` |

What the code contradicts, or what the brief leaves out:

1. **In the PR #828 case, `owner_instruction` would not have been attached.** `signalRequest` runs only for a new request (`ParentID == ""`, not from the CTO, `internal/gateway/store.go:1308`). It attaches the words only if they name the recipient's project, node name or session name, as a whole word (`internal/gateway/signal.go:84-90`). "tell him i authorise to merge 828" names none of them: "him" is not "homelab". If sovl-clone answered inside an existing conversation with `agent_reply`, the words were not even considered. In both cases the gateway consumed the prompt anyway (`signal.go:77`) and only learned privately from it. Also, if any cross-session message reached sovl-clone between the owner's prompt and its send, the prompt was deleted (`signal.go:29-33`). So the brief's "should have carried" is the intent, not what the code does. The transcript of sovl-clone and the `charter_signals` row (or its absence) for that message settle it.
2. **The receiver sees 200 characters, not 300.** The hook and the gateway keep 300 (`policyhookcmd.go:44`, `signal.go:35`), but the message read cuts to 200 (`store.go:1138`, `substr(cs.prompt,1,200)`). Documented as 200 in `docs/reference.md:1218` and `docs/security.md:498`.
3. **CTO sends never carry the owner's words.** The capture accepts only `claude` sessions (`server.go:1137`), the CTO sends from an `owner`-harness session, and `signalRequest` skips CTO sends (`store.go:1308`). The CTO note says "on Nicolas's instruction" with no words behind it.
4. **The owner-instruction note text has no cross-language test.** The Go text is in `internal/protocol/trust.go:78`; Pi and OpenCode repeat it by hand (`plugins/pi/agent-net.ts:95`, `plugins/opencode/agent-net.ts:98`). Unlike `TrustNote`, no test compares them.
5. **Decision 0030 rules out authority from this channel on purpose.** Its scope says the rules "do not infer authority from peer messages ... or guarantee that a prompt and send belong to the same turn" (`docs/decisions/0030-graph-learns-from-instructions.md:24`), and `docs/security.md:496` says a match "does not prove the owner intended that particular send". This design changes that for one narrow case; the change needs its own decision file.
6. **agent-net contradicts itself on a mature node.** `DefaultsForMaturity` gives `mature` the setting `merge_authority delegated` (`internal/protocol/maturity.go:89-97`). `MergeText` turns that into "merge needs your owner's or a delegate's approval" (`internal/protocol/maturity.go:186-187`). `MergeApprovalFor` defines delegates as architects on a `governs` edge (`internal/protocol/maturity.go:244-270`). `RulesText` (`internal/protocol/maturity.go:221`) puts this into homelab's context every turn (`internal/protocol/graph_context.go:47`). Decision 0022 says `auto` acts "as if the owner had asked" (`docs/decisions/0022-component-graph-and-cto.md:71`) and "the owner is above all edges" (line 74), but never says which text wins.
7. **The trust note speaks of the session, not the edge.** It says "Your user has allowed this session to act on requests from their other agents". On an `auto` edge the consent covers one sender node.
8. **Delivered trust is not recorded; the owner's words are.** `RecipientTrust` and `RecipientRelation` are computed at dispatch and never stored. The `messages` table has no column for them, `scanMessage` does not read them (`internal/gateway/store.go:1136-1150`), and the only event is `msg.stored` with type, recipient and size (`internal/gateway/store.go:1347-1351`). So `agent_messages` never shows `recipient_peer_trust` (the JSON name; there is no `recipient_trust` field). `agent_get` shows a session, not a message (`internal/mcpserver/mcpserver.go:179`). The only evidence for the trust note is the current edge plus `graph_history` (`internal/gateway/store.go:160`) and the receiver's transcript. The owner's words, by contrast, are stored with the message ID for 90 days and come back as `owner_instruction` in `agent_messages`. They are already the best-recorded authority fact in agent-net.
9. **Two edges of different kinds between the same pair** resolve by alphabetical `kind` (`internal/gateway/graphread.go:113`, `internal/gateway/store.go:1276-1281`), not by the most restrictive rule. Not the cause here, but a trap.
10. **Grant and refusal seeds exist.** `agent_request_approval` (`internal/mcpserver/mcpserver.go:202`) makes an `approval` inbox item; on approval the gateway sends a notification with a free-text `approval_note` (`internal/gateway/store_inbox.go:124-130`, `internal/protocol/maturity.go:292`). `agent_reply` takes an outcome with a reason (`internal/protocol/outcome.go:9-45`), and a `reject` becomes a `rejection` inbox item (`internal/gateway/inbox_outcome.go:8-16`).

## The two positions, weighed

**A is right** that decision 0022 meant `auto` to carry the owner's consent, and that the trust note says so. **A is wrong** that the fix belongs only in the receiver's rules: agent-net itself tells homelab, every turn, that a merge needs the owner or a delegate, and it tells homelab that the owner's words are not approval.

**B is right** that a relayed message carries no authenticated, scoped consent and that an edge should not hand out more than the receiver's own rules allow. **B is wrong** that only words typed into homelab's own session count. homelab cannot tell who typed a line into its session either. The owner's words typed into sovl-clone, captured by a hook outside the model, stored by the gateway and delivered outside the body, are the same evidence, carried further. What B needs is that agent-net say so in one clear, checkable note, scoped to one PR.

## Recommended option: the owner's words become a single-use grant

### The authority model in plain words

1. The owner is above everything. The owner gives authority in two ways: by typing words in a session, or by a key in the TUI (or phone).
2. **Node settings say what a node may do without asking.** `merge_authority`, `deploy_rule` and `tests_before_merge` live in the gateway, set by the owner. They describe the receiver, not the sender.
3. **Edge rules say who may ask and how.** `never` refuses, `ask` holds for the owner, `normal` delivers for the receiver to judge, `auto` delivers with the owner's standing consent for the low-risk tier: commits, branches, pull requests, and merges **only where the node's `merge_authority` is `full`**. An edge never widens a node setting.
4. **A grant is the owner's consent to one exact action.** On a `delegated` node, a merge needs a grant. A grant comes from the owner's words (when they pass the checks below) or from a key press. It names the action, the repository, the PR and the node that may ask. It is single use and expires.
5. **Repo rules (AGENTS.md) may be stricter, never looser.** When they block what the graph allows, the receiver refuses with a reason code and the owner sees it.
6. **The denied tier never moves.** Deleting data, force-pushing, rewriting history, spending money, sharing secrets, changing credentials or permissions, and deploying beyond the brief need the owner every time. No edge and no merge grant covers them. A grant carries no credentials and skips no tests: the receiver still runs `tests_before_merge` and the repository's own checks.
7. **Enforcement stays where it is real.** The gateway enforces delivery, grant issue, scope, expiry, single use and the record. The merge itself is enforced only by the repository host (branch protection, `docs/security.md:202`). agent-net's notes stay advice to the model.

Which wins when they disagree, strongest first:

```
owner (words that pass the checks, or a key press)
  > denied tier (never delegated)
  > receiver repo rules, if stricter      -> refusal with reason "repo_rule", visible in Needs you
  > node settings (merge_authority, deploy_rule)
  > edge rule (auto / normal / ask / never)
  > charter text (advice)
```

### When the owner's words carry authority

The gateway issues a **words grant** at send time, without asking anyone, only when all of these hold:

| Check | Why | Today |
|---|---|---|
| The words were typed by the owner in a harness that captures prompts | Only a hook outside the model can tell typed words from model output | Claude Code only (`server.go:1137`) |
| No injected turn came between the words and the send | An agent message in between may be what the send answers | Already true: the prompt is deleted (`signal.go:29-33`) |
| The send is within 10 minutes and is the first send after the words | Limits reuse of one sentence for many sends | Already true (`signal.go:77-83`) |
| The words name the action (`merge`) and a PR number | The grant scope comes from the owner, not from the sender's body | New: a small pattern check, `merge` near `#?\d+` |
| The words name the recipient's node, project or session, or the repository | Proves the owner meant this receiver, not just "him" | Already the attach rule (`signal.go:84`); the repository is new |
| The words hold no negation near the action ("don't", "do not", "not", "never", "wait") | The gateway does not parse intent; on doubt it asks | New |
| The receiver node's `merge_authority` is `delegated` | `full` needs no grant; `ask` always wants a key | New |
| The repository is known | A PR number alone is ambiguous | New: from the words, or the receiver node has exactly one repository |

When some checks fail but the words do name merge and a PR, the gateway does not drop them. It makes a **ready grant** in Needs you: "Your words to sovl-clone: «tell him i authorise to merge 828». Grant homelab merge of truenas-stacks#828? (g = grant, x = no)". The request is delivered as today, with a note that a grant is waiting for the owner. When the owner presses `g`, the gateway sends homelab a notification with the grant note. This covers "him", replies inside a conversation, an `ask` node and an unknown repository with one key press, and no retyping.

Nothing changes for words that do not name an action and a PR. They keep the current note, reworded (see below).

### How words grants relate to key grants

One `grants` table, two sources:

| Field | Meaning |
|---|---|
| `id` | `g_...`, made by the gateway |
| `source` | `words` (issued at send) or `key` (owner pressed `g`) |
| `words` | the owner's excerpt, for `words` and for a key grant made from words |
| `action` | `merge` now; `deploy` later with the same shape |
| `node` | the receiver node (homelab) |
| `repo` | tenebrae-nexus/truenas-stacks |
| `ref` | 828 |
| `from_node` | the node whose session carried the words (sovl-clone); the grant is valid only on its requests |
| `message_id` | the send that carried it |
| `issued_at`, `expires_at` | default 4 hours for `words`, 24 hours for `key` |
| `used_at`, `revoked_at` | single use; the owner can revoke until it is used |

A words grant has no head commit: the owner named a PR, not a commit. The receiver records the merged commit when it uses the grant. A key grant made from a `needs_grant` refusal can carry the head the refusing agent reported, and goes stale when the head moves.

How the receiver tells a grant-bearing request from an ordinary one, without trusting the body: the grant is a message field that senders cannot set, like `RecipientTrust`. The adapter writes one note after the body (see the new texts below). The marker rule already stops a body from forging it (`internal/protocol/trust.go:48-56`). For a second check, `agent_grant_check {grant_id}` asks the gateway; `agent_grant_use {grant_id, merged_commit}` marks it used.

### Weaknesses of the owner's words, and what covers each

- **The words are the last prompt before the send, not proof the owner meant this recipient.** Covered by the recipient-name check for a words grant, and by the owner's key for everything else. The Graph will show where the owner's words travelled (gold links, being built now), so a words grant that went somewhere odd is visible after the fact.
- **The cut.** The gateway keeps 300 characters and delivers 200. A grant must be derived from the stored 300, and the note must quote the clause that names the action and PR, not a blind first 200. If the action or PR falls past 300, no grant: the owner sees a ready grant only if the visible part names them.
- **Only Claude Code captures prompts.** A Pi, OpenCode or Codex sender cannot produce a words grant. Its owner gets the key path through the receiver's `needs_grant` refusal. Adding capture to Pi (input events) and OpenCode (chat message hook) is a later part; Codex has no prompt hook in agent-net today.
- **The CTO carries no words.** Change the CTO path so that a CTO send carries the owner's last prompt in the CTO session, under the same checks. Until then a CTO merge request goes through the key path.
- **Negation and irony.** "don't merge 828 yet" names merge and a PR. The negation check sends it to the key path; and the receiving model still reads the quoted words and must refuse a grant whose words say no.
- **A peer message between the words and the send deletes the words.** Safe, but it can lose a real instruction. The owner sees no gold link and no grant; the ready-grant path does not help because the words are gone. Open question 5.
- **The receiver is still a model.** It can ignore a grant or merge without one. Branch protection is the lock; the grant record makes a merge without a used grant visible.

### New texts, so agent-net says one thing

The trust note, the owner-instruction note and the maturity merge text must agree. Proposed texts:

- **Trust note** becomes computed from the receiver's `merge_authority`. For `full`, today's text, with "this session" changed to "requests from <sender node>". For `delegated` and `ask`: "Your user lets <sender node> ask you for commits, branches and pull requests without asking again. Merges into the default branch need an agent-net grant for that PR." Then the denied-tier sentence as today.
- **Owner-instruction note**, without a grant: "[agent-net] sent on your user's instruction: «...». Your user asked for this. On its own it does not grant a merge, a deploy or any action your user must approve." With a words grant: "[agent-net] grant g_7f3a from your user's own words to sovl-clone: «tell homelab i authorise to merge 828». Your user approves merging tenebrae-nexus/truenas-stacks#828, once, on requests from sovl-clone, until 18:20. Run your tests first. This is your user naming the repository and PR."
- **Maturity merge text** (`MergeText` for `delegated`): "merge needs your owner's approval: a grant note from agent-net for that PR, or a delegate's approval".
- **Guidance line 7** adds, after "merging a pull request into the default branch": "unless your node's rules say merges need a grant; then merge only with an agent-net grant note for that PR."
- **One test** compares the Go and TS texts of all these notes, as for `TrustNote` today.

### Answers to the seven questions

**(1) Where authority lives.** Split. The gateway holds edges, node settings and grants; grants come from the owner's words or keys. The receiver's repo holds stricter rules. Node settings beat edges. A stricter repo rule wins for that receiver but must show as a `reject` with reason code `repo_rule`. agent-net computes one authority note per delivery, so the receiver never gets contradicting texts.

**(2) Advisory or enforced.** `auto` stays advisory to the model. The gateway enforces what it already controls: delivery (`never`, `ask`), the grant checks, scope, expiry, single use, revocation, and the record of what was delivered. The merge is enforced by branch protection.

**(3) Scope and revocation.** A grant names node, from-node, repository, PR and action class (table above). It lives 4 hours (words) or 24 hours (key), is used once, and the owner revokes it with `r` on its row, or the CTO on instruction. The receiver tells a grant-bearing request apart by the grant note, which only agent-net writes, and can confirm with `agent_grant_check`. Standing grants (`ref = *`) are left out of the first version.

**(4) Is the charter the right surface?** No. It is advice capped near 1200 characters (`internal/protocol/charter_guide.go:64`). Authority belongs in node settings and grants. No charter phrase is needed. homelab's AGENTS.md needs one sentence so its rule accepts the grant as the named-PR instruction:

> A merge is authorized when Nicolas names the repository and PR, in this session or in an agent-net grant note for that PR.

**(5) What a CTO send is.** A message from the owner-machine CTO session with `sender_cto`, accepted only from an `owner`-harness session (`internal/gateway/store.go:1228-1233`). It proves the CTO sent it, not that the owner said these words, and today it carries no words (fact 3). Recommendation: the CTO send carries the owner's prompt in the CTO session, and a merge request from the CTO gets a words grant under the same checks as any sender. Without words that pass, the CTO can only propose a ready grant for the owner's key. The grant, not the CTO note, carries merge authority, so the receiver has one rule.

**(6) Detecting a repo rule that contradicts the graph.** Add a reason code to the `reject` outcome: `needs_grant`, `repo_rule` (quote the rule in `reason`), `denied_tier`, `charter`. A `reject` already becomes an inbox item. For `needs_grant` and `repo_rule`, the Needs you row gets the one key `g`, which makes a key grant for exactly that request, prefilled from the owner's words if any were linked, and redelivers. A `repo_rule` refusal on an `auto` edge also marks the edge on the Graph, so the owner fixes the edge or the repo rule once. The guidance asks sessions to refuse with an outcome and reason code, not a plain reply.

**(7) Migration and compatibility.**
- Existing edges keep their rules. Only change in meaning: `auto` no longer implies merge on `delegated` and `ask` nodes, which is what `RulesText` already told them. On `early` and `growing` nodes (`merge_authority full`) nothing changes.
- New message fields (`grant_id`, `grant_note`) are `omitempty`. An older adapter ignores them, shows the owner's words with the old note, and refuses as today: safe. The gateway does not issue a grant to a receiver whose daemon does not report the capability; Needs you says "homelab runs an older agent-net; update it".
- Record the delivered authority: `recipient_peer_trust`, relation, effective rule and `grant_id` per delivery, shown in `agent_messages` and a `msg.delivered` event. Older rows show "not recorded".
- `agent_request_approval` with scope fields becomes a key grant; free-text approvals stay as they are.
- A new decision file supersedes the "no authority" line of decision 0030 for this one case.

### Sequence sketch: the PR #828 case under this design

```
owner        sovl-clone (Claude)     gateway                         homelab
  |                |                    |                               |
  | "tell homelab i authorise to merge 828"                             |
  |--------------->| hook: signal.prompt|                               |
  |                |------------------->| store words (300 chars, 10m)  |
  |                | agent_send request |                               |
  |                | "merge truenas-stacks#828"                         |
  |                |------------------->|                               |
  |                |                    | edge auto; homelab delegated  |
  |                |                    | words: owner typed, fresh,    |
  |                |                    |  first send, names homelab,   |
  |                |                    |  "merge", "828", no negation, |
  |                |                    |  homelab has one repo         |
  |                |                    | issue grant g_7f3a (words,    |
  |                |                    |  single use, 4h)              |
  |                |                    | deliver with grant note       |
  |                |                    |------------------------------>|
  |                |                    |              agent_grant_check|
  |                |                    |<------------------------------|
  |                |                    | valid, unused                 |
  |                |                    |------------------------------>|
  |                |                    |          run suite + checks   |
  |                |                    |          gh pr merge 828      |
  |                |                    |   agent_grant_use g_7f3a, sha |
  |                |                    |<------------------------------|
  |                |      reply: merged at <sha>, grant g_7f3a used     |
  |                |<---------------------------------------------------|
  | Graph: gold link sovl-clone -> homelab; grant used                  |
```

With the owner's real words, "tell him i authorise to merge 828", the recipient check fails ("him"). The request is delivered with "a grant from your user's words is waiting for your user", and Needs you shows "Grant homelab merge of truenas-stacks#828 from your words? g". One key, then the gateway sends homelab the grant note and the flow continues at `agent_grant_check`.

### What a legitimate refusal looks like

homelab, with a valid grant, still refuses when the tests fail, the PR changed in a way the words could not cover, or its repo rule demands more. It answers with an outcome, not free text:

```
agent_reply {
  message_id: "m_...",
  outcome: "reject",
  reason_code: "repo_rule",
  reason: "AGENTS.md section 6: tests must pass on the merged head; PR #828 fails test_backup_paths.",
  recommendation: "Fix the test in sovl-clone's branch and ask again; grant g_7f3a stays unused until 18:20."
}
```

The owner sees it in Needs you with the request one key away. A refusal without a reason code is still accepted and shows as "unstructured refusal".

## Alternative 1: key grants only (revision 1 of this draft)

Ignore the owner's words for authority. Every merge on a `delegated` node needs the owner's key in Needs you.

- For: one path; no pattern checks on natural language; never mistakes a sentence.
- Against: the owner already said it. Asking again is the friction that made the CTO want `auto` to cover merges, and it trains the owner to press `g` without reading. It also leaves the owner-instruction note saying "not approval", so the contradiction remains for words.
- Fair use: the fallback whenever a words check fails. The recommended option contains it.

## Alternative 2: edge-level merge scope (closest to A)

Mark an `auto` edge with an extra scope `merge`. Requests on that edge may merge without a grant.

- For: no round trip, no words needed.
- Against: a standing grant with no PR and no expiry, which is B's main objection. Prompt injection in sovl-clone becomes merges on homelab that the owner never sees.
- Fair use: later, as a short window built on the same grant row with `ref = *`.

## Alternative 3: agent-net performs the merge

`agent_merge {grant_id}` run by the daemon with its own token. Real enforcement, but agent-net would hold git credentials for every repository and widen the blast radius of a compromised machine (`docs/security.md:475`). Not recommended.

## What changes, by component

| Component | Change |
|---|---|
| `internal/protocol` | `Grant` type; `Message.GrantID`, `GrantNote`; reason codes on `ReplyOutcome`; `AuthorityNote` takes the receiver's merge authority; new owner-instruction and grant notes; new `MergeText` for `delegated`; one test comparing Go and TS texts |
| `internal/gateway` | `grants` table; words checks in `signalRequest` (action, PR, repo, negation, merge authority) and words grant issue; ready grant inbox item; key grant from `needs_grant`/`repo_rule`; check, use, revoke, expiry; deliver the quoted clause, not a blind 200 characters; store delivered trust, relation, rule and `grant_id`; `msg.delivered` event; CTO sends carry the CTO session's owner prompt |
| `internal/daemon`, adapters (Claude, Codex, Pi, OpenCode) | write the grant note; capability flag; later: prompt capture in Pi and OpenCode |
| `internal/mcpserver` | `agent_grant_check`, `agent_grant_use`; `reason_code` on `agent_reply` |
| `internal/tui` | Needs you rows for ready grants and `needs_grant` refusals with words, repo, PR, expiry; `g` grant, `x` no, `r` revoke; Graph marks edge/repo-rule conflicts and grants on the gold links |
| `internal/guidance/guidance.md` | one clause in line 7; refuse with outcome and reason code |
| docs | `docs/security.md` (instruction signals section: words grants; what is enforced), `docs/reference.md` (fields, tools, the 200/300 limits), new decision file that supersedes part of 0030 and points from 0022 |
| homelab's repo (owner's call) | the one AGENTS.md sentence in answer 4 |

## Risks and blast radius

- **A words grant is issued without a key press.** A wrong match means a merge the owner did not mean. Mitigations: all checks must pass, the grant is for one PR on one node, single use, 4 hours, revocable, shown on the Graph and in the receiver's note, and tests still run. Worst case: one unwanted merge of a PR that passed its tests, on a node the owner marked `delegated`.
- **A compromised sender session cannot forge words.** The words come from the hook, not the model. But a compromised machine can call `signal.prompt` for its own Claude sessions (`server.go:1124-1139` trusts the enrolled daemon), so it can forge words for senders on that machine. Same class as forging edge changes today (`docs/security.md:375`). Grants make it a higher-value target.
- **Model compliance is advice.** Branch protection on mature repositories stays the real lock.
- **Change of meaning for `auto`.** Edges into `delegated` and `ask` nodes stop implying merge. That is what those nodes were already told, so the effect is fewer contradicting texts, not fewer allowed merges.
- **Older harnesses** see no grant and refuse: safe, with an "update it" hint.
- **Privacy.** Words grants keep owner excerpts in one more table. Same retention and owner-only reading as instruction signals (`docs/security.md:494-498`).

## Effort, in worker-sized parts

Each part is one worker run, failing tests first.

1. **Record delivered authority** (small): store trust, relation, rule per delivery; show in `agent_messages`; `msg.delivered` event.
2. **One set of texts** (small): computed trust note, new owner-instruction note, new `MergeText`, guidance clause, Go/TS comparison test. Ends the contradiction before grants exist.
3. **Reason codes on outcomes** (small): `reason_code` on `agent_reply`, stored, shown in Needs you.
4. **Grant store** (medium): table, issue, check, use, revoke, expiry, owner gate; `agent_grant_check`, `agent_grant_use`.
5. **Words grants** (medium): checks in `signalRequest`, words grant issue, quoted clause delivery, ready grant when a check fails, grant note in the four adapters, capability flag.
6. **TUI** (medium): Needs you rows and keys, key grant from refusals with redelivery, Graph markers on gold links.
7. **CTO words and other harnesses** (medium): CTO send carries the owner prompt; Pi and OpenCode prompt capture.
8. **Docs and decision** (small): security, reference, new decision file.

Parts 1 to 3 are independent and can run at once. 4 comes before 5 and 6. 7 after 5.

## Open questions for the owner

1. Should a words grant be issued with no key press when every check passes, or should even words always end in a one-key confirm? Recommendation: no key press when every check passes; the key otherwise.
2. Should "him" count when sovl-clone's send continues a conversation with exactly one other session (homelab)? Recommendation: no for words grants (key path instead); the one key is cheap and "him" is the weakest part of the match.
3. Lifetimes: 4 hours for words grants, 24 hours for key grants?
4. Should the owner's words also be linked for replies (`agent_reply`) inside a conversation, for authority only? Today only new requests are considered. Recommendation: yes, under the same checks.
5. A peer message arriving between the owner's prompt and the send deletes the prompt. Keep that (safe), or keep the prompt but force the key path? Recommendation: keep the prompt, force the key path.
6. Should `auto` keep covering merges on `growing` and `early` nodes without a grant? Recommendation: yes, matching `merge_authority full`.
7. For homelab now: approve PR #828 directly in homelab's session, or by `agent_request_approval` from homelab? This design does not decide that PR.
8. Turn on branch protection for the mature repositories, truenas-stacks first?
