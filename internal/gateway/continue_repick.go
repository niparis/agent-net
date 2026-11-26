package gateway

import (
	"fmt"

	"github.com/niparis/agent-net/internal/model"
	"github.com/niparis/agent-net/internal/protocol"
)

// continueRepick is the picker's answer for a continue of an ended worker
// (decision 0189).
type continueRepick struct {
	// moved is true when the continue runs on another model than the old one.
	moved          bool
	harness, model string
	// note says what happened to the old model: why it moved, or why it
	// stayed although it no longer qualifies. Empty when it qualifies.
	note string
	// rows are the pick's rows when the picker chose again, and quota is what
	// that pick read of the quota windows.
	rows  []protocol.PickCandidate
	quota *protocol.PickQuota
}

// repickContinue checks the model a continue would start again against the
// rules of today, at the level stored with the worker: the pause, the
// protected providers, the cell and ceiling rules, the floors and the quota.
// A model that still qualifies stays. Otherwise the picker chooses again from
// the normal roster at that level. When no other model qualifies, the old
// model stays and the caller's own check says whether it may run.
//
// onlyHarness keeps the roster to the harness the owner named at the worker's
// first start (decision 0191); empty is the whole roster. persist is false for
// a validation call: it moves no rotation.
func (g *Gateway) repickContinue(node *protocol.NodeView, a *protocol.WorkerAssessment, oldHarness, oldModel, onlyHarness string, persist bool) (continueRepick, error) {
	kept := continueRepick{harness: oldHarness, model: oldModel}
	in, err := g.pickInputs(node, onlyHarness)
	if err != nil || len(in.profiles) == 0 {
		// No profiles: automatic starts do not run, so there is no roster to
		// meet, and the continue keeps its model as before.
		return kept, err
	}
	level := protocol.WorkerLevel(a)
	reads := &candidateFacts{}
	candidates, _, _, err := in.candidates(g, reads, nil, node)
	if err != nil {
		return kept, err
	}
	old, inRoster := continueCandidate(candidates, oldHarness, oldModel)
	request := ChooseRequest{
		Purpose:        model.PurposeWorkerAutomatic,
		Node:           node,
		Candidates:     candidates,
		Difficulty:     level,
		D:              protocol.PickerD(a, level),
		DifficultyNote: workerDifficultyWhy(a, level),
		Scores:         in.scores,
		Minimums:       in.minimums,
		Quotas:         in.quotas,
		Running:        in.running,
		Protected:      in.protected,
		Floors:         in.floors,
		Auth:           reads.auth,
	}
	// The check asks the gate for the old model first, without a fresh
	// rotation step: it passes when the ranker keeps the old model and the gate
	// grants it. A protected provider is saved for the orchestrator, so it
	// does not pass here; the pick below may still choose it when nothing else
	// can run.
	var why string
	if inRoster {
		check := request
		check.Prefer = old.Key()
		checked, cerr := g.Choose(check)
		if cerr == nil && checked.Choice.Key() == old.Key() {
			if !isProtected(old.Harness, old.Model, in.protected) {
				return kept, nil
			}
			why = fmt.Sprintf("protected provider (%s), saved for your orchestrator", protocol.ProfileProvider(old.Harness, old.Model))
		} else {
			why = continueLeftOutReason(checked, old, level, cerr)
		}
	} else {
		why = fmt.Sprintf("no worker profile for it can run on %s", node.Hostname)
	}
	oldName := oldHarness + " " + oldModel
	noOther := fmt.Sprintf("continue kept %s: no other model qualifies at %s (old model: %s)", oldName, level, why)
	if len(candidates) == 0 {
		kept.note = noOther
		return kept, nil
	}
	pick := request
	pick.Persist = persist
	chosen, perr := g.Choose(pick)
	if perr != nil {
		kept.note = noOther
		return kept, nil
	}
	if chosen.Choice.Key() == old.Key() {
		// The pick chose the old model again: the only one left was the
		// protected one, or the check met a model the gate
		// still allows as a last resort.
		return kept, nil
	}
	return continueRepick{moved: true, harness: chosen.Choice.Harness, model: chosen.Choice.Model, rows: chosen.Pick, quota: chosen.QuotaRead(g.store.now()),
		note: fmt.Sprintf("continue moved from %s to %s %s at %s (old model: %s)", oldName, chosen.Choice.Harness, chosen.Choice.Model, level, why)}, nil
}

// continueCandidate finds the old model in the roster: the same harness and
// model, else the same harness and model with another effort.
func continueCandidate(candidates []protocol.WorkerModelCandidate, harness, modelName string) (protocol.WorkerModelCandidate, bool) {
	for _, c := range candidates {
		if c.Harness == harness && c.Model == modelName {
			return c, true
		}
	}
	id := protocol.ParseModelSelection(harness, "", modelName, "").ModelID()
	for _, c := range candidates {
		if c.Harness == harness && protocol.ParseModelSelection(c.Harness, "", c.Model, "").ModelID() == id {
			return c, true
		}
	}
	return protocol.WorkerModelCandidate{Harness: harness, Model: modelName}, false
}

// continueLeftOutReason is why the check did not keep the old model: the
// gate's skip (paused, floor, quota), else the ranker's reason.
func continueLeftOutReason(checked ChooseResult, old protocol.WorkerModelCandidate, level string, err error) string {
	for _, s := range checked.Skips {
		if s.Candidate.Key() == old.Key() {
			return s.Reason
		}
	}
	name := rosterName(old.Name, old.Harness, old.Model)
	for _, lo := range append(append([]protocol.WorkerLeftOut(nil), checked.LeftOut...), checked.Choice.LeftOut...) {
		if lo.Name == name || lo.Name == old.Name {
			return lo.Reason
		}
	}
	for _, r := range checked.Pick {
		if sameRowCandidate(r, old) && r.LeftOutReason != "" {
			return r.LeftOutReason
		}
	}
	if err != nil {
		return "cannot run now: " + err.Error()
	}
	return "not allowed at " + level + " by the picker's rules"
}
