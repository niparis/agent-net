package gateway

import (
	"encoding/json"
	"strings"
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
// successful run or assessment. The model ID includes its provider. Judge
// integrations can use the same method and recorded assessment results.
func (s *Store) ModelHealth(harness, model string) (ModelHealth, error) {
	now := s.now()
	rows, err := s.db.Query(`SELECT outcome, ended_at, '', '', '', '', 0, id FROM worker_runs WHERE harness=? AND model=? AND ended_at>=? AND ended_at<=?
 UNION ALL SELECT '', assessed_at, judge_harness, judge_model, error, reasons, reasoning, rowid FROM worker_assessments WHERE assessed_at>=? AND assessed_at<=?
 ORDER BY 2 DESC, 8 DESC`, harness, model, now.Add(-2*time.Hour).Unix(), now.Unix(), now.Add(-2*time.Hour).Unix(), now.Unix())
	if err != nil {
		return ModelHealth{}, err
	}
	defer rows.Close()
	var h ModelHealth
	var latest, oldest time.Time
	for rows.Next() {
		var outcome, judgeHarness, judgeModel, detail, reasons string
		var ended int64
		var reasoning int
		var sequence int64
		if err := rows.Scan(&outcome, &ended, &judgeHarness, &judgeModel, &detail, &reasons, &reasoning, &sequence); err != nil {
			return ModelHealth{}, err
		}
		if outcome == "" {
			var fields map[string]string
			if err := json.Unmarshal([]byte(reasons), &fields); err != nil {
				return ModelHealth{}, err
			}
			name := model
			if !strings.HasPrefix(model, harness) {
				name = harness + " " + model
			}
			timeout := "judge " + name + " timed out "
			if strings.Contains(detail, timeout) || strings.Contains(fields["judge_note"], timeout) {
				outcome = protocol.OutcomeInfra
			} else if judgeHarness == harness && judgeModel == model && detail == "" && reasoning > 0 {
				outcome = protocol.OutcomeComplete
			}
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
