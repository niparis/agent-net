package protocol

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/difficulty"
)

// The worker picker learns from results (decision 0094). Every finished worker
// task gets a score from 0 to 1. The tasks are grouped by model key (model plus
// effort) and by a bucket of the judge's D, and each cell is shrunk toward the
// bucket mean so a thin cell cannot look certain. ChooseWorkerModel reads the
// cells when it is given a PickerInput.

const (
	// PickerDStart is where the first bucket starts. The judge's D runs from
	// 1.0 to 5.0.
	PickerDStart = 1.0
	// PickerDEnd is the top of the last bucket the read API shows.
	PickerDEnd = 5.0
	// PickerMinCellTasks is how many tasks a cell needs before the pick trusts
	// its own record. Below it the benchmark ceiling decides.
	PickerMinCellTasks = 3
	// pickerZ is the z value of a two sided 90 percent interval.
	pickerZ = 1.645
	// pickerEdgeTolerance keeps a D that sits on a bucket edge in the bucket it
	// starts, whatever the rounding of the width.
	pickerEdgeTolerance = 1e-6
)

// PickerSettings are the owner and CTO settings of the picker (picker.* in the
// settings recipe). A zero field takes its default.
type PickerSettings struct {
	// BucketWidth is the width of a D bucket, starting at 1.0.
	BucketWidth float64 `json:"bucket_width"`
	// K is the weight of the bucket mean, in tasks.
	K float64 `json:"k"`
	// AllowedFraction: a model is allowed unless the top of its interval is
	// below this fraction of the best cell.
	AllowedFraction float64 `json:"allowed_fraction"`
	// OverqualifiedMargin is how far under the best cell a model that is
	// well above the task by its benchmark must score to be left out.
	OverqualifiedMargin float64 `json:"overqualified_margin"`
	// UncertainBoost multiplies the rotation weight of a cell with fewer than
	// UncertainN tasks.
	UncertainBoost float64 `json:"uncertain_boost"`
	UncertainN     int     `json:"uncertain_n"`
}

// DefaultPickerSettings are the values the owner approved, the same as the
// recipe defaults (internal/settings/standard.yaml). The bucket width is one
// third to ten digits, which the owner calls 0.33: the buckets read 1.00, 1.33,
// 1.67, 2.00 and so on.
func DefaultPickerSettings() PickerSettings {
	return PickerSettings{BucketWidth: 0.3333333333, K: 5, AllowedFraction: .9, OverqualifiedMargin: .25, UncertainBoost: 1.5, UncertainN: 5}
}

// Normalized replaces every missing or invalid field with its default.
func (s PickerSettings) Normalized() PickerSettings {
	d := DefaultPickerSettings()
	bad := func(v float64) bool { return v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) }
	if bad(s.BucketWidth) {
		s.BucketWidth = d.BucketWidth
	}
	if bad(s.K) {
		s.K = d.K
	}
	if bad(s.AllowedFraction) {
		s.AllowedFraction = d.AllowedFraction
	}
	if bad(s.OverqualifiedMargin) {
		s.OverqualifiedMargin = d.OverqualifiedMargin
	}
	if bad(s.UncertainBoost) {
		s.UncertainBoost = d.UncertainBoost
	}
	if s.UncertainN <= 0 {
		s.UncertainN = d.UncertainN
	}
	return s
}

// PickerTaskFacts is what the score of one task reads.
type PickerTaskFacts struct {
	// Complete is true when the task ended complete.
	Complete       bool
	FixRounds      int
	FailedSteps    int
	Defects        int
	DefectSeverity int
}

// PickerTaskScore is 0 for a task that did not end complete. Otherwise it is
// 1 minus a quarter for each fix round, each failed accept, review or test step
// and each defect plus each point of defect severity, and never below 0. A
// severity 4 defect costs 1.25.
func PickerTaskScore(f PickerTaskFacts) float64 {
	if !f.Complete {
		return 0
	}
	cost := .25 * float64(f.FixRounds+f.FailedSteps+f.Defects+f.DefectSeverity)
	return math.Max(0, 1-cost)
}

