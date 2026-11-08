package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// startCTOTeamFixture creates the team forge with the repo demo as a member,
// so a Team setting can be read through a member's effective charter.
func startCTOTeamFixture(t *testing.T) (*testGateway, *workerNode) {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	if _, err := w.d.call(context.Background(), "hello", map[string]any{"hostname": "fixture", "version": "v0.4.145", "graph_refs": true}); err != nil {
		t.Fatal(err)
	}
	waitGraphCutover(t, g)
	if err := w.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.cli.mustCall("repo_create", map[string]any{"name": "demo"})
	w.cli.mustCall("team_write", map[string]any{"action": "create", "team_id": "forge", "name": "forge", "purpose": "Shared work"})
	w.cli.mustCall("team_write", map[string]any{"action": "membership", "team_id": "forge", "member": map[string]any{"kind": "repo", "id": "demo"}})
	return g, w
}

func TestCTOTeamCharterWorkModeReachesMember(t *testing.T) {
	g, w := startCTOTeamFixture(t)
	raw := json.RawMessage(`{"action":"team_charter","team":"forge","attr":"work_mode","value":"workers_code"}`)
	if class, err := ctoClass("graph_change", raw); err != nil || class != ctoMinor {
		t.Fatalf("class = %s, %v; want minor", class, err)
	}
	if _, err := w.d.runCTOToolRaw(context.Background(), "graph_change", raw); err != nil {
		t.Fatal(err)
	}
	c, err := g.gw.Store().EffectiveCharter("demo", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if c.WorkMode != protocol.WorkModeWorkersCode {
		t.Fatalf("member work_mode = %q, want %q", c.WorkMode, protocol.WorkModeWorkersCode)
	}
	if c.Sources == nil {
		t.Fatal("member charter has no sources")
	}
	list := (*c.Sources)[protocol.AttrWorkMode]
	if len(list) == 0 || list[len(list)-1].Level != "team" || list[len(list)-1].Scope != "forge" {
		t.Fatalf("work_mode sources = %+v, want team forge", list)
	}
	if line := protocol.WorkModeLine(c); !strings.Contains(line, "team forge") {
		t.Fatalf("member charter line = %q, want team forge", line)
	}
}

func TestCTOTeamWriteCharterShape(t *testing.T) {
	g, w := startCTOTeamFixture(t)
	raw, err := json.Marshal(map[string]any{
		"area": "operator", "method": "team_write",
		"arguments": map[string]any{"action": "charter", "team_id": "forge", "attr": "work_mode", "value": "workers_code"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.d.runCTOTool(context.Background(), "settings_change", raw); err != nil {
		t.Fatal(err)
	}
	c, err := g.gw.Store().EffectiveCharter("demo", "", "")
	if err != nil || c.WorkMode != protocol.WorkModeWorkersCode {
		t.Fatalf("member work_mode = %q, %v; want workers_code", c.WorkMode, err)
	}
}

func TestCTOTeamSettingsSetTeamRef(t *testing.T) {
	g, w := startCTOTeamFixture(t)
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"action":"settings_set","ref":{"kind":"team","id":"forge"},"setting":"asks_to","value":"lead"}`),
		json.RawMessage(`{"action":"settings_set","ref":{"kind":"team","id":"forge"},"setting":"work_mode","value":"workers_code"}`),
	} {
		if class, err := ctoClass("graph_change", raw); err != nil || class != ctoMinor {
			t.Fatalf("class = %s, %v; want minor", class, err)
		}
		concrete, _, _, err := ctoConcreteCall("graph_change", raw)
		if err != nil || concrete != "team_settings_set" {
			t.Fatalf("concrete = %q, %v; want team_settings_set", concrete, err)
		}
		if _, err := w.d.runCTOToolRaw(context.Background(), "graph_change", raw); err != nil {
			t.Fatal(err)
		}
	}
	teams, err := g.gw.Store().Teams()
	if err != nil || len(teams) != 1 || teams[0].AsksTo != protocol.AsksToLead {
		t.Fatalf("asks_to = %+v, want lead", teams)
	}
	c, err := g.gw.Store().EffectiveCharter("demo", "", "")
	if err != nil || c.WorkMode != protocol.WorkModeWorkersCode {
		t.Fatalf("member work_mode = %q, %v; want workers_code", c.WorkMode, err)
	}

	if _, err := w.d.runCTOToolRaw(context.Background(), "graph_change", json.RawMessage(`{"action":"settings_set","ref":{"kind":"team","id":"forge"},"setting":"work_mode","value":"always"}`)); err == nil || !strings.Contains(err.Error(), "work_mode must be") {
		t.Fatalf("bad work_mode error = %v", err)
	}
	if _, err := w.d.runCTOToolRaw(context.Background(), "graph_change", json.RawMessage(`{"action":"settings_set","ref":{"kind":"team","id":"forge"},"setting":"autonomy","value":"wake"}`)); err == nil || !strings.Contains(err.Error(), "asks_to") || !strings.Contains(err.Error(), "work_mode") {
		t.Fatalf("unknown team setting error = %v, want the valid team settings named", err)
	}
}
