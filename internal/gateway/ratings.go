package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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

	// An offer sits on the session's first run, or on the session while no run
	// is recorded yet (RecordWorkerRun moves it).
	var asked int
	err = tx.QueryRow(`SELECT rate_asked FROM worker_runs WHERE session_id = ?1 AND offered_at > 0
		UNION ALL SELECT rate_asked FROM sessions WHERE id = ?1 AND rate_offered_at > 0 LIMIT 1`, sessionID).Scan(&asked)
	if err == nil {
		out.Ask = asked == 1
		return out, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}

	var finishedForCaller int
	if err := tx.QueryRow(offersSQL+`SELECT COUNT(*) FROM offers WHERE caller = ?`, caller).Scan(&finishedForCaller); err != nil {
		return out, err
	}
	ordinal := finishedForCaller + 1
	ratings, err := countOfModelLevel(tx, `SELECT r.model, COUNT(*) FROM worker_ratings w JOIN worker_runs r ON r.id = w.run_id
		WHERE r.model = ?1 OR r.model LIKE ?1 || ':%' GROUP BY r.model`, base, effort)
	if err != nil {
		return out, err
	}
	askedForModel, err := countOfModelLevel(tx, offersSQL+`SELECT model, COUNT(*) FROM offers
		WHERE asked = 1 AND (model = ?1 OR model LIKE ?1 || ':%') GROUP BY model`, base, effort)
	if err != nil {
		return out, err
	}
	out.Ask = ordinal%protocol.RateEvery == 0 ||
		(ratings < protocol.RateLittleRatings && askedForModel < protocol.RateFirstTasks)
	askedInt := 0
	if out.Ask {
		askedInt = 1
	}
	at := s.now().Unix()
	res, err := tx.Exec(`UPDATE worker_runs SET offered_at = ?, rate_asked = ? WHERE id = (SELECT MIN(id) FROM worker_runs WHERE session_id = ?)`, at, askedInt, sessionID)
	if err != nil {
		return out, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := tx.Exec(`UPDATE sessions SET rate_offered_at = ?, rate_asked = ? WHERE id = ?`, at, askedInt, sessionID); err != nil {
			return out, err
		}
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}

// countOfModelLevel sums the counts of a "model, count" query over the models
// that are this model and effort. A run keeps its model with the effort suffix
// ("m:high"), the worker's session without it, so the two are compared split.
func countOfModelLevel(q deliveryQuerier, query, base, effort string) (int, error) {
	rows, err := q.Query(query, base)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	total := 0
	for rows.Next() {
		var model string
		var n int
		if err := rows.Scan(&model, &n); err != nil {
			return 0, err
		}
		if b, e := protocol.SplitModelLevel(model); b == base && e == effort {
			total += n
		}
	}
	return total, rows.Err()
}

// offersSQL lists the rate offers made so far: one per worker session, with its
// caller and model. An offer is on the session's first run, or on its session
// while no run is recorded. The model is the session's, which is the one
// OfferRate looks up, so an offer is counted for the model it was made for even
// when the run later reports its model in another form.
const offersSQL = `WITH offers(caller, model, asked) AS (
	SELECT COALESCE(NULLIF(r.caller_session_id, ''), r.caller), COALESCE((SELECT x.model FROM sessions x WHERE x.id = r.session_id), r.model), r.rate_asked
	FROM worker_runs r WHERE r.offered_at > 0
	UNION ALL SELECT json_extract(CASE WHEN json_valid(worker) THEN worker ELSE '{}' END, '$.owner'), model, rate_asked FROM sessions s
	WHERE rate_offered_at > 0 AND NOT EXISTS (SELECT 1 FROM worker_runs r WHERE r.session_id = s.id AND r.offered_at > 0)) `

type ratingFacts struct {
	runID      int64
	caller     string
	harness    string
	model      string
	effort     string
	difficulty string
}

// workerRatingFacts reads the model, harness and caller of a finished worker.
// The run ID, harness and model come from the latest run. The caller and the
// difficulty come from the first run that has them, because a follow-up run
// after the caller messaged the worker again carries neither. It falls back to
// the worker session while that session is still kept.
func (s *Store) workerRatingFacts(sessionID string) (ratingFacts, bool, error) {
	return workerRatingFactsOn(s.db, sessionID)
}

