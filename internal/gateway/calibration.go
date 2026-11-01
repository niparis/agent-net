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

// Calibration reads the read-only difficulty table: per caller and
// difficulty, the weighted rework median of the last seven finished tasks,
// the two offsets and what the loop would do. It never writes state and the
// chooser never reads it.
func (s *Store) Calibration() (protocol.Calibration, error) {
	cut := s.now().Add(-calibrationWindow).Unix()
	rows, err := s.db.Query(`SELECT caller, difficulty, ended_at, continue_count, fix_count
		FROM worker_runs WHERE caller <> '' AND ended_at >= ? ORDER BY ended_at, id`, cut)
	if err != nil {
		return protocol.Calibration{}, err
	}
	samples := []protocol.CalibrationSample{}
	for rows.Next() {
		var caller, difficulty string
		var ended int64
		var cont, fix int
		if err := rows.Scan(&caller, &difficulty, &ended, &cont, &fix); err != nil {
			rows.Close()
			return protocol.Calibration{}, err
		}
		if difficulty == "" {
			difficulty = "normal"
		}
		samples = append(samples, protocol.CalibrationSample{Caller: caller, Difficulty: difficulty,
			Continue: cont, Fix: fix, EndedAt: ended})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return protocol.Calibration{}, err
	}
	rows.Close()

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
	cal.Quality = s.QualityAnalysis()
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
