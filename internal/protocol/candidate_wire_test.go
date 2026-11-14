package protocol

import (
	"encoding/json"
	"testing"
)

// The candidate type is shared by the worker rankers, the gate and the role
// lists. A role list is on the wire, so this pins its four fields and their
// order: a ranker fact or a display name must not leak into it.
func TestRoleListWireForm(t *testing.T) {
	req := RolePickRequest{
		Role:   RoleCTO,
		NodeID: "node-1",
		Candidates: []RoleModel{
			{Harness: "codex", Provider: "openai", Model: "gpt-6.1-sol", Effort: "high"},
			{Name: "opus", Harness: "claude", Model: "opus", DefaultEffort: "high", Failed: true},
		},
		Current: RoleModel{Harness: "claude", Provider: "anthropic", Model: "opus", Effort: "medium"},
		Resume:  true,
	}
	got, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"role":"cto","node_id":"node-1","candidates":[{"harness":"codex","provider":"openai","model":"gpt-6.1-sol","effort":"high"},{"harness":"claude","model":"opus"}],"current":{"harness":"claude","provider":"anthropic","model":"opus","effort":"medium"},"resume":true}`
	if string(got) != want {
		t.Fatalf("role pick request\n got %s\nwant %s", got, want)
	}
	if _, err := json.Marshal(RolePickRequest{Role: RoleChief}); err != nil {
		t.Fatal(err)
	}
	settings := CTOSettings{
		Model:   "opus",
		Harness: "claude",
		Models:  []RoleModel{{Harness: "codex", Provider: "openai", Model: "gpt-6.1-sol", Effort: "high"}},
	}
	got, err = json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	want = `{"model":"opus","harness":"claude","opencode_model":"","claude_model":"","models":[{"harness":"codex","provider":"openai","model":"gpt-6.1-sol","effort":"high"}]}`
	if string(got) != want {
		t.Fatalf("cto settings\n got %s\nwant %s", got, want)
	}
}
