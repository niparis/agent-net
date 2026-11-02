package gateway

import "github.com/niparis/agent-net/internal/protocol"

// previewQualityChoices advances only a private copy for each difficulty.
func previewQualityChoices(candidates []protocol.WorkerModelCandidate, scores []protocol.ModelScore, minimums map[string]float64, quotas *protocol.Quotas, running map[string]int, rotation protocol.WorkerRotation, protected protocol.ProtectedProviders) map[string]string {
	out := map[string]string{}
	for _, level := range protocol.DifficultyLevels {
		copy := rotation.Clone()
		choice, _, err := chooseProtectedWorker(candidates, level, scores, minimums, quotas, running, &copy, protected)
		if err == nil {
			out[level] = qualityModelFamily(choice.Model)
		}
	}
	return out
}

// qualityPreview uses the machine's installed roster, without a project filter.
func (g *Gateway) qualityPreview(node protocol.NodeView) (map[string]string, error) {
	if !node.Online || node.Launch == nil || !*node.Launch {
		return nil, nil
	}
	profiles, err := g.store.WorkerProfiles()
	if err != nil {
		return nil, err
	}
	providers, err := g.store.workerProviders()
	if err != nil {
		return nil, err
	}
	protected, err := g.store.protectedProviders()
	if err != nil {
		return nil, err
	}
	scores, err := g.store.scores()
	if err != nil {
		return nil, err
	}
	minimums, err := g.store.difficultyMinimums()
	if err != nil {
		return nil, err
	}
	agents, err := g.store.Agents(protocol.QueryAgents{OnlineOnly: true})
	if err != nil {
		return nil, err
	}
	running := map[string]int{}
	for _, a := range agents {
		if a.NodeID == node.NodeID && a.Worker != nil {
			running[protocol.QuotaProviderForStart(a.Harness, a.Model)]++
		}
	}
	tracks, err := g.store.TrackRecords(node.NodeID, 0)
	if err != nil {
		return nil, err
	}
	byTrack := map[string]protocol.TrackRecord{}
	for _, t := range tracks {
		byTrack[t.Harness+"\x00"+t.Model] = t
	}
	var candidates []protocol.WorkerModelCandidate
	for _, h := range node.Harnesses {
		if !protocol.WorkerHarnessSupported(h.Name) {
			continue
		}
		failures, err := g.store.RecentWorkerFailures(node.NodeID, h.Name)
		if err != nil {
			return nil, err
		}
		successes, err := g.store.SucceededModels(node.NodeID, h.Name)
		if err != nil {
			return nil, err
		}
		for _, p := range profiles {
			if p.Harness != h.Name {
				continue
			}
			resolved, _ := protocol.ResolveWorkerProfileModel(p.Model, h.Models)
			if resolved == "" || !protocol.WorkerProviderAllowed(h.Name, resolved, providers.Providers, node.Quotas) {
				continue
			}
			model := resolved
			if p.Effort != "" {
				model += ":" + p.Effort
			}
			candidates = append(candidates, protocol.WorkerModelCandidate{Name: p.Name, Harness: h.Name, Model: model, DefaultEffort: h.ModelDefaultEfforts[resolved], Failed: failures[model], Succeeded: !successes[model].IsZero(), Track: byTrack[h.Name+"\x00"+model]})
		}
	}
	g.rotationMu.Lock()
	rotation, err := g.store.WorkerRotation()
	g.rotationMu.Unlock()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, level := range protocol.DifficultyLevels {
		levelCandidates := append([]protocol.WorkerModelCandidate(nil), candidates...)
		if err := g.store.applyQualityWeights(levelCandidates, level); err != nil {
			return nil, err
		}
		if pick := previewQualityChoices(levelCandidates, scores, minimums, node.Quotas, running, rotation, protected)[level]; pick != "" {
			out[level] = pick
		}
	}
	return out, nil
}
