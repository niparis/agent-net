package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

func (d *Daemon) fetchTypedGraph(ctx context.Context) (protocol.Graph, error) {
	raw, err := d.call(ctx, "graph.read", protocol.GraphRead{Typed: true})
	if err != nil {
		return protocol.Graph{}, err
	}
	var res protocol.GraphGot
	if err := json.Unmarshal(raw, &res); err != nil {
		return protocol.Graph{}, err
	}
	return res.Graph, nil
}

func resolveTeamNode(graph protocol.Graph, selector string) (*protocol.Node, error) {
	selector = strings.TrimSpace(selector)
	var found *protocol.Node
	for i := range graph.Nodes {
		n := &graph.Nodes[i]
		if n.Ref == nil || n.Ref.Kind != "team" || (n.Active != nil && !*n.Active) {
			continue
		}
		if strings.EqualFold(n.Ref.ID, selector) || strings.EqualFold(n.Name, selector) {
			if found != nil && *found.Ref != *n.Ref {
				return nil, fmt.Errorf("ambiguous team %q", selector)
			}
			found = n
		}
	}
	if found == nil {
		return nil, fmt.Errorf("unknown team %q", selector)
	}
	return found, nil
}

func memberCurrentTeam(graph protocol.Graph, ref protocol.GraphRef) string {
	for _, e := range graph.Edges {
		if e.Kind == "member_of" && e.FromRef != nil && e.ToRef != nil {
			if e.FromRef.Kind == ref.Kind && e.FromRef.ID == ref.ID && e.ToRef.Kind == "team" {
				return e.ToRef.ID
			}
		}
	}
	return ""
}

type graphTeamLookup struct {
	graph protocol.Graph
}

func (l graphTeamLookup) Team(selector string) (id, name string, err error) {
	node, err := resolveTeamNode(l.graph, selector)
	if err != nil {
		return "", "", err
	}
	return node.Ref.ID, node.Name, nil
}

func (l graphTeamLookup) MemberTeam(ref protocol.GraphRef) (teamID string, err error) {
	return memberCurrentTeam(l.graph, ref), nil
}

func (d *Daemon) resolveTeamWrite(ctx context.Context, action string, raw json.RawMessage) (protocol.TeamWrite, error) {
	if action == "team_create" {
		return protocol.ResolveTeamAction(action, raw, nil)
	}
	graph, err := d.fetchTypedGraph(ctx)
	if err != nil {
		return protocol.TeamWrite{}, err
	}
	return protocol.ResolveTeamAction(action, raw, graphTeamLookup{graph: graph})
}
