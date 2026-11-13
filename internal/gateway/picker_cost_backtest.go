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
	Rows []PickerCostBacktestRow
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

// noticeUsage reads the "tokens:" line of every stored worker notice, by worker
// session. Runs that ended before the gateway kept usage by kind have only
// this. A session's notices are cumulative, so the largest count of each kind
// stands. It is for the backtest only.
func (s *Store) noticeUsage() (map[string]protocol.RunTokens, error) {
	rows, err := s.db.Query(`SELECT body FROM messages WHERE body LIKE '%[agent-net worker notice]%' AND body LIKE '%tokens: %'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]protocol.RunTokens{}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if !strings.Contains(body, "\n") {
			// A body kept with its line breaks escaped.
			body = strings.ReplaceAll(body, `\n`, "\n")
		}
		var session string
		var tokens protocol.RunTokens
		var found bool
		for _, line := range strings.Split(body, "\n") {
			switch {
			case strings.HasPrefix(line, "worker: "):
				session, _, _ = strings.Cut(strings.TrimPrefix(line, "worker: "), " ")
			case strings.HasPrefix(line, "tokens: "):
				tokens, found = protocol.ParseTokenText(strings.TrimPrefix(line, "tokens: "))
			}
		}
		if found && strings.HasPrefix(session, "s_") {
			out[session] = out[session].Max(tokens)
		}
	}
	return out, rows.Err()
}

// PickerCostBacktest re-runs the pick for the last limit finished tasks with a D.
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
	if len(done) > limit {
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
	for _, r := range done {
		row := PickerCostBacktestRow{EndedAt: r.end, D: r.d, Then: r.key}
		if p, _, ok := prices.PriceOf(r.key); ok && !r.tokens.Zero() {
			row.ThenCost = p.Cost(r.tokens)
		}
		level := "normal"
		for _, l := range protocol.DifficultyLevels {
			if lo, hi, ok := protocol.LevelDRange(l); ok && r.d >= lo && r.d < hi {
				level = l
			}
		}
		choice, err := protocol.ChooseWorkerModel(candidates, level, scores, minimums, o.Quotas, nil, &protocol.WorkerRotation{}, &protocol.PickerInput{D: r.d, Table: table, Prices: prices})
		if err == nil {
			row.Now = protocol.PickerModelKey(choice.Harness, choice.Model)
			row.NowCost, _, _, _ = table.ExpectedCost(row.Now, table.Bucket(r.d), prices)
		}
		res.Rows = append(res.Rows, row)
	}
	return res, nil
}
