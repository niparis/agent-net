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
	Name          string
	Harness       string
	Model         string
	Provider      string
	DefaultEffort string
	Failed        bool
	Succeeded     bool
}

// WorkerModelChoice explains an automatic worker model selection.
type WorkerModelChoice struct {
	WorkerModelCandidate
	Score     float64
	Fit       string
	QuotaNote string
}

// ChooseWorkerModel ranks eligible models by fit, quota room and recent failure.
// Rotation selects among equally ranked choices when no quota readings exist.
func ChooseWorkerModel(candidates []WorkerModelCandidate, level string, scores []ModelScore, minimums map[string]float64, quotas *Quotas, running map[string]int, rotation int) (WorkerModelChoice, error) {
	if level == "" {
		level = "normal"
	}
	if _, err := DifficultyFit(0, level, minimums); err != nil {
		return WorkerModelChoice{}, err
	}
	type ranked struct {
		choice   WorkerModelChoice
		fit      int
		room     float64
		measured bool
	}
	var eligible []ranked
	var reasons []string
	for _, c := range candidates {
		s, ok := LookupModelScoreWithDefaultEffort(c.Model, c.DefaultEffort, scores)
		if !ok {
			continue
		}
		fit, _ := DifficultyFit(s.Score, level, minimums)
		reasons = append(reasons, fmt.Sprintf("%s %s (score %g, %s)", c.Harness, c.Model, s.Score, fit))
		if fit != "good" && fit != "excellent" {
			continue
		}
		rank := 0
		provider := c.Provider
		if provider == "" {
			provider = QuotaProviderForStart(c.Harness, c.Model)
		}
		room, note, measured, blocked := workerQuotaRoom(quotas, provider)
		if blocked {
			continue
		}
		room -= 5 * float64(running[provider])
		eligible = append(eligible, ranked{WorkerModelChoice{c, s.Score, fit, note}, rank, room, measured})
	}
	if len(eligible) == 0 {
		if len(reasons) == 0 {
			return WorkerModelChoice{}, fmt.Errorf("no scored models qualify for %s", level)
		}
		sort.Strings(reasons)
		return WorkerModelChoice{}, fmt.Errorf("no model qualifies for %s: %s", level, strings.Join(reasons, ", "))
	}
	measured := false
	for _, r := range eligible {
		measured = measured || r.measured
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		a, b := eligible[i], eligible[j]
		if a.fit != b.fit {
			return a.fit < b.fit
		}
		if a.choice.Score != b.choice.Score {
			return a.choice.Score < b.choice.Score
		}
		if measured && a.room != b.room {
			return a.room > b.room
		}
		if a.choice.Failed != b.choice.Failed {
			return !a.choice.Failed
		}
		if a.choice.Succeeded != b.choice.Succeeded {
			return a.choice.Succeeded
		}
		return false
	})
	best := eligible[0]
	ties := 1
	for ties < len(eligible) {
		r := eligible[ties]
		if r.fit != best.fit || r.choice.Score != best.choice.Score || measured && r.room != best.room || r.choice.Failed != best.choice.Failed || r.choice.Succeeded != best.choice.Succeeded {
			break
		}
		ties++
	}
	if rotation < 0 {
		rotation = 0
	}
	return eligible[rotation%ties].choice, nil
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
			note = fmt.Sprintf("%s %s %.0f%% used at %.0f%% elapsed", provider, QuotaWindowName(w), w.PercentUsed, elapsed)
			measured = true
		}
	}
	return room, note, measured, false
}
