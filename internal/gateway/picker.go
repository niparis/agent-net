package gateway

import (
	"net/http"
	"slices"
	"sort"
	"sync"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// The picker table (decision 0105) is built from the whole data set and cached.
// refreshPicker runs on the same writes as refreshQuality (a worker run, a
// rating, a defect, a score or profile change) and when a failed accept,
// review or test step is recorded. A pick reads the cache and never the
// database.

// pickerState is the cached table. The table is immutable once built, so a
// reader keeps using the pointer it got.
type pickerState struct {
	mu    sync.RWMutex
	table *protocol.PickerTable
	rev   uint64
	// buildMu serializes rebuilds, so a slow rebuild cannot overwrite a newer one.
	buildMu sync.Mutex
}

func (p *pickerState) get() *protocol.PickerTable {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.table
}

// pickerTable is the cached table. It never touches the database. A store that
// has not built its table yet (a bare test store) answers with an empty one.
func (s *Store) pickerTable() *protocol.PickerTable {
	if t := s.picker.get(); t != nil {
		return t
	}
	return protocol.BuildPickerTable(nil, protocol.DefaultPickerSettings())
}

// pickerSettings reads the picker.* settings at network level.
func (s *Store) pickerSettings() (protocol.PickerSettings, error) {
	snap, err := s.SettingsSnapshot()
	if err != nil {
		return protocol.PickerSettings{}, err
	}
	w := settings.Where{}
	return protocol.PickerSettings{
		BucketWidth:     snap.Float("picker.bucket_width", w),
		K:               snap.Float("picker.k", w),
		AllowedFraction: snap.Float("picker.allowed_fraction", w),
		UncertainBoost:  snap.Float("picker.uncertain_boost", w),
		UncertainN:      snap.Int("picker.uncertain_n", w),
		WeakerBias:      snap.Float("picker.weaker_bias", w),
		ExploreShare:    snap.Float("picker.explore_share", w),
	}.Normalized(), nil
}

// pickerTaskRecord is one task of the picker data: the score facts, the
// judge's D, the model key and the end time of its last counted run. The picker
// table and the weekly scores both read it, so they cannot score a task
// differently.
type pickerTaskRecord struct {
	key   string
	d     float64
	facts protocol.PickerTaskFacts
	end   int64
	// tokens is the usage of the task's sessions, zero when none was recorded.
	tokens protocol.RunTokens
}

// pickerTaskRecords reads each task's facts once, whatever its judge result.
// A run and its continuations are one task (decision 0105). The chain link is
// the task root of the run: task_id when the continuation started a new
// session, otherwise the session id, because a continue to the same session
// records another worker_runs row with the same session. This is the same root
// the rework counters use (runsSnapshot). The first counted run gives the model
// and the first judge result of the chain gives D. The last counted run gives
// the outcome and the task's end. Fix rounds, defects and failed steps count
// once per chain; a continuation costs nothing. Stopped and infrastructure runs
// do not contribute. No time cut is applied.
func (s *Store) pickerTaskRecords() ([]pickerTaskRecord, error) {
	return s.pickerTaskRecordsUsing(nil)
}

// pickerTaskRecordsUsing is pickerTaskRecords with the usage of each session
// given. Nil reads worker_usage. The backtest passes the usage it parsed from
// stored notices for runs that have none recorded.
func (s *Store) pickerTaskRecordsUsing(usage map[string]protocol.RunTokens) ([]pickerTaskRecord, error) {
	snap, err := s.runsSnapshot()
	if err != nil {
		return nil, err
	}
	// The first valid judge result of each session. The rows come oldest first.
	judged := map[string]float64{}
	rows, err := s.db.Query(`SELECT session_id, d FROM worker_assessments WHERE d > 0 ORDER BY started_at, assessed_at`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var session string
		var d float64
		if err := rows.Scan(&session, &d); err != nil {
			rows.Close()
			return nil, err
		}
		if judged[session] == 0 {
			judged[session] = d
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	defects := map[string][2]int{}
	rows, err = s.db.Query(`SELECT session_id, COUNT(*), COALESCE(SUM(severity), 0) FROM worker_defects WHERE moved_at = 0 GROUP BY session_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var session string
		var count, severity int
		if err := rows.Scan(&session, &count, &severity); err != nil {
			rows.Close()
			return nil, err
		}
		defects[session] = [2]int{count, severity}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	// A failed accept, review or test step counts against the worker whose
	// session opened the change.
	failed := map[string]int{}
	rows, err = s.db.Query(`SELECT json_extract(c.origin, '$.session_id'), COUNT(*)
		FROM delivery_evidence e JOIN delivery_changes c ON c.id = e.change_id
		WHERE e.result = 'fail' AND e.step IN ('accept', 'review', 'test')
		GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var session *string
		var count int
		if err := rows.Scan(&session, &count); err != nil {
			rows.Close()
			return nil, err
		}
		if session != nil {
			failed[*session] = count
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	if usage == nil {
		if usage, err = s.workerUsage(); err != nil {
			return nil, err
		}
	}
	counts := func(outcome string) bool {
		return outcome != protocol.OutcomeStopped && outcome != protocol.OutcomeInfra
	}
	type chain struct {
		root     string
		key      string
		facts    protocol.PickerTaskFacts
		end      int64
		sessions []string
		tokens   protocol.RunTokens
	}
	groups := map[string]*chain{}
	var order []*chain
	for _, r := range snap.rows {
		if r.session == "" || !counts(r.outcome) {
			continue
		}
		root := r.task
		if root == "" {
			root = r.session
		}
		c := groups[root]
		if c == nil {
			c = &chain{root: root, key: protocol.PickerModelKey(r.harness, r.model)}
			groups[root] = c
			order = append(order, c)
		}
		// The rows come oldest first, so the last row is the final outcome and
		// its end is the task's end.
		c.facts.Complete = r.outcome == protocol.OutcomeComplete
		c.facts.FixRounds += r.fix
		c.end = r.ended
		if !slices.Contains(c.sessions, r.session) {
			c.sessions = append(c.sessions, r.session)
			// A session's usage is its running total, so it counts once even
			// when the session had several runs.
			c.tokens = c.tokens.Plus(usage[r.session])
		}
	}
	out := make([]pickerTaskRecord, 0, len(order))
	for _, c := range order {
		// D is the first judge result of the root session, or of the first
		// session of the chain that has one.
		d := judged[c.root]
		for _, session := range c.sessions {
			// A session is in one chain only, so its evidence counts once.
			c.facts.FailedSteps += failed[session]
			c.facts.Defects += defects[session][0]
			c.facts.DefectSeverity += defects[session][1]
			if d <= 0 {
				d = judged[session]
			}
		}
		out = append(out, pickerTaskRecord{key: c.key, d: d, facts: c.facts, end: c.end, tokens: c.tokens})
	}

	return out, nil
}

// pickerTasksFrom turns task records into picker tasks, pricing each one's
// tokens with the book.
func pickerTasksFrom(records []pickerTaskRecord, book *protocol.PriceBook) []protocol.PickerTask {
	out := make([]protocol.PickerTask, 0, len(records))
	for _, r := range records {
		if r.d <= 0 || r.key == "" {
			continue
		}
		t := protocol.PickerTask{Key: r.key, D: r.d, Score: protocol.PickerTaskScore(r.facts), Tokens: r.tokens}
		if p, _, ok := book.PriceOf(r.key); ok && !r.tokens.Zero() {
			t.Cost = p.Cost(r.tokens)
		}
		out = append(out, t)
	}
	return out
}

// pickerTasks is the picker's task list: the records with a judge result and a
// model key, scored by PickerTaskScore.
func (s *Store) pickerTasks() ([]protocol.PickerTask, error) {
	records, err := s.pickerTaskRecords()
	if err != nil {
		return nil, err
	}
	return pickerTasksFrom(records, s.priceBook()), nil
}

// refreshPicker rebuilds the cached table from the whole data set.
func (s *Store) refreshPicker() error {
	s.picker.buildMu.Lock()
	defer s.picker.buildMu.Unlock()
	set, err := s.pickerSettings()
	if err != nil {
		return err
	}
	tasks, err := s.pickerTasks()
	if err != nil {
		return err
	}
	table := protocol.BuildPickerTable(tasks, set)
	s.picker.mu.Lock()
	s.picker.rev++
	table.Revision = s.picker.rev
	s.picker.table = table
	s.picker.mu.Unlock()
	return nil
}

// refreshPickerLogged is refreshPicker for a write that has already
// committed: an error there is logged, and the next write rebuilds.
func (s *Store) refreshPickerLogged() {
	if err := s.refreshPicker(); err != nil {
		s.logf("picker refresh", "err", err)
	}
}

// pickerSettingsChanged rebuilds the table when a picker.* setting changed. It
// runs after any settings commit.
func (s *Store) pickerSettingsChanged() {
	set, err := s.pickerSettings()
	if err != nil {
		s.logf("picker settings", "err", err)
		return
	}
	if t := s.picker.get(); t != nil && t.Settings() == set {
		return
	}
	s.refreshPickerLogged()
}

// pickerStepCounts tells whether a step result changes a task score.
func pickerStepCounts(step, result string) bool {
	if result != protocol.StepResultFail {
		return false
	}
	switch step {
	case protocol.StepAccept, protocol.StepReview, protocol.StepTest:
		return true
	}
	return false
}

// PickerAnalysis is the read API of the picker: every model key with every
// bucket, the bucket means, the settings in use and the benchmark ceiling of
// each registered profile. It reads the cache, plus the profiles and scores.
func (s *Store) PickerAnalysis() (protocol.PickerAnalysis, error) {
	profiles, err := s.WorkerProfiles()
	if err != nil {
		return protocol.PickerAnalysis{}, err
	}
	scores, err := s.scores()
	if err != nil {
		return protocol.PickerAnalysis{}, err
	}
	minimums, err := s.difficultyMinimums()
	if err != nil {
		return protocol.PickerAnalysis{}, err
	}
	rows := make([]protocol.PickerProfileRow, 0, len(profiles))
	var keys []string
	for _, p := range profiles {
		model := p.Selection().LegacyModel()
		key := protocol.PickerModelKey(p.Harness, model)
		row := protocol.PickerProfileRow{Name: p.Name, Key: key, Model: model}
		c := protocol.WorkerModelCandidate{Name: p.Name, Harness: p.Harness, Model: model}
		if score, level, ceiling, ok := protocol.WorkerBenchmarkCeiling(c, scores, minimums); ok {
			row.Score, row.Level, row.Ceiling, row.Scored = score, level, ceiling, true
		}
		rows = append(rows, row)
		keys = append(keys, key)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	out := s.pickerTable().Analysis(keys...)
	out.Profiles = rows
	return out, nil
}

func (g *Gateway) handlePicker(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.nodeAuthed(r); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	out, err := g.store.PickerAnalysis()
	writeJSON(w, out, err)
}