// workerRatingFactsOn is workerRatingFacts on a database or a transaction.
func workerRatingFactsOn(q deliveryQuerier, sessionID string) (ratingFacts, bool, error) {
	var f ratingFacts
	var model string
	err := q.QueryRow(`SELECT id, harness, model FROM worker_runs
		WHERE session_id = ? ORDER BY id DESC LIMIT 1`, sessionID).Scan(&f.runID, &f.harness, &model)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return f, false, err
	}
	if found {
		// The first run of the session holds what the task asked for: the
		// caller that started it and the difficulty. A follow-up run after
		// the caller messaged the worker again has neither of its own, and
		// the session row may already be gone after cleanup.
		if err := q.QueryRow(`SELECT caller FROM worker_runs
			WHERE session_id = ? AND caller <> '' ORDER BY id LIMIT 1`, sessionID).Scan(&f.caller); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return f, false, err
		}
		if err := q.QueryRow(`SELECT difficulty FROM worker_runs
			WHERE session_id = ? AND difficulty <> '' ORDER BY id LIMIT 1`, sessionID).Scan(&f.difficulty); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return f, false, err
		}
	}
	var sessionHarness, sessionModel, raw string
	serr := q.QueryRow(`SELECT harness, model, worker FROM sessions WHERE id = ?`, sessionID).Scan(&sessionHarness, &sessionModel, &raw)
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

// AddRating stores one caller's rating of a finished worker, or one defect
// reported after its hand-back. A rating is accepted once per worker; every
// defect report is accepted and counts again. Both come only from the session
// that started the worker, or from the owner. The note is cut to one line and
// secrets are masked.
func (s *Store) AddRating(f protocol.RateFrame) (protocol.RateResult, error) {
	switch strings.TrimSpace(f.Kind) {
	case "", protocol.RateKindRating:
		return s.addRating(f)
	case protocol.RateKindDefectLater:
		return s.addDefect(f)
	case protocol.RateKindBriefDefect:
		return s.addBriefDefect(f)
	case protocol.RateKindMoveToBriefDefect:
		return s.moveToBriefDefect(f)
	default:
		return protocol.RateResult{}, reject("unknown rating kind %q", f.Kind)
	}
}

// addRating stores the satisfaction score.
func (s *Store) addRating(f protocol.RateFrame) (protocol.RateResult, error) {
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
	res, err := s.db.Exec(`INSERT OR IGNORE INTO worker_ratings (run_id, session_id, caller, rater_model, score, note, at)
		VALUES (?,?,?,?,?,?,?)`, facts.runID, worker, rater, raterModel, f.Score, note, at)
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
	return protocol.RateResult{Accepted: true, Worker: worker, Kind: protocol.RateKindRating, Score: f.Score,
		Model: facts.model, Harness: facts.harness, Effort: facts.effort, Difficulty: facts.difficulty, At: at}, nil
}

// addDefect stores one defect found after the worker's hand-back. There is no
// once-per-worker rule: each report is one defect. It needs a stored finished
// run to attach to, so a worker that has not handed back yet is refused.
func (s *Store) addDefect(f protocol.RateFrame) (protocol.RateResult, error) {
	worker := strings.TrimSpace(f.Worker)
	rater := strings.TrimSpace(f.Rater)
	out := protocol.RateResult{Worker: worker, Kind: protocol.RateKindDefectLater, Severity: f.Severity}
	if worker == "" || rater == "" {
		return out, reject("a defect report needs a worker session and the session that gives it")
	}
	if !protocol.ValidRateSeverity(f.Severity) {
		return out, reject("severity must be 1 to 4, not %d", f.Severity)
	}
	facts, ok, err := s.workerRatingFacts(worker)
	if err != nil {
		return out, err
	}
	if !ok || facts.runID == 0 {
		out.Reason = "the worker has not finished: no hand-back to report a defect on"
		return out, nil
	}
	if facts.caller != rater {
		owner, err := s.sessionOwner(rater)
		if err != nil {
			return out, err
		}
		if !owner {
			out.Reason = "that worker was not started by this session, and the reporter is not the owner"
			return out, nil
		}
	}
	note := redact.Secrets(protocol.CleanRateNote(f.Note))
	var raterModel string
	s.db.QueryRow(`SELECT model FROM sessions WHERE id = ?`, rater).Scan(&raterModel)
	at := s.now().Unix()
	res, err := s.db.Exec(`INSERT INTO worker_defects (run_id, session_id, caller, rater_model, severity, note, at, cause, escape_phase)
		VALUES (?,?,?,?,?,?,?,?,?)`, facts.runID, worker, rater, raterModel, f.Severity, note, at,
		protocol.CauseUnknown, protocol.DefectPhase("", f.Severity))
	if err != nil {
		return out, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return out, err
	}
	if err := s.refreshPicker(); err != nil {
		return out, err
	}
	out.Accepted, out.Defect, out.Model, out.Harness, out.Effort, out.Difficulty, out.At =
		true, id, facts.model, facts.harness, facts.effort, facts.difficulty, at
	return out, nil
}

