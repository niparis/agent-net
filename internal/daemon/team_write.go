package daemon

import (
	"context"
	"encoding/json"
	"errors"
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

func (d *Daemon) resolveTeamWrite(ctx context.Context, action string, raw json.RawMessage) (protocol.TeamWrite, error) {
	var fields map[string]json.RawMessage
	if len(raw) > 0 && json.Unmarshal(raw, &fields) != nil {
		return protocol.TeamWrite{}, errors.New("bad team action arguments")
	}
	str := func(key string) string {
		var s string
		_ = json.Unmarshal(fields[key], &s)
		return strings.TrimSpace(s)
	}
	graphMember := func() (*protocol.GraphRef, error) {
		var ref protocol.GraphRef
		if rawRef, ok := fields["member"]; ok && len(rawRef) > 0 && string(rawRef) != "null" {
			if err := json.Unmarshal(rawRef, &ref); err == nil && ref.Kind != "" && ref.ID != "" {
				return &ref, nil
			}
		}
		if rawRef, ok := fields["ref"]; ok && len(rawRef) > 0 && string(rawRef) != "null" {
			if err := json.Unmarshal(rawRef, &ref); err == nil && ref.Kind != "" && ref.ID != "" {
				return &ref, nil
			}
		}
		return nil, errors.New("team member ref required")
	}

	switch action {
	case "team_create":
		name := str("name")
		if name == "" {
			return protocol.TeamWrite{}, errors.New("team name required")
		}
		purpose := str("purpose")
		return protocol.TeamWrite{
			Action:  "create",
			ID:      name,
			Name:    name,
			Purpose: purpose,
		}, nil

	case "team_rename":
		teamSel := str("team")
		newName := str("name")
		if teamSel == "" || newName == "" {
			return protocol.TeamWrite{}, errors.New("team and new name required")
		}
		graph, err := d.fetchTypedGraph(ctx)
		if err != nil {
			return protocol.TeamWrite{}, err
		}
		teamNode, err := resolveTeamNode(graph, teamSel)
		if err != nil {
			return protocol.TeamWrite{}, err
		}
		return protocol.TeamWrite{
			Action:     "rename",
			ID:         teamNode.Ref.ID,
			Name:       newName,
			ExpectName: teamNode.Name,
		}, nil

	case "team_archive":
		teamSel := str("team")
		if teamSel == "" {
			return protocol.TeamWrite{}, errors.New("team required")
		}
		graph, err := d.fetchTypedGraph(ctx)
		if err != nil {
			return protocol.TeamWrite{}, err
		}
		teamNode, err := resolveTeamNode(graph, teamSel)
		if err != nil {
			return protocol.TeamWrite{}, err
		}
		return protocol.TeamWrite{
			Action: "archive",
			ID:     teamNode.Ref.ID,
		}, nil

	case "team_member_add":
		teamSel := str("team")
		if teamSel == "" {
			return protocol.TeamWrite{}, errors.New("team required")
		}
		member, err := graphMember()
		if err != nil {
			return protocol.TeamWrite{}, err
		}
		graph, err := d.fetchTypedGraph(ctx)
		if err != nil {
			return protocol.TeamWrite{}, err
		}
		teamNode, err := resolveTeamNode(graph, teamSel)
		if err != nil {
			return protocol.TeamWrite{}, err
		}
		oldTeam := memberCurrentTeam(graph, *member)
		return protocol.TeamWrite{
			Action:     "membership",
			ID:         teamNode.Ref.ID,
			Member:     member,
			ExpectTeam: oldTeam,
		}, nil

	case "team_member_remove":
		teamSel := str("team")
		if teamSel == "" {
			return protocol.TeamWrite{}, errors.New("team required")
		}
		member, err := graphMember()
		if err != nil {
			return protocol.TeamWrite{}, err
		}
		graph, err := d.fetchTypedGraph(ctx)
		if err != nil {
			return protocol.TeamWrite{}, err
		}
		teamNode, err := resolveTeamNode(graph, teamSel)
		if err != nil {
			return protocol.TeamWrite{}, err
		}
		oldTeam := memberCurrentTeam(graph, *member)
		if oldTeam != teamNode.Ref.ID {
			return protocol.TeamWrite{}, fmt.Errorf("member %s is not in team %s", member.ID, teamNode.Ref.ID)
		}
		return protocol.TeamWrite{
			Action:     "membership",
			ID:         "",
			Member:     member,
			ExpectTeam: oldTeam,
		}, nil

	default:
		return protocol.TeamWrite{}, fmt.Errorf("unknown team action %q", action)
	}
}
