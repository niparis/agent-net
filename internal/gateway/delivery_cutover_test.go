package gateway

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestDeliveryCutoverMigratesLegacyRows(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("alpha", "owner"); err != nil {
		t.Fatal(err)
	}
	legacy := map[string]string{protocol.SettingTestsBeforeMerge: "optional", protocol.SettingMergeAuthority: "delegated", protocol.SettingJudgeConsumers: "free", protocol.SettingDeployRule: "ask", protocol.SettingAutonomy: "wake", protocol.SettingWorkerSandbox: "read-only"}
	for key, value := range legacy {
		if _, err := s.db.Exec(`INSERT INTO graph_node_settings VALUES(?,?,?,1,'owner')`, "alpha", key, value); err != nil {
			t.Fatal(err)
		}
	}
	// Replay only the final migration, with data in the old tables.
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version=?`, len(migrations)); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	for key, value := range legacy {
		var old string
		if err := s.db.QueryRow(`SELECT value FROM graph_node_settings WHERE node='alpha' AND setting=?`, key).Scan(&old); err != nil || old != value {
			t.Fatalf("backup %s: %q %v", key, old, err)
		}
		if key == protocol.SettingWorkerSandbox {
			continue
		}
		names := map[string]string{protocol.SettingTestsBeforeMerge: "delivery.tests_before_merge", protocol.SettingMergeAuthority: "delivery.merge_authority", protocol.SettingJudgeConsumers: "charter.judge_consumers", protocol.SettingDeployRule: "delivery.deploy_rule", protocol.SettingAutonomy: "charter.autonomy_mode"}
		var raw, cause, actor string
		err := s.db.QueryRow(`SELECT v.value,h.cause,h.actor FROM setting_values v JOIN setting_history h ON h.revision=v.revision AND h.key=v.key AND h.scope=v.scope WHERE v.key=? AND v.level='repo' AND v.scope='alpha'`, names[key]).Scan(&raw, &cause, &actor)
		var got string
		_ = json.Unmarshal([]byte(raw), &got)
		if err != nil || got != value || cause != "cutover" || actor != settingsCutoverActor {
			t.Errorf("%s migrated: %q %q %q %v", key, got, cause, actor, err)
		}
	}
}
func TestDeliveryAdapterUndoAndRemoval(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("alpha", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO graph_node_settings VALUES('alpha','merge_authority','ask',1,'owner'); INSERT INTO graph_history(type,node,attr,old_value,new_value,at,actor) VALUES('setting','alpha','merge_authority','','ask',1,'owner')`); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := s.db.QueryRow(`SELECT MAX(id) FROM graph_history`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := s.GraphUndo(id, "owner"); err == nil || !strings.Contains(err.Error(), "merge_authority") {
		t.Fatalf("legacy undo: %v", err)
	}
	if err := s.SetNodeSetting("alpha", protocol.SettingMergeAuthority, "delegated", "owner"); err != nil {
		t.Fatal(err)
	}
	got, err := s.NodeSettings("alpha")
	if err != nil || got.Effective.MergeAuthority != "delegated" || got.Overrides[protocol.SettingMergeAuthority] != "delegated" {
		t.Fatalf("adapter: %+v %v", got, err)
	}
	var backup string
	if err = s.db.QueryRow(`SELECT value FROM graph_node_settings WHERE node='alpha' AND setting='merge_authority'`).Scan(&backup); err != nil || backup != "ask" {
		t.Fatalf("backup changed: %s %v", backup, err)
	}
	if err = s.RemoveArchitect("alpha", "owner"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM setting_values WHERE level='repo' AND scope='alpha'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("values left: %d %v", count, err)
	}
	if err = s.db.QueryRow(`SELECT count(*) FROM setting_history WHERE scope='alpha' AND key='delivery.merge_authority' AND new_value IS NULL`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("missing removal history: %d %v", count, err)
	}
}

