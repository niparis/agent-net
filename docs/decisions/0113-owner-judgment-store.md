# 0113 - Owner judgment store

Date: 2026-11-09
Status: Approved by the owner on 2026-11-09 ("i approve all the jobs, start them as needed"). Step 2 of the chief of staff roadmap.

## Context

Agents frequently stop to ask the owner questions or permission for issues the owner has already decided. Standing orders existed, but only the CTO saw them in its initial turn. Ordinary agents had no way to query the owner's past decisions, standing rules, or past answers to previous asks.

The owner's answers already existed as data in `inbox_items` (`answered`, `approved`, `refused` with an answer string), but were not indexed, searchable, or accessible to agent sessions.

## Decision

1. **One store for orders, rules, answers, and corrections.**
   The existing `standing_orders` gateway table is extended with three columns:
   - `kind`: `order`, `rule`, `answer`, or `correction`. Existing rows default to `order`.
   - `area`: optional repo or team name.
   - `ref`: optional reference ID (e.g. the ask ID).
   The FTS5 index (`standing_orders_fts`), triggers, history tracking (`standing_order_history`), and pinning apply to all kinds.

2. **Automatic ingestion of owner answers.**
   When an inbox ask is answered, approved, or refused by the owner (actor `owner`), an entry of `kind=answer` is recorded in `standing_orders`. The text is formatted as `"Q: <question> / A: <answer>"`, with `area` resolved from the ask's project, repo, or team, and `ref` set to the ask ID. Automatic closes, CTO resolutions, and chief actions do not create answer rows.

3. **Backfill migration.**
   A database migration adds the `kind`, `area`, and `ref` columns and backfills the newest 500 answered owner asks from `inbox_items` into `standing_orders` as `kind=answer`, rebuilding the FTS index.

4. **Agent read tool.**
   Every agent can call `agent_owner_judgments_find` with `{query, area, kind, limit}`. When `query` is present, results are ranked by FTS5 `bm25` with snippets. Without `query`, results return newest first. Reads are open to every agent without new gates or permissions, matching the public visibility of charters and rules.

5. **Operator write path for corrections and rules.**
   The operator method `judgment_add` (`{kind: correction|rule, text, area, ref}`) allows the operator and CTO to record owner corrections and rules.

6. **Search on demand, no blanket context injection.**
   Judgments are queried by agents on demand rather than injected into every agent prompt. Guidance instructs agents: "Before asking the owner, search what he already decided: agent_owner_judgments_find." The CTO's first-turn prompt continues to load only orders and rules (pinned first, up to the 4 KB budget).