// addBriefDefect stores one rework caused by a bad brief. It is a worker_defects
// row with blame 'brief', charged to the session that wrote the brief (the
// worker's caller), not to the worker, so the picker's quality data never reads
// it. It names the worker run it caused. Only the caller, or the owner, may
// file one.
func (s *Store) addBriefDefect(f protocol.RateFrame) (protocol.RateResult, error) {
	worker := strings.TrimSpace(f.Worker)
	rater := strings.TrimSpace(f.Rater)
	out := protocol.RateResult{Worker: worker, Kind: protocol.RateKindBriefDefect, Severity: f.Severity}
	if worker == "" || rater == "" {
		return out, reject("a brief defect needs a worker session and the session that reports it")
	}
	if !protocol.ValidRateSeverity(f.Severity) {
		return out, reject("severity must be 1 to 4, not %d", f.Severity)
	}
	facts, ok, err := s.workerRatingFacts(worker)
	if err != nil {
		return out, err
	}
	if !ok || facts.runID == 0 {
		out.Reason = "the worker has not finished: no run to attach the brief defect to"
		return out, nil
	}
	if facts.caller != rater {
		owner, err := s.sessionOwner(rater)
		if err != nil {
			return out, err
		}
		if !owner {
			// A lead may file about a member's brief (the briefing session is
			// the member). Authority uses the same tree check as an order.
			_, above, err := orderAbove(s.db, rater, facts.caller, s.now())
			if err != nil {
				return out, err
			}
			if !above {
				out.Reason = "only the session that wrote the brief, its lead, or the owner may report a brief defect"
				return out, nil
			}
		}
	}
	note := redact.Secrets(protocol.CleanRateNote(f.Note))
	var raterModel string
	s.db.QueryRow(`SELECT model FROM sessions WHERE id = ?`, rater).Scan(&raterModel)
	at := s.now().Unix()
	res, err := s.db.Exec(`INSERT INTO worker_defects (run_id, session_id, caller, rater_model, severity, note, at, blame)
		VALUES (?,?,?,?,?,?,?,'brief')`, facts.runID, worker, facts.caller, raterModel, f.Severity, note, at)
	if err != nil {
		return out, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return out, err
	}
	// No refreshPicker: the worker's quality and the picker read only rows
	// with blame 'worker'.
	if err := s.setWorkerRunCause(worker, protocol.CauseBriefDefect); err != nil {
		return out, err
	}
	out.Accepted, out.BriefDefect, out.Model, out.Harness, out.Effort, out.Difficulty, out.At =
		true, id, facts.model, facts.harness, facts.effort, facts.difficulty, at
	return out, nil
}

