package gateway

import (
	"time"

	"github.com/niparis/agent-net/internal/model"
	"github.com/niparis/agent-net/internal/protocol"
)

// ChooseRequest is one start's question: which candidate may run now. It
// carries the ranker's inputs and the machine's reading, so Choose can build
// one facts snapshot and decide once.
type ChooseRequest struct {
	Purpose    model.Purpose
	Node       *protocol.NodeView
	Candidates []protocol.WorkerModelCandidate
	Difficulty string
	// D is the judge's difficulty score when it has one.
	D        float64
	Scores   []protocol.ModelScore
	Minimums map[string]float64
	// Quotas is the one reading for the start: ownerQuotas with burn.
	Quotas    *protocol.Quotas
	Running   map[string]int
	Protected protocol.ProtectedProviders
	Floors    protocol.WorkerFloors
	// Auth is the auth index the candidate build already read, so a start
	// does not read it twice.
	Auth map[string]authIndex
	// Persist saves the rotation state after a real start. A preview passes
	// false.
	Persist bool
}

// ChooseResult is the gate's answer and the note that explains it.
type ChooseResult struct {
	Choice protocol.WorkerModelChoice
	Note   string
	Skips  []model.Skip
}

// Choose is the one path an automatic worker start uses to pick a model
// (decision 0162): build the facts snapshot, rank the candidates, reorder
// them, then let the gate return the first that can run now. A preview calls
// the same path with DecideOnly set and Persist false.
func (g *Gateway) Choose(req ChooseRequest) (ChooseResult, error) {
	policy := model.PolicyFor(req.Purpose)
	facts, health, err := g.chooseFacts(req.Node, req.Candidates, req.Quotas, req.Floors, req.Auth)
	if err != nil {
		return ChooseResult{}, err
	}
	pick := &protocol.PickerInput{D: req.D, Table: g.store.pickerTable(), Prices: g.store.priceBook()}
	healthFor := func(harness, modelName string) (ModelHealth, error) {
		if h, ok := health[harness+"\x00"+modelName]; ok {
			return h, nil
		}
		return g.store.ModelHealth(harness, modelName)
	}
	choice, note, err := g.chooseAutomaticWorkerCore(healthFor, g.store.WorkerRotation, pick, req.Candidates,
		req.Difficulty, req.Scores, req.Minimums, req.Quotas, req.Running, req.Protected, req.Persist)
	if err != nil {
		return ChooseResult{Choice: choice, Note: note}, err
	}
	order := append([]protocol.WorkerModelChoice{choice},
		protocol.WorkerOrder(choice, req.Candidates, req.Difficulty, req.Scores, req.Minimums, req.Quotas, pick)...)
	if policy.ProtectedLast {
		order = protectedLastOrder(order, req.Protected)
	}
	index := make(map[string]int, len(order))
	list := make([]model.Candidate, len(order))
	for i, c := range order {
		list[i] = modelCandidate(c.WorkerModelCandidate)
		index[list[i].Key()] = i
	}
	picked, skips, gateErr := gateWithCoolingLast(list, policy.Checks, facts)
	if gateErr != nil {
		return ChooseResult{Choice: choice, Note: note, Skips: skips}, gateErr
	}
	i := index[picked.Key()]
	result := ChooseResult{Choice: order[i], Note: note, Skips: skips}
	if i != 0 {
		// The note of the ranker's first pick does not describe this one.
		result.Note = ""
		result.Choice.PickNote = ""
		result.Choice.Weight = 0
		result.Choice.ShareNote = ""
		if result.Choice.Why == "" {
			result.Choice.Why = protocol.WhyLine(req.Difficulty, result.Choice)
		}
	}
	for _, skip := range skips {
		result.Note = appendModelNote(result.Note, "skipped "+skip.Candidate.Display()+": "+skip.Reason)
	}
	return result, nil
}

// gateWithCoolingLast asks the gate twice when cooling is on: a model that is
// cooling down is used only when no other candidate can run, as before
// decision 0162.
func gateWithCoolingLast(list []model.Candidate, checks model.Checks, facts model.Facts) (model.Candidate, []model.Skip, error) {
	picked, skips, err := model.Gate(list, checks, facts)
	if err == nil || !checks.Cooling {
		return picked, skips, err
	}
	lastResort := checks
	lastResort.Cooling = false
	if picked, skips, retryErr := model.Gate(list, lastResort, facts); retryErr == nil {
		return picked, skips, nil
	}
	return picked, skips, err
}

