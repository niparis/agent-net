package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/settings"
)

func TestDeliveryUsesLiveRecipe(t *testing.T) {
	snap := settings.Snapshot{Values: []settings.Value{
		{Key: "delivery.merge_authority", Level: "network", Scope: "network", Value: json.RawMessage(`"ask"`)},
		{Key: "delivery.merge_text_ask", Level: "network", Scope: "network", Value: json.RawMessage(`"custom merge approval"`)},
		{Key: "delivery.worker_branch_prep", Level: "network", Scope: "network", Value: json.RawMessage(`" Prepare this branch."`)},
		{Key: "delivery.caller_review_and_merge", Level: "network", Scope: "network", Value: json.RawMessage(`"review {{where}} carefully"`)},
	}}
	d := NodeDefaultsWithSettings("", nil, snap, "repo")
	if d.MergeAuthority != "ask" || d.MergeText() != "custom merge approval" {
		t.Fatalf("defaults: %+v %q", d, d.MergeText())
	}
	if got := MergeApprovalForWithSettings(Graph{}, "missing", snap); got.Authority != "ask" || got.Text != "custom merge approval" {
		t.Fatalf("no graph node: %+v", got)
	}
	if got := WorkerTaskBodyWithSettings("codex", "task", "/tmp/worktree", "branch", "", "", "sandboxed", snap); !strings.Contains(got, " Prepare this branch.") {
		t.Fatalf("brief: %s", got)
	}
	if got := FormatWorkerNoticeWithSettings(WorkerNotice{Worktree: "/tmp/worktree"}, "", snap); !strings.Contains(got, "review the diff in /tmp/worktree carefully") {
		t.Fatalf("notice: %s", got)
	}
}

func TestDeliveryNoteUsesGatewayRuleText(t *testing.T) {
	note := AuthorityNoteWithRule(true, false, "ask", "custom merge rule")
	if !strings.Contains(note, "custom merge rule") || strings.Contains(note, MergeAskText) {
		t.Fatal(note)
	}
	if got := AuthorityNoteWithRule(true, false, "ask", ""); strings.Contains(got, MergeAskText) {
		t.Fatalf("empty rule was replaced: %s", got)
	}
	owner := OwnerInstructionNoteWithRule("please merge", "ask", "custom merge rule")
	if !strings.Contains(owner, "custom merge rule") {
		t.Fatal(owner)
	}
}

func TestDeliveryBranchPrefixFromSettings(t *testing.T) {
	snap := settings.Snapshot{Values: []settings.Value{{Key: "delivery.worker_branch_prefix", Level: "network", Scope: "network", Value: json.RawMessage(`"worker/"`)}}}
	if got := WorktreeBranchWithSettings("w_1", snap); got != "worker/w_1" {
		t.Fatal(got)
	}
}

func TestDeliveryContextUsesSnapshotText(t *testing.T) {
	snap := settings.Snapshot{Recipe: "current", Values: []settings.Value{{Key: "delivery.merge_text_ask", Level: "network", Scope: "network", Value: json.RawMessage(`"custom rule"`)}, {Key: "delivery.merge_authority", Level: "repo", Scope: "repo", Value: json.RawMessage(`"ask"`)}}}
	g := Graph{Nodes: []Node{{Name: "repo", Charter: Charter{Maturity: "growing"}}}}
	if got := NodeContextWithSettings(g, "repo", snap); !strings.Contains(got, "custom rule") {
		t.Fatal(got)
	}
	if got := (MaturityDefaults{TestsBeforeMerge: "invalid"}).TestText(); got != "run the suite" {
		t.Fatalf("invalid test rule: %q", got)
	}
}

func TestDeliveryDelegatedTextMatchesApproverModes(t *testing.T) {
	snap := settings.Snapshot{Values: []settings.Value{{Key: "delivery.delegated_approver_modes", Level: "network", Scope: "network", Value: json.RawMessage(`["advisor"]`)}}}
	if got := MergeTextWithSettings(MergeDelegated, snap); !strings.Contains(got, "mode advisor") || strings.Contains(got, "owner or arbiter") {
		t.Fatal(got)
	}
	g := Graph{Nodes: []Node{{Name: "repo", Charter: Charter{Maturity: "mature"}}}}
	if got := MergeApprovalForWithSettings(g, "repo", snap).Text; !strings.Contains(got, "mode advisor") || strings.Contains(got, "owner or arbiter") {
		t.Fatal(got)
	}
}
