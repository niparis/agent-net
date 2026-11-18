package main

import (
	"fmt"
	"math"
	"sort"

	"github.com/niparis/agent-net/internal/protocol"
)

// Optional scoring: today's rule is the live code. Option A swaps only the task
// score; the clean share, the run cost and the rescue ladder stay as they are.
func pickerTasks(tasks []Task, cut int64, score func(Task) float64) []protocol.PickerTask {
	out := make([]protocol.PickerTask, 0, len(tasks))
	for _, t := range tasks {
		if t.End > cut || t.D <= 0 || t.Key == "" {
			continue
		}
		out = append(out, protocol.PickerTask{Key: t.Key, D: t.D, Score: score(t), Tokens: t.Tokens, Cost: t.Cost, Dirty: t.Dirty})
	}
	return out
}

// candEval is one candidate of a pick, scored against a table built from the
// data that existed when the pick was made.
type candEval struct {
	row   CandRow
	key   string
	cell  protocol.PickerCell
	clean protocol.PickerCell
	pass  float64

	cost, rescue, expected, adjusted float64
	costKnown                        bool
	recorded, listed                 bool
	priceUnknown                     bool

	allowed   bool
	prior     bool
	qualified bool
	reason    string
	bench     float64
	scored    bool
	ceiling   float64
	rank      float64
}

// replayCandidates applies the cell rule and the cost rule of today to one
// pick, exactly as protocol.pickOrderByCells does, with the candidate roster
// the gateway recorded.
func replayCandidates(p Pick, table *protocol.PickerTable, prices *protocol.PriceBook, scores []protocol.ModelScore, minimums map[string]float64, s *Snapshot) ([]*candEval, int) {
	set := table.Settings()
	bucket := table.Bucket(p.D)
	bucketLo, _ := table.BucketRange(bucket)
	es := make([]*candEval, 0, len(p.Rows))
	for _, r := range p.Rows {
		key := r.Key()
		if key == "" {
			continue
		}
		e := &candEval{row: r, key: key, cell: table.Cell(key, bucket)}
		sel := protocol.ParseModelSelection(r.Harness, r.Provider, r.Model, r.Effort)
		def := defaultEffort(sel.LegacyModel(), r.Harness, s.Profiles)
		e.bench, _, e.ceiling, e.scored = protocol.WorkerBenchmarkCeiling(
			protocol.WorkerModelCandidate{Name: r.Name, Harness: r.Harness, Model: r.Model, Effort: r.Effort, DefaultEffort: def}, scores, minimums)
		es = append(es, e)
	}
	facts := make([]protocol.PickerCandidateFacts, len(es))
	for i, e := range es {
		facts[i] = protocol.PickerCandidateFacts{Key: e.key, Cell: e.cell, Scored: e.scored, Benchmark: e.bench, Ceiling: e.ceiling}
	}
	verdicts, _ := protocol.PickerBucketVerdicts(facts, bucketLo, table.BucketMean(bucket), bucket, set)
	for i, e := range es {
		e.allowed, e.prior, e.reason = verdicts[i].Allowed, verdicts[i].Prior, verdicts[i].Reason
		e.qualified = e.allowed && e.cell.N >= protocol.PickerMinCellTasks
	}
	expectedCosts(es, table, bucket, prices, set)
	return es, bucket
}

// defaultEffort is the harness default effort of a model, from the registered
// worker profiles.
func defaultEffort(model, harness string, profiles []protocol.WorkerProfile) string {
	for _, p := range profiles {
		if p.Harness == harness && p.Selection().LegacyModel() == model && p.Effort != "" {
			return p.Effort
		}
	}
	return ""
}

