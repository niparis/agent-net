package protocol

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// The cell pick (decision 0105) keeps the interval-based allowed set, then
// picks the proven candidate with the lowest expected cost, a run's cost plus
// the rescue of the next dearer model when it fails (decision 0161). Allowed thin cells receive an exploration share. When no candidates qualify,
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
	// cost is the expected cost of one run in this bucket at API prices.
	// recorded is true when cost is the average of real runs; otherwise it is
	// the price times the tokens of an average run. priceUnknown is true when
	// the model had no listed price. pass is p, the clean share of its tasks;
	// rescue is the run cost of the next dearer model; expected is
	// cost + (1 - pass) x rescue (decision 0161).
	cost, pass, rescue, expected float64
	costKnown                    bool
	recorded                     bool
	priceUnknown                 bool
}

// expectedCosts fills the cost terms of the entries. A run's cost already
// holds its fix rounds, so p counts only the tasks that ended clean (complete,
// no failed accept, review or test step, no defect found later) and the
// score's quarter per fix round is not used a second time. p is shrunk to the
// bucket mean like the score, and takes the lower end of its interval while the
// cell is thin. The rescue is the average run cost of the next dearer model in
// the bucket, the ladder being ordered by price per token; the dearest model's
// rescue is itself. An entry with no cost data takes the mean of the others, or
// 1 when none has any, so the ranking then follows p.
func expectedCosts(entries []*pickerEntry, table *PickerTable, bucket int, prices *PriceBook, set PickerSettings) {
	var sum float64
	var known int
	rank := make(map[*pickerEntry]float64, len(entries))
	for _, e := range entries {
		var listed bool
		e.cost, e.recorded, listed, e.costKnown = table.ExpectedCost(e.key, bucket, prices)
		e.priceUnknown = e.costKnown && !e.recorded && !listed
		if e.costKnown {
			sum += e.cost
			known++
		}
		if p, _, ok := prices.PriceOf(e.key); ok {
			rank[e] = p.Prompt + p.Completion
		}
	}
	fallback := 1.0
	if known > 0 {
		fallback = sum / float64(known)
	}
	noPrices := len(rank) == 0
	for _, e := range entries {
		if !e.costKnown {
			e.cost = fallback
		}
		if noPrices {
			// No price list: the run cost stands in for the price per token.
			rank[e] = e.cost
		}
	}
	ladder := append([]*pickerEntry(nil), entries...)
	sort.SliceStable(ladder, func(i, j int) bool {
		if rank[ladder[i]] != rank[ladder[j]] {
			return rank[ladder[i]] < rank[ladder[j]]
		}
		return ladder[i].name < ladder[j].name
	})
	for _, e := range entries {
		e.rescue = e.cost
		for _, up := range ladder {
			if rank[up] > rank[e] {
				e.rescue = up.cost
				break
			}
		}
		c := table.CleanCell(e.key, bucket)
		e.pass = c.Score
		if c.N < set.UncertainN {
			e.pass = c.Lo
		}
		e.expected = e.cost + (1-e.pass)*e.rescue
	}
}

// pickerCostText is the expected cost of an entry for the note. Nothing when
// no cost is known; "est" when the run cost is a price times an average run.
func pickerCostText(e *pickerEntry) string {
	if !e.costKnown {
		return ""
	}
	text := fmt.Sprintf(" $%.2f/run, p %.2f, $%.2f expected", e.cost, e.pass, e.expected)
	if !e.recorded {
		text = " est" + text
	}
	if e.priceUnknown {
		text += " (price unknown)"
	}
	return text
}

// chooseByCells runs the cell rule for a route that has not moved to the gate
// yet. A non-empty fallback says why it chose nothing and the level rule must
// be used; the rotation is then untouched.
func chooseByCells(candidates []WorkerModelCandidate, level string, scores []ModelScore, minimums map[string]float64, quotas *Quotas, running map[string]int, rotation *WorkerRotation, in *PickerInput) (WorkerModelChoice, string) {
	order, _, fallback := pickOrderByCells(candidates, level, scores, minimums, quotas, running, rotation, in, true)
	if fallback != "" {
		return WorkerModelChoice{}, fallback
	}
	return order[0], ""
}

