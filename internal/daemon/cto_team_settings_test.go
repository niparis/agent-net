package daemon

import (
	"context"
	"encoding/json"
	"fmt"
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

// The CTO sets a team's authority mode and held back text with the same
// gateway validation as the owner's Teams list keys, and graph undo restores
// each earlier value.
func TestCTOTeamModeAndHeldBackUndo(t *testing.T) {
	g, w := startCTOTeamFixture(t)
	ctx := context.Background()
	team := func() protocol.Team {
		t.Helper()
		teams, err := g.gw.Store().Teams()
		if err != nil || len(teams) != 1 {
			t.Fatalf("teams = %+v, %v", teams, err)
		}
		return teams[0]
	}
	historyID := func() int64 {
		t.Helper()
		data, err := w.d.call(ctx, "cto.graph_latest", nil)
		if err != nil {
			t.Fatal(err)
		}
		var latest struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(data, &latest); err != nil {
			t.Fatal(err)
		}
		return latest.ID
	}
	set := func(attr, value string) int64 {
		t.Helper()
		raw, err := json.Marshal(map[string]string{"action": "team_charter", "team": "forge", "attr": attr, "value": value})
		if err != nil {
			t.Fatal(err)
		}
		if class, err := ctoClass("graph_change", raw); err != nil || class != ctoMinor {
			t.Fatalf("%s class = %s, %v; want minor", attr, class, err)
		}
		if _, err := w.d.runCTOToolRaw(ctx, "graph_change", raw); err != nil {
			t.Fatalf("%s: %v", attr, err)
		}
		return historyID()
	}
	undo := func(id int64) {
		t.Helper()
		if _, err := w.d.runCTOToolRaw(ctx, "graph_change", json.RawMessage(fmt.Sprintf(`{"action":"undo","id":%d}`, id))); err != nil {
			t.Fatalf("undo %d: %v", id, err)
		}
	}

	modeID := set("mode", protocol.AuthorityAbsolute)
	if got := team().AuthorityMode; got != protocol.AuthorityAbsolute {
		t.Fatalf("mode = %q, want absolute", got)
	}
	heldID := set("held_back", "deploy to production")
	if got := team().HeldBack; got != "deploy to production" {
		t.Fatalf("held back = %q", got)
	}
	if _, err := w.d.runCTOToolRaw(ctx, "graph_change", json.RawMessage(`{"action":"team_charter","team":"forge","attr":"mode","value":"always"}`)); err == nil || !strings.Contains(err.Error(), "authority mode") {
		t.Fatalf("bad mode error = %v, want the authority modes named", err)
	}
	if _, err := w.d.runCTOToolRaw(ctx, "graph_change", json.RawMessage(`{"action":"team_charter","team":"forge","attr":"held_back","value":"bad\u0001text"}`)); err == nil || !strings.Contains(err.Error(), "held back") {
		t.Fatalf("bad held back error = %v, want invalid held back text", err)
	}

	undo(heldID)
	if got := team(); got.HeldBack != "" || got.AuthorityMode != protocol.AuthorityAbsolute {
		t.Fatalf("after held back undo: %+v", got)
	}
	undo(modeID)
	if got := team().AuthorityMode; got != protocol.DefaultAuthorityMode {
		t.Fatalf("after mode undo: %q, want %q", got, protocol.DefaultAuthorityMode)
	}
}

// An ordinary agent session cannot reach the CTO's team write or owner item
// read, whatever the arguments.
func TestCTOTeamModeAndOwnerItemsAreCTOOnly(t *testing.T) {
	_, w := startCTOTeamFixture(t)
	agent, _ := helloPi(t, w.node, "not-the-cto", "/w/not-the-cto", true)
	for _, method := range []string{"graph_change", "cto_proposals_find"} {
		if _, err := agent.call(method, map[string]any{"kind": "all", "action": "team_charter", "team": "forge", "attr": "mode", "value": "absolute"}); err == nil {
			t.Fatalf("agent called %s", method)
		}
	}
}
