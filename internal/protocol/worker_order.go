package protocol

import (
	"math"
	"sort"
	"strconv"
)

// WorkerOrder lists the candidates that could take a start after the chosen
// one, preferred first (decision 0162). The list is the ranked remainder: the
// gate decides which of them can run now, and a start that loses its head to a
// locked check moves down this list. A candidate the level rule leaves out is
// not in the list.
//
// The cost ranker orders by the expected cost of one accepted result; the
// level rule orders by rotation weight, so the weakest adequate model comes
// first and the strong models stay for harder work. Pace is part of the
// weight. The chosen candidate is left out.
func WorkerOrder(chosen WorkerModelChoice, candidates []WorkerModelCandidate, level string, scores []ModelScore, minimums map[string]float64, quotas *Quotas, pick ...*PickerInput) []WorkerModelChoice {
	chosenKey := WorkerModelKey(chosen.WorkerModelCandidate)
	weakest := math.Inf(1)
	for _, c := range candidates {
		score, _, adequate, ok := WorkerCandidateFit(c, level, scores, minimums)
		if ok && adequate && score < weakest {
			weakest = score
		}
	}
	type entry struct {
		choice WorkerModelChoice
		weight float64
	}
	var entries []entry
	for _, c := range candidates {
		if WorkerModelKey(c) == chosenKey {
			continue
		}
		score, fit, adequate, ok := WorkerCandidateFit(c, level, scores, minimums)
		if !ok || !adequate {
			continue
		}
		provider := c.Provider
		if provider == "" {
			provider = QuotaProviderForStart(c.Harness, c.Model)
		}
		// A blocked candidate stays in the list: the gate skips it with its
		// reason, and the roster then names every check that left it out.
		room, note, measured, _ := workerQuotaRoom(quotas, provider, c.Model)
		entries = append(entries, entry{
			choice: WorkerModelChoice{WorkerModelCandidate: c, Score: score, Fit: fit, QuotaNote: note},
			weight: workerRotationWeight(c, score, weakest, room, measured),
		})
	}
	if len(pick) > 0 && pick[0].usable() {
		models := make([]WorkerModelCandidate, len(entries))
		for i := range entries {
			models[i] = entries[i].choice.WorkerModelCandidate
		}
		expected := make(map[string]float64, len(models))
		for _, row := range PickerCostRows(models, pick[0]) {
			expected[row.Key] = row.Expected
		}
		key := func(e entry) string { return PickerModelKey(e.choice.Harness, e.choice.Model) }
		sort.SliceStable(entries, func(i, j int) bool {
			if expected[key(entries[i])] != expected[key(entries[j])] {
				return expected[key(entries[i])] < expected[key(entries[j])]
			}
			return key(entries[i]) < key(entries[j])
		})
	} else {
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].weight != entries[j].weight {
				return entries[i].weight > entries[j].weight
			}
			return WorkerModelKey(entries[i].choice.WorkerModelCandidate) < WorkerModelKey(entries[j].choice.WorkerModelCandidate)
		})
	}
	out := make([]WorkerModelChoice, len(entries))
	for i := range entries {
		out[i] = entries[i].choice
		if out[i].Why == "" {
			out[i].Why = WhyLine(level, out[i])
		}
	}
	return out
}

// WhyLine is the short explanation for a candidate the order lists behind the
// chosen one: the difficulty, the model and its score.
func WhyLine(level string, choice WorkerModelChoice) string {
	return level + ": " + choice.Model + " (score " + trimFloat(choice.Score) + ")"
}

// trimFloat writes a score with at most one decimal.
func trimFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', 3, 64)
}
