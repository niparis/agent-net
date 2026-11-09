package protocol

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// The cell pick (decision 0105) keeps the interval-based allowed set and
// over-qualification rule, then rotates proven candidates whose cell scores
// reach the qualifying threshold. Allowed thin cells receive an exploration
// share. When no candidates qualify, the original allowed rotation applies.
// When nothing is allowed the caller falls back to the level rule.

// PickerCandidateFacts is what the cell rule reads about one candidate in one
// bucket. The TUI builds these from picker_get and the picker builds them from
// its entries, so both run the same decision.
type PickerCandidateFacts struct {
	Key       string
	Cell      PickerCell
	Scored    bool
	Benchmark float64
	Ceiling   float64
}

// PickerVerdict is the cell rule's decision for one candidate in one bucket.
// Reason is empty when the candidate is allowed; it is one of "below 0.9×best",
// "overqualified", "above its benchmark" or "no record, no benchmark" when it
// is left out. Prior is true when the candidate is allowed by its benchmark
// because it has fewer than PickerMinCellTasks tasks in the bucket.
type PickerVerdict struct {
	Key     string
	Allowed bool
	Prior   bool
	Reason  string
}

// pickerBucket is the index of the bucket a D falls in, for a given width.
func pickerBucket(d float64, s PickerSettings) int {
	return max(0, int(math.Floor((d-PickerDStart)/s.BucketWidth+pickerEdgeTolerance)))
}