// pickOrderByCells returns the cell rule's whole order (decision 0162). The
// first choice is the one the rule picked; the rest follow in the order the
// gate should try them.
//
// blockQuota is the rule of the routes part 3 has not moved yet: a candidate
// whose provider has no room leaves the order. The automatic start keeps such a
// candidate at the end, where the gate refuses it with the reason of its own
// check, so the ranking never decides that a candidate may not run.
func pickOrderByCells(candidates []WorkerModelCandidate, level string, scores []ModelScore, minimums map[string]float64, quotas *Quotas, running map[string]int, rotation *WorkerRotation, in *PickerInput, blockQuota bool) ([]WorkerModelChoice, string, string) {
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
		var room float64
		var note string
		var measured, blocked bool
		if blockQuota {
			room, note, measured, blocked = workerQuotaRoom(quotas, provider, c.Model)
		} else {
			// The gate owns the quota check. The ranker only needs a pace for
			// its order, so a used-up window is left out of that reading.
			room, note, measured = WorkerQuotaPace(quotas, provider, c.Model)
		}
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
		return nil, header, header + ": fallback to the level rule (every profile is out of quota)"
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

	expectedCosts(entries, table, bucket, in.Prices, set)
	var qualified, thin, thinOut, allowed []*pickerEntry
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
		e.qualified = true
		qualified = append(qualified, e)
	}
	if len(qualified) > 0 {
		for _, e := range thinOut {
			e.reason = "explore is up only"
		}
	}
	choices := map[*pickerEntry]WorkerModelChoice{}
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
			choices[e] = choice
			eligible = append(eligible, rankedChoice{choice: choice, weight: weight})
		}
		if len(final) == 0 {
			return nil, header, header + ": fallback to the level rule (no profile is allowed by its record or its benchmark)"
		}
	} else {
		// The proven model with the lowest expected cost per accepted result
		// wins. A provider behind its quota pace counts as cheaper, one ahead
		// of it as dearer; a quota floor has already left a model out.
		winner := qualified[0]
		adjusted := func(e *pickerEntry) float64 { return e.expected / quotaPaceFactor(e.room, e.measured) }
		for _, e := range qualified[1:] {
			a, w := adjusted(e), adjusted(winner)
			if a < w || a == w && (e.cell.Score > winner.cell.Score || e.cell.Score == winner.cell.Score && e.name < winner.name) {
				winner = e
			}
		}
		final = []*pickerEntry{winner}
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
			weight := e.weight
			if !exploring {
				weight = 1
			}
			choice := WorkerModelChoice{WorkerModelCandidate: e.c, Fit: "cell", QuotaNote: e.quotaNote, Score: e.bench}
			choices[e] = choice
			eligible = append(eligible, rankedChoice{choice: choice, weight: weight})
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
		return nil, header, header + ": fallback to the level rule (" + err.Error() + ")"
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
		if !exploring {
			// The cost rule picked, not the rotation: a weight would mislead.
			picked.Weight = 0
		}
		picked.PickNote = pickerNoteQualifying(header, entries, chosen, bestEntry, exploring, set)
	} else {
		picked.PickNote = pickerNote(header, entries, chosen, bestEntry)
	}
	if len(quotaExcluded) > 0 {
		picked.PickNote += "; " + strings.Join(quotaExcluded, "; ")
	}
	picked.Why = pickerShortWhy(level, in.D, in.DifficultyNote, chosen, len(qualified), entries, quotaExcluded)
	return pickOrder(level, picked, chosen, entries, final, qualified, allowed, choices), header, ""
}

// pickOrder lays the whole list behind the pick the cell rule made. A
// candidate the rule allowed sorts by the cost rule before the fallback order;
// one it left out comes last with its reason. A blocked candidate is already
// last, for the gate to refuse.
func pickOrder(level string, picked WorkerModelChoice, chosen *pickerEntry, entries, final, qualified, allowed []*pickerEntry, choices map[*pickerEntry]WorkerModelChoice) []WorkerModelChoice {
	order := make([]WorkerModelChoice, 0, len(entries))
	seen := map[*pickerEntry]bool{}
	choiceFor := func(e *pickerEntry) WorkerModelChoice {
		ch, ok := choices[e]
		if !ok {
			ch = WorkerModelChoice{WorkerModelCandidate: e.c, Fit: "cell", QuotaNote: e.quotaNote}
			if e.scored {
				ch.Score = e.bench
			}
		}
		if ch.Why == "" {
			ch.Why = WhyLine(level, ch)
		}
		return ch
	}
	add := func(e *pickerEntry) {
		if seen[e] {
			return
		}
		seen[e] = true
		order = append(order, choiceFor(e))
	}
	order = append(order, picked)
	if chosen != nil {
		seen[chosen] = true
	}
	adjusted := func(e *pickerEntry) float64 { return e.expected / quotaPaceFactor(e.room, e.measured) }
	byWeight := func(list []*pickerEntry) {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].weight != list[j].weight {
				return list[i].weight > list[j].weight
			}
			return list[i].name < list[j].name
		})
	}
	rest := make([]*pickerEntry, 0, len(final))
	for _, e := range final {
		rest = append(rest, e)
	}
	byWeight(rest)
	for _, e := range rest {
		add(e)
	}
	rest = nil
	for _, e := range qualified {
		rest = append(rest, e)
	}
	sort.SliceStable(rest, func(i, j int) bool {
		a, b := adjusted(rest[i]), adjusted(rest[j])
		if a != b {
			return a < b
		}
		if rest[i].cell.Score != rest[j].cell.Score {
			return rest[i].cell.Score > rest[j].cell.Score
		}
		return rest[i].name < rest[j].name
	})
	for _, e := range rest {
		add(e)
	}
	rest = nil
	for _, e := range allowed {
		rest = append(rest, e)
	}
	byWeight(rest)
	for _, e := range rest {
		add(e)
	}
	return order
}

