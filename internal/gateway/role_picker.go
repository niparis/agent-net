package gateway

import (
	"strings"

	"github.com/niparis/agent-net/internal/model"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// pickRole asks the one gate for a role start: the CTO, the chief of staff or
// the merger (decision 0162). A role's ordered list is a setting, except the
// CTO's, which is local daemon state and arrives in the request. Roles get the
// locked checks and cooling only, so a failing model falls to the next entry
// and the system roles do not use the worker's quota protections.
func (g *Gateway) pickRole(nodeID string, req protocol.RolePickRequest) (protocol.RolePickResult, error) {
	out := protocol.RolePickResult{}
	if req.NodeID != "" {
		nodeID = req.NodeID
	}
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
	list, err := g.roleModels(req)
	if err != nil {
		return out, err
	}
	if len(list) == 0 {
		out.Refused = "the " + req.Role + " model list is empty"
		return out, nil
	}
	// A role resumes on the model it already runs. The rest of the list
	// follows, so a locked check moves it to a new conversation on the next
	// entry instead of refusing the role.
	candidates := make([]protocol.RoleModel, 0, len(list)+1)
	if req.Resume && !req.Current.IsZero() {
		candidates = append(candidates, req.Current)
	}
	for _, m := range list {
		if req.Resume && !req.Current.IsZero() && m.Key() == req.Current.Key() {
			continue
		}
		candidates = append(candidates, m)
	}
	agents, err := g.store.Agents(protocol.QueryAgents{OnlineOnly: true})
	if err != nil {
		return out, err
	}
	quotas := g.withQuotaBurn(g.ownerQuotas(nodeID), agents)
	floors, err := g.store.workerFloors()
	if err != nil {
		return out, err
	}
	chosen, err := g.Choose(ChooseRequest{
		Purpose:    model.PurposeRole,
		Node:       node,
		Candidates: roleCandidates(candidates),
		Difficulty: "normal",
		Quotas:     quotas,
		Floors:     floors,
	})
	if err != nil {
		out.Refused = oneRoleLine(joinModelNote(skipNotes(chosen.Skips)))
		if out.Refused == "" {
			out.Refused = oneRoleLine(err.Error())
		}
		return out, nil
	}
	granted := chosen.Granted.Candidate()
	out.Harness, out.Model, out.Effort = granted.Harness, granted.Model, granted.Effort
	// The list's provider is a name for the owner, not the quota provider the
	// gate checks (codex draws on openai, claude on its own window).
	for _, m := range candidates {
		if m.Key() == granted.Key() {
			out.Provider = m.Provider
			break
		}
	}
	out.Note = chosen.Note
	out.NewConversation = req.Resume && granted.Key() != req.Current.Key()
	return out, nil
}

// roleModels is the ordered list a role start decides on.
func (g *Gateway) roleModels(req protocol.RolePickRequest) ([]protocol.RoleModel, error) {
	if len(req.Candidates) > 0 {
		return req.Candidates, nil
	}
	key := protocol.RoleModelsKey(req.Role)
	if key == "" {
		return nil, reject("unknown role %q", req.Role)
	}
	snap, err := g.store.SettingsSnapshot()
	if err != nil {
		return nil, err
	}
	list, err := protocol.ParseRoleModels(snap.String(key, settings.Where{}))
	if err != nil {
		return nil, reject("%s: %v", key, err)
	}
	return list, nil
}

// roleCandidates turns a role list into the worker candidates the ranker and
// the gate use.
func roleCandidates(list []protocol.RoleModel) []protocol.WorkerModelCandidate {
	out := make([]protocol.WorkerModelCandidate, 0, len(list))
	for _, m := range list {
		out = append(out, protocol.WorkerModelCandidate{Name: m.Display(), Harness: m.Harness, Model: m.Model, Effort: m.Effort})
	}
	return out
}

// oneRoleLine keeps a refusal on one line: the TUI shows it as one reason,
// not a block.
func oneRoleLine(text string) string {
	text = strings.Join(strings.Fields(strings.ReplaceAll(text, "\n", " ")), " ")
	return text
}
