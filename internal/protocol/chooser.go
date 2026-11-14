package protocol

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// WorkerModelCandidate is one model available on an installed harness. It is
// the one candidate type, protocol.Candidate.
type WorkerModelCandidate = Candidate

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
	// PickNote explains a pick by cell record: the bucket, the chosen model's
	// cell, the best cell and what was left out.
	PickNote string
	// Fallback is true when no profile was allowed by its cell and the old level
	// rule chose the model.
	Fallback bool
	// Why is the short explanation for the chosen model (at most 2 lines).
	Why string
	// LeftOut lists the candidates the chooser dropped, when it found no model.
	// The picker records each reason where it drops the candidate, so a roster
	// only has to print the list.
	LeftOut []WorkerLeftOut
}

// Ranks for WorkerLeftOut, closest to qualifying first.
const (
	// LeftOutAllowed is a candidate the rules allowed but did not choose.
	LeftOutAllowed = 1
	// LeftOutFit is a candidate too weak or too strong for the task.
	LeftOutFit = 2
	// LeftOutQuota is a candidate whose provider is out of quota.
	LeftOutQuota = 3
	// LeftOutNoScore is a candidate with no benchmark or rating.
	LeftOutNoScore = 4
)

// WorkerLeftOut is one candidate a picker left out: its name, the reason it
// was dropped, and a rank where a lower number is closer to qualifying.
type WorkerLeftOut struct {
	Name   string
	Reason string
	Rank   int
}

// WorkerRotation is the smooth weighted round robin state of automatic worker
// model selection. It is kept per difficulty so a restart does not reset it.
type WorkerRotation struct {
	Levels map[string]*WorkerRotationLevel `json:"levels,omitempty"`
}

// WorkerRotationLevel is one difficulty's rotation state.
type WorkerRotationLevel struct {
	ExploreCredit  float64            `json:"explore_credit,omitempty"`
	ExploreCurrent map[string]float64 `json:"explore_current,omitempty"`
	// Current carries each candidate's weight between starts.
	Current map[string]float64 `json:"current,omitempty"`
	// Recent lists the last picks, newest last.
	Recent []string `json:"recent,omitempty"`
}

// workerRotationWindow is how many recent picks a share counts.
const workerRotationWindow = 10