// PickerTask is one judged task as the table reads it.
type PickerTask struct {
	Key   string
	D     float64
	Score float64
}

// PickerModelKey is the key of a model and its effort. Two efforts of one
// model are two keys. A Codex run with no effort is "<model>:default". The
// provider prefix stays in the key; PickerModelDisplay removes it.
func PickerModelKey(harness, model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return ""
	}
	if _, effort := SplitModelLevel(m); effort == "" && harness == "codex" && !strings.HasSuffix(m, ":default") {
		m += ":default"
	}
	return m
}

// PickerModelDisplay is a key without its provider prefix.
func PickerModelDisplay(key string) string {
	if i := strings.LastIndexByte(key, '/'); i >= 0 {
		return key[i+1:]
	}
	return key
}

// PickerCell is one model key in one bucket: the shrunk score, the 90 percent
// interval and the number of tasks behind it.
type PickerCell struct {
	Score float64 `json:"score"`
	Lo    float64 `json:"lo"`
	Hi    float64 `json:"hi"`
	N     int     `json:"n"`
}

type pickerStat struct {
	n          int
	sum, sumSq float64
}

func (s *pickerStat) add(v float64) { s.n++; s.sum += v; s.sumSq += v * v }
func (s pickerStat) mean() float64 {
	if s.n == 0 {
		return 0
	}
	return s.sum / float64(s.n)
}

// variance is the population variance of the tasks.
func (s pickerStat) variance() float64 {
	if s.n == 0 {
		return 0
	}
	m := s.mean()
	return math.Max(0, s.sumSq/float64(s.n)-m*m)
}

// PickerTable holds the cells. It is built once per change of the data and
// then only read, so a pick takes no lock and no query. Callers must not
// change it.
type PickerTable struct {
	// Revision numbers the builds of one store, so a cache of picks can tell
	// that the table changed.
	Revision  uint64
	settings  PickerSettings
	all       pickerStat
	buckets   map[int]pickerStat
	cells     map[string]map[int]pickerStat
	maxBucket int
}

// neutralMean and neutralVariance stand in for a table with no task: an even
// chance, as uncertain as a coin.
const (
	neutralMean     = .5
	neutralVariance = .25
)

// BuildPickerTable groups the tasks by key and bucket.
func BuildPickerTable(tasks []PickerTask, s PickerSettings) *PickerTable {
	t := &PickerTable{settings: s.Normalized(), buckets: map[int]pickerStat{}, cells: map[string]map[int]pickerStat{}}
	for _, task := range tasks {
		if task.Key == "" || task.D <= 0 {
			continue
		}
		b := t.Bucket(task.D)
		t.all.add(task.Score)
		bs := t.buckets[b]
		bs.add(task.Score)
		t.buckets[b] = bs
		row := t.cells[task.Key]
		if row == nil {
			row = map[int]pickerStat{}
			t.cells[task.Key] = row
		}
		cs := row[b]
		cs.add(task.Score)
		row[b] = cs
		if b > t.maxBucket {
			t.maxBucket = b
		}
	}
	return t
}

// Settings are the settings the table was built with.
func (t *PickerTable) Settings() PickerSettings { return t.settings }

// Tasks is the number of tasks in the table.
func (t *PickerTable) Tasks() int { return t.all.n }

// Bucket is the index of the bucket D falls in. Bucket 0 starts at 1.0.
func (t *PickerTable) Bucket(d float64) int {
	return max(0, int(math.Floor((d-PickerDStart)/t.settings.BucketWidth+pickerEdgeTolerance)))
}

// BucketRange is the D range [lo, hi) of a bucket.
func (t *PickerTable) BucketRange(i int) (lo, hi float64) {
	w := t.settings.BucketWidth
	return PickerDStart + float64(i)*w, PickerDStart + float64(i+1)*w
}

// allMeanVariance is the mean and variance of every task.
func (t *PickerTable) allMeanVariance() (float64, float64) {
	if t.all.n == 0 {
		return neutralMean, neutralVariance
	}
	return t.all.mean(), t.all.variance()
}

