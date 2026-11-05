package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

func TestDeliveryShorthandWritesPipelineRuleAndCanUndo(t *testing.T) {
	r := newDeliveryRig(t)
	change := SettingsChange{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}
	result, err := r.s.WriteSettings([]SettingsChange{change}, "owner", "test")
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err := r.s.db.QueryRow(`SELECT value FROM setting_values WHERE key='delivery.pipeline.merge.approver' AND level='repo' AND scope='demo'`).Scan(&value); err != nil || value != `"owner"` {
		t.Fatalf("canonical rule = %q, %v", value, err)
	}
	var count int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM setting_values WHERE key='delivery.merge_authority' AND level='repo' AND scope='demo'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("shorthand stored as a second rule: %d, %v", count, err)
	}
	snap, err := r.s.SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	e, err := snap.Resolve(change.Key, settings.Where{Repo: "demo"})
	if err != nil || string(e.Value) != `"ask"` {
		t.Fatalf("shorthand read = %s, %v", e.Value, err)
	}
	if _, err := r.s.UndoSettings(result.Revision, "owner"); err != nil {
		t.Fatal(err)
	}
	snap, err = r.s.SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	e, err = snap.Resolve(change.Key, settings.Where{Repo: "demo"})
	if err != nil || string(e.Value) != `"full"` {
		t.Fatalf("undo = %s, %v", e.Value, err)
	}
}

func TestDeliveryShorthandHistoryShowsPipelineChanges(t *testing.T) {
	r := newDeliveryRig(t)
	change := SettingsChange{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}
	if _, err := r.s.WriteSettings([]SettingsChange{change}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	history, err := r.s.SettingsHistory(change.Key, 0, true)
	if err != nil || len(history) != 1 || history[0].Key != change.Key || string(history[0].New) != `"ask"` {
		t.Fatalf("shorthand history = %+v, %v", history, err)
	}
}

func TestDeliveryPipelineUsesCustomMergeRequirements(t *testing.T) {
	r := newDeliveryRig(t)
	change := SettingsChange{Key: "delivery.pipeline.merge.requires", Level: "repo", Scope: "demo", Value: json.RawMessage(`["accept","review:findings_open=0","test:suite"]`)}
	if _, err := r.s.WriteSettings([]SettingsChange{change}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
	p, err := r.s.deliveryPipeline("demo")
	if err != nil {
		t.Fatal(err)
	}
	if tests, _, _ := p.Shorthand(); tests != protocol.ShorthandCustom {
		t.Fatalf("pipeline shorthand = %q", tests)
	}
}

func TestDeliveryPipelineNetworkRequirementsCannotBeWeakened(t *testing.T) {
	r := newDeliveryRig(t)
	key := "delivery.pipeline.merge.requires"
	network := SettingsChange{Key: key, Level: "network", Scope: "network", Value: json.RawMessage(`["accept","review","test:suite"]`)}
	if _, err := r.s.WriteSettings([]SettingsChange{network}, "owner", "network rule"); err != nil {
		t.Fatal(err)
	}
	repo := SettingsChange{Key: key, Level: "repo", Scope: "demo", Value: json.RawMessage(`["accept","review"]`)}
	if _, err := r.s.WriteSettings([]SettingsChange{repo}, "owner", "weaken"); err == nil {
		t.Fatal("repo weakened a network requirement")
	}
	repo.Value = json.RawMessage(`["accept","review","test:suite","test:tests_added"]`)
	if _, err := r.s.WriteSettings([]SettingsChange{repo}, "owner", "tighten"); err != nil {
		t.Fatal(err)
	}
	network.Expect = network.Value
	network.Value = json.RawMessage(`["accept","review","test:suite","review:findings_open=0"]`)
	result, err := r.s.WriteSettings([]SettingsChange{network}, "owner", "tighten network")
	if err != nil || len(result.Moved) == 0 {
		t.Fatalf("network tightening = %+v, %v", result, err)
	}
	snap, err := r.s.SettingsSnapshot()
	if err != nil || snap.String("delivery.tests_before_merge", settings.Where{Repo: "demo"}) != "suite-new-tests-review" {
		t.Fatalf("tightened shorthand = %q, %v", snap.String("delivery.tests_before_merge", settings.Where{Repo: "demo"}), err)
	}
}

func TestDeliveryGraphOwnerUpdatesSettingsSnapshot(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	before, err := r.s.SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "reviewer", To: "demo", Kind: "approves"}, "owner"); err != nil {
		t.Fatal(err)
	}
	after, err := r.s.SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision <= before.Revision || after.String("delivery.merge_authority", settings.Where{Repo: "demo"}) != "custom" {
		t.Fatalf("assignment snapshot revision %d -> %d, shorthand %q", before.Revision, after.Revision, after.String("delivery.merge_authority", settings.Where{Repo: "demo"}))
	}
	legacy := after
	legacy.DeliveryPipelineCutover = false
	if got := legacy.String("delivery.merge_authority", settings.Where{Repo: "demo"}); got != "ask" {
		t.Fatalf("old daemon's conservative merge rule = %q", got)
	}
	authority, text, err := r.s.MergeRuleForSession(r.caller)
	if err != nil || authority != "custom" || !strings.Contains(text, "reviewer") {
		t.Fatalf("delivered merge rule = %q %q, %v", authority, text, err)
	}
}

