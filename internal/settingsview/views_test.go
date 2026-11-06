package settingsview

import (
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
	"strings"
	"testing"
)

func TestViewsEffectiveValueAndHistory(t *testing.T) {
	snap := settings.Snapshot{Revision: 7, Values: []settings.Value{{Key: "delivery.merge_authority", Level: "repo", Scope: "repo", Value: json.RawMessage(`"ask"`)}}}
	history := []protocol.SettingsHistory{{Key: "delivery.merge_authority", Level: "repo", Scope: "repo", Actor: "owner"}}
	rows, err := Views(snap, history, protocol.SettingsReadRequest{Key: "delivery.merge_authority", Repo: "repo"}, true)
	if err != nil || len(rows) != 1 || string(rows[0].Value) != `"ask"` || rows[0].Actor != "owner" || len(rows[0].History) != 1 {
		t.Fatalf("views: %+v %v", rows, err)
	}
	if _, err = Views(snap, nil, protocol.SettingsReadRequest{Key: "unknown"}, true); err == nil {
		t.Fatal("unknown setting accepted")
	}
}

func TestDeliveryShorthandViewIncludesCanonicalHistory(t *testing.T) {
	snap := settings.Snapshot{Revision: 2, DeliveryPipelineCutover: true, Values: []settings.Value{{Key: "delivery.pipeline.merge.approver", Level: "repo", Scope: "repo", Value: json.RawMessage(`"owner"`)}}}
	history := []protocol.SettingsHistory{{Key: "delivery.pipeline.merge.approver", Level: "repo", Scope: "repo", Actor: "owner", New: json.RawMessage(`"owner"`)}}
	rows, err := Views(snap, history, protocol.SettingsReadRequest{Key: "delivery.merge_authority", Repo: "repo"}, true)
	if err != nil || len(rows) != 1 || string(rows[0].Value) != `"ask"` || string(rows[0].Explicit) != `"ask"` || len(rows[0].History) != 1 || string(rows[0].History[0].New) != `"ask"` {
		t.Fatalf("shorthand view: %+v %v", rows, err)
	}
}

func TestCustomShorthandViewKeepsExactExpectedRule(t *testing.T) {
	raw := json.RawMessage(`["review:findings_open=0"]`)
	snap := settings.Snapshot{DeliveryPipelineCutover: true, Values: []settings.Value{{Key: "delivery.pipeline.merge.requires", Level: "repo", Scope: "repo", Value: raw}}}
	rows, err := Views(snap, nil, protocol.SettingsReadRequest{Key: "delivery.tests_before_merge", Repo: "repo"}, true)
	if err != nil || len(rows) != 1 || string(rows[0].Value) != `"custom"` || string(rows[0].Explicit) != string(raw) {
		t.Fatalf("custom shorthand view: %+v, %v", rows, err)
	}
}

func TestDeliveryPipelineViewShowsGraphOwners(t *testing.T) {
	snap := settings.Snapshot{Revision: 4, DeliveryPipelineCutover: true, DeliveryAssignments: []settings.DeliveryAssignment{{Project: "repo", Step: "review", Node: "reviewer", Kind: "reviews"}}}
	rows, err := Views(snap, nil, protocol.SettingsReadRequest{Key: "delivery.pipeline", Repo: "repo"}, true)
	if err != nil || len(rows) != 1 {
		t.Fatalf("pipeline view = %+v, %v", rows, err)
	}
	var p protocol.Pipeline
	if err := json.Unmarshal(rows[0].Value, &p); err != nil {
		t.Fatal(err)
	}
	step, _ := p.Find(protocol.StepRef{Step: protocol.StepReview})
	if step.Owner.Kind != protocol.OwnerNode || step.Owner.Node != "reviewer" || rows[0].Source == "" {
		t.Fatalf("pipeline owner = %+v, source %q", step.Owner, rows[0].Source)
	}
}

func TestDeliveryPipelineViewIncludesReviewSources(t *testing.T) {
	for _, tc := range []struct{ key, value string }{{"delivery.pipeline.review.required_from", `"normal"`}, {"delivery.pipeline.review.independent", `"session"`}, {"delivery.pipeline.review.requires", `["accept"]`}} {
		t.Run(tc.key, func(t *testing.T) {
			snap := settings.Snapshot{DeliveryPipelineCutover: true, Values: []settings.Value{{Key: tc.key, Level: "repo", Scope: "repo", Value: json.RawMessage(tc.value)}}}
			rows, err := Views(snap, nil, protocol.SettingsReadRequest{Key: "delivery.pipeline", Repo: "repo"}, true)
			if err != nil || len(rows) != 1 || !strings.Contains(rows[0].Source, "repo") {
				t.Fatalf("source: %+v, %v", rows, err)
			}
		})
	}
}