// bucketMeanVariance is the mean and variance of the tasks of a bucket over all
// models. A bucket with no task takes those of all tasks.
func (t *PickerTable) bucketMeanVariance(i int) (float64, float64) {
	if bs := t.buckets[i]; bs.n > 0 {
		return bs.mean(), bs.variance()
	}
	return t.allMeanVariance()
}

// BucketMean is the mean task score of a bucket over all models.
func (t *PickerTable) BucketMean(i int) float64 {
	m, _ := t.bucketMeanVariance(i)
	return m
}

// Cell is the shrunk score and interval of a key in a bucket.
//
//	score    = (sum of the cell's scores + k x bucket mean) / (n + k)
//	variance = (n x cell variance + k x bucket variance) / (n + k), at least
//	           half the variance of all tasks
//	interval = score +- 1.645 x sqrt(variance / (n + k)), clipped to [0, 1]
//
// An empty cell takes the bucket mean.
func (t *PickerTable) Cell(key string, bucket int) PickerCell {
	cs := t.cells[key][bucket]
	mean, bucketVar := t.bucketMeanVariance(bucket)
	_, allVar := t.allMeanVariance()
	k, n := t.settings.K, float64(cs.n)
	score := (cs.sum + k*mean) / (n + k)
	variance := math.Max((n*cs.variance()+k*bucketVar)/(n+k), allVar*.5)
	half := pickerZ * math.Sqrt(variance/(n+k))
	return PickerCell{Score: score, Lo: math.Max(0, score-half), Hi: math.Min(1, score+half), N: cs.n}
}

// PickerBucketRow is one bucket of the read API.
type PickerBucketRow struct {
	Index int     `json:"index"`
	Lo    float64 `json:"lo"`
	Hi    float64 `json:"hi"`
	Mean  float64 `json:"mean"`
	N     int     `json:"n"`
}

// PickerModelRow is one model key of the read API. Cells has one entry per
// bucket, in bucket order. A key with no task in a bucket has n 0 and the
// bucket mean.
type PickerModelRow struct {
	Key     string       `json:"key"`
	Display string       `json:"display"`
	Tasks   int          `json:"tasks"`
	Cells   []PickerCell `json:"cells"`
}

// PickerProfileRow is a registered profile and its benchmark ceiling: the
// highest D its benchmark score is assumed to handle where it has too few
// tasks of its own.
type PickerProfileRow struct {
	Name  string `json:"name"`
	Key   string `json:"key"`
	Model string `json:"model"`
	// Score is the benchmark score. Level is the highest level it meets, and
	// Ceiling the top of that level's D range. Scored is false when the model
	// has no benchmark score.
	Score   float64 `json:"score,omitempty"`
	Level   string  `json:"level,omitempty"`
	Ceiling float64 `json:"ceiling,omitempty"`
	Scored  bool    `json:"scored"`
}

// PickerAnalysis is the read API of the picker: every model key with every
// bucket, the bucket means, the settings in use and the ceiling of each
// profile.
type PickerAnalysis struct {
	Settings PickerSettings `json:"settings"`
	Tasks    int            `json:"tasks"`
	// Mean and Variance are those of every task.
	Mean     float64            `json:"mean"`
	Variance float64            `json:"variance"`
	Revision uint64             `json:"revision"`
	Buckets  []PickerBucketRow  `json:"buckets"`
	Models   []PickerModelRow   `json:"models"`
	Profiles []PickerProfileRow `json:"profiles,omitempty"`
}

// OpPickerGet reads the picker analysis for screens.
const OpPickerGet = "picker_get"