func TestDeliveryShorthandReplacesGraphApproverAndUndoRestoresIt(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}}, "owner", "initial"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "reviewer", To: "demo", Kind: "approves"}, "owner"); err != nil {
		t.Fatal(err)
	}
	result, err := r.s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "repo", Scope: "demo", Value: json.RawMessage(`"full"`), Expect: json.RawMessage(`"ask"`)}}, "owner", "use full authority")
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.s.deliveryPipeline("demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, merge, _ := p.Shorthand(); merge != "full" {
		t.Fatalf("shorthand write left graph override: %q", merge)
	}
	undo, err := r.s.UndoSettings(result.Revision, "owner")
	if err != nil {
		t.Fatal(err)
	}
	var restored int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM graph_edges WHERE source='reviewer' AND target='demo' AND kind='approves'`).Scan(&restored); err != nil || restored != 1 {
		t.Fatalf("undo did not restore edge: %d, %v", restored, err)
	}
	if _, err := r.s.UndoSettings(undo.Revision, "owner"); err != nil {
		t.Fatal(err)
	}
	p, err = r.s.deliveryPipeline("demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, merge, _ := p.Shorthand(); merge != "full" {
		t.Fatalf("redo did not remove graph approver: %q", merge)
	}
}

func TestDeliveryPipelineMigratesLegacyShorthand(t *testing.T) {
	r := newDeliveryRig(t)
	if _, err := r.s.db.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision) VALUES('delivery.tests_before_merge','repo','demo','','"suite-new-tests"',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "delivery pipeline settings cutover")); err != nil {
		t.Fatal(err)
	}
	if err := migrateOldFixture(t, r.s); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := r.s.db.QueryRow(`SELECT value FROM setting_values WHERE key='delivery.pipeline.merge.requires' AND level='repo' AND scope='demo'`).Scan(&value); err != nil || value != `["accept","review","test:suite","test:tests_added"]` {
		t.Fatalf("migrated pipeline = %q, %v", value, err)
	}
	var count int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM delivery_settings_legacy WHERE key='delivery.tests_before_merge' AND scope='demo'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy backup = %d, %v", count, err)
	}
}

func TestDeliverySettingsSameApproverKeepsEdge(t *testing.T) {
	for _, tc := range []struct{ key, value, kind string }{{"delivery.merge_authority", `"full"`, "approves"}, {"delivery.deploy_rule", `"brief"`, "approves_deploy"}} {
		t.Run(tc.kind, func(t *testing.T) {
			r := newDeliveryRig(t)
			c := SettingsChange{Key: tc.key, Level: "repo", Scope: "demo", Value: json.RawMessage(tc.value)}
			if _, err := r.s.WriteSettings([]SettingsChange{c}, "owner", "initial"); err != nil {
				t.Fatal(err)
			}
			if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
				t.Fatal(err)
			}
			if err := r.s.SetEdge(protocol.Edge{From: "reviewer", To: "demo", Kind: tc.kind}, "owner"); err != nil {
				t.Fatal(err)
			}
			c.Expect = c.Value
			before, err := r.s.SettingsSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			result, err := r.s.WriteSettings([]SettingsChange{c}, "owner", "same")
			if err != nil {
				t.Fatal(err)
			}
			if result.Revision != before.Revision {
				t.Fatalf("no-op changed revision: %d -> %d", before.Revision, result.Revision)
			}
			var count int
			if err := r.s.db.QueryRow(`SELECT count(*) FROM graph_edges WHERE source='reviewer' AND target='demo' AND kind=?`, tc.kind).Scan(&count); err != nil || count != 1 {
				t.Fatalf("edge count = %d, %v", count, err)
			}
		})
	}
}

func TestDeliverySettingsImplicitApproverNoChange(t *testing.T) {
	for _, tc := range []struct{ key, value, kind string }{{"delivery.merge_authority", `"full"`, "approves"}, {"delivery.deploy_rule", `"brief"`, "approves_deploy"}} {
		t.Run(tc.kind, func(t *testing.T) {
			r := newDeliveryRig(t)
			if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
				t.Fatal(err)
			}
			if err := r.s.SetEdge(protocol.Edge{From: "reviewer", To: "demo", Kind: tc.kind}, "owner"); err != nil {
				t.Fatal(err)
			}
			before, err := r.s.SettingsSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			result, err := r.s.WriteSettings([]SettingsChange{{Key: tc.key, Level: "repo", Scope: "demo", Value: json.RawMessage(tc.value)}}, "owner", "same default")
			if err != nil {
				t.Fatal(err)
			}
			if result.Revision != before.Revision {
				t.Fatalf("no-op changed revision: %d -> %d", before.Revision, result.Revision)
			}
			var n int
			if err := r.s.db.QueryRow(`SELECT count(*) FROM graph_edges WHERE source='reviewer' AND target='demo' AND kind=?`, tc.kind).Scan(&n); err != nil || n != 1 {
				t.Fatalf("edge count %d: %v", n, err)
			}
		})
	}
}

func TestDeliverySettingsRealDeployApproverChangeRemovesEdge(t *testing.T) {
	r := newDeliveryRig(t)
	c := SettingsChange{Key: "delivery.deploy_rule", Level: "repo", Scope: "demo", Value: json.RawMessage(`"ask"`)}
	if _, err := r.s.WriteSettings([]SettingsChange{c}, "owner", "initial"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.AddArchitect("reviewer", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "reviewer", To: "demo", Kind: "approves_deploy"}, "owner"); err != nil {
		t.Fatal(err)
	}
	c.Expect = c.Value
	c.Value = json.RawMessage(`"brief"`)
	if _, err := r.s.WriteSettings([]SettingsChange{c}, "owner", "change"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := r.s.db.QueryRow(`SELECT count(*) FROM graph_edges WHERE target='demo' AND kind='approves_deploy'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("edge count = %d, %v", count, err)
	}
}
