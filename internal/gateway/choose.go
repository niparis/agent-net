package gateway

import (
	"fmt"
	"sort"
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
	// false, and DecideOnly forces it false.
	Persist bool
	// Health is the model health the caller already read for a run of many
	// decisions, so a preview does not read the same rows once per level.
	Health func(harness, model string) (ModelHealth, error)
	// Rotation is a snapshot to decide on instead of the stored one: a preview
	// passes its own copy, so its pick matches a start without moving the
	// rotation the gateway will use next.
	Rotation *protocol.WorkerRotation
	// Facts is one snapshot a caller that decides many times already built
	// (a preview for each level, a backtest for each task). Nil builds it here.
	Facts *model.Facts
	// Prefer is the key of a candidate to try first without re-ranking it
	// away: a queued start asks the gate again for the model it already chose.
	Prefer string
}

// ChooseResult is the gate's answer and the note that explains it.
type ChooseResult struct {
	Choice protocol.WorkerModelChoice
	Note   string
	Skips  []model.Skip
	// Granted is the gate's value for the chosen candidate. Only Gate makes
	// one, so a route cannot launch a model this answer did not grant.
	Granted model.Granted
	// LeftOut names each candidate the ranker left out, with its reason.
	LeftOut []protocol.WorkerLeftOut
	// list and facts stay with the answer, so a launch that fails can ask the
	// gate for the next candidate in the same list.
	list   []protocol.WorkerModelChoice
	facts  model.Facts
	checks model.Checks
	level  string
}

// Next asks the gate for the next candidate in the same list, after the one
// it granted failed to launch. It reports false when the list has no further
// candidate that can run now.
func (r ChooseResult) Next() (ChooseResult, bool) {
	if r.Granted.IsZero() || len(r.list) == 0 {
		return ChooseResult{}, false
	}
	key := r.Granted.Candidate().Key()
	at := -1
	for i, c := range r.list {
		if modelCandidate(c.WorkerModelCandidate).Key() == key {
			at = i
			break
		}
	}
	if at < 0 || at+1 >= len(r.list) {
		return ChooseResult{}, false
	}
	rest := r.list[at+1:]
	granted, skips, err := gateWithCoolingLast(modelCandidates(rest), r.checks, r.facts)
	if err != nil {
		return ChooseResult{}, false
	}
	chosenKey := granted.Candidate().Key()
	index := -1
	for i, c := range rest {
		if modelCandidate(c.WorkerModelCandidate).Key() == chosenKey {
			index = i
			break
		}
	}
	if index < 0 {
		return ChooseResult{}, false
	}
	choice := rest[index]
	var note []string
	if index != 0 {
		choice.PickNote, choice.Weight, choice.ShareNote = "", 0, ""
		if choice.Why == "" {
			choice.Why = protocol.WhyLine(r.level, choice)
		}
	}
	for _, skip := range skips {
		note = append(note, "skipped "+skip.Candidate.Display()+": "+skip.Reason)
	}
	return ChooseResult{Choice: choice, Note: joinModelNote(note), Skips: skips, Granted: granted, list: rest, facts: r.facts, checks: r.checks, level: r.level}, true
}

// modelCandidatesOf converts a candidate list for the gate.
func modelCandidatesOf(list []protocol.WorkerModelCandidate) []model.Candidate {
	out := make([]model.Candidate, len(list))
	for i, c := range list {
		out[i] = modelCandidate(c)
	}
	return out
}

// modelCandidates converts an ordered list for the gate.
func modelCandidates(list []protocol.WorkerModelChoice) []model.Candidate {
	out := make([]model.Candidate, len(list))
	for i, c := range list {
		out[i] = modelCandidate(c.WorkerModelCandidate)
	}
	return out
}

// joinModelNote joins note lines with the separator every model note uses.
func joinModelNote(lines []string) string {
	out := ""
	for _, line := range lines {
		if line == "" {
			continue
		}
		out = appendModelNote(out, line)
	}
	return out
}

