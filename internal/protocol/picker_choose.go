package protocol

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// The cell pick (decision 0094).
//
// For a task with judge D, the bucket of D is read for every candidate that has
// quota left:
//
//  1. best is the highest cell score among the candidates with at least three
//     tasks in the bucket. With none, it is the bucket mean.
//  2. A candidate with three tasks or more is allowed unless the top of its
//     interval is below AllowedFraction x best. A candidate with fewer is
//     allowed when its benchmark ceiling reaches the bucket.
//  3. An allowed candidate is left out when its score is more than
//     OverqualifiedMargin under the best, its benchmark ceiling is more than one
//     bucket above D, and a weaker allowed candidate exists.
//  4. The rest rotate with the smooth weighted round robin of the level rule.
//     The weight is the quota room term, the cell score, the boost for a cell
//     with fewer than UncertainN tasks and the track terms.
//
// When nothing is allowed the caller falls back to the level rule.

// pickerEntry is a candidate with what the cell rule reads about it.
type pickerEntry struct {
	c    WorkerModelCandidate
	key  string
	name string
	cell PickerCell
	// bench is the benchmark score; scored is false for a model that has none.
	bench   float64
	scored  bool
	ceiling float64
	// quota room terms
	quotaNote string
	room      float64
	measured  bool
	allowed   bool
	prior     bool
	reason    string
}

func (e *pickerEntry) benchScore() float64 {
	if !e.scored {
		return math.Inf(-1)
	}
	return e.bench
}

// chooseByCells runs the cell rule. A non-empty fallback says why it chose
// nothing and the level rule must be used; the rotation is then untouched.
func chooseByCells(candidates []WorkerModelCandidate, level string, scores []ModelScore, minimums map[string]float64, quotas *Quotas, running map[string]int, rotation *WorkerRotation, in *PickerInput) (WorkerModelChoice, string) {
	table, set := in.Table, in.Table.Settings()
	bucket := table.Bucket(in.D)
	bucketLo, bucketHi := table.BucketRange(bucket)
	header := fmt.Sprintf("bucket %.2f-%.2f", bucketLo, bucketHi)
	state := rotation
	if state == nil {
		state = &WorkerRotation{}
	}

	var entries []*pickerEntry
	for _, c := range candidates {
		provider := c.Provider
		if provider == "" {
			provider = QuotaProviderForStart(c.Harness, c.Model)
		}
		room, note, measured, blocked := workerQuotaRoom(quotas, provider, c.Model)
		if blocked {
			continue
		}
		if measured {
			room -= 5 * float64(running[provider])
		}
		key := PickerModelKey(c.Harness, c.Model)
		e := &pickerEntry{c: c, key: key, cell: table.Cell(key, bucket), quotaNote: note, room: room, measured: measured}
		e.name = c.Name
		if e.name == "" {
			e.name = PickerModelDisplay(key)
		}
		e.bench, _, e.ceiling, e.scored = WorkerBenchmarkCeiling(c, scores, minimums)
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return WorkerModelChoice{}, header + ": fallback to the level rule (every profile is out of quota)"
	}

	// 1. The best cell among candidates with a record of their own.
	best, bestKnown := table.BucketMean(bucket), false
	var bestEntry *pickerEntry
	for _, e := range entries {
		if e.cell.N >= PickerMinCellTasks && (!bestKnown || e.cell.Score > best) {
			best, bestKnown, bestEntry = e.cell.Score, true, e
		}
	}

	// 2. Allowed by the interval, or by the benchmark where the record is thin.
	for _, e := range entries {
		switch {
		case e.cell.N >= PickerMinCellTasks:
			e.allowed = e.cell.Hi >= set.AllowedFraction*best
			if !e.allowed {
				e.reason = fmt.Sprintf("below %g×best", set.AllowedFraction)
			}
		case e.scored && bucketLo < e.ceiling:
			e.allowed, e.prior = true, true
		case e.scored:
			e.reason = "above its benchmark"
		default:
			e.reason = "no record, no benchmark"
		}
	}

	// 3. Strong models stay off easy work when a weaker allowed one exists.
	var allowed []*pickerEntry
	for _, e := range entries {
		if e.allowed {
			allowed = append(allowed, e)
		}
	}
	for _, e := range allowed {
		if !e.scored || best-e.cell.Score <= set.OverqualifiedMargin || table.Bucket(e.ceiling-pickerEdgeTolerance*10)-bucket <= 1 {
			continue
		}
		for _, other := range allowed {
			if other != e && other.benchScore() < e.benchScore() {
				e.reason = "overqualified"
				break
			}
		}
	}
	var eligible []rankedChoice
	var final []*pickerEntry
	for _, e := range allowed {
		if e.reason != "" {
			e.allowed = false
			continue
		}
		final = append(final, e)
		// An early quit is one failed attempt, with the existing failure weight.
		c := e.c
		c.Failed = c.Failed || c.EarlyQuitLevels[level]
		fit := math.Max(e.cell.Score, .05)
		if e.cell.N < set.UncertainN {
			fit *= set.UncertainBoost
		}
		weight := rotationWeight(c, fit, e.room, e.measured)
		choice := WorkerModelChoice{WorkerModelCandidate: c, Fit: "cell", QuotaNote: e.quotaNote}
		if e.scored {
			choice.Score = e.bench
		}
		eligible = append(eligible, rankedChoice{choice: choice, weight: weight})
	}
	if len(final) == 0 {
		return WorkerModelChoice{}, header + ": fallback to the level rule (no profile is allowed by its record or its benchmark)"
	}

	// 4. Rotate among the allowed.
	picked, err := rotateRanked(state, level, eligible)
	if err != nil {
		return WorkerModelChoice{}, header + ": fallback to the level rule (" + err.Error() + ")"
	}
	pickedKey := WorkerModelKey(picked.WorkerModelCandidate)
	var chosen *pickerEntry
	for _, e := range final {
		if WorkerModelKey(e.c) == pickedKey {
			chosen = e
			break
		}
	}
	picked.PickNote = pickerNote(header, entries, chosen, bestEntry)
	return picked, ""
}

