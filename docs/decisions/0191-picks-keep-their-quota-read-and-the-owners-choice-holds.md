# 0191 - A pick keeps what it read of the quotas, and the owner's choice holds

Date: 2026-11-25
Status: Asked for by the owner on 2026-11-25, through the CTO: "we still dont
get codex workers", "or even gemini". Closes the open point of
[0189](0189-continue-meets-todays-picker.md).

## Problem

E. A pick keeps one row per candidate (`pick_candidates`): its reason, score,
cost, pace room and factor. It does not keep the quota windows it read. When a
model was left out because of its quota, nobody can check afterwards which
window decided it, how full it was, when it reset, or how old the reading was.
Nobody can see either what the chosen run then used of its provider.

K. Since 0189, a continue that names no model meets the picker's rules of
today. A first start where the owner named the model was not remembered, so a
later continue that named none could move his worker to another model.

## Decision

### E. What a pick read of the quotas

1. Each recorded pick keeps one row in `pick_quota`, keyed by the same
   `run_id` as its `pick_candidates` rows, written in the same transaction.
   `read` holds each provider's windows once (window, kind, percent used,
   reset time, the provider's reading time), the time of the reading the pace
   rule measured against, and the pick time. For each candidate, by its row
   position, it holds its quota provider, the windows that serve its model,
   the window that decided (the one that held it back, else the one that set
   its pace room), the gate step that held it back (`floor`, `block90`,
   `burn`, `exhausted`, `paused` and the others) and the owner's floor for its
   provider. The reasons, the pace room and the factor stay in the rows only.
2. When the chosen run ends, `run_end` gets the run's tokens by kind (as the
   run row holds them) and its provider's windows as read at the end. The
   windows before are the pick's own reading.
3. The record is built only by a start that records the pick: a preview or a
   check builds nothing. The rows are kept as long as the pick rows, which
   retention does not prune.
4. `agent_workers_find` with `detail: full`, for agents and for the CTO, adds
   `quota_read`: plain lines that say what the pick read and what the run used.
   It is one gateway read (`query.pick_quota`) for the page shown. The TUI
   screens do not change.

Measured on the record write with 12 rows (`BenchmarkRecordPick`): about
140 µs without the quota read and 187 µs with it. The choice itself does not
change.

### What the pace rule reads in a window that was just reset

A window that was just reset is 0 percent used at 0 percent elapsed. The pace
rule reads it as on pace: room 0, factor 1. The rule keeps the smallest room
of a provider's windows, so this window decides, and the room of the longer
windows is not read. In the test case, the Codex week window is 20 percent
used at 60 percent of the week, 40 points of room and a factor of 4, but the
pick reads factor 1. In the first minutes of a window, any use reads as ahead
of pace. The rule is not changed here. `TestPaceRuleReadsAJustResetWindowAsOnPace`
records what it reads.

Two more readings to know: the pace rule counts the elapsed time of every
provider up to the time of the start's own reading, also for a provider whose
reading was borrowed from another machine and is older; and a window whose
reset time passed after the reading still counts its old percent until a newer
reading arrives. The record keeps both times, so a reviewer can see these cases.

### K. The owner's choice holds

1. A model, provider, harness or effort that the owner named for a worker at
   its first start is kept on the start record (`worker_starts.owner_choice`).
   His own command line or TUI counts, and his words as the caller's daemon
   found them (`owner_selected`). A start by the CTO without his words, or by
   an agent, does not.
2. A continue that names none of these fields runs what he named:
   - a named model runs again on its harness, as his: a model he had paused
     when he named it still runs (0188);
   - a named harness alone keeps the 0189 re-pick inside that harness;
   - a named provider or effort alone keeps the old model with it.
   The note says `kept <what>, which the owner named at the worker's first
   start`.
3. The gate's locked checks still apply (quota used up, not signed in,
   provider limited). A continue that cannot run is refused with the gate's
   reason and the same note.
4. A continue that names its own fields replaces his choice only when they
   are his. An agent's named continue runs as today, and the next continue
   that names none goes back to his choice.
5. The new worker keeps the choice for its own continues. The choice lives in
   the gateway database, so it survives daemon and gateway restarts. A start
   record that holds one is kept as long as its worker session.
6. A worker whose start the owner did not name re-picks as 0189 says.
7. The owner's newest word wins. The start record keeps when he named the
   model (`named_at`), and the score command keeps when he set each score
   (`set_at`, in `owner.model_scores`). A held model that he paused after he
   named it is treated as paused: the continue is refused or re-picked exactly
   as for a model he never named, and the note says `the owner paused <model>
   after he named it at the worker's first start`. A held model that was
   already paused when he named it keeps running. A pause he lifts again
   leaves his choice in force; a pause he sets after that is newer, so it
   wins again. A pause with no `set_at` was set before this decision, so
   before any held choice: it is older than his naming.

   Only the score command stamps `set_at`. A score list restored by a
   settings undo keeps the times it had, so an undone lift brings back the
   pause with its old time.

## Not changed

No limit, price, share or score rule. The pace rule and the floors are as
they were.