// pickerShortWhy builds the short why line (at most 2 lines):
// the difficulty and D; the chosen profile with its record (rate, n) or its score;
// the count of other qualifying profiles; and the top reasons others were left
// out, grouped.
func pickerShortWhy(level string, d float64, difficultyNote string, chosen *pickerEntry, qualifyingCount int, entries []*pickerEntry, quotaExcluded []string) string {
	first := fmt.Sprintf("difficulty %s (D %.2f)", level, d)
	if difficultyNote != "" {
		first = difficultyNote
	}
	parts := []string{first}
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
	switch altCount {
	case 0:
		parts = append(parts, "no other qualifying")
	case 1:
		parts = append(parts, "1 other qualifying")
	default:
		parts = append(parts, fmt.Sprintf("%d other qualifying", altCount))
	}

	type leftOutItem struct {
		name   string
		reason string
	}
	var leftItems []leftOutItem
	for _, e := range entries {
		if e == chosen {
			continue
		}
		if e.reason != "" {
			leftItems = append(leftItems, leftOutItem{name: e.name, reason: e.reason})
		}
	}
	for _, qe := range quotaExcluded {
		name := qe
		reason := "out of quota"
		if idx := strings.Index(qe, " left out (quota: "); idx >= 0 {
			name = qe[:idx]
			reason = strings.TrimSpace(strings.TrimSuffix(qe[idx+len(" left out (quota: "):], ")"))
			// An older note may carry no reason; never show empty parentheses.
			if reason == "" {
				reason = "out of quota"
			}
		}
		leftItems = append(leftItems, leftOutItem{name: name, reason: reason})
	}

	// The owner reads profile names, so a group lists the profiles it covers,
	// never a provider label. A long group keeps the count honest and the line
	// short.
	const maxWhyNames = 3
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
			shown := items
			if len(shown) > maxWhyNames {
				shown = shown[:maxWhyNames]
			}
			names := make([]string, len(shown))
			for i, it := range shown {
				names[i] = it.name
			}
			label := strings.Join(names, ", ")
			if rest := len(items) - len(shown); rest > 0 {
				label += fmt.Sprintf(", +%d more", rest)
			}
			groups = append(groups, fmt.Sprintf("%s (%s)", label, r))
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
		return strings.TrimSpace(e.name+" "+pickerCellText(e.cell)) + pickerCostText(e) + tail
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
// names the chosen cell, every qualifier with its expected cost, the thin
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
			qualifying = append(qualifying, cell(e)+pickerCostText(e))
		case e.allowed && e.cell.N < PickerMinCellTasks && e.reason == "":
			thin = append(thin, cell(e)+" (benchmark)"+pickerCostText(e))
		case e.reason != "":
			left = append(left, cell(e)+pickerCostText(e)+" left out ("+e.reason+")")
		}
	}
	tag := ", lowest expected cost"
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

// PickerCostRow is the cost terms of one candidate in one bucket.
type PickerCostRow struct {
	Name, Key              string
	N                      int
	Cost, Pass, Rescue     float64
	Expected               float64
	Recorded, PriceUnknown bool
	CostKnown              bool
}

// PickerCostRows gives the cost terms the pick compares for each candidate at
// the D of the input, dearest last. It applies no quota and no allowed test: it
// is for reports.
func PickerCostRows(candidates []WorkerModelCandidate, in *PickerInput) []PickerCostRow {
	table := in.Table
	bucket := table.Bucket(in.D)
	var entries []*pickerEntry
	for _, c := range candidates {
		key := PickerModelKey(c.Harness, c.Model)
		e := &pickerEntry{c: c, key: key, name: c.Name, cell: table.Cell(key, bucket), allowed: true}
		if e.name == "" {
			e.name = PickerModelDisplay(key)
		}
		entries = append(entries, e)
	}
	expectedCosts(entries, table, bucket, in.Prices, table.Settings())
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].expected < entries[j].expected })
	out := make([]PickerCostRow, len(entries))
	for i, e := range entries {
		out[i] = PickerCostRow{Name: e.name, Key: e.key, N: e.cell.N, Cost: e.cost, Pass: e.pass, Rescue: e.rescue, Expected: e.expected,
			Recorded: e.recorded, PriceUnknown: e.priceUnknown, CostKnown: e.costKnown}
	}
	return out
}
