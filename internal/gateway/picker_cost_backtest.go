package gateway

import (
	"fmt"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// PickerCostBacktestRow is one finished task: the model that ran it then and what
// it cost, and the model the cost rule picks now and what one run of it is
// expected to cost (decision 0161).
type PickerCostBacktestRow struct {
	EndedAt  int64
	D        float64
	Then     string
	ThenCost float64 // 0 when the task has no usage on record
	Now      string
	NowCost  float64 // 0 when it cannot be priced
}

// PickerCostBacktestBucket is what the rule compares in one D bucket: each
// candidate's average run cost, p, rescue and expected cost, and the winner.
type PickerCostBacktestBucket struct {
	Lo, Hi float64
	Tasks  int
	Winner string
	Rows   []protocol.PickerCostRow
}

// PickerCostBacktestOptions are the inputs the live picker reads from memory
// or the network, which a database copy does not hold.
type PickerCostBacktestOptions struct {
	// Prices is the price list. Nil uses the copy in model_prices.
	Prices *protocol.PriceBook
	// Quotas is a quota reading. The backtest applies today's worker floors
	// (from the database) to it, and the chooser's own limits. Nil means no
	// reading, so no floor applies.
	Quotas *protocol.Quotas
}

// PickerCostBacktestResult is the table plus what shaped it.
type PickerCostBacktestResult struct {
	Rows    []PickerCostBacktestRow
	Buckets []PickerCostBacktestBucket
	// OutByFloor names the profiles today's worker floors keep out.
	OutByFloor []string
	// QuotaReadings says, for each registered profile, which quota reading it
	// met: the provider and the used percent of the windows that serve it, or
	// "no reading".
	QuotaReadings []string
	// Tasks is how many tasks with a D the table holds, TasksWithUsage how many
	// of them have tokens on record.
	Tasks, TasksWithUsage int
	// Prices is how many models the price list holds.
	Prices int
}

// PickerCostBacktest re-runs the pick for the last limit finished tasks with a D
// (every task when limit is 0), and summarizes each bucket they fall in.
// It uses today's registered profiles, the given prices and quota reading and
// the database's worker floors, with a fresh rotation. The table is built with
// the usage of each task: what the gateway recorded, else the tokens line of its
// stored notices. It is a read-only check of the rule against real history.
func (s *Store) PickerCostBacktest(limit int, o PickerCostBacktestOptions) (PickerCostBacktestResult, error) {
	var res PickerCostBacktestResult
	prices := o.Prices
	if prices == nil {
		prices = s.priceBook()
	}
	res.Prices = prices.Len()
	usage, err := s.noticeUsage()
	if err != nil {
		return res, err
	}
	recorded, err := s.workerUsage()
	if err != nil {
		return res, err
	}
	for session, t := range recorded {
		usage[session] = t
	}
	records, err := s.pickerTaskRecordsUsing(usage)
	if err != nil {
		return res, err
	}
	tasks := pickerTasksFrom(records, prices)
	set, err := s.pickerSettings()
	if err != nil {
		return res, err
	}
	table := protocol.BuildPickerTable(tasks, set)
	res.Tasks = len(tasks)
	for _, t := range tasks {
		if !t.Tokens.Zero() {
			res.TasksWithUsage++
		}
	}

	var done []pickerTaskRecord
	for _, r := range records {
		if r.d > 0 && r.key != "" {
			done = append(done, r)
		}
	}
	sort.SliceStable(done, func(i, j int) bool { return done[i].end < done[j].end })
	if limit > 0 && len(done) > limit {
		done = done[len(done)-limit:]
	}

	profiles, err := s.WorkerProfiles()
	if err != nil {
		return res, err
	}
	floors, err := s.workerFloors()
	if err != nil {
		return res, err
	}
	var candidates []protocol.WorkerModelCandidate
	for _, p := range profiles {
		c := protocol.WorkerModelCandidate{Name: p.Name, Harness: p.Harness, Model: p.Selection().LegacyModel(), DefaultEffort: p.Effort}
		if under, sentence, _ := protocol.CheckWorkerFloor(o.Quotas, floors, c.Harness, c.Model); under {
			res.OutByFloor = append(res.OutByFloor, fmt.Sprintf("%s: %s", c.Name, sentence))
			continue
		}
		candidates = append(candidates, c)
	}
	for _, p := range profiles {
		model := p.Selection().LegacyModel()
		provider := protocol.QuotaProviderForStart(p.Harness, model)
		text := "no reading"
		if qp, ok := o.Quotas.Provider(provider); ok {
			var used []string
			for _, w := range qp.Windows {
				if protocol.QuotaWindowServes(w, model) {
					used = append(used, fmt.Sprintf("%s %.0f%% used", w.ID, w.PercentUsed))
				}
			}
			text = strings.Join(used, ", ")
		}
		res.QuotaReadings = append(res.QuotaReadings, fmt.Sprintf("%s -> quota provider %q: %s", p.Name, provider, text))
	}
	scores, err := s.scores()
	if err != nil {
		return res, err
	}
	minimums, err := s.difficultyMinimums()
	if err != nil {
		return res, err
	}
	levelOf := func(d float64) string {
		level := "normal"
		for _, l := range protocol.DifficultyLevels {
			if lo, hi, ok := protocol.LevelDRange(l); ok && d >= lo && d < hi {
				level = l
			}
		}
		return level
	}
	buckets := map[int]*PickerCostBacktestBucket{}
	var order []int
	for _, r := range done {
		b := table.Bucket(r.d)
		if buckets[b] == nil {
			lo, hi := table.BucketRange(b)
			buckets[b] = &PickerCostBacktestBucket{Lo: lo, Hi: hi}
			order = append(order, b)
		}
		buckets[b].Tasks++
	}
	sort.Ints(order)
	for _, b := range order {
		sum := buckets[b]
		in := &protocol.PickerInput{D: (sum.Lo + sum.Hi) / 2, Table: table, Prices: prices}
		sum.Rows = protocol.PickerCostRows(candidates, in)
		if choice, err := protocol.ChooseWorkerModel(candidates, levelOf(in.D), scores, minimums, o.Quotas, nil, &protocol.WorkerRotation{}, in); err == nil {
			sum.Winner = protocol.PickerModelKey(choice.Harness, choice.Model)
		}
		res.Buckets = append(res.Buckets, *sum)
	}
	for _, r := range done {
		row := PickerCostBacktestRow{EndedAt: r.end, D: r.d, Then: r.key}
		if p, _, ok := prices.PriceOf(r.key); ok && !r.tokens.Zero() {
			row.ThenCost = p.Cost(r.tokens)
		}
		choice, err := protocol.ChooseWorkerModel(candidates, levelOf(r.d), scores, minimums, o.Quotas, nil, &protocol.WorkerRotation{}, &protocol.PickerInput{D: r.d, Table: table, Prices: prices})
		if err == nil {
			row.Now = protocol.PickerModelKey(choice.Harness, choice.Model)
			row.NowCost, _, _, _ = table.ExpectedCost(row.Now, table.Bucket(r.d), prices)
		}
		res.Rows = append(res.Rows, row)
	}
	return res, nil
}
