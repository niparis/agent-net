package gateway

import (
	"encoding/json"
	"sort"
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
	var events []healthEvent
	for rows.Next() {
		var e healthEvent
		if err := rows.Scan(&e.outcome, &e.ended, &e.judgeHarness, &e.judgeModel, &e.detail, &e.reasons, &e.reasoning, &e.sequence); err != nil {
			return ModelHealth{}, err
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return ModelHealth{}, err
	}
	return modelHealthOf(harness, model, events, now)
}

// healthEvent is one row of the health window: a worker run of the model
// (outcome set) or a judge assessment (outcome empty).
type healthEvent struct {
	outcome, judgeHarness, judgeModel, detail, reasons string
	ended                                              int64
	reasoning                                          int
	sequence                                           int64
}

// modelHealthOf reads the events newest first, as ModelHealth always has.
func modelHealthOf(harness, model string, events []healthEvent, now time.Time) (ModelHealth, error) {
	var h ModelHealth
	var latest, oldest time.Time
	for _, e := range events {
		outcome := e.outcome
		if outcome == "" {
			var fields map[string]string
			if err := json.Unmarshal([]byte(e.reasons), &fields); err != nil {
				return ModelHealth{}, err
			}
			name := model
			if !strings.HasPrefix(model, harness) {
				name = harness + " " + model
			}
			timeout := "judge " + name + " timed out "
			if strings.Contains(e.detail, timeout) || strings.Contains(fields["judge_note"], timeout) {
				outcome = protocol.OutcomeInfra
			} else if e.judgeHarness == harness && e.judgeModel == model && e.detail == "" && e.reasoning > 0 {
				outcome = protocol.OutcomeComplete
			}
		}
		if outcome == protocol.OutcomeComplete {
			break
		}
		if outcome == protocol.OutcomeInfra {
			at := time.Unix(e.ended, 0)
			if h.Failures == 0 {
				latest = at
			}
			oldest = at
			h.Failures++
		}
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

// healthReader answers model health for many models of one call from a single
// read of the two-hour window. The answers are those of Store.ModelHealth
// for the same clock reading.
type healthReader struct {
	s      *Store
	now    time.Time
	loaded bool
	runs   map[[2]string][]healthEvent
	judges []healthEvent
	cache  map[[2]string]ModelHealth
}

func (s *Store) newHealthReader() *healthReader {
	return &healthReader{s: s, now: s.now(), cache: map[[2]string]ModelHealth{}}
}

func (r *healthReader) load() error {
	lo, hi := r.now.Add(-2*time.Hour).Unix(), r.now.Unix()
	r.runs = map[[2]string][]healthEvent{}
	rows, err := r.s.db.Query(`SELECT harness, model, outcome, ended_at, id FROM worker_runs WHERE ended_at>=? AND ended_at<=?`, lo, hi)
	if err != nil {
		return err
	}
	for rows.Next() {
		var harness, model string
		var e healthEvent
		if err := rows.Scan(&harness, &model, &e.outcome, &e.ended, &e.sequence); err != nil {
			rows.Close()
			return err
		}
		key := [2]string{harness, model}
		r.runs[key] = append(r.runs[key], e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = r.s.db.Query(`SELECT assessed_at, judge_harness, judge_model, error, reasons, reasoning, rowid FROM worker_assessments WHERE assessed_at>=? AND assessed_at<=?`, lo, hi)
	if err != nil {
		return err
	}
	for rows.Next() {
		var e healthEvent
		if err := rows.Scan(&e.ended, &e.judgeHarness, &e.judgeModel, &e.detail, &e.reasons, &e.reasoning, &e.sequence); err != nil {
			rows.Close()
			return err
		}
		r.judges = append(r.judges, e)
	}
	err = rows.Err()
	rows.Close()
	r.loaded = err == nil
	return err
}

// health is ModelHealth(harness, model).
func (r *healthReader) health(harness, model string) (ModelHealth, error) {
	key := [2]string{harness, model}
	if h, ok := r.cache[key]; ok {
		return h, nil
	}
	if !r.loaded {
		if err := r.load(); err != nil {
			return ModelHealth{}, err
		}
	}
	events := append(append([]healthEvent(nil), r.runs[key]...), r.judges...)
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].ended != events[j].ended {
			return events[i].ended > events[j].ended
		}
		return events[i].sequence > events[j].sequence
	})
	h, err := modelHealthOf(harness, model, events, r.now)
	if err == nil {
		r.cache[key] = h
	}
	return h, err
}
