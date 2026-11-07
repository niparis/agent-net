package protocol

import (
	"encoding/json"
	"errors"
	"testing"
)

type fakeTeamLookup struct {
	teams   map[string]struct{ id, name string }
	members map[GraphRef]string
}

func (f *fakeTeamLookup) Team(selector string) (string, string, error) {
	if t, ok := f.teams[selector]; ok {
		return t.id, t.name, nil
	}
	return "", "", errors.New("unknown team")
}

func (f *fakeTeamLookup) MemberTeam(ref GraphRef) (string, error) {
	if teamID, ok := f.members[ref]; ok {
		return teamID, nil
	}
	return "", nil
}

func TestResolveTeamAction(t *testing.T) {
	look := &fakeTeamLookup{
		teams: map[string]struct{ id, name string }{
			"team-alpha": {id: "t_alpha", name: "team-alpha"},
			"t_alpha":    {id: "t_alpha", name: "team-alpha"},
			"team-beta":  {id: "t_beta", name: "team-beta"},
		},
		members: map[GraphRef]string{
			{Kind: "agent", ID: "worker-1"}: "t_alpha",
			{Kind: "repo", ID: "repo-1"}:    "t_beta",
		},
	}

	// 1. team_create
	tw, err := ResolveTeamAction("team_create", json.RawMessage(`{"name":"ops","purpose":"Operations"}`), look)
	if err != nil {
		t.Fatalf("team_create: %v", err)
	}
	if tw.Action != "create" || tw.ID != "ops" || tw.Name != "ops" || tw.Purpose != "Operations" {
		t.Fatalf("unexpected team_create: %+v", tw)
	}

	// team_create with nil look should also succeed
	if _, err := ResolveTeamAction("team_create", json.RawMessage(`{"name":"ops"}`), nil); err != nil {
		t.Fatalf("team_create nil look: %v", err)
	}

	// team_create missing name
	if _, err := ResolveTeamAction("team_create", json.RawMessage(`{}`), look); err == nil {
		t.Fatal("expected error on missing name")
	}

	// 2. team_rename
	tw, err = ResolveTeamAction("team_rename", json.RawMessage(`{"team":"team-alpha","name":"ops-core"}`), look)
	if err != nil {
		t.Fatalf("team_rename: %v", err)
	}
	if tw.Action != "rename" || tw.ID != "t_alpha" || tw.Name != "ops-core" || tw.ExpectName != "team-alpha" {
		t.Fatalf("unexpected team_rename: %+v", tw)
	}
	// team_rename unknown team
	if _, err := ResolveTeamAction("team_rename", json.RawMessage(`{"team":"unknown","name":"ops"}`), look); err == nil {
		t.Fatal("expected error on unknown team")
	}
	// team_rename missing new name
	if _, err := ResolveTeamAction("team_rename", json.RawMessage(`{"team":"team-alpha"}`), look); err == nil {
		t.Fatal("expected error on missing name")
	}

	// 3. team_archive
	tw, err = ResolveTeamAction("team_archive", json.RawMessage(`{"team":"team-alpha"}`), look)
	if err != nil {
		t.Fatalf("team_archive: %v", err)
	}
	if tw.Action != "archive" || tw.ID != "t_alpha" {
		t.Fatalf("unexpected team_archive: %+v", tw)
	}
	// team_archive missing team
	if _, err := ResolveTeamAction("team_archive", json.RawMessage(`{}`), look); err == nil {
		t.Fatal("expected error on missing team")
	}

	// 4. team_member_add
	// Adding member not currently in a team (ExpectTeam == "")
	tw, err = ResolveTeamAction("team_member_add", json.RawMessage(`{"team":"team-alpha","member":{"kind":"agent","id":"worker-2"}}`), look)
	if err != nil {
		t.Fatalf("team_member_add: %v", err)
	}
	if tw.Action != "membership" || tw.ID != "t_alpha" || tw.Member == nil || tw.Member.Kind != "agent" || tw.Member.ID != "worker-2" || tw.ExpectTeam != "" {
		t.Fatalf("unexpected team_member_add: %+v", tw)
	}
	// Adding member currently in another team (ExpectTeam == "t_beta") using "ref" alias
	tw, err = ResolveTeamAction("team_member_add", json.RawMessage(`{"team":"team-alpha","ref":{"kind":"repo","id":"repo-1"}}`), look)
	if err != nil {
		t.Fatalf("team_member_add with ref: %v", err)
	}
	if tw.Action != "membership" || tw.ID != "t_alpha" || tw.ExpectTeam != "t_beta" {
		t.Fatalf("unexpected team_member_add move: %+v", tw)
	}
	// team_member_add missing member
	if _, err := ResolveTeamAction("team_member_add", json.RawMessage(`{"team":"team-alpha"}`), look); err == nil {
		t.Fatal("expected error on missing member")
	}

	// 5. team_member_remove
	// Removing member who is in team
	tw, err = ResolveTeamAction("team_member_remove", json.RawMessage(`{"team":"team-alpha","member":{"kind":"agent","id":"worker-1"}}`), look)
	if err != nil {
		t.Fatalf("team_member_remove: %v", err)
	}
	if tw.Action != "membership" || tw.ID != "" || tw.Member.ID != "worker-1" || tw.ExpectTeam != "t_alpha" {
		t.Fatalf("unexpected team_member_remove: %+v", tw)
	}
	// Removing member who is NOT in team
	if _, err := ResolveTeamAction("team_member_remove", json.RawMessage(`{"team":"team-beta","member":{"kind":"agent","id":"worker-1"}}`), look); err == nil {
		t.Fatal("expected error removing member from wrong team")
	}

	// 6. Unknown action
	if _, err := ResolveTeamAction("team_destroy", json.RawMessage(`{"team":"team-alpha"}`), look); err == nil {
		t.Fatal("expected error on unknown action")
	}
}

func TestTeamRefAndPublicRead(t *testing.T) {
	ref := GraphRef{Kind: "team", ID: "alpha"}
	if err := ref.Validate(); err != nil {
		t.Fatal(err)
	}
	active := false
	g := Graph{Nodes: []Node{{Ref: &ref, Name: "Alpha", Kind: "team", Active: &active, Charter: Charter{Purpose: "Shared work"}}}}
	page := PublicGraph(g, PublicGraphQuery{Ref: &ref})
	if len(page.Nodes) != 1 || len(page.Nodes[0].Rules) != 0 || page.Nodes[0].Active == nil || *page.Nodes[0].Active {
		t.Fatalf("team read: %+v", page)
	}
}
