package protocol

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// This fixture records delivery output before the settings cutover. Each node
// override is exercised at each maturity, including an unset maturity.
func deliveryFixture() []byte {
	out := map[string]any{}
	choices := map[string][]string{
		"none": {""}, SettingTestsBeforeMerge: {"optional", "suite", "suite-new-tests", "suite-new-tests-review"},
		SettingMergeAuthority: {"full", "delegated", "ask"}, SettingWorkerSandbox: {"sandboxed", "no-network", "read-only"},
		SettingJudgeConsumers: {"free", "charter", "strict", "escalate"}, SettingDeployRule: {"brief", "ask", "owner"}, SettingAutonomy: {"off", "wake"},
	}
	for _, maturity := range []string{"", "early", "growing", "mature", "critical"} {
		for key, values := range choices {
			for _, value := range values {
				overrides := map[string]string{}
				if key != "none" {
					overrides[key] = value
				}
				d := NodeDefaults(maturity, overrides)
				g := Graph{Nodes: []Node{{Name: "repo", Charter: Charter{Maturity: maturity}, Settings: overrides}}, Edges: []Edge{{From: "lead", To: "repo", Kind: "governs", Mode: "owner"}, {From: "adviser", To: "repo", Kind: "governs", Mode: "advisor"}}}
				out[maturity+"/"+key+"/"+value] = map[string]any{"rules": d.RulesText(), "consumer": d.ConsumerText(), "approval": MergeApprovalFor(g, "repo"), "trust": AuthorityNote(true, false, d.MergeAuthority), "owner_note": OwnerInstructionNote("do work", "sender", time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC), d.MergeAuthority), "brief": WorkerTaskBodyWithCharter("codex", "do work", "/tmp/worktree", "agent-net/w_test", "", d.RulesText(), "sandboxed"), "autonomy": NodeAutonomy(overrides), "brief-claude": WorkerTaskBodyWithCharter("claude", "do work", "/tmp/worktree", "agent-net/w_test", "", d.RulesText(), "sandboxed"), "brief-agy": WorkerTaskBodyWithCharter("agy", "do work", "/tmp/worktree", "agent-net/w_test", "", d.RulesText(), "sandboxed"), "brief-opencode": WorkerTaskBodyWithCharter("opencode", "do work", "/tmp/worktree", "agent-net/w_test", "", d.RulesText(), "sandboxed"), "worker-deploy": WorkerDeployApprovalRules(Graph{Nodes: g.Nodes, Edges: []Edge{{From: "deployer", To: "repo", Kind: "deploys"}}}, "deployer")}
			}
		}
	}
	out["no-node"] = MergeApprovalFor(Graph{}, "repo")
	out["checklist-worktree"] = noticeChecklist("/tmp/worktree", true)
	out["checklist-project"] = noticeChecklist("", false)
	b, _ := json.MarshalIndent(out, "", "  ")
	return append(b, '\n')
}
func TestDeliveryBaselineFixture(t *testing.T) {
	got := deliveryFixture()
	const path = "testdata/delivery-baseline.json"
	if os.Getenv("UPDATE_DELIVERY_FIXTURE") == "1" {
		if err := os.WriteFile(path, got, 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The owner chose the network answer for an uncharted repo. Keep the
	// original observation in the baseline while checking the approved change.
	var actual, baseline map[string]json.RawMessage
	if err := json.Unmarshal(got, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &baseline); err != nil {
		t.Fatal(err)
	}
	var uncharted MergeApproval
	if err := json.Unmarshal(actual["no-node"], &uncharted); err != nil || uncharted.Authority != MergeFull || uncharted.Required {
		t.Fatalf("uncharted: %+v %v", uncharted, err)
	}
	actual["no-node"] = baseline["no-node"]
	got, _ = json.MarshalIndent(actual, "", "  ")
	got = append(got, '\n')
	if !bytes.Equal(got, want) {
		t.Fatal("delivery output differs from baseline fixture")
	}
}
