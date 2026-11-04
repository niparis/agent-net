package settings_test

import (
	"encoding/json"
	"testing"

	. "github.com/niparis/agent-net/internal/settings"
)

func TestDeliveryPipelineShorthandReadsCanonicalRules(t *testing.T) {
	s := Snapshot{DeliveryPipelineCutover: true, Maturity: map[string]string{"demo": "mature"}, Values: []Value{
		{Key: "delivery.pipeline.merge.approver", Level: "repo", Scope: "demo", Value: json.RawMessage(`"owner"`)},
		{Key: "delivery.pipeline.merge.requires", Level: "repo", Scope: "demo", Value: json.RawMessage(`["review:findings_open=0"]`)},
	}}
	for _, tc := range []struct{ key, want string }{
		{"delivery.merge_authority", `"ask"`},
		{"delivery.tests_before_merge", `"custom"`},
		{"delivery.deploy_rule", `"ask"`},
	} {
		got, err := s.Resolve(tc.key, Where{Repo: "demo"})
		if err != nil || string(got.Value) != tc.want {
			t.Fatalf("%s = %s, %v; want %s", tc.key, got.Value, err, tc.want)
		}
	}
}

func TestDeliveryPipelineRejectsUnknownRequirement(t *testing.T) {
	d, ok := DefinitionFor("delivery.pipeline.merge.requires")
	if !ok {
		t.Fatal("merge requirements are missing")
	}
	if _, err := d.Codec(json.RawMessage(`["test:unknown"]`)); err == nil {
		t.Fatal("unknown requirement was accepted")
	}
}

func TestDeliveryReviewRiskBoundsAndOrdering(t *testing.T) {
	d, ok := DefinitionFor("delivery.pipeline.review.required_risk")
	if !ok {
		t.Fatal("risk threshold is missing")
	}
	for _, raw := range []string{"0", "5", "-1", "2.5"} {
		if _, err := d.Codec(json.RawMessage(raw)); err == nil {
			t.Errorf("accepted risk threshold %s", raw)
		}
	}
	value, err := d.StricterValue(json.RawMessage(`4`), json.RawMessage(`2`))
	if err != nil || string(value) != "2" {
		t.Fatalf("stricter risk=%s err=%v", value, err)
	}
}
