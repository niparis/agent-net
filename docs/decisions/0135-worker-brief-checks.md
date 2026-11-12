# 0135 - Worker brief checks before launch

Date: 2026-11-11
Status: Superseded by [0141](0141-retire-brief-format-rules.md) for the six format rules; the overlap refusal stays.

## Context

The owner ordered checks on every worker brief. The briefs that caused today's
brief defects missed obvious things: three workers on the same file; a literal
`$(cat)` instead of task text. The difficulty judge already read every brief,
but it only rates the work. Nothing refused a brief that was incomplete.

The owner's rules (message m_ddd4d47fed6f4bf3):

1. Code checks, with no LLM, run inside `agent_worker_start` before launch,
   plus the difficulty judge's existing pass.
2. Reject, do not warn. The refusal lists every missing or wrong item. At most
   two refusals per task: the third attempt starts anyway, and the remaining
   failures are one brief defect against the caller.
3. Overlap is refused like the rest unless the brief says `overlap ok: reason`.
4. The format is in the tool description, which every caller reads, with a
   pointer to a template.
5. `mode: "check"` previews the result and starts nothing.
6. Rejections are counted per caller in the weekly table.

## Decision

- The deterministic text checks live in `internal/protocol`:
  `CheckWorkerBrief`, `BriefPaths`, `BriefOverlapAllowed`, `BriefTaskKey`.
  They are case-insensitive and ignore markdown decoration.
- The store checks live in `internal/gateway`: `checkWorkerBrief` compares the
  brief's paths with the full task text of running workers in the same project
  (the task message, not the truncated summary) and the changed files of open
  changes there, from each change's current diff stat. A change or worker the
  start itself targets is skipped: its id in the brief, an open change whose
  branch is the start's base, or the start's continue or fixes target. Without
  that, every fix round would be refused for overlapping its own change.
- The gateway runs the checks after the judge, before it picks a model or
  launches. A validation-only call skips them and starts nothing.
- A refusal is one line per failed item. It is stored in
  `worker_brief_rejections` with the caller, the task key and the time. The
  task key is the caller, the project, and the title or first 200 characters
  of the goal line, lower-cased. Rejections within 24 hours count together.
- The third attempt on one task starts anyway. The gateway inserts one
  `worker_brief_defects` row, severity 2, charged to the caller, with the
  remaining failures in the note. The start's model note says so.
- `mode: "check"` returns `BriefFailures` in the start answer, and the tool
  answers `brief: ok` or `brief: refused` with the list. Nothing is tracked or
  launched.
- `BriefRejectionCountsByCaller(cut)` is the indexed query the weekly table
  calls beside `BriefDefectCountsByCaller(cut)`. The weekly table code is not
  edited here.
- The six items are in the `agent_worker_start` description and in
  [docs/brief-template.md](../brief-template.md).

There are no new settings. This is an explicit owner order, not a new approval
gate.

## Tests

`internal/protocol/brief_check_test.go` covers each text check, the markdown
forms, path extraction, the overlap statement and the task key.
`internal/gateway/brief_check_test.go` covers a complete brief, overlap with a
running worker and with an open change, a fix round on its own change, the
24-hour count, the third attempt that starts and records one defect, and check
mode that starts nothing.
`internal/toolcatalog/brief_test.go` holds the description and schema.