// expectedCosts is the cost rule of decision 0161. It repeats
// protocol.expectedCosts, which is unexported, so the replay can run the rule
// outside the gateway. Keep the two in step.
func expectedCosts(es []*candEval, table *protocol.PickerTable, bucket int, prices *protocol.PriceBook, set protocol.PickerSettings) {
	var sum float64
	var known int
	rank := make(map[*candEval]float64, len(es))
	for _, e := range es {
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
	for _, e := range es {
		if !e.costKnown {
			e.cost = fallback
		}
		if noPrices {
			rank[e] = e.cost
		}
	}
	ladder := append([]*candEval(nil), es...)
	sort.SliceStable(ladder, func(i, j int) bool {
		if rank[ladder[i]] != rank[ladder[j]] {
			return rank[ladder[i]] < rank[ladder[j]]
		}
		return ladder[i].row.Name < ladder[j].row.Name
	})
	for _, e := range es {
		e.rescue = e.cost
		for _, up := range ladder {
			if rank[up] > rank[e] {
				e.rescue = up.cost
				break
			}
		}
		e.clean = table.CleanCell(e.key, bucket)
		e.pass = e.clean.Score
		if e.clean.N < set.UncertainN {
			e.pass = e.clean.Lo
		}
		e.expected = e.cost + (1-e.pass)*e.rescue
		pace := e.row.PaceFactor
		if pace <= 0 {
			pace = 1
		}
		e.adjusted = e.expected / pace
	}
}

// pickToday is the cell rule's winner: the proven, allowed candidate with the
// lowest pace-adjusted expected cost.
func pickToday(es []*candEval) *candEval {
	var winner *candEval
	for _, e := range es {
		if !e.qualified {
			continue
		}
		if winner == nil || e.adjusted < winner.adjusted ||
			e.adjusted == winner.adjusted && (e.cell.Score > winner.cell.Score ||
				e.cell.Score == winner.cell.Score && e.row.Name < winner.row.Name) {
			winner = e
		}
	}
	return winner
}

// pickOptionA is the CTO proposal: drop the relative interval gate, keep every
// candidate with a record (thin cells still need their benchmark), and rank by
// expected cost times exp(alpha x (best quality - this quality)).
func pickOptionA(es []*candEval, alpha float64) *candEval {
	best := math.Inf(-1)
	for _, e := range es {
		if e.cell.N >= protocol.PickerMinCellTasks && e.cell.Score > best {
			best = e.cell.Score
		}
	}
	if math.IsInf(best, -1) {
		return nil
	}
	var winner *candEval
	for _, e := range es {
		e.allowed = e.cell.N >= protocol.PickerMinCellTasks || e.scored && e.row.BucketLo < e.ceiling
		e.qualified = e.cell.N >= protocol.PickerMinCellTasks
		if !e.qualified {
			continue
		}
		e.rank = e.adjusted * math.Exp(alpha*(best-e.cell.Score))
		if winner == nil || e.rank < winner.rank ||
			e.rank == winner.rank && (e.cell.Score > winner.cell.Score || e.cell.Score == winner.cell.Score && e.row.Name < winner.row.Name) {
			winner = e
		}
	}
	return winner
}

type modelCount struct {
	Name  string
	Count int
}

// summary is one method's result over the replayed picks.
type summary struct {
	Name     string
	Picks    int
	Models   map[string]int
	MeanP    float64
	MeanCost float64
	MeanAdj  float64
	MeanQual float64
	NoPick   int
}

func newSummary(name string) *summary { return &summary{Name: name, Models: map[string]int{}} }

func (s *summary) add(winner *candEval, quality float64) {
	s.Picks++
	if winner == nil {
		s.NoPick++
		return
	}
	s.Models[winner.row.Name]++
	s.MeanP += winner.pass
	s.MeanCost += winner.expected
	s.MeanAdj += winner.adjusted
	s.MeanQual += quality
}

func (s *summary) modelCounts() []modelCount {
	out := make([]modelCount, 0, len(s.Models))
	for name, n := range s.Models {
		out = append(out, modelCount{name, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (s *summary) averages() (p, cost, adj, qual float64) {
	n := float64(max(1, s.Picks-s.NoPick))
	return s.MeanP / n, s.MeanCost / n, s.MeanAdj / n, s.MeanQual / n
}

// replayCheck compares today's rule, rebuilt from the snapshot, with the pick
// the gateway recorded.
type replayCheck struct {
	Picks, Match int
	CellRows     int
	CellMatch    int
	CleanRows    int
	CleanMatch   int
	CostRows     int
	CostMatch    int
	Mismatches   []mismatch
}

type mismatch struct {
	Pick     string
	D        float64
	Recorded string
	Ours     string
	Reason   string
}

func (c *replayCheck) matchRate() float64 {
	if c.Picks == 0 {
		return 0
	}
	return float64(c.Match) / float64(c.Picks)
}

// RunReplay performs every measurement the decision draft reports.
func RunReplay(s *Snapshot, alphas []float64) *Report {
	r := &Report{Snapshot: s, Alphas: alphas}
	picks := append([]Pick(nil), s.Picks...)
	sort.Slice(picks, func(i, j int) bool { return picks[i].At < picks[j].At })
	r.Today = newSummary("today")
	r.Recorded = newSummary("recorded")
	r.Check = &replayCheck{}
	for _, a := range alphas {
		r.OptionsA = append(r.OptionsA, newSummary(fmt.Sprintf("A alpha %.1f", a)))
	}
	for _, p := range picks {
		tasks := s.TasksAt(p.At)
		tableToday := protocol.BuildPickerTable(pickerTasks(tasks, math.MaxInt64, func(t Task) float64 { return t.ScoreToday }), protocol.DefaultPickerSettings())
		tableA := protocol.BuildPickerTable(pickerTasks(tasks, math.MaxInt64, OptionAScore), protocol.DefaultPickerSettings())
		esToday, _ := replayCandidates(p, tableToday, s.Prices, s.Scores, s.Minimums, s)
		winnerToday := pickToday(esToday)
		// Quality is reported on today's score for every method, so the rows
		// compare. Option A ranks on its own reweighted score, shown in the case.
		commonQ := func(w *candEval) float64 {
			if w == nil {
				return 0
			}
			return tableToday.Cell(w.key, tableToday.Bucket(p.D)).Score
		}
		r.Today.add(winnerToday, commonQ(winnerToday))

		for _, row := range p.Rows {
			if !row.Chosen {
				continue
			}
			r.Recorded.Picks++
			r.Recorded.Models[row.Name]++
			r.Recorded.MeanP += row.PClean
			r.Recorded.MeanCost += row.Expected
			r.Recorded.MeanAdj += row.Adjusted
			r.Recorded.MeanQual += tableToday.Cell(row.Key(), tableToday.Bucket(p.D)).Score
		}

		recorded := ""
		for _, row := range p.Rows {
			if row.Chosen {
				recorded = row.Name
			}
		}
		if winnerToday != nil {
			r.Check.Picks++
			if winnerToday.row.Name == recorded {
				r.Check.Match++
			} else {
				r.Check.Mismatches = append(r.Check.Mismatches, mismatch{
					Pick: p.Session, D: p.D, Recorded: recorded, Ours: winnerToday.row.Name,
					Reason: mismatchReason(esToday, recorded, winnerToday),
				})
			}
		}
		for _, row := range p.Rows {
			if row.CellN > 0 {
				r.Check.CellRows++
				cell := tableToday.Cell(row.Key(), tableToday.Bucket(p.D))
				if math.Abs(cell.Score-row.CellScore) < 0.01 {
					r.Check.CellMatch++
				}
				if row.CellN >= protocol.PickerMinCellTasks {
					r.Check.CleanRows++
					if math.Abs(tableToday.CleanCell(row.Key(), tableToday.Bucket(p.D)).Score-row.PClean) < 0.01 {
						r.Check.CleanMatch++
					}
				}
			}
			if row.HasCost {
				r.Check.CostRows++
				if e := findKey(esToday, row.Key()); e != nil && math.Abs(e.cost-row.CostPerRun) < 0.01 {
					r.Check.CostMatch++
				}
			}
		}

		esA, _ := replayCandidates(p, tableA, s.Prices, s.Scores, s.Minimums, s)
		for i, a := range alphas {
			w := pickOptionA(esA, a)
			r.OptionsA[i].add(w, commonQ(w))
		}
		if r.Case == nil {
			if c := casePick(p); c != nil {
				r.Case = r.caseReport(*c, tableToday, tableA, alphas)
			}
		}
	}
	return r
}

// casePick is the D 3.20 pick the owner named: it chose the cheap DeepSeek
// profile over Sol, which scores better and ends clean more often.
func casePick(p Pick) *Pick {
	if p.D < 3.15 || p.D >= 3.25 {
		return nil
	}
	for _, row := range p.Rows {
		if row.Chosen && row.Name == "deepseek alibaba high" {
			return &p
		}
	}
	return nil
}

// CaseReport is the owner's example under every method.
type CaseReport struct {
	Pick     Pick
	Variants []CaseChoice
	Today    []*candEval
}

// CaseChoice is one method's winner for the case, with the two profiles the
// owner compared.
type CaseChoice struct {
	Variant  string
	Winner   string
	Quality  float64
	P        float64
	Cost     float64
	Expected float64
	Adjusted float64
	Rank     float64
	Cands    []CaseCand
}

// CaseCand is one profile's numbers under one method, copied so a later method
// does not overwrite them.
type CaseCand struct {
	Name     string
	Quality  float64
	P        float64
	Cost     float64
	Expected float64
	Adjusted float64
	Rank     float64
}

func snapshotCand(e *candEval) *CaseCand {
	if e == nil {
		return nil
	}
	return &CaseCand{Name: e.row.Name, Quality: e.cell.Score, P: e.pass, Cost: e.cost, Expected: e.expected, Adjusted: e.adjusted, Rank: e.rank}
}

func named(es []*candEval, name string) *candEval {
	for _, e := range es {
		if e.row.Name == name {
			return e
		}
	}
	return nil
}

func (r *Report) caseReport(p Pick, tableToday, tableA *protocol.PickerTable, alphas []float64) *CaseReport {
	c := &CaseReport{Pick: p}
	esToday, _ := replayCandidates(p, tableToday, r.Snapshot.Prices, r.Snapshot.Scores, r.Snapshot.Minimums, r.Snapshot)
	c.Today = esToday
	add := func(variant string, w *candEval, rank float64, es []*candEval) {
		ch := CaseChoice{Variant: variant}
		for _, name := range []string{"deepseek alibaba high", "sol codex"} {
			if c := snapshotCand(named(es, name)); c != nil {
				ch.Cands = append(ch.Cands, *c)
			}
		}
		if w != nil {
			ch.Winner = w.row.Name
			ch.Quality, ch.P, ch.Cost, ch.Expected, ch.Adjusted, ch.Rank = w.cell.Score, w.pass, w.cost, w.expected, w.adjusted, rank
		}
		c.Variants = append(c.Variants, ch)
	}
	add("today", pickToday(esToday), 0, esToday)
	esA, _ := replayCandidates(p, tableA, r.Snapshot.Prices, r.Snapshot.Scores, r.Snapshot.Minimums, r.Snapshot)
	for _, a := range alphas {
		w := pickOptionA(esA, a)
		rank := 0.0
		if w != nil {
			rank = w.rank
		}
		add(fmt.Sprintf("A alpha %.1f", a), w, rank, esA)
	}
	return c
}

func findKey(es []*candEval, key string) *candEval {
	for _, e := range es {
		if e.key == key {
			return e
		}
	}
	return nil
}

// mismatchReason says why the rebuilt rule chose another model.
func mismatchReason(es []*candEval, recorded string, winner *candEval) string {
	for _, e := range es {
		if e.row.Name != recorded {
			continue
		}
		switch {
		case !e.allowed:
			return "recorded pick is not allowed in the rebuilt table (" + e.reason + ")"
		case !e.qualified:
			return fmt.Sprintf("recorded pick has %d tasks in the rebuilt cell, below the %d it needs", e.cell.N, protocol.PickerMinCellTasks)
		default:
			return fmt.Sprintf("cost drift: rebuilt recorded pick $%.2f adjusted, winner $%.2f; clean p %.2f vs %.2f",
				e.adjusted, winner.adjusted, e.pass, winner.pass)
		}
	}
	return "recorded pick is not in the rebuilt roster"
}

// Report is everything the draft needs.
type Report struct {
	Snapshot *Snapshot
	Alphas   []float64
	Check    *replayCheck
	Today    *summary
	Recorded *summary
	OptionsA []*summary
	Case     *CaseReport
}
