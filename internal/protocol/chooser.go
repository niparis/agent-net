package protocol

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// WorkerModelCandidate is one model available on an installed harness.
type WorkerModelCandidate struct {
	// QualityWeight is supplied only when owner quality weighting is enabled.
	QualityWeight float64
	Name          string
	Harness       string
	Model         string
	Provider      string
	DefaultEffort string
	Failed        bool
	Succeeded     bool
	Track         TrackRecord
}

// WorkerModelChoice explains an automatic worker model selection.
type WorkerModelChoice struct {
	WorkerModelCandidate
	Score     float64
	Fit       string
	QuotaNote string
	// Weight is the rotation weight the model had for this start.
	Weight float64
	// ShareNote says how the recent starts of this difficulty were spread.
	ShareNote string
}

// WorkerRotation is the smooth weighted round robin state of automatic worker
// model selection. It is kept per difficulty so a restart does not reset it.
type WorkerRotation struct {
	Levels map[string]*WorkerRotationLevel `json:"levels,omitempty"`
}

// WorkerRotationLevel is one difficulty's rotation state.
type WorkerRotationLevel struct {
	// Current carries each candidate's weight between starts.
	Current map[string]float64 `json:"current,omitempty"`
	// Recent lists the last picks, newest last.
	Recent []string `json:"recent,omitempty"`
}

// workerRotationWindow is how many recent picks a share counts.
const workerRotationWindow = 10

// WorkerModelKey identifies a candidate in the rotation state.
func WorkerModelKey(c WorkerModelCandidate) string { return c.Harness + "\x00" + c.Model }

// Clone copies the rotation state, so a preview can advance it without
// changing what the gateway will do.
func (r WorkerRotation) Clone() WorkerRotation {
	out := WorkerRotation{Levels: make(map[string]*WorkerRotationLevel, len(r.Levels))}
	for level, state := range r.Levels {
		if state == nil {
			continue
		}
		copied := &WorkerRotationLevel{Recent: append([]string(nil), state.Recent...)}
		if state.Current != nil {
			copied.Current = make(map[string]float64, len(state.Current))
			for key, weight := range state.Current {
				copied.Current[key] = weight
			}
		}
		out.Levels[level] = copied
	}
	return out
}

func (r *WorkerRotation) level(name string) *WorkerRotationLevel {
	if r.Levels == nil {
		r.Levels = map[string]*WorkerRotationLevel{}
	}
	state := r.Levels[name]
	if state == nil {
		state = &WorkerRotationLevel{}
		r.Levels[name] = state
	}
	return state
}

