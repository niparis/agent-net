# 0158 - Gateway notes, version 1

Date: 2026-11-12
Status: Approved by the owner on 2026-11-12: move agent memory into agent-net, starting with a light first version that reuses the owner judgment store.

## Context

Agent memory today is a Claude Code folder per project directory. Only Claude reads it. The owner cannot see it. The merger and the lead share one folder by accident. The CTO has no file tools, so it has no memory beyond its conversation.

The owner's worry: "won't we end up with hundreds of small notes really fast?" The lead's own index holds about 90 one-line entries after two weeks, so the worry is fair. The design has to keep the cost of many notes flat.

## Decision

1. **Notes live in the owner judgment store.** A note is a row of the `standing_orders` table (decision 0113) that has a name. There is no second store. One migration, appended at the end of the list, adds these columns: `scope`, `scope_ref`, `name`, `description`, `used_at` and `uses`. Existing rows keep their meaning: `scope` is `owner`, `name` is empty. A unique index keeps one note per `(scope, scope_ref, name)`. The migration also replaces the update trigger so that a read, which only changes `used_at` and `uses`, adds no history row and no index work.

2. **Scopes.** `agent` (one agent node: its sessions on every machine, and the workers it starts), `project` (one project) and `owner` (every session). A write names a scope; the gateway reads the agent and the project from the writing session, so a session cannot write into another agent's notes. The default scope is `agent`, the narrowest. Only the owner and the CTO write the `owner` scope, because it reaches every session. The CTO's agent is `cto`. It has no project.

3. **Types (kind).** `rule` (an owner rule: the owner's existing rules, written only through the existing owner path), `feedback` (how to work), `fact` (something true about the work), `reference` (where to look). The agent write tool takes `feedback`, `fact` and `reference`; it refuses `rule`. The default kind is `fact`. In version 1 the index and the find tool list named notes only. Owner rules stay reachable through `agent_owner_judgments_find`.

4. **Two tools for every harness.** `agent_note_write` (`name`, `description`, `body`, `scope`, `kind`) and `agent_notes_find` (`text`, `scope`, `stale`). Both are in the catalog for agents, workers and the CTO, so the plugins for Pi and OpenCode get them through the generated tool files, and the CTO gets them through its MCP server. Agent-scoped and project-scoped notes have no owner gate. Owner-wide notes are the owner's: the owner writes them, or the CTO on his words, and the gateway refuses an owner-scope write from any other session (it checks the session's harness, which the caller cannot set). Owner rules keep their existing owner-only path.

5. **Anti-sprawl rules.** These answer the owner's worry.
   - *A name is a key.* Writing an existing name in the same scope updates the note. The old text stays in `standing_order_history`.
   - *Merge, do not add.* Every write answers with up to 3 similar notes the session can see (words of name, description and body overlap, or a shared word in the names). The answer tells the agent to merge into one of them.
   - *The start text has a fixed length.* See point 6. A hundred notes cost the same context as ten.
   - *Most used first.* Use is counted when a note is written, and when it is among the 3 best matches of a text search. Listing without text does not count as use, so browsing does not flatten the ranking.
   - *Old notes are reviewed, not deleted.* A note nobody read or wrote for 30 days is flagged `stale`. `agent_notes_find` answers with `stale_count` and a flag on each note, and `stale: true` lists only those. The owner's judgments view (`agent_owner_judgments_find` with `stale: true`, and Settings in the TUI) shows the same flag. Nothing is deleted by age. Only the owner deletes a note, in the TUI.
   - *One fact per note.* The tool description says so. Version 1 sets no size limit on a body.

6. **What a session sees at start, and when notes change.** The gateway answers `context.get` with the charter text and, beside it in a separate field (`notes`), the notes block. The daemon hashes the charter to decide when to send it again, so the notes are kept out of that text: a note change never sends the charter again. The block holds the titles and one-line descriptions only, never a body:
   - at most 8 titles for the agent scope, 8 for the project scope and 8 for the owner scope: at most 24 title lines;
   - each title line is at most 160 characters;
   - one line says how many more notes exist ("N more notes: search them with agent_notes_find");
   - one header line.
   The 8 shown in each scope are the most used (then the most recently used). They are printed in name order, so the text changes only when the chosen set or a title changes, not when a count goes up. A session with no notes gets no block. Bodies come only through `agent_notes_find`, which answers with at most 20 notes.
   In a Claude Code session, whose hook answers stay in the transcript, the whole block is sent on the first prompt and again after a compaction. After that a change sends only a short notice, `[agent-net] Your notes changed. Changed titles:` and the new or changed title lines, plus `- no longer listed: name` for a note that left the list. At most 8 lines; more are counted. A new body with the same title sends nothing. Pi and OpenCode put the block in the system prompt of every request, so they get it every turn, as they get the charter.
   Measured in the daemon test: the first send is 997 bytes (charter and index). Before this rule a note change sent 1119 bytes (the charter again with the change line, and the index). Now it sends 90 bytes.

7. **The owner sees and deletes notes.** Settings, section "Standing orders and notes", lists each note with its kind, name, description and scope, and flags stale ones. `x` deletes the selected note and `u` puts it back, as for standing orders.

8. **File memory stays for Claude sessions until a later version.** This version does not import, move or delete the Claude Code memory folders. A later version may import them.

## Not in this version

- The CTO does not get the notes block at the start of a conversation. Its start code belongs to the one-session-model work (decision 0156). It reads its notes with `agent_notes_find`.
- Notes are not merged by the gateway. It only points at similar notes.
- No size limits and no per-agent note count limit. Agent and project notes need no owner approval.
- Agents cannot delete notes. The owner can.

## Consequences

- Hundreds of notes cost storage and a search, not context. The start text stays under 26 lines.
- A noisy agent can still add many notes. The similar-notes answer, the use ranking and the stale list are what keep that visible and cheap. If this fails in practice, the next step is a merge tool or a per-scope count, and the owner decides it.
