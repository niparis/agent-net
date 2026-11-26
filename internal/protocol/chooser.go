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
	// PickRows is the pick's row for each candidate, in roster order. The
	// picker writes PickNote from them. Only the first choice of a pick
	// carries them.
	PickRows []PickCandidate `json:"-"`
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

// rankedChoice is a candidate that passed the rules of a pick, with its
// rotation weight.
type rankedChoice struct {
	choice WorkerModelChoice
	weight float64
	poor   bool
}

// rotateRanked is the smooth weighted round robin step shared by both rules.
// It advances the rotation state of the level and returns the picked choice,
// with the share of each candidate of eligible: its long-run share of the
// picks. The shares line up with eligible as it was passed in.
func rotateRanked(state *WorkerRotation, level string, eligible []rankedChoice) (WorkerModelChoice, []float64, error) {
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
	// The shares are read before the tier is sorted, while eligible still lines
	// up with the caller's list.
	shares := make([]float64, len(eligible))
	var tierWeight float64
	for _, e := range eligible {
		if !hasGood || !e.poor {
			tierWeight += e.weight
		}
	}
	for i, e := range eligible {
		if (!hasGood || !e.poor) && tierWeight > 0 {
			shares[i] = e.weight / tierWeight
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
		return WorkerModelChoice{}, nil, fmt.Errorf("no model qualifies for %s", level)
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
	return out, shares, nil
}

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
	for i, name := range DifficultyLevels {
		if name == maxLevel {
			eligible = level == name || i > 0 && level == DifficultyLevels[i-1]
			break
		}
	}
	return s.Score, fit, eligible && (fit == "good" || fit == "excellent"), true
}

// WorkerMaximumLevel is the highest level whose minimum the score meets.
// It returns "none" when the score meets no minimum.
func WorkerMaximumLevel(score float64, minimums map[string]float64) string {
	maxLevel := "none"
	for _, level := range DifficultyLevels {
		fit, _ := DifficultyFit(score, level, minimums)
		if fit == "good" || fit == "excellent" {
			maxLevel = level
		}
	}
	return maxLevel
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
// weight, so none is starved. The fit term is the caller's: the fallback draw
// passes its cost weight, the cell rotation its cell score.
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
	room, note, measured, _, _ = quotaPaceRead(q, provider, model, true)
	return room, note, measured
}

func workerQuotaRoom(q *Quotas, provider, model string) (float64, string, bool, bool) {
	room, note, measured, blocked, _ := quotaPaceRead(q, provider, model, false)
	return room, note, measured, blocked
}

// QuotaPaceWindow is the window that decided a pace reading: the window that
// blocked the model, or the one with the least room. skipBlocked is the level
// rule's reading (WorkerQuotaPace), false the cell rule's and the gate's
// (QuotaRoomForProvider). ok is false when no window decided.
func QuotaPaceWindow(q *Quotas, provider, model string, skipBlocked bool) (QuotaWindow, bool) {
	_, _, measured, blocked, w := quotaPaceRead(q, provider, model, skipBlocked)
	return w, measured || blocked
}

// quotaPaceRead is the one pace reading of a provider's windows for a model.
// Room is percentage points behind pace in the tightest window, measured at the
// reading's own time. A window that is used up or above 90 percent blocks the
// model, unless skipBlocked leaves it out of the reading. decided is the window
// that blocked, or the one with the least room.
func quotaPaceRead(q *Quotas, provider, model string, skipBlocked bool) (room float64, note string, measured, blocked bool, decided QuotaWindow) {
	p, ok := q.Provider(provider)
	if !ok || !p.usable() {
		return 0, "", false, false, QuotaWindow{}
	}
	for _, w := range p.Windows {
		if !QuotaWindowServes(w, model) {
			continue
		}
		if QuotaWindowUsedUp(w) || w.PercentUsed > 90 {
			if skipBlocked {
				continue
			}
			return 0, quotaWindowBlockedReason(provider, w), false, true, w
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
			decided = w
		}
	}
	return room, note, measured, false, decided
}

// quotaWindowBlockedReason says why a window keeps a profile out of a choice.
// It names the provider and the window so the why line never prints an empty
// reason. A window whose reading is at or over its limit is reported as used up.
func quotaWindowBlockedReason(provider string, w QuotaWindow) string {
	if QuotaWindowUsedUp(w) {
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