// Choose is the one path an automatic worker start uses to pick a model
// (decision 0162): build the facts snapshot, rank the candidates, reorder
// them, then let the gate return the first that can run now. The ranker removes
// no candidate; every check that can refuse one is in the gate.
func (g *Gateway) Choose(req ChooseRequest) (ChooseResult, error) {
	policy := model.PolicyFor(req.Purpose)
	var facts model.Facts
	var err error
	if req.Facts != nil {
		facts = *req.Facts
	} else {
		facts, _, err = g.chooseFacts(req.Node, req.Candidates, req.Quotas, req.Floors, req.Auth, req.Health)
		if err != nil {
			return ChooseResult{}, err
		}
	}
	level := req.Difficulty
	if level == "" {
		level = "normal"
	}
	var order []protocol.WorkerModelChoice
	var resultLeftOut []protocol.WorkerLeftOut
	switch policy.Ranker {
	case model.RankCost:
		rotation := protocol.WorkerRotation{}
		persist := req.Persist && !policy.DecideOnly
		if req.Rotation != nil {
			rotation = req.Rotation.Clone()
		} else {
			g.rotationMu.Lock()
			var readErr error
			rotation, readErr = g.store.WorkerRotation()
			g.rotationMu.Unlock()
			if readErr != nil {
				return ChooseResult{}, readErr
			}
		}
		pick := &protocol.PickerInput{D: req.D, Table: g.store.pickerTable(), Prices: g.store.priceBook()}
		var leftOut []protocol.WorkerLeftOut
		order, leftOut, err = protocol.WorkerPickOrder(req.Candidates, req.Difficulty, req.Scores, req.Minimums, req.Quotas, req.Running, &rotation, pick)
		if err == nil {
			resultLeftOut = leftOut
		}
		if err == nil && persist {
			g.rotationMu.Lock()
			err = g.store.saveWorkerRotation(rotation)
			g.rotationMu.Unlock()
		}
		if err != nil {
			return ChooseResult{}, err
		}
	case model.RankFixed, model.RankSame:
		order = make([]protocol.WorkerModelChoice, len(req.Candidates))
		for i, c := range req.Candidates {
			score, fit, _, _ := protocol.WorkerCandidateFit(c, level, req.Scores, req.Minimums)
			order[i] = protocol.WorkerModelChoice{WorkerModelCandidate: c, Score: score, Fit: fit}
			order[i].Why = protocol.WhyLine(level, order[i])
		}
		if policy.Pace {
			order = paceOrder(order, req.Quotas)
		}
	default:
		return ChooseResult{}, fmt.Errorf("purpose %q has no ranker", req.Purpose)
	}
	if len(order) == 0 {
		return ChooseResult{}, fmt.Errorf("no model qualifies for %s; register a profile for %s, or pass model explicitly", level, level)
	}
	// The ranker's note explains the whole pick: the bucket, the cells and the
	// cost terms. Keep it when the gate moves to another candidate, or the
	// start reads like the old level rule.
	rankerNote := order[0].PickNote
	if policy.ProtectedLast {
		order = protectedLastOrder(order, req.Protected)
	}
	if req.Prefer != "" {
		order = preferFirst(order, req.Prefer)
	}
	granted, skips, gateErr := gateWithCoolingLast(modelCandidates(order), policy.Checks, facts)
	result := ChooseResult{Skips: skips, LeftOut: resultLeftOut, facts: facts, checks: policy.Checks, list: order, level: level}
	if gateErr != nil {
		result.Note = joinModelNote(skipNotes(skips))
		return result, gateErr
	}
	key := granted.Candidate().Key()
	index := -1
	for i, c := range order {
		if modelCandidate(c.WorkerModelCandidate).Key() == key {
			index = i
			break
		}
	}
	if index < 0 {
		return result, fmt.Errorf("the gate granted a candidate outside the list")
	}
	result.Granted = granted
	result.Choice = order[index]
	if index != 0 {
		result.Choice.PickNote = rankerNote
		result.Choice.Fallback = order[0].Fallback
		result.Choice.Weight, result.Choice.ShareNote = 0, ""
		if result.Choice.Why == "" {
			result.Choice.Why = protocol.WhyLine(level, result.Choice)
		}
	}
	var notes []string
	if pickedCooling(result.Choice, facts) && hasHealthy(order, result.Choice, facts) {
		notes = append(notes, "used model cooling down because no healthy alternative is adequate")
	}
	notes = append(notes, protectionNotes(order, result.Choice, level, req.Scores, req.Minimums, req.Protected)...)
	if result.Granted.FloorUnread() {
		notes = append(notes, "no quota reading, floor not applied")
	}
	notes = append(notes, skipNotes(skips)...)
	result.Note = joinModelNote(notes)
	return result, nil
}

// preferFirst moves one candidate to the front of the order and keeps the
// rest in place. It is how a queued start asks the gate again for the model it
// already chose, instead of taking a fresh rotation step.
func preferFirst(order []protocol.WorkerModelChoice, key string) []protocol.WorkerModelChoice {
	for i, c := range order {
		if modelCandidate(c.WorkerModelCandidate).Key() != key {
			continue
		}
		if i == 0 {
			return order
		}
		out := make([]protocol.WorkerModelChoice, 0, len(order))
		out = append(out, c)
		out = append(out, order[:i]...)
		out = append(out, order[i+1:]...)
		return out
	}
	return order
}

// skipNotes writes one line for each candidate the gate skipped.
func skipNotes(skips []model.Skip) []string {
	out := make([]string, 0, len(skips))
	for _, skip := range skips {
		out = append(out, "skipped "+skip.Candidate.Display()+": "+skip.Reason)
	}
	return out
}