// ChooseWorkerModel picks an adequate model by weighted rotation.
//
// A candidate must score at or above the difficulty minimum, and the task
// must be its maximum level or exactly one level below it. Exhausted
// windows and windows above 90 percent used are excluded. Unprotected
// candidates come first, and candidates with a poor track record are used only
// when no other adequate candidate remains. The rest rotate with a smooth
// weighted round robin, so one model with a little more quota room does not win
// every start.
func ChooseWorkerModel(candidates []WorkerModelCandidate, level string, scores []ModelScore, minimums map[string]float64, quotas *Quotas, running map[string]int, rotation *WorkerRotation) (WorkerModelChoice, error) {
	if level == "" {
		level = "normal"
	}
	if _, err := DifficultyFit(0, level, minimums); err != nil {
		return WorkerModelChoice{}, err
	}
	state := rotation
	if state == nil {
		state = &WorkerRotation{}
	}
	type ranked struct {
		choice WorkerModelChoice
		weight float64
		poor   bool
	}
	// Over-qualification is measured from the weakest adequate candidate of
	// this start, so every difficulty fills from the bottom of its adequate
	// range and the strong models stay available for harder work.
	weakestAdequate := math.Inf(1)
	for _, c := range candidates {
		score, _, adequate, ok := WorkerCandidateFit(c, level, scores, minimums)
		if ok && adequate && score < weakestAdequate {
			weakestAdequate = score
		}
	}
	var eligible []ranked
	var adequate []string
	var reasons []string
	for _, c := range candidates {
		score, fit, isAdequate, ok := WorkerCandidateFit(c, level, scores, minimums)
		name := c.Name
		if name == "" {
			name = c.Harness + " " + c.Model
		}
		if !ok {
			reasons = append(reasons, name+" (max unknown: unscored)")
			continue
		}
		reasons = append(reasons, fmt.Sprintf("%s (max %s, score %g, %s)", name, WorkerMaximumLevel(score, minimums), score, fit))
		if !isAdequate {
			continue
		}
		provider := c.Provider
		if provider == "" {
			provider = QuotaProviderForStart(c.Harness, c.Model)
		}
		room, note, measured, blocked := workerQuotaRoom(quotas, provider)
		if blocked {
			continue
		}
		servedRoom := room
		if measured {
			servedRoom -= 5 * float64(running[provider])
		}
		weight := workerRotationWeight(c, score, weakestAdequate, servedRoom, measured)
		eligible = append(eligible, ranked{
			choice: WorkerModelChoice{WorkerModelCandidate: c, Score: score, Fit: fit, QuotaNote: note},
			weight: weight,
			poor:   poorTrack(c.Track),
		})
		adequate = append(adequate, WorkerModelKey(c))
	}
	if len(eligible) == 0 {
		sort.Strings(reasons)
		roster := strings.Join(reasons, ", ")
		if roster == "" {
			roster = "none"
		}
		return WorkerModelChoice{}, fmt.Errorf("no model qualifies for %s under the max-or-one-level-below rule (or eligible providers are out of quota); registered profiles: %s; register a profile for %s, or pass model explicitly", level, roster, level)
	}
	lvl := state.level(level)
	// Follow the roster: a model that left or lost its allowance no longer
	// carries weight.
	keep := make(map[string]bool, len(adequate))
	for _, key := range adequate {
		keep[key] = true
	}
	for key := range lvl.Current {
		if !keep[key] {
			delete(lvl.Current, key)
		}
	}
	// Prefer candidates without a poor track record. Only when none remain do
	// the poor ones rotate.
	tier := eligible
	hasGood := false
	for _, e := range eligible {
		if !e.poor {
			hasGood = true
			break
		}
	}
	if hasGood {
		good := make([]ranked, 0, len(eligible))
		for _, e := range eligible {
			if !e.poor {
				good = append(good, e)
			}
		}
		tier = good
	}
	sort.Slice(tier, func(i, j int) bool {
		return WorkerModelKey(tier[i].choice.WorkerModelCandidate) < WorkerModelKey(tier[j].choice.WorkerModelCandidate)
	})
	total := 0.0
	for _, e := range tier {
		total += e.weight
	}
	if total <= 0 {
		return WorkerModelChoice{}, fmt.Errorf("no model qualifies for %s", level)
	}
	if lvl.Current == nil {
		lvl.Current = map[string]float64{}
	}
	best := -1
	var bestCurrent float64
	for i, e := range tier {
		key := WorkerModelKey(e.choice.WorkerModelCandidate)
		current := lvl.Current[key] + e.weight
		lvl.Current[key] = current
		if best < 0 || current > bestCurrent {
			best, bestCurrent = i, current
		}
	}
	picked := tier[best]
	pickedKey := WorkerModelKey(picked.choice.WorkerModelCandidate)
	lvl.Current[pickedKey] -= total
	lvl.Recent = append(lvl.Recent, pickedKey)
	if len(lvl.Recent) > workerRotationWindow {
		lvl.Recent = lvl.Recent[len(lvl.Recent)-workerRotationWindow:]
	}
	out := picked.choice
	out.Weight = picked.weight
	names := make(map[string]string, len(eligible))
	for _, e := range eligible {
		c := e.choice.WorkerModelCandidate
		name := c.Name
		if name == "" {
			name = c.Model
		}
		names[WorkerModelKey(c)] = name
	}
	out.ShareNote = rotationShareNote(level, names, lvl.Recent)
	return out, nil
}

// workerOverQualificationScale is the score gap above the weakest adequate
// candidate at which a model's rotation weight falls to 1/e. A small scale
// reduces over-qualification among eligible candidates. Models more than one
// level below their maximum are excluded before weights are calculated.
const workerOverQualificationScale = 8.0

// WorkerCandidateFit looks up a candidate's score and classifies it against a
// difficulty. ok is false when the model has no score or the level is unknown;
// adequate requires a good or excellent fit and a task at the model's maximum
// level or exactly one below. Callers that show or choose share this rule.
func WorkerCandidateFit(c WorkerModelCandidate, level string, scores []ModelScore, minimums map[string]float64) (score float64, fit string, adequate, ok bool) {
	s, found := LookupModelScoreWithDefaultEffort(c.Model, c.DefaultEffort, scores)
	if !found {
		return 0, "", false, false
	}
	fit, err := DifficultyFit(s.Score, level, minimums)
	if err != nil {
		return s.Score, "", false, false
	}
	if level == "" {
		level = "normal"
	}
	maxLevel := WorkerMaximumLevel(s.Score, minimums)
	eligible := false
	for i, name := range workerDifficultyLevels {
		if name == maxLevel {
			eligible = level == name || i > 0 && level == workerDifficultyLevels[i-1]
			break
		}
	}
	return s.Score, fit, eligible && (fit == "good" || fit == "excellent"), true
}

var workerDifficultyLevels = []string{"trivial", "simple", "normal", "hard", "very_complex"}

// WorkerMaximumLevel is the highest level whose minimum the score meets.
// It returns "none" when the score meets no minimum.
func WorkerMaximumLevel(score float64, minimums map[string]float64) string {
	maxLevel := "none"
	for _, level := range workerDifficultyLevels {
		fit, _ := DifficultyFit(score, level, minimums)
		if fit == "good" || fit == "excellent" {
			maxLevel = level
		}
	}
	return maxLevel
}

