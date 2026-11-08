package gateway

import (
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// pickDifficultyJudge uses exactly the worker candidate builder and chooser,
// including their shared gateway rotation. The connection supplies the node;
// a daemon cannot ask the gateway to spend another machine's quota.
func (g *Gateway) pickDifficultyJudge(nodeID string, req protocol.DifficultyJudgePick) (protocol.DifficultyJudgeChoice, error) {
	var out protocol.DifficultyJudgeChoice
	nodes, err := g.store.Nodes()
	if err != nil {
		return out, err
	}
	var node *protocol.NodeView
	for i := range nodes {
		if nodes[i].NodeID == nodeID {
			node = &nodes[i]
			break
		}
	}
	if node == nil {
		return out, reject("unknown node")
	}
	out.Machine = node.Hostname
	profiles, err := g.store.WorkerProfiles()
	if err != nil {
		return out, err
	}
	providers, err := g.store.workerProviders()
	if err != nil {
		return out, err
	}
	protected, err := g.store.protectedProviders()
	if err != nil {
		return out, err
	}
	quotas := g.nodeQuotas(nodeID)
	candidates, _, _, err := g.workerModelCandidates(node, profiles, providers, quotas, "", false, protected)
	if err != nil {
		return out, err
	}
	filtered := candidates[:0]
	for _, c := range candidates {
		if req.ExcludeProvider != "" && strings.EqualFold(req.ExcludeProvider, protocol.ProfileProvider(c.Harness, c.Model)) {
			continue
		}
		filtered = append(filtered, c)
	}
	scores, err := g.store.scores()
	if err != nil {
		return out, err
	}
	minimums, err := g.store.difficultyMinimums()
	if err != nil {
		return out, err
	}
	agents, err := g.store.Agents(protocol.QueryAgents{OnlineOnly: true})
	if err != nil {
		return out, err
	}
	running := map[string]int{}
	for _, a := range agents {
		if a.NodeID == nodeID && a.Worker != nil {
			running[protocol.QuotaProviderForStart(a.Harness, a.Model)]++
		}
	}
	// Distinguish an empty eligibility set from a storage error when persisting
	// the rotation. The preview uses the same protection and quota rules.
	pick := &protocol.PickerInput{D: protocol.PickerD(nil, "normal"), Table: g.store.pickerTable()}
	if _, _, err := chooseProtectedWorker(filtered, "normal", scores, minimums, quotas, running, nil, protected, pick); err != nil {
		return out, nil
	}
	choice, _, err := g.chooseAutomaticWorker(filtered, "normal", scores, minimums, quotas, running, protected, true)
	if err != nil {
		return out, err
	}
	out.Harness, out.Model = choice.Harness, choice.Model
	out.Provider = protocol.ProfileProvider(choice.Harness, choice.Model)
	return out, nil
}
