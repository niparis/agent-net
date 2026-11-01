package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAutonomyIsOneNodeSettingWithTwoValues(t *testing.T) {
	for _, value := range []string{"", AutonomyOff, AutonomyWake} {
		if err := ValidateNodeSetting(SettingAutonomy, value); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
	}
	if err := ValidateNodeSetting(SettingAutonomy, "always"); err == nil {
		t.Fatal("accepted an unknown autonomy value")
	}
	if got := NodeAutonomy(nil); got != AutonomyOff {
		t.Fatalf("default = %q, want off", got)
	}
	if got := NodeAutonomy(map[string]string{SettingAutonomy: AutonomyWake}); got != AutonomyWake {
		t.Fatalf("wake = %q", got)
	}
	if got := NodeAutonomy(map[string]string{SettingAutonomy: "always"}); got != AutonomyOff {
		t.Fatalf("invalid value = %q, want off", got)
	}
}

func TestPublicGraphShowsAutonomyAndHidesOtherSettings(t *testing.T) {
	g := Graph{Nodes: []Node{
		{Name: "shop", Kind: "component", Settings: map[string]string{
			SettingAutonomy:       AutonomyWake,
			SettingMergeAuthority: "OWNER_ONLY_NODE_SETTING_CANARY",
		}},
		{Name: "billing", Kind: "component"},
	}}
	page := PublicGraph(g, PublicGraphQuery{})
	if len(page.Nodes) != 2 {
		t.Fatalf("nodes: %+v", page.Nodes)
	}
	byName := map[string]PublicGraphNode{}
	for _, n := range page.Nodes {
		byName[n.Name] = n
	}
	if got := byName["shop"].Autonomy; got != AutonomyWake {
		t.Fatalf("shop autonomy = %q", got)
	}
	if !strings.Contains(byName["shop"].Line, "autonomy: wake") {
		t.Fatalf("shop line does not show autonomy: %q", byName["shop"].Line)
	}
	if got := byName["billing"].Autonomy; got != AutonomyOff {
		t.Fatalf("default autonomy = %q, want off", got)
	}
	data, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(data)
	if strings.Contains(raw, "OWNER_ONLY_NODE_SETTING_CANARY") || strings.Contains(raw, "settings") {
		t.Fatalf("public graph leaked an owner-only setting: %s", raw)
	}
}