// PickerBucketVerdicts applies the cell rule to one bucket. facts are the
// candidates, bucketLo is the bottom of the bucket, bucketMean is the mean task
// score over all models in the bucket, bucket is the bucket index and s the
// picker settings. The second return value is the index in facts of the best
// candidate: the one with the highest cell score and at least
// PickerMinCellTasks tasks. It is -1 when no candidate has a record, and the
// bucket mean is then the best.
//
// The decision here is exactly the one chooseByCells uses: a candidate with a
// record is allowed unless the top of its interval is below AllowedFraction x
// best, a thin candidate is allowed by its benchmark ceiling, and an allowed
// candidate well above the work is left out when a weaker allowed candidate
// exists.
func PickerBucketVerdicts(facts []PickerCandidateFacts, bucketLo, bucketMean float64, bucket int, s PickerSettings) ([]PickerVerdict, int) {
	s = s.Normalized()
	out := make([]PickerVerdict, len(facts))
	best, bestIdx := bucketMean, -1
	for i, f := range facts {
		out[i].Key = f.Key
		if f.Cell.N >= PickerMinCellTasks && (bestIdx < 0 || f.Cell.Score > best) {
			best, bestIdx = f.Cell.Score, i
		}
	}
	above := make([]bool, len(facts))
	for i, f := range facts {
		above[i] = f.Scored && pickerBucket(f.Ceiling-pickerEdgeTolerance*10, s)-bucket > 1
		switch {
		case f.Cell.N >= PickerMinCellTasks:
			out[i].Allowed = f.Cell.Hi >= s.AllowedFraction*best
			if !out[i].Allowed {
				out[i].Reason = fmt.Sprintf("below %g×best", s.AllowedFraction)
			}
		case f.Scored && bucketLo < f.Ceiling:
			out[i].Allowed, out[i].Prior = true, true
		case f.Scored:
			out[i].Reason = "above its benchmark"
		default:
			out[i].Reason = "no record, no benchmark"
		}
	}
	// The over-qualification test reads the allowed set as it stood before any
	// candidate was left out, which is what chooseByCells has always done.
	initiallyAllowed := make([]bool, len(facts))
	for i := range out {
		initiallyAllowed[i] = out[i].Allowed
	}
	bench := func(f PickerCandidateFacts) float64 {
		if !f.Scored {
			return math.Inf(-1)
		}
		return f.Benchmark
	}
	for i, f := range facts {
		if !initiallyAllowed[i] {
			continue
		}
		if !above[i] || f.Cell.N >= PickerMinCellTasks && best-f.Cell.Score <= s.OverqualifiedMargin {
			continue
		}
		for j, other := range facts {
			if j != i && initiallyAllowed[j] && bench(other) < bench(f) {
				out[i].Allowed, out[i].Reason = false, "overqualified"
				break
			}
		}
	}
	return out, bestIdx
}

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
	// above is true when the benchmark ceiling is more than one bucket above D:
	// the model is well above this work.
	above bool
	// quota room terms
	quotaNote string
	room      float64
	measured  bool
	allowed   bool
	prior     bool
	reason    string
	weight    float64
	qualified bool
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
	var quotaExcluded []string
	for _, c := range candidates {
		provider := c.Provider
		if provider == "" {
			provider = QuotaProviderForStart(c.Harness, c.Model)
		}
		room, note, measured, blocked := workerQuotaRoom(quotas, provider, c.Model)
		if blocked {
			name := c.Name
			if name == "" {
				name = PickerModelDisplay(PickerModelKey(c.Harness, c.Model))
			}
			quotaExcluded = append(quotaExcluded, name+" left out (quota: "+note+")")
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
		e.above = e.scored && table.Bucket(e.ceiling-pickerEdgeTolerance*10)-bucket > 1
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return WorkerModelChoice{}, header + ": fallback to the level rule (every profile is out of quota)"
	}

	// 1-3. The same decision the Quality subtab shows, one verdict per entry.
	// The best cell, the interval test, the benchmark prior and the
	// over-qualification test all live in PickerBucketVerdicts.
	facts := make([]PickerCandidateFacts, len(entries))
	for i, e := range entries {
		facts[i] = PickerCandidateFacts{Key: e.key, Cell: e.cell, Scored: e.scored, Benchmark: e.bench, Ceiling: e.ceiling}
	}
	verdicts, bestIdx := PickerBucketVerdicts(facts, bucketLo, table.BucketMean(bucket), bucket, set)
	var bestEntry *pickerEntry
	if bestIdx >= 0 {
		bestEntry = entries[bestIdx]
	}
	for i, e := range entries {
		e.allowed = verdicts[i].Allowed
		e.prior = verdicts[i].Prior
		e.reason = verdicts[i].Reason
	}

	var qualified, thin, allowed []*pickerEntry
	best := table.BucketMean(bucket)
	if bestEntry != nil {
		best = bestEntry.cell.Score
	}
	bestBench := 0.0
	for _, e := range entries {
		if !e.allowed {
			continue
		}
		allowed = append(allowed, e)
		if e.scored && e.bench > bestBench {
			bestBench = e.bench
		}
		if e.cell.N < PickerMinCellTasks {
			thin = append(thin, e)
			continue
		}
		if e.cell.Score >= set.AllowedFraction*best {
			e.qualified = true
			qualified = append(qualified, e)
		} else {
			e.reason = fmt.Sprintf("score below %g×best", set.AllowedFraction)
		}
	}
	var eligible []rankedChoice
	var final []*pickerEntry
	exploring := false
	if len(qualified) == 0 {
		eligible = nil
		final = nil
		for _, e := range entries {
			if !e.allowed {
				continue
			}
			final = append(final, e)
			// An early quit is one failed attempt, with the existing failure weight.
			c := e.c
			c.Failed = c.Failed || c.EarlyQuitLevels[level]
			fit := math.Max(e.cell.Score, .05)
			if e.cell.N < set.UncertainN && !e.above {
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

	} else {
		final = qualified
		if len(thin) > 0 {
			lvl := state.level(level)
			lvl.ExploreCredit += set.ExploreShare
			if lvl.ExploreCredit >= 1-1e-9 {
				lvl.ExploreCredit -= 1
				final, exploring = thin, true
			}
		}
		for _, e := range allowed {
			weight := quotaPaceFactor(e.room, e.measured)
			if e.scored && e.bench > 0 && bestBench > 0 {
				weight *= math.Pow(bestBench/e.bench, set.WeakerBias)
			}
			e.weight = weight
		}
		for _, e := range final {
			choice := WorkerModelChoice{WorkerModelCandidate: e.c, Fit: "cell", QuotaNote: e.quotaNote, Score: e.bench}
			eligible = append(eligible, rankedChoice{choice: choice, weight: e.weight})
		}
	}
	var saved map[string]float64
	if exploring {
		lvl := state.level(level)
		saved, lvl.Current = lvl.Current, lvl.ExploreCurrent
	}

	// 4. Rotate among the allowed.
	picked, err := rotateRanked(state, level, eligible)
	if exploring {
		lvl := state.level(level)
		lvl.ExploreCurrent, lvl.Current = lvl.Current, saved
	}
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
	if len(qualified) > 0 {
		picked.PickNote = pickerNoteQualifying(header, entries, chosen, bestEntry, exploring, set)
	} else {
		picked.PickNote = pickerNote(header, entries, chosen, bestEntry)
	}
	if len(quotaExcluded) > 0 {
		picked.PickNote += "; " + strings.Join(quotaExcluded, "; ")
	}
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

// pickerNoteQualifying is the note of a pick made from the qualifying set. It
// names the chosen cell, every qualifier with its rotation weight, the thin
// cells that were open to the exploration share, and each candidate that was
// left out with the reason.
func pickerNoteQualifying(header string, entries []*pickerEntry, chosen, best *pickerEntry, exploring bool, set PickerSettings) string {
	cell := func(e *pickerEntry) string {
		return strings.TrimSpace(e.name + " " + pickerCellText(e.cell))
	}
	var qualifying, thin, left []string
	for _, e := range entries {
		switch {
		case e.qualified:
			qualifying = append(qualifying, fmt.Sprintf("%s weight %.2f", cell(e), e.weight))
		case e.allowed && e.cell.N < PickerMinCellTasks:
			thin = append(thin, fmt.Sprintf("%s (benchmark) weight %.2f", cell(e), e.weight))
		case e.reason != "":
			left = append(left, cell(e)+" left out ("+e.reason+")")
		}
	}
	tag := ", chosen"
	switch {
	case exploring:
		tag = ", exploration pick"
	case chosen == best:
		tag = ", best"
	}
	parts := []string{cell(chosen) + tag}
	parts = append(parts, "qualifying: "+strings.Join(qualifying, ", "))
	if len(thin) > 0 {
		share := fmt.Sprintf("thin cells (explore share %g): ", set.ExploreShare)
		parts = append(parts, share+strings.Join(thin, ", "))
	}
	if len(left) > 0 {
		parts = append(parts, strings.Join(left, "; "))
	}
	return header + ": " + strings.Join(parts, "; ")
}