// pickedCooling reports whether the chosen candidate is cooling down.
func pickedCooling(choice protocol.WorkerModelChoice, facts model.Facts) bool {
	cool, ok := facts.Cooling[modelCandidate(choice.WorkerModelCandidate).Key()]
	return ok && cool.Until.After(facts.Now)
}

// hasHealthy reports whether another candidate is not cooling down.
func hasHealthy(order []protocol.WorkerModelChoice, choice protocol.WorkerModelChoice, facts model.Facts) bool {
	for _, c := range order {
		if modelCandidate(c.WorkerModelCandidate).Key() == modelCandidate(choice.WorkerModelCandidate).Key() {
			continue
		}
		cool, ok := facts.Cooling[modelCandidate(c.WorkerModelCandidate).Key()]
		if !ok || !cool.Until.After(facts.Now) {
			return true
		}
	}
	return false
}

// protectionNotes is the protected-provider note: a protected candidate that
// was skipped for an ordinary one, or the note that only a protected one was
// left. It is a modifier note, not a check.
func protectionNotes(order []protocol.WorkerModelChoice, choice protocol.WorkerModelChoice, level string, scores []protocol.ModelScore, minimums map[string]float64, setting protocol.ProtectedProviders) []string {
	if len(setting.Providers) == 0 {
		return nil
	}
	if isProtected(choice.Harness, choice.Model, setting) {
		reason := setting.Reason
		if reason == "" {
			reason = "your orchestrator's provider"
		}
		return []string{fmt.Sprintf("used %s %s (protected: %s) because no other profile meets %s, or the others are out of quota", choice.Harness, choice.Model, reason, level)}
	}
	var notes []string
	for _, c := range order {
		if !isProtected(c.Harness, c.Model, setting) {
			continue
		}
		if _, _, eligible, _ := protocol.WorkerCandidateFit(c.WorkerModelCandidate, level, scores, minimums); eligible {
			notes = append(notes, fmt.Sprintf("skipped %s: protected provider (%s), saved for your orchestrator", c.Name, protocol.ProfileProvider(c.Harness, c.Model)))
		}
	}
	return notes
}

// gateWithCoolingLast asks the gate twice when cooling is on: a model that is
// cooling down is used only when no other candidate can run, as before
// decision 0162.
func gateWithCoolingLast(list []model.Candidate, checks model.Checks, facts model.Facts) (model.Granted, []model.Skip, error) {
	picked, skips, err := model.Gate(list, checks, facts)
	if err == nil || !checks.Cooling {
		return picked, skips, err
	}
	lastResort := checks
	lastResort.Cooling = false
	if retried, _, retryErr := model.Gate(list, lastResort, facts); retryErr == nil {
		// Keep the first skips: they say which models were set aside for
		// cooling, and the note must still name them.
		return retried, skips, nil
	}
	return picked, skips, err
}

// paceOrder reorders a fixed list by quota pace: a provider behind its pace
// comes first. It is a modifier, so it never removes a candidate.
func paceOrder(order []protocol.WorkerModelChoice, quotas *protocol.Quotas) []protocol.WorkerModelChoice {
	if len(order) < 2 {
		return order
	}
	type paced struct {
		choice protocol.WorkerModelChoice
		room   float64
		note   string
	}
	list := make([]paced, len(order))
	for i, c := range order {
		room, note, _ := protocol.WorkerQuotaPace(quotas, c.Provider, c.Model)
		list[i] = paced{choice: c, room: room, note: note}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].note == "" || list[j].note == "" {
			return false
		}
		return list[i].room > list[j].room
	})
	out := make([]protocol.WorkerModelChoice, len(order))
	for i, p := range list {
		out[i] = p.choice
	}
	return out
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
func (g *Gateway) chooseFacts(node *protocol.NodeView, candidates []protocol.WorkerModelCandidate, quotas *protocol.Quotas, floors protocol.WorkerFloors, auth map[string]authIndex, health func(harness, model string) (ModelHealth, error)) (model.Facts, map[string]ModelHealth, error) {
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
	readHealth := health
	if readHealth == nil {
		readHealth = g.store.ModelHealth
	}
	healths := make(map[string]ModelHealth, len(candidates))
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
		h, err := readHealth(c.Harness, c.Model)
		if err != nil {
			return model.Facts{}, nil, err
		}
		healths[key] = h
		if h.Cooling {
			facts.Cooling[key] = model.CoolingFact{Failures: h.Failures, Until: h.Until}
		}
	}
	return facts, healths, nil
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

// feedGateSkips records the refusals the gate made that the owner's feeds know.
func (g *Gateway) feedGateSkips(skips []model.Skip) {
	for _, skip := range skips {
		switch skip.Check {
		case "floor":
			g.quotaFloorFeed(skip.Candidate.Harness, skip.Candidate.Model, skip.Reason)
		case "exhausted":
			g.quotaStuckFeed(skip.Candidate.Harness, skip.Candidate.Model, skip.Reason)
		}
	}
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
