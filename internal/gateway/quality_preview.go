package gateway

import (
	"encoding/json"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// qualityPreviewInputs holds what every node's preview reads from the store.
// NodesInfo reads it once per call instead of once per node.
type qualityPreviewInputs struct {
	profiles  []protocol.WorkerProfile
	providers protocol.WorkerProviders
	protected protocol.ProtectedProviders
	scores    []protocol.ModelScore
	minimums  map[string]float64
	// running counts the online workers by quota provider, per node.
	running map[string]map[string]int
	// healths holds model health by harness and model. It does not depend on
	// the node or the difficulty level, so one read serves the whole call.
	healths map[[2]string]ModelHealth
}

func (g *Gateway) qualityPreviewInputs() (qualityPreviewInputs, error) {
	in := qualityPreviewInputs{healths: map[[2]string]ModelHealth{}}
	var err error
	if in.profiles, err = g.store.WorkerProfiles(); err != nil {
		return in, err
	}
	if in.providers, err = g.store.workerProviders(); err != nil {
		return in, err
	}
	if in.protected, err = g.store.protectedProviders(); err != nil {
		return in, err
	}
	if in.scores, err = g.store.scores(); err != nil {
		return in, err
	}
	if in.minimums, err = g.store.difficultyMinimums(); err != nil {
		return in, err
	}
	if in.running, err = g.store.onlineWorkerCounts(); err != nil {
		return in, err
	}
	return in, nil
}

// qualityPreviewWith previews the pick for every difficulty level. Model
// health does not depend on the level, so it is read once per model.
func (g *Gateway) qualityPreviewWith(in qualityPreviewInputs, node protocol.NodeView) (map[string]string, map[string]string, map[string]string, error) {
	if !node.Online || node.Launch == nil || !*node.Launch {
		return nil, nil, nil, nil
	}
	running := in.running[node.NodeID]
	if running == nil {
		running = map[string]int{}
	}
	candidates, _, _, err := g.workerModelCandidates(&node, in.profiles, in.providers, node.Quotas, "", false, in.protected)
	if err != nil {
		return nil, nil, nil, err
	}
	healths := in.healths
	health := func(harness, model string) (ModelHealth, error) {
		key := [2]string{harness, model}
		if h, ok := healths[key]; ok {
			return h, nil
		}
		h, err := g.store.ModelHealth(harness, model)
		if err == nil {
			healths[key] = h
		}
		return h, err
	}
	out := map[string]string{}
	reasons := map[string]string{}
	models := map[string]string{}
	for _, level := range protocol.DifficultyLevels {
		levelCandidates := append([]protocol.WorkerModelCandidate(nil), candidates...)
		choice, _, err := g.chooseAutomaticWorkerWith(health, levelCandidates, level, in.scores, in.minimums, node.Quotas, running, in.protected, false)
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

// onlineWorkerCounts counts the online workers by node and quota provider. It
// reads the same sessions as Agents(OnlineOnly) with a worker, without the
// delegation and charter work a full agent view needs.
func (s *Store) onlineWorkerCounts() (map[string]map[string]int, error) {
	rows, err := s.db.Query(`SELECT s.node_id, s.harness, s.model, s.worker
		FROM sessions s LEFT JOIN nodes n ON n.id = s.node_id
		WHERE s.archived_at=0 AND n.id IS NOT NULL AND s.online=1 AND s.worker<>'' AND s.harness NOT IN ('bot','owner')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]int{}
	for rows.Next() {
		var node, harness, model, worker string
		if err := rows.Scan(&node, &harness, &model, &worker); err != nil {
			return nil, err
		}
		var w protocol.WorkerInfo
		if json.Unmarshal([]byte(worker), &w) != nil {
			continue
		}
		if out[node] == nil {
			out[node] = map[string]int{}
		}
		out[node][protocol.QuotaProviderForStart(harness, model)]++
	}
	return out, rows.Err()
}
