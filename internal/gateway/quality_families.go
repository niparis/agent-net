package gateway

import (
	"sort"
	"strings"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

// familyTask is one worker session with known counters: a task, not a run.
// Late defects reported after the hand-back add their severity points to the
// task's headache.
type familyTask struct {
	caller, model, harness, difficulty string
	fix, cont                          int
	lateDefects                        int
	lateSeverity                       float64
	defectMinutes                      float64
}

type familyCount struct {
	tasks, fixes, continues int
	lateDefects             int
	lateSeverity            float64
	defectMinutes           float64
}

// qualityModelFamily folds a model name into the family the owner reads:
// deepseek, luna, sol, terra, sonnet or opus. An unknown name is its own row.
func qualityModelFamily(model string) string {
	name := qualityModel(model)
	if name == "" {
		return "default"
	}
	for _, token := range strings.FieldsFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		switch token {
		case "deepseek", "luna", "sol", "terra", "sonnet", "opus":
			return token
		}
	}
	return name
}

// qualityLevelName buckets the requested difficulty. Anything that is not a
// known level, including an empty value, is the dim "unknown" table.
func qualityLevelName(difficulty string) string {
	for _, level := range protocol.DifficultyLevels {
		if difficulty == level {
			return level
		}
	}
	return "unknown"
}

func qualityLevelOrder() []string {
	return append(append([]string(nil), protocol.DifficultyLevels...), "unknown")
}

func addFamilyTask(counts map[string]*familyCount, family string, task familyTask) {
	c := counts[family]
	if c == nil {
		c = &familyCount{}
		counts[family] = c
	}
	c.tasks++
	c.fixes += task.fix
	c.continues += task.cont
	c.lateDefects += task.lateDefects
	c.lateSeverity += task.lateSeverity
	c.defectMinutes += task.defectMinutes
}

func familyRows(counts map[string]*familyCount) []protocol.QualityFamily {
	out := make([]protocol.QualityFamily, 0, len(counts))
	for name, c := range counts {
		if c.tasks == 0 {
			continue
		}
		row := protocol.QualityFamily{Family: name, Tasks: c.tasks, Fixes: c.fixes, Continues: c.continues, LateDefects: c.lateDefects}
		row.FollowUps = (float64(c.fixes) + protocol.CalibrationContinueWeight*float64(c.continues)) / float64(c.tasks)
		if c.lateDefects > 0 {
			row.LateDefectSeverity = c.lateSeverity / float64(c.lateDefects)
		}
		row.Headache = (protocol.QualityContinueMinutes*float64(c.continues) +
			protocol.QualityFixMinutes*float64(c.fixes) + c.defectMinutes) / float64(c.tasks)
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tasks != out[j].Tasks {
			return out[i].Tasks > out[j].Tasks
		}
		return out[i].Family < out[j].Family
	})
	return out
}

func copyQualityLevels(levels []protocol.QualityLevel) []protocol.QualityLevel {
	out := append([]protocol.QualityLevel(nil), levels...)
	for i := range out {
		out[i].Families = append([]protocol.QualityFamily(nil), out[i].Families...)
	}
	return out
}

func levelRows(levels map[string]map[string]*familyCount) []protocol.QualityLevel {
	out := []protocol.QualityLevel{}
	for _, level := range qualityLevelOrder() {
		families := familyRows(levels[level])
		if len(families) == 0 {
			continue
		}
		tasks := 0
		for _, f := range families {
			tasks += f.Tasks
		}
		out = append(out, protocol.QualityLevel{Difficulty: level, Tasks: tasks, Families: families})
	}
	return out
}

type defectSummary struct {
	count    int
	severity float64
	minutes  float64
}

// qualityDefects reads late defect reports by worker session. The worker_defects
// table and its severity column ship with the rating work, so a report always
// has a severity. The severity decides how many orchestrator minutes the task
// pays, not just the displayed average.
func (s *Store) qualityDefects(cut int64) (map[string]defectSummary, error) {
	out := map[string]defectSummary{}
	rows, err := s.db.Query(`SELECT session_id, severity, COUNT(*) FROM worker_defects
		WHERE at >= ? GROUP BY session_id, severity`, cut)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var session string
		var severity, n int
		if err := rows.Scan(&session, &severity, &n); err != nil {
			return nil, err
		}
		d := out[session]
		d.count += n
		d.severity += float64(severity * n)
		d.minutes += protocol.DefectMinutes(severity) * float64(n)
		out[session] = d
	}
	return out, rows.Err()
}

