# 0170 - Friction reports

Date: 2026-11-16

The owner asked agents to say what slowed them down. Until now he found the
friction himself, and a failure in the skills panel sat in the logs for days.
The agents doing the work already hit these problems, and nobody asked them.
This decision collects the reports. It does not digest them.

## The field

`agent_handback` and `agent_step_report` take an optional `friction` list.
Each entry is `{area, text}`:

- `area` is one of `tooling`, `permissions`, `brief`, `review`, `ci`,
  `environment`, `skill`.
- `text` is one line, at most 200 characters, and says what slowed the agent
  down.

An empty list means nothing to report, and it is the normal case. The worker
brief's closing checklist asks for the list, and says that none is fine. The
catalog schemas describe the field, so a model sees it before it hands back.

## Storage

The gateway keeps the reports in a `friction` table: `id`, `project`,
`session`, `change_id`, `harness`, `model`, `area`, `text`, `created_at`,
`digest_id`. `digest_id` is empty in T1; T2 fills it when a digest carries the
row.

The text is redacted with `redact.Secrets` before it is stored, because a
reporter may paste part of a command line. The migration is appended at the
end of the gateway's migration list and is never edited after it ships.

## No blame

Friction is a report about the work, not a score of the worker. Friction rows
never enter the picker's inputs, the model track record, the worker rating or
the defect counts. A test pins this. A report that costs nothing stays a
report, and a report never makes a worker look worse.

## The read

The CTO reads the reports with `friction_find`, an owner read. It filters by
`project`, `area` and `since`, newest first, and returns the newest rows plus a
count per project. The text comes back as quoted data, so a reader does not
mistake a worker's line for an instruction.

## The Work tab

The TUI Work tab (tab 3) gets one read-only section below the subscriptions:
the newest friction rows, each with project, area, harness, age and the text
cut to the width, and a count per project. The section never writes, and it
appears only after its first read.

## T2, the digest

T2 starts after two weeks of collecting. It groups reports by repo and sends a
weekly digest:

- The model that writes the digest is chosen by the same job picker the
  difficulty judge uses.
- Only repeats and costly reports reach it. A report is costly when a step
  failed, or when its run time was above the median of its difficulty level and
  that median has at least 10 runs.
- A single entry with no cost never reaches the digest.
- A lead gets its repo groups. The owner gets a weekly top-5 Desk brief.
- A quiet repo sends nothing.
- A fingerprint marks the report that named a problem whose fix did not work,
  so the next digest says so.