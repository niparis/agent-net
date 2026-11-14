package protocol

import (
	"fmt"
	"strconv"
)

// WorkerPickOrder is the cost ranker's whole order for one start (decision
// 0162). The first choice is the one the ranker picks; the rest follow in the
// order the gate should try them. The ranker removes no candidate: a provider
// who has no room is at the end, and the gate refuses it with its own reason.
//
// The cell rule orders by the expected cost of one accepted result, with the
// level rule as its fallback. Pace is part of the weight; protected providers
// last and rotation are modifiers the caller applies to this list.
func WorkerPickOrder(candidates []WorkerModelCandidate, level string, scores []ModelScore, minimums map[string]float64, quotas *Quotas, running map[string]int, rotation *WorkerRotation, in *PickerInput) ([]WorkerModelChoice, []WorkerLeftOut, error) {
	if in != nil && in.usable() {
		if level == "" {
			level = "normal"
		}
		if _, err := DifficultyFit(0, level, minimums); err != nil {
			return nil, nil, err
		}
		order, _, fallback := pickOrderByCells(candidates, level, scores, minimums, quotas, running, rotation, in, false)
		if fallback == "" {
			return order, leftOutAfter(candidates, order, level, scores, minimums), nil
		}
	}
	order, leftOut, err := levelOrder(candidates, level, scores, minimums, quotas, running, rotation, true)
	return order, leftOut, err
}

// leftOutAfter names each candidate the ranker did not put in the order, and
// why. These are the candidates that never reach the gate: they are not
// eligible for the work, or their record leaves them out.
func leftOutAfter(candidates []WorkerModelCandidate, order []WorkerModelChoice, level string, scores []ModelScore, minimums map[string]float64) []WorkerLeftOut {
	kept := make(map[string]bool, len(order))
	for _, c := range order {
		kept[WorkerModelKey(c.WorkerModelCandidate)] = true
	}
	var out []WorkerLeftOut
	for _, c := range candidates {
		if kept[WorkerModelKey(c)] {
			continue
		}
		name := c.Name
		if name == "" {
			name = c.Harness + " " + c.Model
		}
		score, fit, adequate, ok := WorkerCandidateFit(c, level, scores, minimums)
		switch {
		case !ok:
			out = append(out, WorkerLeftOut{Name: name, Reason: "unscored (no benchmark or rating)", Rank: LeftOutNoScore})
		case !adequate:
			out = append(out, WorkerLeftOut{Name: name, Reason: fmt.Sprintf("max %s, score %g, %s", WorkerMaximumLevel(score, minimums), score, fit), Rank: LeftOutFit})
		default:
			out = append(out, WorkerLeftOut{Name: name, Reason: "left out by the record of its cell", Rank: LeftOutQuota})
		}
	}
	return out
}

// WhyLine is the short explanation for a candidate the order lists behind the
// chosen one: the difficulty, the model and its score.
func WhyLine(level string, choice WorkerModelChoice) string {
	return level + ": " + choice.Model + " (score " + trimFloat(choice.Score) + ")"
}

// trimFloat writes a score with at most one decimal.
func trimFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', 3, 64)
}
