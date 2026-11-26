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
	in, err := g.pickInputs(node, "")
	if err != nil {
		return out, err
	}
	candidates, _, _, err := in.candidates(g, &candidateFacts{}, nil, node)
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
	chosen, err := g.Choose(ChooseRequest{
		Purpose:    model.PurposeDifficultyJudge,
		Node:       node,
		Candidates: filtered,
		Difficulty: "normal",
		D:          protocol.PickerD(nil, "normal"),
		Scores:     in.scores,
		Minimums:   in.minimums,
		Quotas:     in.quotas,
		Running:    in.running,
		Protected:  in.protected,
		Floors:     in.floors,
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
