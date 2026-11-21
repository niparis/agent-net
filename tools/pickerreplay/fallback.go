package main

import (
	"fmt"
	"math"
	"sort"

	"github.com/niparis/agent-net/internal/protocol"
)

// fallbackEdge is the tolerance the picker adds to a ceiling before it counts
// the model as above its bucket. The live picker keeps it unexported.
const fallbackEdge = 1e-5

// FallbackReport is the replay of the recorded picks that no candidate
// qualified for: the fallback draw. It sets the recorded share of each allowed
// candidate beside the share the draw by expected cost gives it.
type FallbackReport struct {
	Picks    int // recorded cell picks with no qualifying candidate
	Replayed int // of those, the picks with an allowed candidate to draw from
	// Unbounded counts picks left out of the cost means: an allowed candidate
	// had no finite expected cost.
	Unbounded int
	// Changed counts the picks whose most likely candidate changes. Moves
	// counts them by "recorded -> new".
	Changed int
	Moves   map[string]int
	// OldCost and NewCost are the mean over the costed picks of the expected
	// cost of the draw, weighted by each candidate's share.
	OldCost, NewCost float64
	// OldCheapest and NewCheapest are the mean share of the cheapest allowed
	// candidate of each pick.
	OldCheapest, NewCheapest float64
	costed                   int
}

// RunFallback replays the fallback draw of every recorded pick that no
// candidate qualified for. The recorded shares, expected costs and allowed
// set are the ones the live picker stored. The new weight is the quota pace
// times protocol.FallbackDrawWeight of the expected cost, times the uncertain
// boost for a thin cell that is not far above the work. The track record and
// the recent success or failure marks are not replayed, because the rows do not
// hold them.
func RunFallback(s *Snapshot) *FallbackReport {
	r := &FallbackReport{Moves: map[string]int{}}
	for _, p := range s.Picks {
		if !isFallbackPick(p) {
			continue
		}
		r.Picks++
		allowed := allowedRows(p)
		if len(allowed) == 0 {
			continue
		}
		r.Replayed++
		tasks := s.TasksAt(p.At)
		table := protocol.BuildPickerTable(pickerTasks(tasks, math.MaxInt64, func(t Task) float64 { return t.ScoreToday }), protocol.DefaultPickerSettings())
		set := table.Settings()
		bucket := table.Bucket(p.D)
		replayed := map[string]*candEval{}
		es, _ := replayCandidates(p, table, s.Prices, s.Scores, s.Minimums, s, protocol.PickerCostOptions{})
		for _, e := range es {
			replayed[e.key] = e
		}

		newShare := make([]float64, len(allowed))
		var total float64
		for i, row := range allowed {
			w := protocol.FallbackDrawWeight(expectedOf(row)) * rowPace(row)
			if e := replayed[row.Key()]; e != nil {
				above := e.scored && table.Bucket(e.ceiling-fallbackEdge*10)-bucket > 1
				if e.cell.N < set.UncertainN && !above {
					w *= set.UncertainBoost
				}
			} else if row.CellN < set.UncertainN {
				w *= set.UncertainBoost
			}
			newShare[i] = w
			total += w
		}
		for i := range newShare {
			newShare[i] /= total
		}

		oldTop, newTop, cheapest := 0, 0, 0
		for i, row := range allowed {
			if row.Share > allowed[oldTop].Share || row.Share == allowed[oldTop].Share && row.Name < allowed[oldTop].Name {
				oldTop = i
			}
			if newShare[i] > newShare[newTop] || newShare[i] == newShare[newTop] && row.Name < allowed[newTop].Name {
				newTop = i
			}
			if adjustedOf(row) < adjustedOf(allowed[cheapest]) || adjustedOf(row) == adjustedOf(allowed[cheapest]) && row.Name < allowed[cheapest].Name {
				cheapest = i
			}
		}
		if allowed[oldTop].Name != allowed[newTop].Name {
			r.Changed++
			r.Moves[allowed[oldTop].Name+" -> "+allowed[newTop].Name]++
		}
		r.OldCheapest += allowed[cheapest].Share
		r.NewCheapest += newShare[cheapest]

		var oldCost, newCost float64
		finite := true
		for i, row := range allowed {
			exp := adjustedOf(row)
			if math.IsInf(exp, 1) {
				finite = false
				break
			}
			oldCost += row.Share * exp
			newCost += newShare[i] * exp
		}
		if !finite {
			r.Unbounded++
			continue
		}
		r.costed++
		r.OldCost += oldCost
		r.NewCost += newCost
	}
	if r.costed > 0 {
		r.OldCost /= float64(r.costed)
		r.NewCost /= float64(r.costed)
	}
	if r.Replayed > 0 {
		r.OldCheapest /= float64(r.Replayed)
		r.NewCheapest /= float64(r.Replayed)
	}
	return r
}

// isFallbackPick is true for a cell pick that no candidate qualified for.
func isFallbackPick(p Pick) bool {
	if len(p.Rows) == 0 || p.Rows[0].Rule != protocol.PickRuleCell {
		return false
	}
	for _, r := range p.Rows {
		if r.Qualified {
			return false
		}
	}
	return true
}

// allowedRows are the candidates the live picker allowed, in roster order.
func allowedRows(p Pick) []CandRow {
	var out []CandRow
	for _, r := range p.Rows {
		if r.Allowed {
			out = append(out, r)
		}
	}
	return out
}

// expectedOf is the stored expected cost. The live picker stores zero for an
// unbounded cost, so zero reads as unbounded here, as the live draw clamps it.
func expectedOf(r CandRow) float64 {
	if r.Expected == 0 {
		return math.Inf(1)
	}
	return r.Expected
}

// rowPace is the stored quota pace factor, one when the row has none.
func rowPace(r CandRow) float64 {
	if r.PaceFactor <= 0 {
		return 1
	}
	return r.PaceFactor
}

// adjustedOf is the expected cost after pace, the cost the draw ranks by.
func adjustedOf(r CandRow) float64 {
	return expectedOf(r) / rowPace(r)
}

func printFallback(r *FallbackReport) {
	fmt.Printf("# Fallback draw by expected cost\n\n")
	fmt.Printf("Recorded picks with no qualifying candidate: %d. With an allowed candidate to draw from: %d.\n", r.Picks, r.Replayed)
	if r.Replayed == 0 {
		return
	}
	fmt.Printf("The most likely candidate changes in %d of %d picks.\n", r.Changed, r.Replayed)
	keys := make([]string, 0, len(r.Moves))
	for k := range r.Moves {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if r.Moves[keys[i]] != r.Moves[keys[j]] {
			return r.Moves[keys[i]] > r.Moves[keys[j]]
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		fmt.Printf("- %s: %d\n", k, r.Moves[k])
	}
	fmt.Printf("Mean share of the cheapest allowed candidate: recorded %.1f%%, new %.1f%%.\n", 100*r.OldCheapest, 100*r.NewCheapest)
	fmt.Printf("Mean expected cost of the draw over %d costed picks: recorded %.4f, new %.4f.", r.costed, r.OldCost, r.NewCost)
	if r.Unbounded > 0 {
		fmt.Printf(" %d picks had an unbounded candidate and are left out of the means.", r.Unbounded)
	}
	fmt.Println()
}
