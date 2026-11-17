package gateway

// pickCandidatesMigration keeps one row per candidate of each automatic pick.
// A row holds the terms the picker read for that candidate and what the pick
// did with it. The run_id is the judge result (worker_assessments.run_id) that
// the start stored with the pick. No worker_runs row carries that id, so a
// pick is joined to its run by session and start time, as the worker log pairs
// an assessment with its run (docs/reference.md has the query). The picker note
// is written from these rows. Appended at the end of the migration list and
// never edited once it ships.
const pickCandidatesMigration = `
CREATE TABLE IF NOT EXISTS pick_candidates (
 run_id TEXT NOT NULL,
 pos INTEGER NOT NULL,
 name TEXT NOT NULL DEFAULT '',
 harness TEXT NOT NULL DEFAULT '',
 provider TEXT NOT NULL DEFAULT '',
 model TEXT NOT NULL DEFAULT '',
 effort TEXT NOT NULL DEFAULT '',
 rule TEXT NOT NULL,
 bucket_lo REAL NOT NULL,
 bucket_hi REAL NOT NULL,
 fallback TEXT NOT NULL DEFAULT '',
 cell_score REAL,
 cell_low REAL,
 cell_high REAL,
 cell_n INTEGER,
 is_allowed INTEGER NOT NULL,
 is_qualified INTEGER NOT NULL,
 is_prior INTEGER NOT NULL,
 is_best INTEGER NOT NULL,
 is_chosen INTEGER NOT NULL,
 is_winner INTEGER NOT NULL,
 left_out_reason TEXT NOT NULL DEFAULT '',
 cost_known INTEGER,
 cost_recorded INTEGER,
 price_unknown INTEGER,
 cost_per_run REAL,
 p_clean REAL,
 rescue_cost REAL,
 expected_cost REAL,
 pace_room REAL,
 pace_factor REAL NOT NULL,
 adjusted_cost REAL,
 weight REAL NOT NULL DEFAULT 0,
 probability REAL NOT NULL DEFAULT 0,
 skipped_reason TEXT NOT NULL DEFAULT '',
 explore INTEGER NOT NULL DEFAULT 0,
 explore_credit_before REAL,
 explore_credit_after REAL,
 margin REAL,
 PRIMARY KEY(run_id, pos));
`
