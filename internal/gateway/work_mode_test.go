package gateway

import (
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func workModeOf(t *testing.T, s *Store, repo, agent string) (string, string) {
	t.Helper()
	c, err := s.EffectiveCharter(repo, agent, "")
	if err != nil {
		t.Fatal(err)
	}
	where := ""
	if c.Sources != nil {
		if list := (*c.Sources)["work_mode"]; len(list) > 0 {
			where = protocol.WorkModeSourceText(list[len(list)-1])
		}
	}
	return c.WorkMode, where
}

func TestWorkModeInheritsMostSpecificLevelWins(t *testing.T) {
	s := teamStore(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "forge", Name: "Forge", Purpose: "Team purpose"})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "forge", Member: &protocol.GraphRef{Kind: "repo", ID: "demo"}})
	if mode, _ := workModeOf(t, s, "demo", "demo"); mode != "" {
		t.Fatalf("default is not empty: %q", mode)
	}
	set := func(level, node, value string) {
		t.Helper()
		if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: level, Node: node, Attr: "work_mode", Value: value}, "owner", false); err != nil {
			t.Fatalf("%s: %v", level, err)
		}
	}
	set("global", "", "on_request")
	if mode, where := workModeOf(t, s, "demo", "demo"); mode != "on_request" || where != "global" {
		t.Fatalf("global: %q %q", mode, where)
	}
	set("team", "forge", "workers_code")
	if mode, where := workModeOf(t, s, "demo", "demo"); mode != "workers_code" || where != "team forge" {
		t.Fatalf("team: %q %q", mode, where)
	}
	set("repo", "demo", "on_request")
	if mode, where := workModeOf(t, s, "demo", "demo"); mode != "on_request" || where != "repo demo" {
		t.Fatalf("repo: %q %q", mode, where)
	}
	set("agent", "demo", "workers_code")
	if mode, where := workModeOf(t, s, "demo", "demo"); mode != "workers_code" || where != "agent demo" {
		t.Fatalf("agent: %q %q", mode, where)
	}
	set("agent", "demo", "")
	if mode, where := workModeOf(t, s, "demo", "demo"); mode != "on_request" || where != "repo demo" {
		t.Fatalf("cleared agent: %q %q", mode, where)
	}
}

func TestWorkModeRejectsBadValue(t *testing.T) {
	s := teamStore(t)
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "global", Attr: "work_mode", Value: "always"}, "owner", false); err == nil {
		t.Fatal("accepted a bad value")
	}
	if err := s.SetNodeSetting("demo", "work_mode", "always", "owner"); err == nil {
		t.Fatal("node_settings_set accepted a bad value")
	}
}

func TestWorkModeCTOSetsWithoutAProposal(t *testing.T) {
	s := teamStore(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "forge", Name: "Forge", Purpose: "Team purpose"})
	for tool, raw := range map[string]string{
		"node_settings_set": `{"node":"demo","setting":"work_mode","value":"workers_code"}`,
	} {
		class, err := s.CTOGraphClass(tool, json.RawMessage(raw))
		if err != nil || class != "minor" {
			t.Fatalf("%s class %q %v", tool, class, err)
		}
	}
	// The CTO's own actor may write the team level and the repo level.
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "team", Node: "forge", Attr: "work_mode", Value: "workers_code"}, "CTO (minor)", false); err != nil {
		t.Fatalf("team write by CTO: %v", err)
	}
	if err := s.SetNodeSetting("demo", "work_mode", "workers_code", "CTO (minor)"); err != nil {
		t.Fatalf("node_settings_set by CTO: %v", err)
	}
	if mode, where := workModeOf(t, s, "demo", ""); mode != "workers_code" || where != "repo demo" {
		t.Fatalf("after node_settings_set: %q %q", mode, where)
	}
	// Another inherited attribute still needs the owner.
	if err := s.SetLevelCharter(protocol.CharterSetRequest{Level: "team", Node: "forge", Attr: "rules", Value: "x"}, "CTO (minor)", false); err == nil {
		t.Fatal("rules at team level no longer needs the owner")
	}
}