// moveToBriefDefect reclassifies one late defect as rework from the caller's
// own brief. Only the session that filed the row may move it, and only once.
// The row changes its blame to 'brief' and is charged to the worker's caller,
// so the worker's quality data stops counting it.
func (s *Store) moveToBriefDefect(f protocol.RateFrame) (protocol.RateResult, error) {
	worker := strings.TrimSpace(f.Worker)
	rater := strings.TrimSpace(f.Rater)
	out := protocol.RateResult{Worker: worker, Kind: protocol.RateKindMoveToBriefDefect, Defect: f.Defect}
	if worker == "" || rater == "" {
		return out, reject("a move needs a worker session and the session that moves it")
	}
	defectID := f.Defect
	if defectID <= 0 {
		candidates, err := s.defectsToMove(worker, rater)
		if err != nil {
			return out, err
		}
		switch len(candidates) {
		case 0:
			out.Reason = "no unmoved defect filed by this session on that worker"
			return out, nil
		case 1:
			defectID = candidates[0].id
		default:
			out.Reason = severalDefectsReason(candidates)
			return out, nil
		}
	}
	out.Defect = defectID
	var sessionID, filer, blame string
	var auto bool
	err := s.db.QueryRow(`SELECT session_id, caller, blame, auto_change_id IS NOT NULL FROM worker_defects WHERE id = ?`, defectID).
		Scan(&sessionID, &filer, &blame, &auto)
	if errors.Is(err, sql.ErrNoRows) {
		out.Reason = "no defect with that id"
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if sessionID != worker {
		out.Reason = "that defect belongs to another worker"
		return out, nil
	}
	if blame == "brief" {
		out.Reason = "this defect was already moved to a brief defect"
		out.BriefDefect = defectID
		return out, nil
	}
	if filer != rater {
		out.Reason = "only the session that filed this defect may move it"
		return out, nil
	}
	if auto {
		out.Reason = "agent-net recorded this defect itself; it cannot be moved to a brief defect"
		return out, nil
	}
	facts, ok, err := s.workerRatingFacts(worker)
	if err != nil {
		return out, err
	}
	if !ok {
		out.Reason = "the worker's run is gone: there is no caller to charge the brief defect to"
		return out, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE worker_defects SET blame = 'brief', caller = ? WHERE id = ? AND blame = 'worker'`, facts.caller, defectID)
	if err != nil {
		return out, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		out.Reason = "this defect was already moved to a brief defect"
		return out, err
	}
	if err := setWorkerRunCauseTx(tx, worker, protocol.CauseBriefDefect); err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	// The worker's severity leaves the picker and the quality fit.
	if err := s.refreshPicker(); err != nil {
		return out, err
	}
	out.Accepted, out.BriefDefect = true, defectID
	out.Model, out.Harness, out.Effort, out.Difficulty, out.At =
		facts.model, facts.harness, facts.effort, facts.difficulty, s.now().Unix()
	return out, nil
}

// defectToMove is one unmoved, non-automatic late defect the filer can name in
// a move.
type defectToMove struct {
	id       int64
	severity int
	note     string
	at       int64
}

// defectsToMove finds the rows a move without a defect id can choose: the
// caller's own late defects on the worker, not moved yet and not recorded by
// agent-net itself.
func (s *Store) defectsToMove(worker, rater string) ([]defectToMove, error) {
	rows, err := s.db.Query(`SELECT id, severity, note, at FROM worker_defects
		WHERE session_id = ? AND caller = ? AND blame = 'worker'
		  AND auto_change_id IS NULL
		ORDER BY at, id`, worker, rater)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []defectToMove
	for rows.Next() {
		var d defectToMove
		if err := rows.Scan(&d.id, &d.severity, &d.note, &d.at); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// severalDefectsReason lists the candidates so the caller can retry with one
// id. The date is UTC and the note is cut to 60 characters.
func severalDefectsReason(candidates []defectToMove) string {
	parts := make([]string, 0, len(candidates))
	for _, d := range candidates {
		note := []rune(d.note)
		if len(note) > 60 {
			note = note[:60]
		}
		parts = append(parts, fmt.Sprintf("%d severity %d, %s, %s",
			d.id, d.severity, time.Unix(d.at, 0).UTC().Format("2006-01-02"), string(note)))
	}
	return "several defects match; retry with defect=<id>: " + strings.Join(parts, "; ")
}

// sessionOwner tells whether a session is a machine's owner pseudo-session.
func (s *Store) sessionOwner(sessionID string) (bool, error) {
	var harness string
	err := s.db.QueryRow(`SELECT harness FROM sessions WHERE id = ?`, sessionID).Scan(&harness)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return protocol.IsOwner(harness), nil
}
