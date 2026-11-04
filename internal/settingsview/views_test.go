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
