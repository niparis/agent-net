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
// The decision here is exactly the one pickOrderByCells uses: a candidate with a
// record is allowed unless the top of its interval is below AllowedFraction x
// best, and a thin candidate is allowed by its benchmark ceiling. A strong
// model is never left out for being far above the work: the weaker bias and the
// qualifying rule in pickOrderByCells steer the work instead. With
// s.CeilingRule on, the one exception is a model whose benchmark ceiling is
// below the bucket: it is allowed only when its Lo is at least the Lo of the
// best candidate.
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
			} else if s.CeilingRule && f.Scored && bucketLo >= f.Ceiling && f.Cell.Lo < facts[bestIdx].Cell.Lo {
				// Above its benchmark ceiling, the record has to carry the
				// model: its low end must reach the best model's low end.
				out[i].Allowed = false
				out[i].Reason = "above its benchmark, record not as good as best"
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
	// blocked is true when a window of the provider that limits the model is
	// used up (or over the gate's 90 percent line): a start would be refused.
	blocked   bool
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

// adjusted is the expected cost over the quota pace factor. A provider behind
// its pace has the lower adjusted cost, one ahead of it the higher.
func (e *pickerEntry) adjusted() float64 {
	return e.expected / quotaPaceFactor(e.room, e.measured)
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
// PickerCost is one candidate's inputs and computed cost terms.
type PickerCost struct {
	Key, Name                                     string
	Quality                                       float64
	Cost, Pass, Rescue, Expected, Rank            float64
	CostKnown, Recorded, PriceUnknown, MeetsFloor bool
}

// PickerCostOptions select the experimental cost and optional replay policy.
type PickerCostOptions struct {
	QualityV2                      bool
	Alpha, BestQuality, CleanFloor float64
}

// PickerExpectedCosts calculates the live picker cost terms in roster order.
func PickerExpectedCosts(entries []*PickerCost, table *PickerTable, bucket int, prices *PriceBook, set PickerSettings, options PickerCostOptions) {
	var sum float64
	var known int
	rank := make(map[*PickerCost]float64, len(entries))
	for _, e := range entries {
		var listed bool
		e.Cost, e.Recorded, listed, e.CostKnown = table.ExpectedCost(e.Key, bucket, prices)
		e.PriceUnknown = e.CostKnown && !e.Recorded && !listed
		if e.CostKnown {
			sum += e.Cost
			known++
		}
		if p, _, ok := prices.PriceOf(e.Key); ok {
			rank[e] = p.Prompt + p.Completion
		}
	}
	fallback := 1.0
	if known > 0 {
		fallback = sum / float64(known)
	}
	noPrices := len(rank) == 0
	for _, e := range entries {
		if !e.CostKnown {
			e.Cost = fallback
		}
		if noPrices {
			// No price list: the run cost stands in for the price per token.
			rank[e] = e.Cost
		}
	}
	ladder := append([]*PickerCost(nil), entries...)
	sort.SliceStable(ladder, func(i, j int) bool {
		if rank[ladder[i]] != rank[ladder[j]] {
			return rank[ladder[i]] < rank[ladder[j]]
		}
		return ladder[i].Name < ladder[j].Name
	})
	for _, e := range entries {
		e.Rescue = e.Cost
		for _, up := range ladder {
			if rank[up] > rank[e] {
				e.Rescue = up.Cost
				break
			}
		}
		c := table.CleanCell(e.Key, bucket)
		e.Pass = c.Score
		if c.N < set.UncertainN {
			e.Pass = c.Lo
		}
		if options.QualityV2 {
			// Independent retries have a geometric mean of 1/p runs per acceptance.
			if e.Pass == 0 {
				e.Expected = math.Inf(1)
			} else {
				e.Expected = e.Cost / e.Pass
			}
		} else {
			e.Expected = e.Cost + (1-e.Pass)*e.Rescue
		}
		e.Rank = e.Expected * math.Exp(options.Alpha*(options.BestQuality-e.Quality))
		e.MeetsFloor = e.Pass >= options.CleanFloor
	}
}

func expectedCosts(entries []*pickerEntry, table *PickerTable, bucket int, prices *PriceBook, set PickerSettings) {
	costs := make([]*PickerCost, len(entries))
	for i, e := range entries {
		costs[i] = &PickerCost{Key: e.key, Name: e.name, Quality: e.cell.Score}
	}
	PickerExpectedCosts(costs, table, bucket, prices, set, PickerCostOptions{QualityV2: set.QualityV2})
	for i, e := range entries {
		c := costs[i]
		e.cost, e.pass, e.rescue, e.expected = c.Cost, c.Pass, c.Rescue, c.Expected
		e.costKnown, e.recorded, e.priceUnknown = c.CostKnown, c.Recorded, c.PriceUnknown
	}
}

// pickOrderByCells returns the cell rule's whole order (decision 0162). The
// first choice is the one the rule picked and carries the pick's rows and note;
// the rest follow in the order the gate should try them.
//
// A candidate whose provider has no room stays in the order, at the end where
// the gate refuses it with the reason of its own check. It does not compete: it
// has no pace and no part in the best cell, the cost rule or the rotation, so
// an exhausted provider is never ranked first or cheaper for the room left in
// another of its windows.
//
// The second return value is empty when the cell rule picked. Otherwise it is
// the reason the level rule makes the pick, for the note.
func pickOrderByCells(candidates []WorkerModelCandidate, level string, scores []ModelScore, minimums map[string]float64, quotas *Quotas, running map[string]int, rotation *WorkerRotation, in *PickerInput) ([]WorkerModelChoice, string) {
	table, set := in.Table, in.Table.Settings()
	bucket := table.Bucket(in.D)
	bucketLo, bucketHi := table.BucketRange(bucket)
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
		// The gate owns the refusal. The ranker only keeps a blocked candidate
		// out of the contest; the pace of the others is the tightest window.
		room, note, measured, blocked := QuotaRoomForProvider(quotas, provider, c.Model)
		if measured {
			room -= 5 * float64(running[provider])
		}
		key := PickerModelKey(c.Harness, c.Model)
		e := &pickerEntry{c: c, key: key, cell: table.Cell(key, bucket), quotaNote: note, room: room, measured: measured, blocked: blocked}
		e.name = c.Name
		if e.name == "" {
			e.name = PickerModelDisplay(key)
		}
		e.bench, e.benchLevel, e.ceiling, e.scored = WorkerBenchmarkCeiling(c, scores, minimums)
		e.above = e.scored && table.Bucket(e.ceiling-pickerEdgeTolerance*10)-bucket > 1
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return nil, "fallback to the level rule (no candidate)"
	}

	// 1-3. The same decision the Quality subtab shows, one verdict per entry.
	// The best cell, the interval test, the benchmark prior and the
	// over-qualification test all live in PickerBucketVerdicts. A blocked
	// candidate is not in it: it cannot run, so it must not be the best that
	// the others are measured against.
	var live []*pickerEntry
	var facts []PickerCandidateFacts
	for _, e := range entries {
		if e.blocked {
			e.reason = e.quotaNote
			continue
		}
		live = append(live, e)
		facts = append(facts, PickerCandidateFacts{Key: e.key, Cell: e.cell, Scored: e.scored, Benchmark: e.bench, Ceiling: e.ceiling})
	}
	if len(live) == 0 {
		return nil, "fallback to the level rule (every candidate is out of quota)"
	}
	verdicts, bestIdx := PickerBucketVerdicts(facts, bucketLo, table.BucketMean(bucket), bucket, set)
	var bestEntry *pickerEntry
	if bestIdx >= 0 {
		bestEntry = live[bestIdx]
	}
	for i, e := range live {
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
	var winner *pickerEntry
	exploring := false
	// The exploration credit before and after the pick. Nil when the pick had
	// no thin cell, so the credit did not move.
	var creditBefore, creditAfter *float64
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
			return nil, "fallback to the level rule (no profile is allowed by its record or its benchmark)"
		}
	} else {
		// The proven model with the lowest expected cost per accepted result
		// wins. A provider behind its quota pace counts as cheaper, one ahead
		// of it as dearer; a quota floor has already left a model out.
		winner = qualified[0]
		for _, e := range qualified[1:] {
			a, w := e.adjusted(), winner.adjusted()
			if a < w || a == w && (e.cell.Score > winner.cell.Score || e.cell.Score == winner.cell.Score && e.name < winner.name) {
				winner = e
			}
		}
		final = []*pickerEntry{winner}
		if len(thin) > 0 {
			lvl := state.level(level)
			before := lvl.ExploreCredit
			lvl.ExploreCredit += set.ExploreShare
			if lvl.ExploreCredit >= 1-1e-9 {
				lvl.ExploreCredit -= 1
				final, exploring = thin, true
			}
			after := lvl.ExploreCredit
			creditBefore, creditAfter = &before, &after
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
	// owners and weights line up with eligible. rotateRanked may reorder
	// eligible, so the weights are read here, before the rotation.
	owners := final
	weights := make([]float64, len(eligible))
	for i := range eligible {
		weights[i] = eligible[i].weight
	}
	var saved map[string]float64
	if exploring {
		lvl := state.level(level)
		saved, lvl.Current = lvl.Current, lvl.ExploreCurrent
	}

	// 4. Rotate among the allowed.
	picked, shares, err := rotateRanked(state, level, eligible)
	if exploring {
		lvl := state.level(level)
		lvl.ExploreCurrent, lvl.Current = lvl.Current, saved
	}
	if err != nil {
		return nil, "fallback to the level rule (" + err.Error() + ")"
	}
	pickedKey := picked.WorkerModelCandidate.Key()
	var chosen *pickerEntry
	for _, e := range final {
		if e.c.Key() == pickedKey {
			chosen = e
			break
		}
	}
	rotated := len(qualified) == 0 || exploring
	if !rotated {
		// The cost rule picked, not the rotation: a weight would mislead.
		picked.Weight = 0
	}
	weightOf := map[*pickerEntry]float64{}
	shareOf := map[*pickerEntry]float64{}
	for i, e := range owners {
		shareOf[e] = shares[i]
		if rotated {
			weightOf[e] = weights[i]
		}
	}
	rows := cellPickRows(entries, cellPick{lo: bucketLo, hi: bucketHi, chosen: chosen, best: bestEntry, winner: winner,
		exploring: exploring, creditBefore: creditBefore, creditAfter: creditAfter, weight: weightOf, share: shareOf})
	picked.PickRows = rows
	picked.PickNote = PickNote(rows, set.ExploreShare)
	picked.Why = pickerShortWhy(level, in.D, in.DifficultyNote, chosen, len(qualified), entries)
	return pickOrder(level, picked, chosen, entries, final, qualified, allowed, choices), ""
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
		a, b := rest[i].adjusted(), rest[j].adjusted()
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
	// A blocked candidate goes last, for the gate to refuse with its own reason.
	for _, e := range entries {
		if e.blocked {
			add(e)
		}
	}
	return order
}

// pickerShortWhy builds the short why line (at most 2 lines):
// the difficulty and D; the chosen profile with its record (rate, n) or its score;
// the count of other qualifying profiles; and the top reasons others were left
// out, grouped.
func pickerShortWhy(level string, d float64, difficultyNote string, chosen *pickerEntry, qualifyingCount int, entries []*pickerEntry) string {
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

// cellPick is what a cell pick decided, as its rows show it.
type cellPick struct {
	lo, hi                    float64
	chosen, best, winner      *pickerEntry
	exploring                 bool
	creditBefore, creditAfter *float64
	// weight and share are the rotation weight and share of each candidate the
	// rotation chose from. A candidate outside that rotation has neither.
	weight, share map[*pickerEntry]float64
}

// cellPickRows writes one row per candidate of a cell pick, in roster order.
// The rows hold the terms the rule read, and the note is written from them.
func cellPickRows(entries []*pickerEntry, p cellPick) []PickCandidate {
	rows := make([]PickCandidate, len(entries))
	for i, e := range entries {
		r := PickCandidate{Name: e.name, Harness: e.c.Harness, Provider: e.c.Provider, Model: e.c.Model, Effort: e.c.Effort,
			Rule: PickRuleCell, BucketLo: p.lo, BucketHi: p.hi,
			Cell: e.cell, Allowed: e.allowed, Qualified: e.qualified, Prior: e.prior,
			Best: e == p.best, Chosen: e == p.chosen, Winner: e == p.winner, LeftOutReason: e.reason,
			CostKnown: e.costKnown, CostRecorded: e.recorded, PriceUnknown: e.priceUnknown,
			CostPerRun: e.cost, PClean: e.pass, RescueCost: e.rescue, ExpectedCost: e.expected,
			PaceFactor: quotaPaceFactor(e.room, e.measured), AdjustedCost: e.adjusted(),
			Weight: p.weight[e], Probability: p.share[e],
			Explore: p.exploring, ExploreCreditBefore: p.creditBefore, ExploreCreditAfter: p.creditAfter}
		if math.IsInf(e.expected, 1) {
			r.CostUnbounded = true
			r.ExpectedCost = 0
			r.AdjustedCost = 0
		}
		if e.measured {
			room := e.room
			r.PaceRoom = &room
		}
		rows[i] = r
	}
	// The margin is the runner-up's adjusted cost over the winner's. It needs a
	// runner-up, and a winner with a cost above zero.
	if p.winner != nil && p.winner.adjusted() > 0 {
		var runnerUp *pickerEntry
		for _, e := range entries {
			if e.qualified && e != p.winner && (runnerUp == nil || e.adjusted() < runnerUp.adjusted()) {
				runnerUp = e
			}
		}
		if runnerUp != nil && !math.IsInf(runnerUp.adjusted(), 0) && !math.IsInf(p.winner.adjusted(), 0) {
			margin := runnerUp.adjusted() / p.winner.adjusted()
			for i, e := range entries {
				if e == p.winner {
					rows[i].Margin = &margin
				}
			}
		}
	}
	return rows
}

// PickerCostRow is the cost terms of one candidate in one bucket.
type PickerCostRow struct {
	Name, Key              string
	N                      int
	Cost, Pass, Rescue     float64
	Expected               float64
	Recorded, PriceUnknown bool
	CostKnown              bool
	CostUnbounded          bool `json:"cost_unbounded,omitempty"`
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
		if math.IsInf(e.expected, 1) {
			out[i].Expected = 0
			out[i].CostUnbounded = true
		}
	}
	return out
}
