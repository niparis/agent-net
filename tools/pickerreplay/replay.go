package main

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/difficulty"
	"github.com/niparis/agent-net/internal/protocol"
)

// pickerTasks converts snapshot tasks into the live table's input.
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

// replayCandidates applies the selected live cell and cost rule to one
// pick, exactly as protocol.pickOrderByCells does, with the candidate roster
// the gateway recorded.
func replayCandidates(p Pick, table *protocol.PickerTable, prices *protocol.PriceBook, scores []protocol.ModelScore, minimums map[string]float64, s *Snapshot, opt protocol.PickerCostOptions) ([]*candEval, int) {
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
	costs := make([]*protocol.PickerCost, len(es))
	for i, e := range es {
		costs[i] = &protocol.PickerCost{Key: e.key, Name: e.row.Name, Quality: e.cell.Score}
	}
	best := math.Inf(-1)
	for _, e := range es {
		if e.qualified && e.cell.Score > best {
			best = e.cell.Score
		}
	}
	if !math.IsInf(best, -1) {
		opt.BestQuality = best
	}
	protocol.PickerExpectedCosts(costs, table, bucket, prices, set, opt)
	for i, e := range es {
		c := costs[i]
		e.cost, e.pass, e.rescue, e.expected = c.Cost, c.Pass, c.Rescue, c.Expected
		e.costKnown, e.recorded, e.priceUnknown = c.CostKnown, c.Recorded, c.PriceUnknown
		e.clean = table.CleanCell(e.key, bucket)
		pace := e.row.PaceFactor
		if pace <= 0 {
			pace = 1
		}
		e.adjusted = e.expected / pace
		e.rank = c.Rank / pace
	}
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

// levelForD uses the judge's cut points, including their lower boundaries.
func levelForD(d float64) string {
	labels := []string{"trivial", "simple", "normal", "hard", "very_complex"}
	for i, cut := range difficulty.LabelCutPoints() {
		if d < cut {
			return labels[i]
		}
	}
	return labels[len(labels)-1]
}

type floorSet struct {
	name   string
	levels map[string]float64
}

func (f floorSet) floor(level string) float64 { return f.levels[level] }
func floorSets() []floorSet {
	return []floorSet{
		{"floor A", map[string]float64{"hard": .70, "normal": .60}},
		{"floor B", map[string]float64{"hard": .75, "normal": .65, "simple": .55}},
		{"floor C", map[string]float64{"hard": .60, "normal": .50}},
	}
}

func parseFloorSets(value string) ([]floorSet, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var selected []floorSet
	for _, part := range strings.Split(value, ",") {
		name := strings.ToUpper(strings.TrimSpace(part))
		found := false
		for _, f := range floorSets() {
			if f.name == "floor "+name {
				selected = append(selected, f)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown floor set %q", part)
		}
	}
	return selected, nil
}

func pickV2(es []*candEval, floor float64) *candEval {
	var winner *candEval
	for _, e := range es {
		if !e.qualified || e.pass < floor {
			continue
		}
		if winner == nil || e.rank < winner.rank || e.rank == winner.rank && (e.cell.Score > winner.cell.Score || e.cell.Score == winner.cell.Score && e.row.Name < winner.row.Name) {
			winner = e
		}
	}
	return winner
}

func pickWithFloor(es []*candEval, today *candEval, floor float64) (*candEval, bool) {
	if w := pickV2(es, floor); w != nil {
		return w, false
	}
	return today, true
}

type modelCount struct {
	Name  string
	Count int
}

// costView prices one pick's chosen model with the V2 table, whichever rule
// chose it, so every row reports cost per accepted result the same way.
type costView struct {
	Run, Clean, PerAccepted, Adjusted float64
}

// yardstick is the run cost over the clean share: the cost of one accepted
// result when failed runs are retried. The pace factor then divides it, as the
// live ranking divides its expected cost.
func yardstick(e *candEval, pace float64) costView {
	v := costView{Run: e.cost, Clean: e.pass, PerAccepted: math.Inf(1)}
	if e.pass > 0 {
		v.PerAccepted = e.cost / e.pass
	}
	v.Adjusted = v.PerAccepted / pace
	return v
}

// paceOf is the quota pace factor of a roster row; a row without one counts as on pace.
func paceOf(r CandRow) float64 {
	if r.PaceFactor <= 0 {
		return 1
	}
	return r.PaceFactor
}

// viewOf prices the winner of a pick from the V2 candidates of that pick. A
// pick with no winner has no view.
func viewOf(v2 []*candEval, winner *candEval) costView {
	if winner == nil {
		return costView{}
	}
	return yardstick(findKey(v2, winner.key), paceOf(winner.row))
}

// summary is one method's result over the replayed picks. Rule says how the
// own-rule cost is priced; the yardstick columns are the same for every method.
type summary struct {
	Name     string
	Rule     string
	Picks    int
	Models   map[string]int
	sumP     float64
	sumRun   float64
	sumPer   float64
	sumAdj   float64
	sumOwn   float64
	sumQual  float64
	NoPick   int
	Fallback int
	ByLevel  map[string]map[string]int
}

func newSummary(name, rule string) *summary {
	return &summary{Name: name, Rule: rule, Models: map[string]int{}, ByLevel: map[string]map[string]int{}}
}

func (s *summary) add(winner *candEval, view costView, quality float64, level string) {
	s.Picks++
	if winner == nil {
		s.NoPick++
		return
	}
	s.Models[winner.row.Name]++
	if s.ByLevel[level] == nil {
		s.ByLevel[level] = map[string]int{}
	}
	s.ByLevel[level][winner.row.Name]++
	s.sumP += view.Clean
	s.sumRun += view.Run
	s.sumPer += view.PerAccepted
	s.sumAdj += view.Adjusted
	s.sumOwn += winner.expected
	s.sumQual += quality
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

// averageView is a summary's mean per pick over the picks that had a winner.
type averageView struct {
	P, Run, PerAcc, Adj, Own, Qual float64
}

func (s *summary) averages() averageView {
	n := float64(max(1, s.Picks-s.NoPick))
	return averageView{
		P:      s.sumP / n,
		Run:    s.sumRun / n,
		PerAcc: s.sumPer / n,
		Adj:    s.sumAdj / n,
		Own:    s.sumOwn / n,
		Qual:   s.sumQual / n,
	}
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
func RunReplay(s *Snapshot, alphas []float64, floors []floorSet) *Report {
	r := &Report{Snapshot: s, Alphas: alphas}
	picks := append([]Pick(nil), s.Picks...)
	sort.Slice(picks, func(i, j int) bool { return picks[i].At < picks[j].At })
	r.Today = newSummary("today", "today: run cost + (1 - p) x rescue")
	r.Check = &replayCheck{}
	for _, a := range alphas {
		r.OptionsA = append(r.OptionsA, newSummary(fmt.Sprintf("V2 alpha %.0f", a), "V2: run cost / p"))
	}
	for _, f := range floors {
		r.Floors = append(r.Floors, newSummary(f.name, "V2: run cost / p; fallbacks: today's rule"))
	}
	for _, p := range picks {
		tasks := s.TasksAt(p.At)
		tableToday := protocol.BuildPickerTable(pickerTasks(tasks, math.MaxInt64, func(t Task) float64 { return t.ScoreToday }), protocol.DefaultPickerSettings())
		v2set := protocol.DefaultPickerSettings()
		v2set.QualityV2 = true
		tableA := protocol.BuildPickerTable(pickerTasks(tasks, math.MaxInt64, OptionAScore), v2set)
		esToday, _ := replayCandidates(p, tableToday, s.Prices, s.Scores, s.Minimums, s, protocol.PickerCostOptions{})
		winnerToday := pickToday(esToday)
		// Every row is priced from the V2 candidates of this pick.
		v2es, _ := replayCandidates(p, tableA, s.Prices, s.Scores, s.Minimums, s, protocol.PickerCostOptions{QualityV2: true})
		// Quality is reported on today's score for every method, so the rows
		// compare. Option A ranks on its own reweighted score, shown in the case.
		commonQ := func(w *candEval) float64 {
			if w == nil {
				return 0
			}
			return tableToday.Cell(w.key, tableToday.Bucket(p.D)).Score
		}
		r.Today.add(winnerToday, viewOf(v2es, winnerToday), commonQ(winnerToday), levelForD(p.D))

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

		level := levelForD(p.D)
		for i, a := range alphas {
			es, _ := replayCandidates(p, tableA, s.Prices, s.Scores, s.Minimums, s, protocol.PickerCostOptions{QualityV2: true, Alpha: a})
			w := pickV2(es, 0)
			r.OptionsA[i].add(w, viewOf(v2es, w), commonQ(w), level)
		}
		for i, f := range floors {
			es, _ := replayCandidates(p, tableA, s.Prices, s.Scores, s.Minimums, s, protocol.PickerCostOptions{QualityV2: true, CleanFloor: f.floor(level)})
			w, fallback := pickWithFloor(es, winnerToday, f.floor(level))
			if fallback {
				r.Floors[i].Fallback++
			}
			r.Floors[i].add(w, viewOf(v2es, w), commonQ(w), level)
		}
		if r.Case == nil {
			if c := casePick(p); c != nil {
				r.Case = r.caseReport(*c, tableToday, tableA, alphas, floors)
			}
		}
	}
	return r
}

// casePick is the D 3.20 pick the owner named: it chose the cheap DeepSeek
// profile over Sol, which scores better and ends clean more often.
func casePick(p Pick) *Pick {
	if p.Session != "s_c1ce54688a069800" {
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
	Roster   []CaseRoster
	Variants []CaseChoice
}

// CaseRoster is one candidate of the case's pick as today's table scores it,
// with its V2 cost yardstick.
type CaseRoster struct {
	Name    string
	Model   string
	Quality float64
	N       int
	Allowed bool
	Pace    float64
	View    costView
}

// CaseChoice is one method's winner for the case, with the profiles the owner
// compared under the same method.
type CaseChoice struct {
	Variant string
	Winner  string
	Quality float64
	View    costView
	Own     float64
	Rank    float64
	Cands   []CaseCand
}

// CaseCand is one profile's numbers under one method.
type CaseCand struct {
	Name    string
	Quality float64
	View    costView
}

// caseProfiles are the two profiles the owner compared, and the winner is
// always listed too.
var caseProfiles = []string{"deepseek alibaba high", "sol codex"}

func named(es []*candEval, name string) *candEval {
	for _, e := range es {
		if e.row.Name == name {
			return e
		}
	}
	return nil
}

func (r *Report) caseReport(p Pick, tableToday, tableA *protocol.PickerTable, alphas []float64, floors []floorSet) *CaseReport {
	s := r.Snapshot
	c := &CaseReport{Pick: p}
	esToday, _ := replayCandidates(p, tableToday, s.Prices, s.Scores, s.Minimums, s, protocol.PickerCostOptions{})
	v2es, _ := replayCandidates(p, tableA, s.Prices, s.Scores, s.Minimums, s, protocol.PickerCostOptions{QualityV2: true})
	for _, e := range esToday {
		c.Roster = append(c.Roster, CaseRoster{
			Name: e.row.Name, Model: protocol.PickerModelDisplay(e.key), Quality: e.cell.Score,
			N: e.cell.N, Allowed: e.allowed, Pace: paceOf(e.row), View: viewOf(v2es, e),
		})
	}
	add := func(variant string, w *candEval, rank float64, es []*candEval) {
		ch := CaseChoice{Variant: variant, Rank: rank}
		if w != nil {
			ch.Winner = w.row.Name
			ch.Quality = w.cell.Score
			ch.View = viewOf(v2es, w)
			ch.Own = w.expected
		}
		names := caseProfiles
		if w != nil && w.row.Name != caseProfiles[0] && w.row.Name != caseProfiles[1] {
			names = append(append([]string(nil), caseProfiles...), w.row.Name)
		}
		for _, name := range names {
			e := named(es, name)
			if e == nil {
				continue
			}
			ch.Cands = append(ch.Cands, CaseCand{Name: name, Quality: e.cell.Score, View: viewOf(v2es, e)})
		}
		c.Variants = append(c.Variants, ch)
	}
	add("today", pickToday(esToday), 0, esToday)
	for _, a := range alphas {
		esA, _ := replayCandidates(p, tableA, s.Prices, s.Scores, s.Minimums, s, protocol.PickerCostOptions{QualityV2: true, Alpha: a})
		w := pickV2(esA, 0)
		rank := 0.0
		if w != nil {
			rank = w.rank
		}
		add(fmt.Sprintf("V2 alpha %.0f", a), w, rank, esA)
	}
	for _, f := range floors {
		es, _ := replayCandidates(p, tableA, s.Prices, s.Scores, s.Minimums, s, protocol.PickerCostOptions{QualityV2: true, CleanFloor: f.floor(levelForD(p.D))})
		w, fallback := pickWithFloor(es, pickToday(esToday), f.floor(levelForD(p.D)))
		if fallback {
			add(f.name+" (fallback)", w, 0, esToday)
		} else {
			add(f.name, w, w.rank, es)
		}
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
	OptionsA []*summary
	Floors   []*summary
	Case     *CaseReport
}
