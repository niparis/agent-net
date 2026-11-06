# 0067 - Record worker early quits

Date: 2026-11-06
Status: Built

Short incomplete runs can consume an attempt without producing useful changes.
Keep measured evidence alongside the original outcome so callers can distinguish
these runs from longer attempts. This is a heuristic, not a verdict about the
model's report or whether its claimed tests passed.

The daemon measures files and added plus deleted text lines against the worker's
base commit, including untracked files. It measures before committing leftovers.
An incomplete run is an early quit when it is shorter than
`workers.early_quit_threshold` (120 seconds by default), and either changes no
files or changes fewer than 20 lines without committed file changes. A short
committed change counts as an attempt. Binary files count as files with zero
text lines. Missing worktrees or failed measurements remain unknown and do not
trigger the flag. A zero threshold disables detection.

The gateway keeps these facts with each run. Migration 127 adds nullable file,
line and completion fields, and an early-quit flag defaulting to false. Existing
records remain unknown. The daemon also persists the evidence so reporting can
survive disconnection and restart.

An early quit applies the existing failed-attempt rotation multiplier of 0.5,
for the same machine, harness, model and difficulty, within the existing model
failure window. Several early quits do not multiply that penalty, and an
already-failed run is not penalized twice. Original outcomes, quality scoring,
difficulty assessment and other picker weights stay unchanged.

This change does not start another worker. Automatic escalation would need a
retry budget, a policy for choosing a different model, preserved task and
worktree state, a way to prevent duplicate concurrent attempts, and a clear
notice of the new attempt and its cost. The existing auto-continue feature
keeps its current behavior.