// pickerNote is the explanation of a cell pick: the chosen cell, the best cell
// and what was left out with its reason. It names at most three allowed and
// five left out models.
func pickerNote(header string, entries []*pickerEntry, chosen, best *pickerEntry) string {
	item := func(e *pickerEntry, tail string) string {
		return strings.TrimSpace(e.name+" "+pickerCellText(e.cell)) + tail
	}
	var allowed, left []*pickerEntry
	for _, e := range entries {
		switch {
		case e == chosen:
		case e.reason != "":
			left = append(left, e)
		default:
			allowed = append(allowed, e)
		}
	}
	byScore := func(list []*pickerEntry) {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].cell.Score != list[j].cell.Score {
				return list[i].cell.Score > list[j].cell.Score
			}
			return list[i].name < list[j].name
		})
	}
	byScore(allowed)
	byScore(left)

	tag := func(e *pickerEntry) string {
		if e.prior {
			return " (benchmark)"
		}
		return ""
	}
	parts := []string{}
	switch {
	case chosen == best:
		parts = append(parts, item(chosen, ", best"+tag(chosen)))
	default:
		parts = append(parts, item(chosen, ", chosen"+tag(chosen)))
	}
	for i, e := range allowed {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("+%d more allowed", len(allowed)-3))
			break
		}
		t := " allowed"
		if e == best {
			t += ", best"
		}
		parts = append(parts, item(e, t+tag(e)))
	}
	for i, e := range left {
		if i == 5 {
			parts = append(parts, fmt.Sprintf("+%d more left out", len(left)-5))
			break
		}
		parts = append(parts, item(e, " left out ("+e.reason+")"))
	}
	return header + ": " + strings.Join(parts, "; ")
}
