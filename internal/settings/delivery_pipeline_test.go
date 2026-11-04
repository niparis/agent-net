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
