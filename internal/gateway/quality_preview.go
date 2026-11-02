package gateway

import (
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// qualityPreview uses the machine's installed roster, without a project filter.
func (g *Gateway) qualityPreview(node protocol.NodeView) (map[string]string, map[string]string, map[string]string, error) {
	if !node.Online || node.Launch == nil || !*node.Launch {
		return nil, nil, nil, nil
	}
	profiles, err := g.store.WorkerProfiles()
	if err != nil {
		return nil, nil, nil, err
	}
	providers, err := g.store.workerProviders()
	if err != nil {
		return nil, nil, nil, err
	}
	protected, err := g.store.protectedProviders()
	if err != nil {
		return nil, nil, nil, err
	}
	scores, err := g.store.scores()
	if err != nil {
		return nil, nil, nil, err
	}
	minimums, err := g.store.difficultyMinimums()
	if err != nil {
		return nil, nil, nil, err
	}
	agents, err := g.store.Agents(protocol.QueryAgents{OnlineOnly: true})
	if err != nil {
		return nil, nil, nil, err
	}
	running := map[string]int{}
	for _, a := range agents {
		if a.NodeID == node.NodeID && a.Worker != nil {
			running[protocol.QuotaProviderForStart(a.Harness, a.Model)]++
		}
	}
	candidates, _, _, err := g.workerModelCandidates(&node, profiles, providers, node.Quotas, "", false, protected)
	if err != nil {
		return nil, nil, nil, err
	}
	out := map[string]string{}
	reasons := map[string]string{}
	models := map[string]string{}
	for _, level := range protocol.DifficultyLevels {
		levelCandidates := append([]protocol.WorkerModelCandidate(nil), candidates...)
		choice, _, err := g.chooseAutomaticWorker(levelCandidates, level, scores, minimums, node.Quotas, running, protected, false)
		if err == nil {
			out[level] = qualityModelFamily(choice.Model)
			models[level] = qualityModelKey(choice.Model)
			if reason := qualityPaceClause(choice.QuotaNote); reason != "" {
				reasons[level] = reason
			}
		}
	}
	return out, reasons, models, nil
}

// qualityPaceClause preserves the chooser's concise pace description.
func qualityPaceClause(note string) string {
	clause, _, _ := strings.Cut(note, " (")
	if strings.Contains(clause, " ahead of pace") || strings.Contains(clause, " behind pace") {
		return clause
	}
	return ""
}
