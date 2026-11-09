package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCTOTeamCharterSchemaAcceptsWorkMode(t *testing.T) {
	raw := json.RawMessage(`{"action":"team_charter","team":"forge","attr":"work_mode","value":"workers_code"}`)
	if err := ValidateCTOActionSchema("graph_change", raw); err != nil {
		t.Fatalf("graph_change team_charter work_mode: %v", err)
	}
	if err := CTOArguments("team_charter", json.RawMessage(`{"team":"forge","attr":"work_mode","value":"workers_code"}`)); err != nil {
		t.Fatalf("team_charter work_mode: %v", err)
	}
}

func TestCTOTeamSettingsSetSchema(t *testing.T) {
	for _, setting := range []string{"asks_to", "work_mode"} {
		raw, err := json.Marshal(map[string]any{
			"action": "settings_set", "ref": map[string]string{"kind": "team", "id": "forge"},
			"setting": setting, "value": "lead",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateCTOActionSchema("graph_change", raw); err != nil {
			t.Errorf("settings_set team %s: %v", setting, err)
		}
	}
	raw, err := json.Marshal(map[string]any{
		"action": "settings_set", "ref": map[string]string{"kind": "team", "id": "forge"},
		"setting": "autonomy", "value": "wake",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = ValidateCTOActionSchema("graph_change", raw)
	if err == nil || !strings.Contains(err.Error(), "asks_to") || !strings.Contains(err.Error(), "work_mode") {
		t.Fatalf("unknown team setting error = %v, want the valid team settings named", err)
	}
}

func TestCTOTeamCharterSchemaAcceptsLeadMerges(t *testing.T) {
	raw := json.RawMessage(`{"action":"team_charter","team":"forge","attr":"lead_merges","value":"on"}`)
	if err := ValidateCTOActionSchema("graph_change", raw); err != nil {
		t.Fatalf("graph_change team_charter lead_merges: %v", err)
	}
	if err := CTOArguments("team_charter", json.RawMessage(`{"team":"forge","attr":"lead_merges","value":"on"}`)); err != nil {
		t.Fatalf("team_charter lead_merges: %v", err)
	}
	settings, err := json.Marshal(map[string]any{
		"action": "settings_set", "ref": map[string]string{"kind": "team", "id": "forge"},
		"setting": "lead_merges", "value": "on",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCTOActionSchema("graph_change", settings); err != nil {
		t.Fatalf("settings_set team lead_merges: %v", err)
	}
}

func TestCTOTeamCharterSchemaAcceptsModeAndHeldBack(t *testing.T) {
	for _, attr := range []string{"mode", "held_back"} {
		raw, err := json.Marshal(map[string]string{"action": "team_charter", "team": "forge", "attr": attr, "value": "x"})
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateCTOActionSchema("graph_change", raw); err != nil {
			t.Errorf("graph_change team_charter %s: %v", attr, err)
		}
		args, err := json.Marshal(map[string]string{"team": "forge", "attr": attr, "value": "x"})
		if err != nil {
			t.Fatal(err)
		}
		if err := CTOArguments("team_charter", args); err != nil {
			t.Errorf("team_charter %s: %v", attr, err)
		}
	}
}

func TestCTOProposalsFindListsLearned(t *testing.T) {
	list, err := ctoTestSession(t).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool.Name != "cto_proposals_find" {
			continue
		}
		props, _ := ctoToolSchema(t, tool)
		got := strings.Join(enumStrings(props["kind"]["enum"]), " ")
		for _, want := range []string{"all", "draft", "learned"} {
			if !strings.Contains(got, want) {
				t.Errorf("cto_proposals_find kind enum = %q, want %s", got, want)
			}
		}
		return
	}
	t.Fatal("cto_proposals_find is not listed")
}
