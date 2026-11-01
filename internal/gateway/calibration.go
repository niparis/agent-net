package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// calibrationWindow is how far back the calibration reads runs. The gateway
// prunes worker_runs after TrackRecordDays, so the window is the same.
const calibrationWindow = protocol.TrackRecordDays * 24 * time.Hour

// calibrationSession gathers the rows of one worker session. A task is the
// session (one agent_start), not one row per turn: a continue after a hand-back
// starts another run and leaves its counters on a new row.
type calibrationSession struct {
	caller, difficulty string
	cont, fix          int
	unknown            bool
	endedAt            int64
}

// Calibration reads the read-only difficulty table: per caller and
// difficulty, the weighted rework median of the last seven finished tasks,
// the two offsets and what the loop would do. It never writes state and the
// chooser never reads it.
func (s *Store) Calibration() (protocol.Calibration, error) {
	cut := s.now().Add(-calibrationWindow).Unix()
	rows, err := s.db.Query(`SELECT session_id, caller, difficulty, ended_at, continue_count, fix_count, counters_known
		FROM worker_runs WHERE ended_at >= ? AND outcome <> ? ORDER BY ended_at, id`, cut, protocol.OutcomeStopped)
	if err != nil {
		return protocol.Calibration{}, err
	}
	sessions := map[string]*calibrationSession{}
	order := []string{}
	for rows.Next() {
		var sessionID, caller, difficulty string
		var ended int64
		var cont, fix, known int
		if err := rows.Scan(&sessionID, &caller, &difficulty, &ended, &cont, &fix, &known); err != nil {
			rows.Close()
			return protocol.Calibration{}, err
		}
		if sessionID == "" {
			continue
		}
		sess := sessions[sessionID]
		if sess == nil {
			// The first row holds what the task asked for. A follow-up run
			// may have no caller or difficulty of its own.
			sess = &calibrationSession{caller: caller, difficulty: difficulty}
			sessions[sessionID] = sess
			order = append(order, sessionID)
		}
		sess.cont += cont
		sess.fix += fix
		if known == 0 {
			sess.unknown = true
		}
		sess.endedAt = ended
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return protocol.Calibration{}, err
	}
	rows.Close()

	samples := []protocol.CalibrationSample{}
	for _, sessionID := range order {
		sess := sessions[sessionID]
		// A run from before the counters shipped says nothing about rework.
		if sess.caller == "" || sess.unknown {
			continue
		}
		difficulty := sess.difficulty
		if difficulty == "" {
			difficulty = "normal"
		}
		samples = append(samples, protocol.CalibrationSample{Caller: sess.caller, Difficulty: difficulty,
			Continue: sess.cont, Fix: sess.fix, EndedAt: sess.endedAt})
	}

	scores, err := s.scores()
	if err != nil {
		return protocol.Calibration{}, err
	}
	callers := []string{}
	seen := map[string]bool{}
	for _, sample := range samples {
		if !seen[sample.Caller] {
			seen[sample.Caller] = true
			callers = append(callers, sample.Caller)
		}
	}
	names, models, strong, err := s.callerFacts(scores, callers)
	if err != nil {
		return protocol.Calibration{}, err
	}
	cal := protocol.BuildCalibration(samples, strong)
	ratings, err := s.RatingCounts()
	if err != nil {
		return protocol.Calibration{}, err
	}
	cal.Ratings = ratings
	for i := range cal.Callers {
		cal.Callers[i].Name = names[cal.Callers[i].Caller]
		cal.Callers[i].Model = models[cal.Callers[i].Caller]
	}
	sort.SliceStable(cal.Callers, func(i, j int) bool {
		a, b := cal.Callers[i], cal.Callers[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Caller < b.Caller
	})
	return cal, nil
}

// callerFacts returns the display name, model and strength of the callers in
// the window. Only those are read: the table is built on every nodes call.
func (s *Store) callerFacts(scores []protocol.ModelScore, callers []string) (names, models map[string]string, strong map[string]bool, err error) {
	names, models, strong = map[string]string{}, map[string]string{}, map[string]bool{}
	if len(callers) == 0 {
		return names, models, strong, nil
	}
	args := make([]any, len(callers))
	for i, c := range callers {
		args[i] = c
	}
	rows, err := s.db.Query(`SELECT s.id, COALESCE(NULLIF(p.name,''), s.name, ''), s.model
		FROM sessions s LEFT JOIN profiles p ON p.session_id = s.id
		WHERE s.id IN (?`+strings.Repeat(",?", len(callers)-1)+`)`, args...)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, model string
		if err := rows.Scan(&id, &name, &model); err != nil {
			return nil, nil, nil, err
		}
		names[id], models[id] = name, model
		if score, ok := protocol.LookupModelScore(model, scores); ok && score.Score >= protocol.CalibrationStrongScore {
			strong[id] = true
		}
	}
	return names, models, strong, rows.Err()
}

// WorkerSessionOwner returns the session that started a worker, from the
// worker part of its session record. The record is still there when the end
// report arrives; the run keeps the caller after the session is cleaned. A
// missing session or an older machine gives an empty owner.
func (s *Store) WorkerSessionOwner(sessionID string) (string, error) {
	var raw string
	err := s.db.QueryRow(`SELECT worker FROM sessions WHERE id = ?`, sessionID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || raw == "" {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var w protocol.WorkerInfo
	if json.Unmarshal([]byte(raw), &w) != nil {
		return "", nil
	}
	return w.Owner, nil
}
