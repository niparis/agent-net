package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	_ "modernc.org/sqlite"
)

// The replay tool reads a copy of the gateway store. It never writes: the file
// is opened read only, and nothing here calls a gateway Store method, because a
// Store open migrates the file.

// Task is one picker task: a run and its continuations, with its judge result
// and its outcome, built the way gateway.pickerTaskRecords builds it.
type Task struct {
	Root    string
	Key     string
	Harness string

	D float64

	Complete       bool
	FixRounds      int
	Defects        int
	DefectSeverity int
	FailedAccept   int
	FailedTest     int
	FailedReview   int
	// Reusable is true when a task that did not end complete still opened a
	// change that merged. Option A may keep such work instead of scoring it 0.
	Reusable bool

	End    int64
	Tokens protocol.RunTokens
	Cost   float64

	ScoreToday float64
	ScoreA     float64
	Dirty      bool
}

// Snapshot is the read-only contents of one gateway store copy. The raw rows
// are kept so a task can be rebuilt as it stood at any past moment: a pick can
// then be replayed against the table the picker had then, not against today's.
type Snapshot struct {
	Runs    []runRec
	Judged  map[string][]judgedRec
	Defects map[string][]defectRec
	Failed  map[string][]stepRec
	Merged  map[string]int64
	Usage   map[string]protocol.RunTokens

	// Tasks is the whole history, used by the replay's checks.
	Tasks []Task

	Picks      []Pick
	Prices     *protocol.PriceBook
	Profiles   []protocol.WorkerProfile
	Scores     []protocol.ModelScore
	Minimums   map[string]float64
	PriceCount int

	// Exhausted is the quota providers a start would be refused on. The store
	// keeps no quota reading from the time of a pick, so the replay applies the
	// set to every pick: such a candidate does not compete, as in the live
	// picker.
	Exhausted map[string]bool
	// Protected is the protected providers (profile provider ids, as
	// protocol.ProfileProvider names them). The live picker never starts on
	// one, so no method competes with them.
	Protected map[string]bool
}

// gate returns the pick without the candidates whose provider is exhausted or
// protected.
// The gateway records the whole roster, a blocked candidate included, and the
// pace it recorded for one can come from the windows that still have room.
func (s *Snapshot) gate(p Pick) (Pick, []string) {
	if len(s.Exhausted) == 0 && len(s.Protected) == 0 {
		return p, nil
	}
	out := p
	out.Rows = nil
	var left []string
	for _, r := range p.Rows {
		if s.Exhausted[protocol.QuotaProviderForStart(r.Harness, r.Model)] || s.Protected[strings.ToLower(protocol.ProfileProvider(r.Harness, r.Model))] {
			left = append(left, r.Name)
			continue
		}
		out.Rows = append(out.Rows, r)
	}
	return out, left
}

type runRec struct {
	session, task, model, harness, outcome string
	ended                                  int64
	fix                                    int
}

type judgedRec struct {
	d        float64
	assessed int64
}

type defectRec struct {
	at       int64
	severity int
}

type stepRec struct {
	at   int64
	step string
}

// OpenSnapshot opens path read only and reads everything the replay needs.
func OpenSnapshot(path string) (*Snapshot, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	s := &Snapshot{Judged: map[string][]judgedRec{}, Defects: map[string][]defectRec{},
		Failed: map[string][]stepRec{}, Merged: map[string]int64{}, Usage: map[string]protocol.RunTokens{}}
	// The price book and the scores are needed while the tasks are priced.
	if err := s.readPrices(db); err != nil {
		return nil, fmt.Errorf("prices: %w", err)
	}
	if err := s.readSettings(db); err != nil {
		return nil, fmt.Errorf("settings: %w", err)
	}
	if err := s.readRuns(db); err != nil {
		return nil, fmt.Errorf("runs: %w", err)
	}
	if err := s.readEvidence(db); err != nil {
		return nil, fmt.Errorf("evidence: %w", err)
	}
	if err := s.readUsage(db); err != nil {
		return nil, fmt.Errorf("usage: %w", err)
	}
	s.Tasks = s.TasksAt(math.MaxInt64)
	if err := s.readPicks(db); err != nil {
		return nil, fmt.Errorf("picks: %w", err)
	}
	return s, nil
}

