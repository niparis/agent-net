package gateway

import (
	"strings"

	"github.com/niparis/agent-net/internal/model"
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
	floors, err := g.store.workerFloors()
	if err != nil {
		return out, err
	}
	quotas := g.ownerQuotas(nodeID)
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
	quotas = g.withQuotaBurn(quotas, agents)
	chosen, err := g.Choose(ChooseRequest{
		Purpose:    model.PurposeDifficultyJudge,
		Node:       node,
		Candidates: filtered,
		Difficulty: "normal",
		D:          protocol.PickerD(nil, "normal"),
		Scores:     scores,
		Minimums:   minimums,
		Quotas:     quotas,
		Running:    running,
		Protected:  protected,
		Floors:     floors,
	})
	if err != nil {
		// No judge model can run now is not a judge error: the task starts at
		// normal with the reason.
		return out, nil
	}
	out.Harness, out.Model = chosen.Choice.Harness, chosen.Choice.Model
	out.Provider = protocol.ProfileProvider(chosen.Choice.Harness, chosen.Choice.Model)
	return out, nil
}