// Analysis lists the keys with at least one task, and the extra keys the
// caller names (registered profiles with no task yet), sorted by key. The
// buckets run from D 1.0 to 5.0, or further when a task is higher.
func (t *PickerTable) Analysis(extraKeys ...string) PickerAnalysis {
	mean, variance := t.allMeanVariance()
	out := PickerAnalysis{Settings: t.settings, Tasks: t.all.n, Mean: mean, Variance: variance, Revision: t.Revision, Buckets: []PickerBucketRow{}, Models: []PickerModelRow{}}
	last := max(t.Bucket(PickerDEnd-pickerEdgeTolerance*10), t.maxBucket)
	for i := 0; i <= last; i++ {
		lo, hi := t.BucketRange(i)
		out.Buckets = append(out.Buckets, PickerBucketRow{Index: i, Lo: lo, Hi: hi, Mean: t.BucketMean(i), N: t.buckets[i].n})
	}
	seen := map[string]bool{}
	var keys []string
	for key := range t.cells {
		seen[key] = true
		keys = append(keys, key)
	}
	for _, key := range extraKeys {
		if key != "" && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		row := PickerModelRow{Key: key, Display: PickerModelDisplay(key), Cells: make([]PickerCell, 0, len(out.Buckets))}
		for i := range out.Buckets {
			c := t.Cell(key, i)
			row.Tasks += c.N
			row.Cells = append(row.Cells, c)
		}
		out.Models = append(out.Models, row)
	}
	return out
}

// LevelDRange is the range of D the judge gives a label: [lo, hi). The edges
// are the judge's label cut points.
func LevelDRange(level string) (lo, hi float64, ok bool) {
	cuts := difficulty.LabelCutPoints()
	edges := append(append([]float64{PickerDStart}, cuts...), PickerDEnd)
	for i, name := range DifficultyLevels {
		if name == level && i+1 < len(edges) {
			return edges[i], edges[i+1], true
		}
	}
	return 0, 0, false
}

// PickerD is the D a pick reads. It is the judge's D, held inside the range of
// the level the start runs at: a bottleneck that lifts the label keeps the pick
// at the floor of that label. When the judge failed, has low confidence or its
// label is not the level, D is not trusted and the middle of the level's range
// stands in.
func PickerD(a *WorkerAssessment, level string) float64 {
	lo, hi, ok := LevelDRange(level)
	if !ok {
		level = "normal"
		lo, hi, _ = LevelDRange(level)
	}
	if a == nil || a.Error != "" || a.D <= 0 || a.Confidence == "low" || a.Label != level {
		return (lo + hi) / 2
	}
	return math.Max(lo, math.Min(a.D, hi-.001))
}

// BenchmarkCeiling is the top of the D range of the highest level a benchmark
// score meets, and that level. A score that meets no level has ceiling 0.
func BenchmarkCeiling(score float64, minimums map[string]float64) (level string, ceiling float64) {
	level = WorkerMaximumLevel(score, minimums)
	if _, hi, ok := LevelDRange(level); ok {
		return level, hi
	}
	return level, 0
}

// workerCandidateScore looks up the benchmark score of a candidate.
func workerCandidateScore(c WorkerModelCandidate, scores []ModelScore) (ModelScore, bool) {
	selection := ParseModelSelection(c.Harness, "", c.Model, c.Effort)
	return LookupModelScoreWithDefaultEffort(selection.LegacyModel(), c.DefaultEffort, scores)
}

// WorkerBenchmarkCeiling is the benchmark ceiling of a candidate. ok is false
// for a model with no benchmark score.
func WorkerBenchmarkCeiling(c WorkerModelCandidate, scores []ModelScore, minimums map[string]float64) (score float64, level string, ceiling float64, ok bool) {
	s, found := workerCandidateScore(c, scores)
	if !found {
		return 0, "", 0, false
	}
	level, ceiling = BenchmarkCeiling(s.Score, minimums)
	return s.Score, level, ceiling, true
}

// PickerInput is what the cell pick reads besides the candidates: the judge's
// D for this start and the cached table.
type PickerInput struct {
	D     float64
	Table *PickerTable
}

func (p *PickerInput) usable() bool { return p != nil && p.Table != nil }

func pickerCellText(c PickerCell) string {
	if c.N == 0 {
		return "n0"
	}
	return fmt.Sprintf("%.2f [%.2f-%.2f] n%d", c.Score, c.Lo, c.Hi, c.N)
}