func TestViewsNamedTargetStructuredFields(t *testing.T) {
	snap := settings.Snapshot{Revision: 9, Values: []settings.Value{
		{Key: "workers.auto_continue", Level: "network", Scope: "network", Value: json.RawMessage("false")},
		{Key: "workers.auto_continue", Level: "repo", Scope: "shop", Value: json.RawMessage("true")},
	}}
	target := &protocol.SettingsRef{Kind: "repo", ID: "shop", Name: "shop", Token: "token"}
	rows, err := ViewsWithTarget(snap, nil, protocol.SettingsReadRequest{Key: "workers.auto_continue"}, true, settings.Where{}, target)
	if err != nil || len(rows) != 1 {
		t.Fatalf("views: %+v %v", rows, err)
	}
	row := rows[0]
	if string(row.Value) != "false" || !row.Raised || string(row.Bound) != "false" {
		t.Fatalf("raised fields: %+v", row)
	}
	if row.Source != "network" || row.SourceLevel != "network" || row.SourceRef == nil || row.SourceRef.Kind != "network" || row.SourceLabel != "network" {
		t.Fatalf("source fields: %+v", row)
	}
	if string(row.Explicit) != "true" || row.ExplicitNote != "" {
		t.Fatalf("explicit here: %+v", row)
	}
	if row.Target == nil || row.Target.ID != "shop" || row.RecipeDigest != settings.RecipeHash() || row.ContextRevision != 9 {
		t.Fatalf("target and context: %+v", row)
	}

	snap.Values[0].Value = json.RawMessage("true")
	snap.Values[1].Value = json.RawMessage("false")
	rows, err = ViewsWithTarget(snap, nil, protocol.SettingsReadRequest{Key: "workers.auto_continue"}, true, settings.Where{}, target)
	if err != nil || len(rows) != 1 {
		t.Fatalf("repo source: %+v %v", rows, err)
	}
	row = rows[0]
	if string(row.Value) != "false" || row.Raised || row.SourceLevel != "repo" || row.SourceRef == nil || row.SourceRef.ID != "shop" || row.SourceLabel != "repo shop" {
		t.Fatalf("repo source fields: %+v", row)
	}

	agent := &protocol.SettingsRef{Kind: "agent", ID: "reviewer", Name: "reviewer", Token: "agent-token"}
	rows, err = ViewsWithTarget(settings.Snapshot{Revision: 3}, nil, protocol.SettingsReadRequest{Key: "workers.auto_continue"}, true, settings.Where{}, agent)
	if err != nil || len(rows) != 1 {
		t.Fatalf("not-set fields: %+v %v", rows, err)
	}
	if row := rows[0]; row.ExplicitNote != "not set here" || row.SourceLevel != "recipe" || row.SourceLabel != "standard recipe" || row.Explicit != nil {
		t.Fatalf("not-set fields: %+v", row)
	}
}

func TestViewsLegacyRepoReadKeepsOriginals(t *testing.T) {
	snap := settings.Snapshot{Revision: 7, Values: []settings.Value{{Key: "delivery.merge_authority", Level: "repo", Scope: "repo", Value: json.RawMessage(`"ask"`)}}}
	rows, err := Views(snap, nil, protocol.SettingsReadRequest{Key: "delivery.merge_authority", Repo: "repo"}, true)
	if err != nil || len(rows) != 1 {
		t.Fatalf("legacy: %+v %v", rows, err)
	}
	row := rows[0]
	if string(row.Value) != `"ask"` || row.Source != "repo repo" || string(row.Explicit) != `"ask"` || row.Target != nil {
		t.Fatalf("legacy read changed: %+v", row)
	}
}

func TestDeliveryPipelineViewRequiresSuiteAndNewTests(t *testing.T) {
	snap := settings.Snapshot{DeliveryPipelineCutover: true, Values: []settings.Value{{Key: "delivery.pipeline.merge.requires", Level: "repo", Scope: "repo", Value: json.RawMessage(`["accept","review","test:suite","test:tests_added"]`)}}}
	rows, err := Views(snap, nil, protocol.SettingsReadRequest{Key: "delivery.pipeline", Repo: "repo"}, true)
	if err != nil || len(rows) != 1 {
		t.Fatalf("view: %+v, %v", rows, err)
	}
	var p protocol.Pipeline
	if err := json.Unmarshal(rows[0].Value, &p); err != nil {
		t.Fatal(err)
	}
	step, _ := p.Find(protocol.StepRef{Step: protocol.StepMerge})
	for _, req := range step.Requires {
		if req.Step == protocol.StepTest && req.Fields[protocol.FieldSuite] == protocol.FieldAny && req.Fields[protocol.FieldTestsAdded] == protocol.FieldYes {
			return
		}
	}
	t.Fatalf("view dropped test requirement: %+v", step.Requires)
}