// qualityFamilyTasks reads the per-session rework counters once. One task is
// one worker session: the counters of its runs add up. Stopped runs and
// sessions with an unknown counter say nothing and stay out. The caller list
// still includes a caller whose runs were all stopped, so the selector can
// show every agent that started work.
func (s *Store) qualityFamilyTasks() ([]familyTask, []string, error) {
	cut := s.now().Add(-calibrationWindow).Unix()
	rows, err := s.db.Query(`SELECT session_id, caller, model, harness, difficulty, outcome, continue_count, fix_count, counters_known
		FROM worker_runs WHERE ended_at >= ? ORDER BY ended_at, id`, cut)
	if err != nil {
		return nil, nil, err
	}
	type sessionTask struct {
		task    familyTask
		unknown bool
	}
	sessions := map[string]*sessionTask{}
	order := []string{}
	callers := []string{}
	seenCaller := map[string]bool{}
	for rows.Next() {
		var sessionID, caller, model, harness, difficulty, outcome string
		var cont, fix, known int
		if err := rows.Scan(&sessionID, &caller, &model, &harness, &difficulty, &outcome, &cont, &fix, &known); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if caller != "" && !seenCaller[caller] {
			seenCaller[caller] = true
			callers = append(callers, caller)
		}
		if outcome == protocol.OutcomeStopped || sessionID == "" {
			continue
		}
		sess := sessions[sessionID]
		if sess == nil {
			// The first row holds what the task asked for. A follow-up run
			// may have no caller, model or difficulty of its own.
			sess = &sessionTask{task: familyTask{caller: caller, model: model, harness: harness, difficulty: difficulty}}
			sessions[sessionID] = sess
			order = append(order, sessionID)
		}
		sess.task.fix += fix
		sess.task.cont += cont
		if known == 0 {
			sess.unknown = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	defects, err := s.qualityDefects(cut)
	if err != nil {
		return nil, nil, err
	}
	tasks := make([]familyTask, 0, len(order))
	for _, sessionID := range order {
		sess := sessions[sessionID]
		if sess.unknown || sess.task.caller == "" {
			continue
		}
		if d, ok := defects[sessionID]; ok {
			sess.task.lateDefects = d.count
			sess.task.lateSeverity = d.severity
			sess.task.defectMinutes = d.minutes
		}
		tasks = append(tasks, sess.task)
	}
	return tasks, callers, nil
}

// qualityFamilyTables builds the all-agent level tables and one set per
// caller. The fit calls it from refreshQuality, which holds the quality lock;
// reads of the snapshot never touch the database.
func (s *Store) qualityFamilyTables() ([]protocol.QualityLevel, []protocol.QualityAgent, error) {
	tasks, callers, err := s.qualityFamilyTasks()
	if err != nil {
		return nil, nil, err
	}
	all := map[string]map[string]*familyCount{}
	byCaller := map[string]map[string]map[string]*familyCount{}
	for _, task := range tasks {
		level := qualityLevelName(task.difficulty)
		family := qualityModelFamily(task.model)
		levelCounts := all[level]
		if levelCounts == nil {
			levelCounts = map[string]*familyCount{}
			all[level] = levelCounts
		}
		addFamilyTask(levelCounts, family, task)
		callerLevels := byCaller[task.caller]
		if callerLevels == nil {
			callerLevels = map[string]map[string]*familyCount{}
			byCaller[task.caller] = callerLevels
		}
		callerCounts := callerLevels[level]
		if callerCounts == nil {
			callerCounts = map[string]*familyCount{}
			callerLevels[level] = callerCounts
		}
		addFamilyTask(callerCounts, family, task)
	}
	names, _, _, err := s.callerFacts(nil, callers)
	if err != nil {
		return nil, nil, err
	}
	agents := make([]protocol.QualityAgent, 0, len(callers))
	for _, caller := range callers {
		agents = append(agents, protocol.QualityAgent{Caller: caller, Name: names[caller], Levels: levelRows(byCaller[caller])})
	}
	sort.SliceStable(agents, func(i, j int) bool {
		a, b := agents[i], agents[j]
		an, bn := a.Name, b.Name
		if an == "" {
			an = a.Caller
		}
		if bn == "" {
			bn = b.Caller
		}
		if an != bn {
			return an < bn
		}
		return a.Caller < b.Caller
	})
	return levelRows(all), agents, nil
}
