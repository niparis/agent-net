package gateway

import (
	"net/http"
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
		BucketWidth:         snap.Float("picker.bucket_width", w),
		K:                   snap.Float("picker.k", w),
		AllowedFraction:     snap.Float("picker.allowed_fraction", w),
		OverqualifiedMargin: snap.Float("picker.overqualified_margin", w),
		UncertainBoost:      snap.Float("picker.uncertain_boost", w),
		UncertainN:          snap.Int("picker.uncertain_n", w),
	}.Normalized(), nil
}

// pickerTasks scores each task once. A continuation's task_id links its run
// to the root session. Its first model and assessment and last counted outcome
// describe the task; fixes and session evidence count once across the chain.
// Stopped and infrastructure runs do not contribute. No time cut is applied.
func (s *Store) pickerTasks() ([]protocol.PickerTask, error) {
	snap, err := s.runsSnapshot()
	if err != nil {
		return nil, err
	}
	// The latest valid judge result of each session. The rows come oldest first.
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
		judged[session] = d
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}

	defects := map[string][2]int{}
	rows, err = s.db.Query(`SELECT session_id, COUNT(*), COALESCE(SUM(severity), 0) FROM worker_defects GROUP BY session_id`)
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

	counts := func(outcome string) bool {
		return outcome != protocol.OutcomeStopped && outcome != protocol.OutcomeInfra
	}
	// A continuation records the root session in task_id. Only group roots
	// that have such a link: unrelated runs of a session remain separate.
	linked := map[string]bool{}
	for _, r := range snap.rows {
		if r.task != "" && r.task != r.session {
			linked[r.task] = true
		}
	}
	type chain struct {
		key      string
		d        float64
		facts    protocol.PickerTaskFacts
		sessions map[string]bool
	}
	groups := map[string]*chain{}
	var order []*chain
	for _, r := range snap.rows {
		if r.session == "" || !counts(r.outcome) {
			continue
		}
		root := r.task
		if root == "" && linked[r.session] {
			root = r.session
		}
		var c *chain
		if root != "" {
			c = groups[root]
		}
		if c == nil {
			c = &chain{key: protocol.PickerModelKey(r.harness, r.model), d: judged[r.session], sessions: map[string]bool{}}
			if root != "" && judged[root] > 0 {
				c.d = judged[root]
			}
			if root != "" {
				groups[root] = c
			}
			order = append(order, c)
		}
		c.facts.Complete = r.outcome == protocol.OutcomeComplete
		c.facts.FixRounds += r.fix
		c.sessions[r.session] = true
	}
	// Session evidence belongs once to its final counted run, as before.
	last := map[string]*chain{}
	for _, c := range order {
		for session := range c.sessions {
			last[session] = c
		}
	}
	out := make([]protocol.PickerTask, 0, len(order))
	for _, c := range order {
		if c.d <= 0 || c.key == "" {
			continue
		}
		for session := range c.sessions {
			if last[session] != c {
				continue
			}
			c.facts.FailedSteps += failed[session]
			c.facts.Defects += defects[session][0]
			c.facts.DefectSeverity += defects[session][1]
		}
		out = append(out, protocol.PickerTask{Key: c.key, D: c.d, Score: protocol.PickerTaskScore(c.facts)})
	}

	return out, nil
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