// Clone copies the rotation state, so a preview can advance it without
// changing what the gateway will do.
func (r WorkerRotation) Clone() WorkerRotation {
	out := WorkerRotation{Levels: make(map[string]*WorkerRotationLevel, len(r.Levels))}
	for level, state := range r.Levels {
		if state == nil {
			continue
		}
		copied := &WorkerRotationLevel{ExploreCredit: state.ExploreCredit, Recent: append([]string(nil), state.Recent...)}
		if state.ExploreCurrent != nil {
			copied.ExploreCurrent = make(map[string]float64, len(state.ExploreCurrent))
			for k, v := range state.ExploreCurrent {
				copied.ExploreCurrent[k] = v
			}
		}
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

// levelOrder returns the level rule's whole order. The first choice is the one
// the rotation picked; the rest follow by rotation weight, so the gate can try
// them in the same order. keepBlocked leaves a candidate whose provider has no
// room at the end, for the gate to refuse; without it the candidate leaves the
// order, as the routes part 3 has not moved yet expect.
func levelOrder(candidates []WorkerModelCandidate, level string, scores []ModelScore, minimums map[string]float64, quotas *Quotas, running map[string]int, rotation *WorkerRotation) ([]WorkerModelChoice, []WorkerLeftOut, error) {
	if level == "" {
		level = "normal"
	}
	if _, err := DifficultyFit(0, level, minimums); err != nil {
		return nil, nil, err
	}
	state := rotation
	if state == nil {
		state = &WorkerRotation{}
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
	var eligible []rankedChoice
	var reasons []string
	var leftOut []WorkerLeftOut
	for _, c := range candidates {
		score, fit, isAdequate, ok := WorkerCandidateFit(c, level, scores, minimums)
		name := c.Name
		if name == "" {
			name = c.Harness + " " + c.Model
		}
		if !ok {
			reasons = append(reasons, name+" (max unknown: unscored)")
			leftOut = append(leftOut, WorkerLeftOut{Name: name, Reason: "unscored (no benchmark or rating)", Rank: LeftOutNoScore})
			continue
		}
		scoreText := fmt.Sprintf("%g", score)
		if s, sOk := workerCandidateScore(c, scores); sOk && s.FallbackEffort != "" {
			scoreText = fmt.Sprintf("%g via %s", score, s.FallbackEffort)
		}
		reasons = append(reasons, fmt.Sprintf("%s (max %s, score %s, %s)", name, WorkerMaximumLevel(score, minimums), scoreText, fit))
		if !isAdequate {
			leftOut = append(leftOut, WorkerLeftOut{Name: name, Reason: fmt.Sprintf("max %s, score %g, %s", WorkerMaximumLevel(score, minimums), score, fit), Rank: LeftOutFit})
			continue
		}
		provider := c.Provider
		if provider == "" {
			provider = QuotaProviderForStart(c.Harness, c.Model)
		}
		// The gate owns the quota check. The ranker only needs a pace for its
		// order, so a used-up window is left out of that reading.
		room, note, measured := WorkerQuotaPace(quotas, provider, c.Model)
		servedRoom := room
		if measured {
			servedRoom -= 5 * float64(running[provider])
		}
		// An early quit is one failed attempt, with the existing failure weight.
		c.Failed = c.Failed || c.EarlyQuitLevels[level]
		weight := workerRotationWeight(c, score, weakestAdequate, servedRoom, measured)
		eligible = append(eligible, rankedChoice{
			choice: WorkerModelChoice{WorkerModelCandidate: c, Score: score, Fit: fit, QuotaNote: note},
			weight: weight,
			poor:   poorTrack(c.Track),
		})
	}
	if len(eligible) == 0 {
		sort.Strings(reasons)
		roster := strings.Join(reasons, ", ")
		if roster == "" {
			roster = "none"
		}
		return nil, leftOut, fmt.Errorf("no model qualifies for %s under the max-or-one-level-below rule (or eligible providers are out of quota); registered profiles: %s; register a profile for %s, or pass model explicitly", level, roster, level)
	}
	picked, err := rotateRanked(state, level, eligible)
	if err != nil {
		return nil, leftOut, err
	}
	order := []WorkerModelChoice{picked}
	seen := map[string]bool{picked.WorkerModelCandidate.Key(): true}
	rest := append([]rankedChoice(nil), eligible...)
	sort.SliceStable(rest, func(i, j int) bool {
		if rest[i].poor != rest[j].poor {
			return !rest[i].poor
		}
		if rest[i].weight != rest[j].weight {
			return rest[i].weight > rest[j].weight
		}
		return rest[i].choice.WorkerModelCandidate.Key() < rest[j].choice.WorkerModelCandidate.Key()
	})
	for _, e := range rest {
		key := e.choice.WorkerModelCandidate.Key()
		if seen[key] {
			continue
		}
		seen[key] = true
		ch := e.choice
		ch.Why = WhyLine(level, ch)
		order = append(order, ch)
	}
	return order, leftOut, nil
}

// rankedChoice is a candidate that passed the rules of a pick, with its
// rotation weight.
type rankedChoice struct {
	choice WorkerModelChoice
	weight float64
	poor   bool
}

// rotateRanked is the smooth weighted round robin step shared by both rules.
// It advances the rotation state of the level and returns the picked choice.
func rotateRanked(state *WorkerRotation, level string, eligible []rankedChoice) (WorkerModelChoice, error) {
	lvl := state.level(level)
	// Follow the roster: a model that left or lost its allowance no longer
	// carries weight.
	keep := make(map[string]bool, len(eligible))
	for _, e := range eligible {
		keep[e.choice.WorkerModelCandidate.Key()] = true
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
		good := make([]rankedChoice, 0, len(eligible))
		for _, e := range eligible {
			if !e.poor {
				good = append(good, e)
			}
		}
		tier = good
	}
	sort.Slice(tier, func(i, j int) bool {
		return tier[i].choice.WorkerModelCandidate.Key() < tier[j].choice.WorkerModelCandidate.Key()
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
		key := e.choice.WorkerModelCandidate.Key()
		current := lvl.Current[key] + e.weight
		lvl.Current[key] = current
		if best < 0 || current > bestCurrent {
			best, bestCurrent = i, current
		}
	}
	picked := tier[best]
	pickedKey := picked.choice.WorkerModelCandidate.Key()
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
		names[c.Key()] = name
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
	selection := ParseModelSelection(c.Harness, "", c.Model, c.Effort)
	s, found := LookupModelScoreWithDefaultEffort(selection.LegacyModel(), c.DefaultEffort, scores)
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

// workerRotationWeight is the level rule's weight. Fit decays with the distance
// above the weakest adequate score. The weakest adequate model keeps the full
// weight, and a model 8 points stronger gets about a third of it.
func workerRotationWeight(c WorkerModelCandidate, score, weakestAdequate, room float64, measured bool) float64 {
	fit := 1.0
	if !math.IsInf(weakestAdequate, 1) {
		fit = math.Exp(-math.Max(0, score-weakestAdequate) / workerOverQualificationScale)
	}
	return rotationWeight(c, fit, room, measured)
}

// quotaPaceFactor is the rotation weight a provider's quota pace gives.
// Room is percentage points behind pace. Five points behind is about a quarter
// more weight, and a lagging provider can lean further. Ahead of pace keeps
// less, but never zero. A missing or stale reading is neutral.
func quotaPaceFactor(room float64, measured bool) float64 {
	if !measured {
		return 1
	}
	return math.Max(0.1, math.Min(4, math.Exp(room/20)))
}

// rotationWeight turns quota room, a fit term and track record into a positive
// weight. Every candidate that passes the rules of a pick keeps a positive
// weight, so none is starved. The fit term is the caller's: the level rule
// passes its over-qualification decay, the cell rule its cell score.
func rotationWeight(c WorkerModelCandidate, fit, room float64, measured bool) float64 {
	weight := quotaPaceFactor(room, measured)
	weight *= fit
	if n := c.Track.Complete + c.Track.Partial + c.Track.Failed; n >= 5 {
		weight *= 1 + 0.20*float64(c.Track.Complete)/float64(n)
	}
	if c.Failed {
		weight *= 0.5
	} else if c.Succeeded {
		weight *= 1.2
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

// QuotaRoomForProvider reports the room a start has under a provider's
// windows, with the pace note. blocked is true when a window that serves the
// model is exhausted or above 90 percent used (decision 0162's 90% step).
func QuotaRoomForProvider(q *Quotas, provider, model string) (room float64, note string, measured, blocked bool) {
	return workerQuotaRoom(q, provider, model)
}

// WorkerQuotaPace is the room and pace note a ranker uses to order candidates.
// A window that is used up or above 90 percent says nothing about pace: it is
// skipped, so the ranker can still order the candidate. The gate refuses it.
func WorkerQuotaPace(q *Quotas, provider, model string) (room float64, note string, measured bool) {
	p, ok := q.Provider(provider)
	if !ok || !p.usable() {
		return 0, "", false
	}
	for _, w := range p.Windows {
		if !QuotaWindowServes(w, model) || w.Exhausted || w.PercentUsed > 90 {
			continue
		}
		duration := quotaWindowDuration(w)
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
	return room, note, measured
}

func workerQuotaRoom(q *Quotas, provider, model string) (float64, string, bool, bool) {
	p, ok := q.Provider(provider)
	if !ok || !p.usable() {
		return 0, "", false, false
	}
	var room float64
	var note string
	measured := false
	for _, w := range p.Windows {
		if !QuotaWindowServes(w, model) {
			continue
		}
		if w.Exhausted || w.PercentUsed > 90 {
			return 0, quotaWindowBlockedReason(provider, w), false, true
		}
		duration := quotaWindowDuration(w)
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

// quotaWindowBlockedReason says why a window keeps a profile out of a choice.
// It names the provider and the window so the why line never prints an empty
// reason. A window whose reading is at or over its limit is reported as used up.
func quotaWindowBlockedReason(provider string, w QuotaWindow) string {
	if w.Exhausted || w.PercentUsed >= 100 {
		return fmt.Sprintf("%s %s used up", provider, QuotaWindowName(w))
	}
	return fmt.Sprintf("%s %s at %.0f%% used", provider, QuotaWindowName(w), w.PercentUsed)
}

// quotaWindowDuration is the length of a window, from its kind. It is 0 when
// the kind names no length, or a monthly window has no reset time.
func quotaWindowDuration(w QuotaWindow) time.Duration {
	switch strings.ToLower(w.Kind) {
	case "week", "weekly":
		return 7 * 24 * time.Hour
	case "day", "daily":
		return 24 * time.Hour
	case "five_hour", "5h", "session":
		return 5 * time.Hour
	case "month", "monthly":
		if w.ResetsAt <= 0 {
			return 0
		}
		reset := time.Unix(w.ResetsAt, 0).UTC()
		first := time.Date(reset.Year(), reset.Month(), 1, reset.Hour(), reset.Minute(), reset.Second(), 0, time.UTC)
		previousMonth := first.AddDate(0, -1, 0)
		days := previousMonth.AddDate(0, 1, -1).Day()
		day := min(reset.Day(), days)
		start := time.Date(previousMonth.Year(), previousMonth.Month(), day, reset.Hour(), reset.Minute(), reset.Second(), 0, time.UTC)
		return reset.Sub(start)
	}
	return 0
}

// Only finished outcomes that describe task quality count; stopped and timed-out
// runs can reflect circumstances unrelated to the model.
func poorTrack(t TrackRecord) bool {
	n := t.Complete + t.Partial + t.Failed
	return n >= 5 && 2*t.Complete < n
}

// OpWorkerRotationGet reads the gateway's worker rotation state for screens.
const OpWorkerRotationGet = "worker_rotation_get"