// protectedLastOrder moves protected providers after the others without
// changing the order inside either group. It reorders, never removes.
func protectedLastOrder(order []protocol.WorkerModelChoice, setting protocol.ProtectedProviders) []protocol.WorkerModelChoice {
	if len(setting.Providers) == 0 {
		return order
	}
	out := make([]protocol.WorkerModelChoice, 0, len(order))
	for _, c := range order {
		if !isProtected(c.Harness, c.Model, setting) {
			out = append(out, c)
		}
	}
	for _, c := range order {
		if isProtected(c.Harness, c.Model, setting) {
			out = append(out, c)
		}
	}
	return out
}

func modelCandidate(c protocol.WorkerModelCandidate) model.Candidate {
	return model.Candidate{Name: c.Name, Harness: c.Harness, Model: c.Model, Effort: c.Effort, Provider: c.Provider}
}

// providerLimit is the latest limit a live session reported for one provider.
type providerLimit struct {
	until  int64
	reason string
}

// providerLimits reads the provider limits that live sessions reported, by
// quota provider. A limit in the past is ignored.
func (s *Store) providerLimits(now time.Time) (map[string]providerLimit, error) {
	rows, err := s.db.Query(`SELECT harness, model, limited_until, limited_reason FROM sessions
 WHERE limited_until > ? AND archived_at = 0`, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]providerLimit{}
	for rows.Next() {
		var harness, modelName, reason string
		var until int64
		if err := rows.Scan(&harness, &modelName, &until, &reason); err != nil {
			return nil, err
		}
		provider := protocol.QuotaProviderForStart(harness, modelName)
		if provider == "" {
			continue
		}
		if current, ok := out[provider]; !ok || until > current.until {
			out[provider] = providerLimit{until: until, reason: reason}
		}
	}
	return out, rows.Err()
}

// chooseFacts is the one snapshot a start decides on. It reads the machine's
// harnesses, the auth and health of each candidate, and the provider limits
// that live sessions reported. The model health it reads is passed back, so
// the ranker reuses it instead of reading again.
func (g *Gateway) chooseFacts(node *protocol.NodeView, candidates []protocol.WorkerModelCandidate, quotas *protocol.Quotas, floors protocol.WorkerFloors, auth map[string]authIndex) (model.Facts, map[string]ModelHealth, error) {
	now := g.store.now()
	facts := model.Facts{
		Now:         now,
		Quotas:      quotas,
		Floors:      floors,
		Harnesses:   map[string]model.HarnessFact{},
		AuthBlocked: map[string]bool{},
		Cooling:     map[string]model.CoolingFact{},
	}
	if node != nil {
		facts.Hostname = node.Hostname
		for _, h := range node.Harnesses {
			facts.Harnesses[h.Name] = model.HarnessFact{Hostname: node.Hostname, Installed: true, SignedIn: h.SignedIn}
		}
	}
	limits, err := g.store.providerLimits(now)
	if err != nil {
		return model.Facts{}, nil, err
	}
	facts.Limits = make(map[string]model.Limit, len(limits))
	for provider, limit := range limits {
		facts.Limits[provider] = model.Limit{Until: limit.until, Reason: limit.reason}
	}
	health := make(map[string]ModelHealth, len(candidates))
	if auth == nil {
		auth = map[string]authIndex{}
	}
	for _, c := range candidates {
		key := c.Harness + "\x00" + c.Model
		if node != nil {
			idx, ok := auth[c.Harness]
			if !ok {
				idx, err = g.store.modelAuthIndex(node.NodeID, c.Harness)
				if err != nil {
					return model.Facts{}, nil, err
				}
				auth[c.Harness] = idx
			}
			if idx.blocked(c.Model) {
				facts.AuthBlocked[key] = true
			}
		}
		h, err := g.store.ModelHealth(c.Harness, c.Model)
		if err != nil {
			return model.Facts{}, nil, err
		}
		health[key] = h
		if h.Cooling {
			facts.Cooling[key] = model.CoolingFact{Failures: h.Failures, Until: h.Until}
		}
	}
	return facts, health, nil
}

// rosterRankForSkip orders gate skips in the refusal roster.
func rosterRankForSkip(check string) int {
	switch check {
	case "floor":
		return rosterFloor
	case "installed", "signed-in", "auth":
		return rosterUnavailable
	}
	return protocol.LeftOutQuota
}

// appendGateSkips adds the gate's refusals to the roster a start shows when
// no candidate is left.
func appendGateSkips(roster *notStartedRoster, skips []model.Skip) {
	for _, skip := range skips {
		if skip.Candidate.Name == "" && skip.Candidate.Harness == "" {
			continue
		}
		roster.add(skip.Candidate.Display(), "skipped: "+skip.Reason, rosterRankForSkip(skip.Check))
	}
}
