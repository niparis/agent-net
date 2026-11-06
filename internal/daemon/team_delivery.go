package daemon

import (
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

type missedTeamMember struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// teamRecipients only uses typed graph identities, never announced project names.
func teamRecipients(graph protocol.Graph, selector string, sessions []protocol.AgentView) (map[string]bool, []missedTeamMember, error) {
	selector = strings.TrimSpace(selector)
	var team *protocol.GraphRef
	for _, n := range graph.Nodes {
		if n.Ref == nil || n.Ref.Kind != "team" || n.Active != nil && !*n.Active {
			continue
		}
		if strings.EqualFold(n.Ref.ID, selector) || strings.EqualFold(n.Name, selector) {
			if team != nil && *team != *n.Ref {
				return nil, nil, fmt.Errorf("ambiguous team %q", selector)
			}
			r := *n.Ref
			team = &r
		}
	}
	if team == nil {
		return nil, nil, fmt.Errorf("unknown team %q", selector)
	}
	owners := map[string]string{}
	names := map[protocol.GraphRef]string{}
	for _, n := range graph.Nodes {
		if n.Ref != nil {
			names[*n.Ref] = n.Name
		}
		if n.Ref != nil && n.Ref.Kind == "repo" && n.Owner != nil && n.Owner.Kind == "agent" {
			owners[n.Ref.ID] = n.Owner.ID
		}
	}
	ids := map[string]bool{}
	var missed []missedTeamMember
	for _, e := range graph.Edges {
		if e.Kind != "member_of" || e.FromRef == nil || e.ToRef == nil || *e.ToRef != *team {
			continue
		}
		member := *e.FromRef
		agent := member.ID
		if member.Kind == "repo" {
			agent = owners[member.ID]
		} else if member.Kind != "agent" {
			continue
		}
		found := false
		if agent != "" {
			for _, a := range sessions {
				if a.Online && a.AgentID == agent && a.SessionID != "" {
					ids[a.SessionID] = true
					found = true
				}
			}
		}
		if !found {
			reason := "no online session"
			if member.Kind == "repo" && agent == "" {
				reason = "repo has no linked agent"
			}
			missed = append(missed, missedTeamMember{Kind: member.Kind, ID: member.ID, Name: names[member], Reason: reason})
		}
	}
	return ids, missed, nil
}
