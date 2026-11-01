package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// The caller's rating of a finished worker (difficulty calibration, part 4).
// A rating is kept with the run's facts, never with the task text beyond the
// caller's own one-line note. Part 4 is read only: nothing here is used by
// ChooseWorkerModel.

const ratingDifficultyDefault = "normal"

// OfferRate decides whether the caller should be asked to rate a worker whose
// end notice is about to go out, and records the offer once per worker. It
// asks for every RateEvery-th finished worker of a caller, and for the first
// RateFirstTasks finished tasks of a model and effort that has fewer than
// RateLittleRatings ratings.
func (s *Store) OfferRate(sessionID string) (protocol.RateOfferResult, error) {
	out := protocol.RateOfferResult{SessionID: sessionID}
	if strings.TrimSpace(sessionID) == "" {
		return out, nil
	}
	var harness, model, raw string
	err := s.db.QueryRow(`SELECT harness, model, worker FROM sessions WHERE id = ?`, sessionID).Scan(&harness, &model, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var w protocol.WorkerInfo
	json.Unmarshal([]byte(raw), &w)
	caller := strings.TrimSpace(w.Owner)
	if caller == "" {
		return out, nil
	}
	difficulty := firstNonEmpty(w.DifficultyAsked, w.DifficultyUsed, ratingDifficultyDefault)
	base, effort := protocol.SplitModelLevel(model)
	out.Model, out.Effort, out.Harness, out.Difficulty = base, effort, harness, difficulty

	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()

	var asked int
	err = tx.QueryRow(`SELECT asked FROM worker_rate_offers WHERE session_id = ?`, sessionID).Scan(&asked)
	if err == nil {
		out.Ask = asked == 1
		return out, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}

	var finishedForCaller int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM worker_rate_offers WHERE caller = ?`, caller).Scan(&finishedForCaller); err != nil {
		return out, err
	}
	ordinal := finishedForCaller + 1

	var ratings int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM worker_ratings WHERE model = ? AND effort = ?`, base, effort).Scan(&ratings); err != nil {
		return out, err
	}
	var askedForModel int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM worker_rate_offers WHERE model = ? AND effort = ? AND asked = 1`, base, effort).Scan(&askedForModel); err != nil {
		return out, err
	}
	out.Ask = ordinal%protocol.RateEvery == 0 ||
		(ratings < protocol.RateLittleRatings && askedForModel < protocol.RateFirstTasks)
	askedInt := 0
	if out.Ask {
		askedInt = 1
	}
	if _, err := tx.Exec(`INSERT INTO worker_rate_offers (session_id, caller, harness, model, effort, difficulty, asked, at)
		VALUES (?,?,?,?,?,?,?,?)`, sessionID, caller, harness, base, effort, difficulty, askedInt, s.now().Unix()); err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}

type ratingFacts struct {
	runID      int64
	caller     string
	harness    string
	model      string
	effort     string
	difficulty string
}

// workerRatingFacts reads the model, harness and caller of a finished worker.
// It prefers the run that the gateway recorded, and falls back to the worker
// session while that session is still kept.
func (s *Store) workerRatingFacts(sessionID string) (ratingFacts, bool, error) {
	var f ratingFacts
	var model string
	err := s.db.QueryRow(`SELECT id, caller, harness, model, difficulty FROM worker_runs
		WHERE session_id = ? ORDER BY id DESC LIMIT 1`, sessionID).Scan(&f.runID, &f.caller, &f.harness, &model, &f.difficulty)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return f, false, err
	}
	var sessionHarness, sessionModel, raw string
	serr := s.db.QueryRow(`SELECT harness, model, worker FROM sessions WHERE id = ?`, sessionID).Scan(&sessionHarness, &sessionModel, &raw)
	var w protocol.WorkerInfo
	if serr == nil {
		json.Unmarshal([]byte(raw), &w)
		if !found {
			found = strings.TrimSpace(w.Owner) != ""
			f.harness, model = sessionHarness, sessionModel
			f.caller = w.Owner
			f.difficulty = firstNonEmpty(w.DifficultyAsked, w.DifficultyUsed)
		}
		if f.caller == "" {
			f.caller = w.Owner
		}
		if f.harness == "" {
			f.harness = sessionHarness
		}
		if model == "" {
			model = sessionModel
		}
	} else if !errors.Is(serr, sql.ErrNoRows) {
		return f, false, serr
	}
	if !found {
		return f, false, nil
	}
	f.model, f.effort = protocol.SplitModelLevel(model)
	if f.difficulty == "" {
		f.difficulty = ratingDifficultyDefault
	}
	return f, true, nil
}

// AddRating stores one caller's rating of a finished worker. It is accepted
// once per worker, and only from the session that started it. The note is cut
// to one line and secrets are masked.
func (s *Store) AddRating(f protocol.RateFrame) (protocol.RateResult, error) {
	worker := strings.TrimSpace(f.Worker)
	rater := strings.TrimSpace(f.Rater)
	if worker == "" || rater == "" {
		return protocol.RateResult{}, reject("a rating needs a worker session and the session that gives it")
	}
	if !protocol.ValidRateScore(f.Score) {
		return protocol.RateResult{}, reject("score must be 1 to 5, not %d", f.Score)
	}
	facts, ok, err := s.workerRatingFacts(worker)
	if err != nil {
		return protocol.RateResult{}, err
	}
	if !ok {
		return protocol.RateResult{Worker: worker, Score: f.Score,
			Reason: "no finished worker with that session ID"}, nil
	}
	if facts.caller != rater {
		return protocol.RateResult{Worker: worker, Score: f.Score,
			Reason: "that worker was not started by this session"}, nil
	}
	note := redact.Secrets(protocol.CleanRateNote(f.Note))
	var raterModel string
	s.db.QueryRow(`SELECT model FROM sessions WHERE id = ?`, rater).Scan(&raterModel)
	at := s.now().Unix()
	res, err := s.db.Exec(`INSERT OR IGNORE INTO worker_ratings
		(run_id, session_id, caller, rater_model, harness, model, effort, difficulty, score, note, at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`, facts.runID, worker, rater, raterModel, facts.harness,
		facts.model, facts.effort, facts.difficulty, f.Score, note, at)
	if err != nil {
		return protocol.RateResult{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return protocol.RateResult{}, err
	}
	if n == 0 {
		return protocol.RateResult{Worker: worker, Score: f.Score,
			Reason: "this worker already has a rating"}, nil
	}
	if err := s.refreshQuality(); err != nil {
		return protocol.RateResult{}, err
	}
	return protocol.RateResult{Accepted: true, Worker: worker, Score: f.Score, Model: facts.model,
		Harness: facts.harness, Effort: facts.effort, Difficulty: facts.difficulty, At: at}, nil
}

// RatingCounts is how many ratings each model has, for the read-only Quotas
// table.
func (s *Store) RatingCounts() ([]protocol.CalibrationRating, error) {
	rows, err := s.db.Query(`SELECT model, COUNT(*) FROM worker_ratings WHERE model <> '' GROUP BY model ORDER BY model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.CalibrationRating{}
	for rows.Next() {
		var r protocol.CalibrationRating
		if err := rows.Scan(&r.Model, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
