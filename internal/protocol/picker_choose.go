package protocol

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// The cell pick (decision 0105) keeps the interval-based allowed set, then
// rotates proven candidates whose cell scores reach the qualifying threshold.
// Allowed thin cells receive an exploration share. When no candidates qualify,
// the original allowed rotation applies. When nothing is allowed the caller
// falls back to the level rule.

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
// "above its benchmark" or "no record, no benchmark" when it is left out.
// Prior is true when the candidate is allowed by its benchmark because it has
// fewer than PickerMinCellTasks tasks in the bucket.
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
// best, and a thin candidate is allowed by its benchmark ceiling. A strong
// model is never left out for being far above the work: the weaker bias and the
// qualifying rule in chooseByCells steer the work instead.
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
	for i, f := range facts {
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
	// benchLevel is the highest level the benchmark score meets.
	benchLevel string
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
		e.bench, e.benchLevel, e.ceiling, e.scored = WorkerBenchmarkCeiling(c, scores, minimums)
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

	var qualified, thin, thinOut, allowed []*pickerEntry
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
			// Explore is up only: a thin cell is tried when the task is a step
			// up for the model, never a step down.
			if exploreIsUp(table, e, level, bucket) {
				thin = append(thin, e)
			} else {
				thinOut = append(thinOut, e)
			}
			continue
		}
		if e.cell.Score >= set.AllowedFraction*best {
			e.qualified = true
			qualified = append(qualified, e)
		} else {
			e.reason = fmt.Sprintf("score below %g×best", set.AllowedFraction)
		}
	}
	if len(qualified) > 0 {
		for _, e := range thinOut {
			e.reason = "explore is up only"
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
	picked.Why = pickerShortWhy(level, in.D, chosen, len(qualified), entries, quotaExcluded)
	return picked, ""
}

// pickerShortWhy builds the short why line (at most 2 lines):
// the difficulty and D; the chosen profile with its record (rate, n) or its score;
// the count of qualifying alternatives; and the top reasons others were left out, grouped.
func pickerShortWhy(level string, d float64, chosen *pickerEntry, qualifyingCount int, entries []*pickerEntry, quotaExcluded []string) string {
	parts := []string{fmt.Sprintf("difficulty %s (D %.2f)", level, d)}
	if chosen != nil {
		if chosen.cell.N > 0 {
			parts = append(parts, fmt.Sprintf("%s %.2f (n%d)", chosen.name, chosen.cell.Score, chosen.cell.N))
		} else if chosen.scored && chosen.bench > 0 {
			parts = append(parts, fmt.Sprintf("%s (score %.1f)", chosen.name, chosen.bench))
		} else {
			parts = append(parts, chosen.name)
		}
	}
	altCount := qualifyingCount
	if chosen != nil && chosen.qualified && altCount > 0 {
		altCount--
	}
	if altCount == 1 {
		parts = append(parts, "1 qualifying")
	} else {
		parts = append(parts, fmt.Sprintf("%d qualifying", altCount))
	}

	type leftOutItem struct {
		name     string
		provider string
		reason   string
	}
	var leftItems []leftOutItem
	for _, e := range entries {
		if e == chosen {
			continue
		}
		if e.reason != "" {
			provider := e.c.Provider
			if provider == "" {
				provider = QuotaProviderForStart(e.c.Harness, e.c.Model)
			}
			leftItems = append(leftItems, leftOutItem{name: e.name, provider: provider, reason: e.reason})
		}
	}
	for _, qe := range quotaExcluded {
		name := qe
		reason := "out of quota"
		if idx := strings.Index(qe, " left out (quota: "); idx >= 0 {
			name = qe[:idx]
			reason = strings.TrimSuffix(qe[idx+len(" left out (quota: "):], ")")
		}
		provider := ""
		for _, e := range entries {
			if e.name == name {
				provider = e.c.Provider
				if provider == "" {
					provider = QuotaProviderForStart(e.c.Harness, e.c.Model)
				}
				break
			}
		}
		leftItems = append(leftItems, leftOutItem{name: name, provider: provider, reason: reason})
	}

	if len(leftItems) > 0 {
		reasonOrder := []string{}
		reasonGroups := map[string][]leftOutItem{}
		for _, it := range leftItems {
			if _, exists := reasonGroups[it.reason]; !exists {
				reasonOrder = append(reasonOrder, it.reason)
			}
			reasonGroups[it.reason] = append(reasonGroups[it.reason], it)
		}
		var groups []string
		for _, r := range reasonOrder {
			items := reasonGroups[r]
			sameProvider := items[0].provider != ""
			for _, it := range items {
				if it.provider != items[0].provider {
					sameProvider = false
					break
				}
			}
			if sameProvider && len(items) >= 2 {
				groups = append(groups, fmt.Sprintf("%s profiles (%s)", items[0].provider, r))
			} else {
				names := make([]string, len(items))
				for i, it := range items {
					names[i] = it.name
				}
				groups = append(groups, fmt.Sprintf("%s (%s)", strings.Join(names, ", "), r))
			}
		}
		if len(groups) > 0 {
			parts = append(parts, "left out: "+strings.Join(groups, "; "))
		}
	}
	return strings.Join(parts, "; ")
}

// exploreIsUp tells whether the task is a step up for a thin cell (decision
// 0112). Either the model has a proven record in a lower bucket and none in
// this bucket or above, or it has no proven record anywhere and its benchmark
// level is exactly the task's level.
func exploreIsUp(table *PickerTable, e *pickerEntry, level string, bucket int) bool {
	below, atOrAbove := table.ProvenRecords(e.key, bucket)
	if below || atOrAbove {
		return below && !atOrAbove
	}
	return e.scored && e.benchLevel == level
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
		case e.allowed && e.cell.N < PickerMinCellTasks && e.reason == "":
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