func TestDeliveryUnchartedSessionUsesNetwork(t *testing.T) {
	s, _ := newTestStore(t)
	ids, _ := graphSessions(t, s, "ghost")
	if _, err := s.db.Exec(`UPDATE sessions SET graph_excluded=1 WHERE id=?`, ids["ghost"]); err != nil {
		t.Fatal(err)
	}
	changes := []SettingsChange{
		{Key: "delivery.merge_authority", Level: "repo", Scope: "ghost", Value: json.RawMessage(`"ask"`)},
	}
	if _, err := s.WriteSettings(changes, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	graph, err := s.Graph()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range graph.Nodes {
		if n.Name == "ghost" {
			t.Fatal("test repo has a graph node")
		}
	}
	if got, err := s.MergeAuthorityForSession(ids["ghost"]); err != nil || got != "full" {
		t.Fatalf("uncharted authority: %q %v", got, err)
	}
}

func TestDeliveryExplicitNetworkDefaultTightensOverrides(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("alpha", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeSetting("alpha", protocol.SettingAutonomy, "wake", "owner"); err != nil {
		t.Fatal(err)
	}
	change := SettingsChange{Key: "charter.autonomy_mode", Level: "network", Scope: "network", Value: json.RawMessage(`"off"`)}
	result, err := s.WriteSettings([]SettingsChange{change}, "owner", "network bound")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Moved) != 1 || result.Moved[0].Scope != "alpha" || !result.Moved[0].Rewritten {
		t.Fatalf("missing child move: %+v", result.Moved)
	}
	got, err := s.NodeSettings("alpha")
	if err != nil || got.Autonomy != "off" || got.Overrides[protocol.SettingAutonomy] != "off" {
		t.Fatalf("not tightened: %+v %v", got, err)
	}
}

func TestDeliveryAdapterRepairsInvalidMigratedValue(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("alpha", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision) VALUES('delivery.merge_authority','repo','alpha','',?,0)`, `"broken"`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeSetting("alpha", protocol.SettingMergeAuthority, "ask", "owner"); err != nil {
		t.Fatalf("repair invalid value: %v", err)
	}
	got, err := s.NodeSettings("alpha")
	if err != nil || got.Effective.MergeAuthority != "ask" {
		t.Fatalf("repair: %+v %v", got, err)
	}
}

func TestDeliveryAdaptersMatchMaturityFixtures(t *testing.T) {
	raw, err := os.ReadFile("../protocol/testdata/delivery-baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	s, _ := newTestStore(t)
	if err = s.AddArchitect("repo", "owner"); err != nil {
		t.Fatal(err)
	}
	choices := map[string][]string{
		protocol.SettingTestsBeforeMerge: {"optional", "suite", "suite-new-tests", "suite-new-tests-review"},
		protocol.SettingMergeAuthority:   {"full", "delegated", "ask"},
		protocol.SettingWorkerSandbox:    {"sandboxed", "no-network", "read-only"},
		protocol.SettingJudgeConsumers:   {"free", "charter", "strict", "escalate"},
		protocol.SettingDeployRule:       {"brief", "ask", "owner"},
		protocol.SettingAutonomy:         {"off", "wake"},
	}
	for _, maturity := range []string{"", "early", "growing", "mature", "critical"} {
		if err = s.SetCharter("repo", "maturity", maturity, "owner"); err != nil {
			t.Fatal(err)
		}
		for key, values := range choices {
			for _, value := range values {
				if err = s.SetNodeSetting("repo", key, value, "owner"); err != nil {
					t.Fatalf("%s %s=%s: %v", maturity, key, value, err)
				}
				got, err := s.NodeSettings("repo")
				if err != nil {
					t.Fatal(err)
				}
				fixture, ok := fixtures[maturity+"/"+key+"/"+value]
				if !ok {
					t.Fatal("missing fixture", maturity, key, value)
				}
				var want struct{ Rules, Consumer, Autonomy string }
				if err = json.Unmarshal(fixture, &want); err != nil {
					t.Fatal(err)
				}
				if got.Overrides[key] != value || got.Effective.RulesText() != want.Rules || got.Effective.ConsumerText() != want.Consumer || got.Autonomy != want.Autonomy {
					t.Fatalf("%s %s=%s: got %+v; want %+v", maturity, key, value, got, want)
				}
				if err = s.SetNodeSetting("repo", key, "", "owner"); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestDeliveryCutoverActorIsRecognizedButCannotWrite(t *testing.T) {
	const cutoverActor = "agent-net cutover"
	if !settingsActor(cutoverActor) {
		t.Fatal("cutover history actor is not recognized")
	}
	for _, kind := range []string{"advice", "rule"} {
		if actorAllowed(cutoverActor, kind) {
			t.Fatalf("cutover actor can write %s", kind)
		}
	}
	s, _ := newTestStore(t)
	change := SettingsChange{Key: "guidance.intro_text", Level: "network", Scope: "network", Value: json.RawMessage(`"test"`)}
	if _, err := s.WriteSettings([]SettingsChange{change}, cutoverActor, "spoofed cutover"); err == nil {
		t.Fatal("cutover actor wrote through settings API")
	}
}

func TestDeliveryNodeSettingsFrameActorCompatibility(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("alpha", "owner"); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s}
	ctx := context.Background()
	for _, actor := range []string{"", "agent", "agent-net cutover", "owner (CTO proposal p_bad)"} {
		raw, _ := json.Marshal(map[string]any{"node": "alpha", "setting": "merge_authority", "value": "ask", "by": actor})
		if _, err := g.handle(ctx, &nodeConn{}, "node.settings.set", raw); err == nil {
			t.Errorf("accepted actor %q", actor)
		}
	}
	for _, raw := range []string{`{"node":"alpha","setting":"merge_authority","value":"ask","by":null}`, `{"node":"alpha","setting":"merge_authority","value":"ask","by":7}`} {
		if _, err := g.handle(ctx, &nodeConn{}, "node.settings.set", []byte(raw)); err == nil {
			t.Errorf("accepted invalid actor in %s", raw)
		}
	}
	for _, raw := range []string{`{"node":"alpha","setting":"merge_authority","value":"ask"}`, `{"node":"alpha","setting":"merge_authority","value":"full","by":"owner"}`} {
		if _, err := g.handle(ctx, &nodeConn{}, "node.settings.set", []byte(raw)); err != nil {
			t.Fatalf("legacy/owner frame %s: %v", raw, err)
		}
	}
}
