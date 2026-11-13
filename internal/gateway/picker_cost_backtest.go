package gateway

import (
	"sort"

	"github.com/niparis/agent-net/internal/protocol"
)

// PickerCostBacktestRow is one finished task: the model that ran it then and what it
// cost, and the model the cost rule picks now and what one run of it is
// expected to cost (decision 0160).
type PickerCostBacktestRow struct {
	EndedAt  int64
	D        float64
	Then     string
	ThenCost float64 // 0 when the task has no recorded usage
	Now      string
	NowCost  float64 // 0 when it cannot be priced
}

// PickerCostBacktest re-runs the pick for the last limit finished tasks with a D,
// using today's table, prices and registered profiles, with no quota and a fresh
// rotation. It is a read-only check of the rule against real history.
func (s *Store) PickerCostBacktest(limit int) ([]PickerCostBacktestRow, error) {
	records, err := s.pickerTaskRecords()
	if err != nil {
		return nil, err
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
		return nil, err
	}
	var candidates []protocol.WorkerModelCandidate
	for _, p := range profiles {
		candidates = append(candidates, protocol.WorkerModelCandidate{Name: p.Name, Harness: p.Harness, Model: p.Selection().LegacyModel()})
	}
	scores, err := s.scores()
	if err != nil {
		return nil, err
	}
	minimums, err := s.difficultyMinimums()
	if err != nil {
		return nil, err
	}
	table, prices := s.pickerTable(), s.priceBook()
	var out []PickerCostBacktestRow
	for _, r := range done {
		row := PickerCostBacktestRow{EndedAt: r.end, D: r.d, Then: r.key}
		if !r.tokens.Zero() {
			row.ThenCost, _, _ = s.RunCost(r.key, r.tokens)
		}
		level := "normal"
		for _, l := range protocol.DifficultyLevels {
			if lo, hi, ok := protocol.LevelDRange(l); ok && r.d >= lo && r.d < hi {
				level = l
			}
		}
		choice, err := protocol.ChooseWorkerModel(candidates, level, scores, minimums, nil, nil, &protocol.WorkerRotation{}, &protocol.PickerInput{D: r.d, Table: table, Prices: prices})
		if err == nil {
			row.Now = protocol.PickerModelKey(choice.Harness, choice.Model)
			row.NowCost, _, _, _ = table.ExpectedCost(row.Now, table.Bucket(r.d), prices)
		}
		out = append(out, row)
	}
	return out, nil
}