// OptionAScore is the CTO proposal for the task score: a fix round costs
// nothing (its tokens are already in the run cost), a failed test costs 0.2, a
// failed review 0.3, and a later defect costs 0.25 to 1.0 by severity, counted
// once. A task that did not end complete scores 0 unless its work was reusable.
func OptionAScore(t Task) float64 {
	return protocol.PickerTaskScoreV2(protocol.PickerTaskFacts{Complete: t.Complete, Merged: t.Reusable, FixRounds: t.FixRounds, FailedAccept: t.FailedAccept, FailedTest: t.FailedTest, FailedReview: t.FailedReview, Defects: t.Defects, DefectSeverity: t.DefectSeverity})
}

// TasksAt rebuilds the task list as it stood at cut (unix seconds): only runs
// that had ended, judge results that had arrived, defects and failed steps that
// had been recorded, and changes that had merged, by then. A chain that grew a
// continuation after cut did not yet hold that continuation then.
func (s *Snapshot) TasksAt(cut int64) []Task {
	type chain struct {
		task     Task
		sessions []string
		seen     map[string]bool
		facts    protocol.PickerTaskFacts
	}
	groups := map[string]*chain{}
	var order []*chain
	for _, r := range s.Runs {
		if r.ended > cut || r.session == "" || r.outcome == protocol.OutcomeStopped || r.outcome == protocol.OutcomeInfra {
			continue
		}
		root := r.task
		if root == "" {
			root = r.session
		}
		c := groups[root]
		if c == nil {
			c = &chain{task: Task{Root: root, Key: protocol.PickerModelKey(r.harness, r.model), Harness: r.harness}, seen: map[string]bool{}}
			groups[root] = c
			order = append(order, c)
		}
		// The runs come oldest first, so the last one sets the outcome and end.
		c.facts.Complete = r.outcome == protocol.OutcomeComplete
		c.facts.FixRounds += r.fix
		c.task.End = r.ended
		if !c.seen[r.session] {
			c.seen[r.session] = true
			c.sessions = append(c.sessions, r.session)
		}
	}
	out := make([]Task, 0, len(order))
	for _, c := range order {
		j, ok := s.judgedAt(c.task.Root, cut)
		if !ok {
			for _, session := range c.sessions {
				if j, ok = s.judgedAt(session, cut); ok {
					break
				}
			}
		}
		if !ok || j.d <= 0 {
			continue
		}
		c.task.D = j.d
		for _, session := range c.sessions {
			for _, st := range s.Failed[session] {
				if st.at > cut {
					continue
				}
				switch st.step {
				case protocol.StepAccept:
					c.task.FailedAccept++
				case protocol.StepReview:
					c.task.FailedReview++
				case protocol.StepTest:
					c.task.FailedTest++
				}
			}
			for _, d := range s.Defects[session] {
				if d.at <= cut {
					c.facts.Defects++
					c.facts.DefectSeverity += d.severity
					c.task.Defects++
					c.task.DefectSeverity += d.severity
				}
			}
			if at, ok := s.Merged[session]; ok && at <= cut {
				c.task.Reusable = true
			}
			c.task.Tokens = c.task.Tokens.Plus(s.Usage[session])
		}
		c.facts.FailedSteps = c.task.FailedAccept + c.task.FailedTest + c.task.FailedReview
		c.task.Complete = c.facts.Complete
		c.task.FixRounds = c.facts.FixRounds
		c.task.ScoreToday = protocol.PickerTaskScore(c.facts)
		c.task.ScoreA = OptionAScore(c.task)
		c.task.Dirty = !c.task.Complete || c.task.FailedAccept+c.task.FailedTest+c.task.FailedReview > 0 || c.task.Defects > 0
		if p, _, ok := s.Prices.PriceOf(c.task.Key); ok && !c.task.Tokens.Zero() {
			c.task.Cost = p.Cost(c.task.Tokens)
		}
		out = append(out, c.task)
	}
	return out
}

// judgedAt is the first judge result of a session that had arrived by cut.
func (s *Snapshot) judgedAt(session string, cut int64) (judgedRec, bool) {
	list := s.Judged[session]
	if len(list) == 0 || list[0].assessed > cut {
		return judgedRec{}, false
	}
	return list[0], true
}

