package gateway

import (
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// ModelHealth is a short-lived provider health signal, independent of quality.
type ModelHealth struct {
	Failures int
	Until    time.Time
	Cooling  bool
}

// ModelHealth returns infrastructure failures across nodes since the last
// successful run. The model ID includes its provider. Judge integrations can
// use the same method and record classified outcomes with RecordWorkerRun.
func (s *Store) ModelHealth(harness, model string) (ModelHealth, error) {
	now := s.now()
	rows, err := s.db.Query(`SELECT outcome, ended_at FROM worker_runs WHERE harness=? AND model=? AND ended_at>=? AND ended_at<=? ORDER BY ended_at DESC, id DESC`, harness, model, now.Add(-2*time.Hour).Unix(), now.Unix())
	if err != nil {
		return ModelHealth{}, err
	}
	defer rows.Close()
	var h ModelHealth
	var latest, oldest time.Time
	for rows.Next() {
		var outcome string
		var ended int64
		if err := rows.Scan(&outcome, &ended); err != nil {
			return ModelHealth{}, err
		}
		if outcome == protocol.OutcomeComplete {
			break
		}
		if outcome == protocol.OutcomeInfra {
			at := time.Unix(ended, 0)
			if h.Failures == 0 {
				latest = at
			}
			oldest = at
			h.Failures++
		}
	}
	if err := rows.Err(); err != nil {
		return ModelHealth{}, err
	}
	if h.Failures >= 2 && (h.Failures > 2 || latest.Sub(oldest) <= 15*time.Minute) {
		delay := 15 * time.Minute
		for i := 2; i < h.Failures && delay < 2*time.Hour; i++ {
			delay *= 2
		}
		if delay > 2*time.Hour {
			delay = 2 * time.Hour
		}
		h.Until = latest.Add(delay)
		h.Cooling = now.Before(h.Until)
	}
	return h, nil
}
