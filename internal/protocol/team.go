package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Team is an additive grouping identity. It grants no authority or settings.
type Team struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Purpose   string    `json:"purpose"`
	Owner     *GraphRef `json:"owner,omitempty"`
	Active    bool      `json:"active"`
	CreatedAt int64     `json:"created_at"`
	UpdatedAt int64     `json:"updated_at"`
}

// TeamWrite is owner-only. Membership ID is the new Team, empty to remove.
// ExpectTeam is the old Team ID, empty for an unassigned member. Moves are
// atomic. Rename requires ExpectName. Ownership is set at creation only.
type TeamWrite struct {
	Action       string    `json:"action"`
	ID           string    `json:"team_id"`
	Name         string    `json:"name,omitempty"`
	Purpose      string    `json:"purpose,omitempty"`
	Owner        *GraphRef `json:"owner,omitempty"`
	Member       *GraphRef `json:"member,omitempty"`
	ExpectTeam   string    `json:"expect_team,omitempty"`
	ExpectName   string    `json:"expect_name,omitempty"`
	OwnerSession string    `json:"owner_session,omitempty"`
}

// TeamChange retains immutable before/after JSON snapshots or Team IDs for a
// membership change. A move is one row, visible in both Teams' histories.
type TeamChange struct {
	ID        int64     `json:"id"`
	Action    string    `json:"action"`
	TeamID    string    `json:"team_id"`
	OldTeamID string    `json:"old_team_id,omitempty"`
	Member    *GraphRef `json:"member,omitempty"`
	OldValue  string    `json:"old_value"`
	NewValue  string    `json:"new_value"`
	At        int64     `json:"at"`
	By        string    `json:"by"`
}

// TeamLookup resolves active teams and current member associations.
type TeamLookup interface {
	Team(selector string) (id, name string, err error)  // active team by id or name
	MemberTeam(ref GraphRef) (teamID string, err error) // current team of a member, "" if none
}

// ResolveTeamAction parses and resolves CTO/operator team action arguments into a TeamWrite.
func ResolveTeamAction(action string, raw json.RawMessage, look TeamLookup) (TeamWrite, error) {
	var fields map[string]json.RawMessage
	if len(raw) > 0 && json.Unmarshal(raw, &fields) != nil {
		return TeamWrite{}, errors.New("bad team action arguments")
	}
	str := func(key string) string {
		var s string
		_ = json.Unmarshal(fields[key], &s)
		return strings.TrimSpace(s)
	}
	graphMember := func() (*GraphRef, error) {
		var ref GraphRef
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
			return TeamWrite{}, errors.New("team name required")
		}
		return TeamWrite{
			Action:  "create",
			ID:      name,
			Name:    name,
			Purpose: str("purpose"),
		}, nil

	case "team_rename":
		teamSel := str("team")
		newName := str("name")
		if teamSel == "" || newName == "" {
			return TeamWrite{}, errors.New("team and new name required")
		}
		if look == nil {
			return TeamWrite{}, errors.New("team lookup required")
		}
		id, expectName, err := look.Team(teamSel)
		if err != nil {
			return TeamWrite{}, err
		}
		return TeamWrite{
			Action:     "rename",
			ID:         id,
			Name:       newName,
			ExpectName: expectName,
		}, nil

	case "team_archive":
		teamSel := str("team")
		if teamSel == "" {
			return TeamWrite{}, errors.New("team required")
		}
		if look == nil {
			return TeamWrite{}, errors.New("team lookup required")
		}
		id, _, err := look.Team(teamSel)
		if err != nil {
			return TeamWrite{}, err
		}
		return TeamWrite{
			Action: "archive",
			ID:     id,
		}, nil

	case "team_member_add":
		teamSel := str("team")
		if teamSel == "" {
			return TeamWrite{}, errors.New("team required")
		}
		member, err := graphMember()
		if err != nil {
			return TeamWrite{}, err
		}
		if look == nil {
			return TeamWrite{}, errors.New("team lookup required")
		}
		id, _, err := look.Team(teamSel)
		if err != nil {
			return TeamWrite{}, err
		}
		oldTeam, err := look.MemberTeam(*member)
		if err != nil {
			return TeamWrite{}, err
		}
		return TeamWrite{
			Action:     "membership",
			ID:         id,
			Member:     member,
			ExpectTeam: oldTeam,
		}, nil

	case "team_member_remove":
		teamSel := str("team")
		if teamSel == "" {
			return TeamWrite{}, errors.New("team required")
		}
		member, err := graphMember()
		if err != nil {
			return TeamWrite{}, err
		}
		if look == nil {
			return TeamWrite{}, errors.New("team lookup required")
		}
		id, _, err := look.Team(teamSel)
		if err != nil {
			return TeamWrite{}, err
		}
		oldTeam, err := look.MemberTeam(*member)
		if err != nil {
			return TeamWrite{}, err
		}
		if oldTeam != id {
			return TeamWrite{}, fmt.Errorf("member %s is not in team %s", member.ID, id)
		}
		return TeamWrite{
			Action:     "membership",
			ID:         "",
			Member:     member,
			ExpectTeam: oldTeam,
		}, nil

	default:
		return TeamWrite{}, fmt.Errorf("unknown team action %q", action)
	}
}