func (s *Snapshot) readRuns(db *sql.DB) error {
	rows, err := db.Query(`SELECT session_id,task_id,model,harness,outcome,ended_at,fix_count FROM worker_runs ORDER BY ended_at,id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r runRec
		if err := rows.Scan(&r.session, &r.task, &r.model, &r.harness, &r.outcome, &r.ended, &r.fix); err != nil {
			return err
		}
		s.Runs = append(s.Runs, r)
	}
	return rows.Err()
}

// readEvidence reads the judge results, defects, failed steps and merged
// changes with the time each one landed, so TasksAt can leave out the ones that
// did not exist at a past pick.
func (s *Snapshot) readEvidence(db *sql.DB) error {
	rows, err := db.Query(`SELECT session_id,d,assessed_at
		FROM worker_assessments WHERE d > 0 ORDER BY started_at,assessed_at`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var session string
		var j judgedRec
		if err := rows.Scan(&session, &j.d, &j.assessed); err != nil {
			rows.Close()
			return err
		}
		s.Judged[session] = append(s.Judged[session], j)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	// The defect agent-net records for a failed merger test step is skipped:
	// the failed step is already counted, and one failure costs once.
	rows, err = db.Query(`SELECT session_id,severity,at FROM worker_defects
		WHERE moved_at = 0 AND auto_kind IS NOT 'test'`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var session string
		var d defectRec
		if err := rows.Scan(&session, &d.severity, &d.at); err != nil {
			rows.Close()
			return err
		}
		s.Defects[session] = append(s.Defects[session], d)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	rows, err = db.Query(`SELECT json_extract(c.origin, '$.session_id'), e.step, e.at
		FROM delivery_evidence e JOIN delivery_changes c ON c.id = e.change_id
		WHERE e.result = 'fail' AND e.step IN ('accept','review','test')`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var session *string
		var st stepRec
		if err := rows.Scan(&session, &st.step, &st.at); err != nil {
			rows.Close()
			return err
		}
		if session != nil && *session != "" {
			s.Failed[*session] = append(s.Failed[*session], st)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	rows, err = db.Query(`SELECT json_extract(origin, '$.session_id'), MAX(updated_at) FROM delivery_changes
		WHERE state IN ('merged','done') GROUP BY 1`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var session *string
		var at int64
		if err := rows.Scan(&session, &at); err != nil {
			return err
		}
		if session != nil && *session != "" {
			s.Merged[*session] = at
		}
	}
	return rows.Err()
}

func (s *Snapshot) readUsage(db *sql.DB) error {
	rows, err := db.Query(`SELECT session_id,usage FROM worker_usage`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var session, raw string
		if err := rows.Scan(&session, &raw); err != nil {
			return err
		}
		var t protocol.RunTokens
		if json.Unmarshal([]byte(raw), &t) == nil && !t.Zero() {
			s.Usage[session] = t
		}
	}
	return rows.Err()
}

func (s *Snapshot) readPrices(db *sql.DB) error {
	rows, err := db.Query(`SELECT id,prompt,completion,cache_read,cache_write FROM model_prices`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var list []protocol.ModelPrice
	for rows.Next() {
		var p protocol.ModelPrice
		if err := rows.Scan(&p.ID, &p.Prompt, &p.Completion, &p.CacheRead, &p.CacheWrite); err != nil {
			return err
		}
		list = append(list, p)
	}
	s.PriceCount = len(list)
	s.Prices = protocol.NewPriceBook(list)
	return rows.Err()
}

func (s *Snapshot) readSettings(db *sql.DB) error {
	s.Scores = protocol.EmbeddedModelScores()
	s.Minimums = map[string]float64{}
	var raw string
	err := db.QueryRow(`SELECT value FROM setting_values WHERE key = 'owner.model_scores' AND level = 'network' AND scope = 'network' AND item = ''`).Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if raw != "" {
		var extra []protocol.ModelScore
		if err := json.Unmarshal([]byte(raw), &extra); err != nil {
			return err
		}
		s.Scores = append(extra, s.Scores...)
	}
	raw = ""
	err = db.QueryRow(`SELECT value FROM setting_values WHERE key = 'owner.difficulty_minimums' AND level = 'network' AND scope = 'network' AND item = ''`).Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &s.Minimums); err != nil {
			return err
		}
	}
	raw = ""
	err = db.QueryRow(`SELECT value FROM setting_values WHERE key = 'owner.worker_profiles' AND level = 'network' AND scope = 'network' AND item = ''`).Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &s.Profiles); err != nil {
			return err
		}
	}
	return nil
}

// Pick is one recorded pick: the rows the gateway stored and when it happened.
type Pick struct {
	Session string
	At      int64 // unix seconds
	D       float64
	Rows    []CandRow
}

// CandRow is one candidate of a recorded pick, with the values the live picker
// computed. Zero-valued nullable columns keep their zero.
type CandRow struct {
	Pos        int
	Name       string
	Harness    string
	Provider   string
	Model      string
	Effort     string
	Rule       string
	BucketLo   float64
	BucketHi   float64
	CellScore  float64
	CellLo     float64
	CellHi     float64
	CellN      int
	Allowed    bool
	Qualified  bool
	Prior      bool
	Best       bool
	Chosen     bool
	Winner     bool
	LeftOut    string
	PaceRoom   float64
	PaceFactor float64
	CostPerRun float64
	PClean     float64
	Rescue     float64
	Expected   float64
	Adjusted   float64
	HasCost    bool
}

// Key is the model key of the candidate, as the picker table holds it.
func (r CandRow) Key() string { return protocol.PickerModelKey(r.Harness, r.Model) }

func (s *Snapshot) readPicks(db *sql.DB) error {
	byRun := map[string]*Pick{}
	var order []*Pick
	rows, err := db.Query(`SELECT run_id,pos,name,harness,provider,model,effort,rule,bucket_lo,bucket_hi,
		cell_score,cell_low,cell_high,cell_n,is_allowed,is_qualified,is_prior,is_best,is_chosen,is_winner,left_out_reason,
		cost_per_run,p_clean,rescue_cost,expected_cost,pace_room,pace_factor,adjusted_cost
		FROM pick_candidates ORDER BY run_id,pos`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var runID string
		var r CandRow
		var cellScore, cellLo, cellHi, paceRoom, cost, p, rescue, expected, adjusted sql.NullFloat64
		var cellN sql.NullInt64
		if err := rows.Scan(&runID, &r.Pos, &r.Name, &r.Harness, &r.Provider, &r.Model, &r.Effort, &r.Rule, &r.BucketLo, &r.BucketHi,
			&cellScore, &cellLo, &cellHi, &cellN, &r.Allowed, &r.Qualified, &r.Prior, &r.Best, &r.Chosen, &r.Winner, &r.LeftOut,
			&cost, &p, &rescue, &expected, &paceRoom, &r.PaceFactor, &adjusted); err != nil {
			rows.Close()
			return err
		}
		r.CellScore, r.CellLo, r.CellHi = cellScore.Float64, cellLo.Float64, cellHi.Float64
		r.CellN = int(cellN.Int64)
		r.PaceRoom = paceRoom.Float64
		r.CostPerRun, r.PClean, r.Rescue, r.Expected, r.Adjusted = cost.Float64, p.Float64, rescue.Float64, expected.Float64, adjusted.Float64
		r.HasCost = cost.Valid
		pick := byRun[runID]
		if pick == nil {
			pick = &Pick{}
			byRun[runID] = pick
			order = append(order, pick)
		}
		pick.Rows = append(pick.Rows, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	meta, err := db.Query(`SELECT p.run_id,a.session_id,a.started_at,a.d
		FROM (SELECT DISTINCT run_id FROM pick_candidates) p
		JOIN worker_assessments a ON a.run_id = p.run_id`)
	if err != nil {
		return err
	}
	defer meta.Close()
	for meta.Next() {
		var runID, session string
		var started int64
		var d float64
		if err := meta.Scan(&runID, &session, &started, &d); err != nil {
			return err
		}
		if pick := byRun[runID]; pick != nil {
			pick.Session, pick.At, pick.D = session, started/1e9, d
		}
	}
	if err := meta.Err(); err != nil {
		return err
	}
	s.Picks = make([]Pick, len(order))
	sort.Slice(order, func(i, j int) bool { return order[i].At < order[j].At })
	for i, p := range order {
		s.Picks[i] = *p
	}
	return nil
}
