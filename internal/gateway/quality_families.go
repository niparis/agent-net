package gateway

import (
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

// familyTask combines all sessions of a task.
// Late defects reported after the hand-back add their severity points to the
// task's headache.
type familyTask struct {
	caller, model, harness, difficulty string
	fix, cont, midrun                  int
	worstSeverity                      int
	ratings                            []float64
	touches                            map[string]bool
	lateDefects                        int
	lateSeverity                       float64
	defectMinutes                      float64
	// thisWeek is true when the task ended in the last 7 days.
	thisWeek bool
}

type familyCount struct {
	tasks, fixes, continues, midrun    int
	week                               int
	helped, worstSeverity, ratingCount int
	ratingSum                          float64
	lateDefects                        int
	lateSeverity                       float64
	defectMinutes                      float64
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

// qualityModelKey is a model's row name in Quality details: its family and,
// when the model names one, its effort ("gpt-6.1-terra:high" is "terra high").
func qualityModelKey(model string) string {
	family := qualityModelFamily(model)
	if i := strings.LastIndex(model, ":"); i >= 0 && i < len(model)-1 {
		return family + " " + model[i+1:]
	}
	return family
}

// qualityLevelName buckets the requested difficulty. Anything that is not a
// known level is the dim "unknown" table. An empty value defaults to normal.
func qualityLevelName(difficulty string) string {
	if difficulty == "" {
		return "normal"
	}
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
	if task.thisWeek {
		c.week++
	}
	if task.fix > 0 || task.cont > 0 {
		c.helped++
	}
	c.worstSeverity = max(c.worstSeverity, task.worstSeverity)
	for _, rating := range task.ratings {
		c.ratingSum += rating
		c.ratingCount++
	}
	c.fixes += task.fix
	c.continues += task.cont
	c.midrun += task.midrun
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
		row := protocol.QualityFamily{Family: name, Tasks: c.tasks, TasksWeek: c.week, Fixes: c.fixes, Continues: c.continues, Midrun: c.midrun, LateDefects: c.lateDefects}
		row.NeededHelp = float64(c.helped) / float64(c.tasks)
		row.WorstSeverity = c.worstSeverity
		if c.ratingCount > 0 {
			avg := c.ratingSum / float64(c.ratingCount)
			row.AvgRating = &avg
		}
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

func levelRows(levels, models map[string]map[string]*familyCount) []protocol.QualityLevel {
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
		out = append(out, protocol.QualityLevel{Difficulty: level, Tasks: tasks, Families: families, Models: familyRows(models[level])})
	}
	return out
}

type defectSummary struct {
	count    int
	worst    int
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
		WHERE at >= ? AND moved_at = 0 GROUP BY session_id, severity`, cut)
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
		d.worst = max(d.worst, severity)
		d.severity += float64(severity * n)
		d.minutes += protocol.DefectMinutes(severity) * float64(n)
		out[session] = d
	}
	return out, rows.Err()
}

// qualityFamilyTasks sums runs and defects from every session in each task.
// Tasks with only stopped runs stay out. The caller list
// still includes a caller whose runs were all stopped, so the selector can
// show every agent that started work.
func (s *Store) qualityFamilyTasks() ([]familyTask, []string, error) {
	cut := s.now().Add(-calibrationWindow).Unix()
	runs, callers, err := s.reworkTasks(cut)
	if err != nil {
		return nil, nil, err
	}
	defects, err := s.qualityDefects(cut)
	if err != nil {
		return nil, nil, err
	}
	ratings := map[string]float64{}
	rows, err := s.db.Query(`SELECT session_id, score FROM worker_ratings WHERE at >= ?`, cut)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var session string
		var score float64
		if err := rows.Scan(&session, &score); err != nil {
			rows.Close()
			return nil, nil, err
		}
		ratings[session] = score
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	week := s.now().Add(-7 * 24 * time.Hour).Unix()
	tasks := make([]familyTask, 0, len(runs))
	for _, run := range runs {
		if run.caller == "" {
			continue
		}
		task := familyTask{caller: run.caller, model: run.model, harness: run.harness, difficulty: run.difficulty,
			fix: run.fixCount, cont: run.continueCount, midrun: run.midrun, touches: run.touches, thisWeek: run.endedAt >= week}
		for session := range run.sessions {
			d := defects[session]
			task.worstSeverity = max(task.worstSeverity, d.worst)
			if rating, ok := ratings[session]; ok {
				task.ratings = append(task.ratings, rating)
			}
			task.lateDefects += d.count
			task.lateSeverity += d.severity
			task.defectMinutes += d.minutes
		}
		tasks = append(tasks, task)
	}
	return tasks, callers, nil
}

// qualityFamilyTables builds the all-agent level tables and one set per
// caller. The fit calls it from refreshQuality, which holds the quality lock;
// reads of the snapshot never touch the database.
func (s *Store) qualityFamilyTables() ([]protocol.QualityLevel, []protocol.QualityFamily, []protocol.QualityAgent, error) {
	tasks, callers, err := s.qualityFamilyTasks()
	if err != nil {
		return nil, nil, nil, err
	}
	allAreas := map[string]*familyCount{}
	callerAreas := map[string]map[string]*familyCount{}
	all := map[string]map[string]*familyCount{}
	byCaller := map[string]map[string]map[string]*familyCount{}
	allModels := map[string]map[string]*familyCount{}
	callerModels := map[string]map[string]map[string]*familyCount{}
	for _, task := range tasks {
		if callerAreas[task.caller] == nil {
			callerAreas[task.caller] = map[string]*familyCount{}
		}
		areas := task.touches
		if len(areas) == 0 {
			areas = map[string]bool{"not set": true}
		}
		for area := range areas {
			addFamilyTask(allAreas, area, task)
			addFamilyTask(callerAreas[task.caller], area, task)
		}
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
		key := qualityModelKey(task.model)
		if allModels[level] == nil {
			allModels[level] = map[string]*familyCount{}
		}
		addFamilyTask(allModels[level], key, task)
		if callerModels[task.caller] == nil {
			callerModels[task.caller] = map[string]map[string]*familyCount{}
		}
		if callerModels[task.caller][level] == nil {
			callerModels[task.caller][level] = map[string]*familyCount{}
		}
		addFamilyTask(callerModels[task.caller][level], key, task)
	}
	names, _, _, err := s.callerFacts(nil, callers)
	if err != nil {
		return nil, nil, nil, err
	}
	agents := make([]protocol.QualityAgent, 0, len(callers))
	for _, caller := range callers {
		agents = append(agents, protocol.QualityAgent{Caller: caller, Name: names[caller], Levels: levelRows(byCaller[caller], callerModels[caller]), Areas: areaRows(callerAreas[caller])})
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
	return levelRows(all, allModels), areaRows(allAreas), agents, nil
}

func areaRows(counts map[string]*familyCount) []protocol.QualityFamily {
	rows := familyRows(counts)
	byName := map[string]protocol.QualityFamily{}
	for _, row := range rows {
		byName[row.Family] = row
	}
	out := make([]protocol.QualityFamily, 0, 8)
	for _, name := range []string{"db", "hot-path", "ui", "docs", "tests", "config", "other", "not set"} {
		row := byName[name]
		row.Family = name
		out = append(out, row)
	}
	return out
}