// workerRotationWeight turns quota room, over-qualification and track record
// into a positive weight. Every adequate candidate keeps a positive weight, so
// none is starved.
func workerRotationWeight(c WorkerModelCandidate, score, weakestAdequate, room float64, measured bool) float64 {
	weight := 1.0
	if measured {
		// Room is percentage points behind pace. Five points behind is about a
		// quarter more weight, and a lagging provider can lean further. Ahead of
		// pace keeps less, but never zero.
		weight *= math.Max(0.1, math.Min(4, math.Exp(room/20)))
	}
	// Fit decays with the distance above the weakest adequate score. The
	// weakest adequate model keeps the full weight, and a model 8 points
	// stronger gets about a third of it.
	if !math.IsInf(weakestAdequate, 1) {
		weight *= math.Exp(-math.Max(0, score-weakestAdequate) / workerOverQualificationScale)
	}
	if n := c.Track.Complete + c.Track.Partial + c.Track.Failed; n >= 5 {
		weight *= 1 + 0.20*float64(c.Track.Complete)/float64(n)
	}
	if c.Failed {
		weight *= 0.5
	} else if c.Succeeded {
		weight *= 1.2
	}
	if c.QualityWeight > 0 {
		weight *= math.Max(.5, math.Min(1.5, c.QualityWeight))
	}
	return weight
}

// rotationShareNote describes the recent picks of one difficulty, in the order
// of most picked first. It names the models the caller offered.
func rotationShareNote(level string, names map[string]string, recent []string) string {
	counts := map[string]int{}
	seen := 0
	for _, key := range recent {
		if _, ok := names[key]; !ok {
			continue
		}
		counts[key]++
		seen++
	}
	if seen == 0 {
		return ""
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return names[keys[i]] < names[keys[j]]
	})
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s: %d", names[key], counts[key]))
	}
	return fmt.Sprintf("%s of the last %d %s starts", strings.Join(parts, ", "), seen, level)
}

// WorkerRotationShares summarizes the recent picks of a difficulty for the
// models the caller offers. It reads the state without advancing it.
func WorkerRotationShares(level string, candidates []WorkerModelCandidate, rotation *WorkerRotation) string {
	if rotation == nil {
		return ""
	}
	state := rotation.Levels[level]
	if state == nil {
		return ""
	}
	names := make(map[string]string, len(candidates))
	for _, c := range candidates {
		name := c.Name
		if name == "" {
			name = c.Model
		}
		names[WorkerModelKey(c)] = name
	}
	return rotationShareNote(level, names, state.Recent)
}

func workerQuotaRoom(q *Quotas, provider string) (float64, string, bool, bool) {
	p, ok := q.Provider(provider)
	if !ok || !p.usable() {
		return 0, "", false, false
	}
	var room float64
	var note string
	measured := false
	for _, w := range p.Windows {
		if w.Exhausted || w.PercentUsed > 90 {
			return 0, "", false, true
		}
		var duration time.Duration
		switch strings.ToLower(w.Kind) {
		case "week", "weekly":
			duration = 7 * 24 * time.Hour
		case "day", "daily":
			duration = 24 * time.Hour
		case "five_hour", "5h", "session":
			duration = 5 * time.Hour
		case "month", "monthly":
			reset := time.Unix(w.ResetsAt, 0).UTC()
			first := time.Date(reset.Year(), reset.Month(), 1, reset.Hour(), reset.Minute(), reset.Second(), 0, time.UTC)
			previousMonth := first.AddDate(0, -1, 0)
			days := previousMonth.AddDate(0, 1, -1).Day()
			day := min(reset.Day(), days)
			start := time.Date(previousMonth.Year(), previousMonth.Month(), day, reset.Hour(), reset.Minute(), reset.Second(), 0, time.UTC)
			duration = reset.Sub(start)
		}
		if duration == 0 || w.ResetsAt <= 0 || q.GeneratedAt <= 0 {
			continue
		}
		elapsed := 100 * (1 - float64(w.ResetsAt-q.GeneratedAt)/duration.Seconds())
		elapsed = math.Max(0, math.Min(100, elapsed))
		current := elapsed - w.PercentUsed
		if !measured || current < room {
			room = current
			pace := "on pace"
			if current > 0.05 {
				pace = fmt.Sprintf("%.0f%% behind pace", current)
			} else if current < -0.05 {
				pace = fmt.Sprintf("%.0f%% ahead of pace", -current)
			}
			note = fmt.Sprintf("%s %s (%s, %.0f%% used at %.0f%% elapsed)", provider, pace, QuotaWindowName(w), w.PercentUsed, elapsed)
			measured = true
		}
	}
	return room, note, measured, false
}

// Only finished outcomes that describe task quality count; stopped and timed-out
// runs can reflect circumstances unrelated to the model.
func poorTrack(t TrackRecord) bool {
	n := t.Complete + t.Partial + t.Failed
	return n >= 5 && 2*t.Complete < n
}

// OpWorkerRotationGet reads the gateway's worker rotation state for screens.
const OpWorkerRotationGet = "worker_rotation_get"
