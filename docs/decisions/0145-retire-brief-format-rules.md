# 0145 - Retire the six brief format rules and record the cause of rework

Date: 2026-11-12
Status: Built.

## Context

Decision [0135](0135-worker-brief-checks.md) refused a worker brief that missed
one of six format items: a Goal line naming who asked, Files or areas, Limits, a
Definition of done, How you are graded, and Evidence with at least three lines.
The owner asked whether those rules predict a bad worker outcome.

An analysis of 1,399 briefs from 2026-10-30 to 2026-11-12 (1,334 launched) found
they do not:

- Briefs that fail more of the six rules do slightly better, not worse. The
  adjusted odds ratio for a bad outcome is 0.83 per standard deviation (95% CI
  0.72 to 0.95), and 0.68 (0.55 to 0.83) for a defect found later. Both point
  the wrong way.
- The rules that do predict a bad outcome are not format: a named check command
  with no broad gate (OR 1.83), brief size in deliverables or words (OR 1.43 to
  1.51 per standard deviation), and an ambiguous "its branch" reference (defect
  OR 4.12, small sample). The caller is the largest single factor.
- Each refusal costs a rewrite: the first two attempts on one task are refused,
  and the third starts anyway.
- The record cannot say what caused a bad outcome. Only 17 of 544 bad outcomes
  carry a brief attribution. Of 122 workers with a defect found later, 71 have
  maximum severity 1, so the outcome mixes catches before release with escapes.

The owner approved retiring the six format rules ("4. yes") and recording the
cause of defects and rework ("3. this matters yes"). He rejected the three new
silent start-time checks that the analysis proposed ("not well thought out"), so
no new brief check is added.

## Decision

- `protocol.CheckWorkerBrief` and its six failure lines are deleted. The brief
  is not checked or scored for format, and no warning replaces the rules.
- The only brief refusal is overlap, unchanged: a brief may not name a file a
  running worker's brief or an open change's changed files in the same project
  name, unless it says `overlap ok: <reason>`. A change or worker the start
  itself targets is exempt as before. The rejection count and the third attempt
  that starts anyway stay for overlap refusals.
- The six items are removed from the `agent_worker_start` description, the
  worker start guidance written to agent instruction files, and the brief check
  section of `docs/reference.md`. [docs/brief-template.md](../brief-template.md)
  stays and now says it is a template, not a rule.
- A cause is stored on the records agent-net already keeps, from signals it
  already has, in `protocol.Cause*`:
  - `quota_auth`: a provider quota or sign-in refusal in the run's error text
    (429, resource exhausted, quota reached, a usage or session limit, a low
    credit balance, 401 or 403, not logged in). A failed Codex turn reports
    "The Codex turn failed: ... usage limit ..." as its hand-back report, so a
    failed hand-back's report becomes the run's failure text and reaches
    `worker.stopped` the same way.
  - `sandbox`: a write or command the sandbox refused (`operation not
    permitted`, `sandbox`, `read-only file system`).
  - `harness`: a harness crash, a start that never ran, a lost run, or the idle
    or longest-run limit.
  - `test_failure`: a failed test step in the merger's suite.
  - `accept_finding`: a failed accept step.
  - `brief_defect`: the caller's own brief defect rating, or a defect moved to
    one.
  - `unknown`: none of the above, including rows written before this change.
- `worker_runs.cause` is written when the run ends, and a later signal fills it
  only when it is empty or unknown. The first specific cause is kept.
  `worker_defects.cause` follows the same rule. `worker_defects.escape_phase`
  marks a defect found later as `caught` (severity 1 or found by the test step)
  or `escaped` (found after release).
- The cause is shown in `agent_workers_find` with `detail: full`, and the weekly
  scores row carries `causes` plus the caught and escaped counts, shown in the
  TUI weekly table's CAUSE column on a wide screen.
- The columns come from one migration appended last, `workerCauseMigration`.
  It adds columns only, so a replay is safe.

## Tests

`internal/protocol/cause_test.go` covers every cause source, the first-specific
rule and the defect phase. `internal/protocol/brief_check_test.go` keeps the
overlap statement, path extraction, path sharing and task key. The gateway
tests cover a plain brief that starts with no refusal, the overlap refusal that
still holds, the third attempt that starts after two overlap refusals, one
stored cause per source, the test and accept step signals, the escaped defect,
the brief defect signal, the worker list and the weekly counts.
`internal/tui/quality_weekly_test.go` covers the CAUSE column.