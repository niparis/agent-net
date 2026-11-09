package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func permissionRig(t *testing.T) (*Store, string, string) {
	t.Helper()
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "na", Name: "forge-agent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddProjects(node, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "forge", Path: "/tmp/forge"}}}); err != nil {
		t.Fatal(err)
	}
	return s, node, session
}

// An unregistered target is refused at proposal time and named.
func TestPermissionProposalRejectsUnregisteredTarget(t *testing.T) {
	s, _, session := permissionRig(t)
	_, err := s.ProposePermission(protocol.PermissionProposeFrame{
		SessionID: session,
		PermissionProposeRequest: protocol.PermissionProposeRequest{
			Rules: []string{"Bash(gh pr merge *)"}, Targets: []string{"forge", "missing-repo"}, Reason: "merge PRs",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "missing-repo") {
		t.Fatalf("unregistered target: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM permission_proposals`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("proposals stored after a refused target: %d %v", n, err)
	}
	var items int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items`).Scan(&items); err != nil || items != 0 {
		t.Fatalf("asks stored after a refused target: %d %v", items, err)
	}
}

// The ask lists every rule, target, reason and proposer, waits for the owner,
// and neither the chief nor the CTO can take it over.
func TestPermissionAskIsOwnerOnlyAndRouteFixed(t *testing.T) {
	s, _, session := permissionRig(t)
	res, err := s.ProposePermission(protocol.PermissionProposeFrame{
		SessionID: session,
		PermissionProposeRequest: protocol.PermissionProposeRequest{
			Rules:   []string{"Bash(gh pr merge *)", "Bash(gh pr close *)"},
			Targets: []string{"forge"},
			Reason:  "the forge repos need PR merges",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "pending" || len(res.Targets) != 1 || res.Targets[0].Name != "forge" || res.Targets[0].Path != "/tmp/forge" {
		t.Fatalf("proposal answer: %+v", res)
	}
	var askID, text, detail, next string
	var fixed int
	if err := s.db.QueryRow(`SELECT id,text,detail,next_who,route_fixed FROM inbox_items WHERE source_ref=?`,
		permissionAskPrefix+res.ID).Scan(&askID, &text, &detail, &next, &fixed); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Bash(gh pr merge *)", "Bash(gh pr close *)", "forge", "box", "/tmp/forge",
		"the forge repos need PR merges", "forge-agent", "restart"} {
		if !strings.Contains(text, want) || !strings.Contains(detail, want) {
			t.Errorf("ask does not name %q:\n%s", want, text)
		}
	}
	if next != protocol.AskNextOwner || fixed != 1 {
		t.Fatalf("ask next=%q route_fixed=%d, want owner and fixed", next, fixed)
	}
	if n, err := adoptChiefAsks(s.db, "s_chief", s.now().UnixNano()); err != nil || n != 0 {
		t.Fatalf("chief adopted the permission ask: %d %v", n, err)
	}
	if _, err := s.AnswerInboxAs(askID, "approve", "cto"); err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("the CTO answered a permission ask: %v", err)
	}
	p, err := s.PermissionProposal(res.ID)
	if err != nil || p.Status != "pending" {
		t.Fatalf("proposal after the CTO answer: %+v %v", p, err)
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM inbox_items WHERE id=?`, askID).Scan(&status); err != nil || status != "open" {
		t.Fatalf("ask after the CTO answer: %q %v", status, err)
	}
	// The owner's answer is accepted.
	if _, err := s.AnswerInboxAs(askID, "approve", "owner"); err != nil {
		t.Fatal(err)
	}
	if p, err = s.PermissionProposal(res.ID); err != nil || p.Status != "approved" {
		t.Fatalf("proposal after the owner answer: %+v %v", p, err)
	}
}

// The approval log keeps its undo option until the undo settles it.
func TestPermissionResultLogSettlesOnUndo(t *testing.T) {
	s, _, session := permissionRig(t)
	res, err := s.ProposePermission(protocol.PermissionProposeFrame{
		SessionID: session,
		PermissionProposeRequest: protocol.PermissionProposeRequest{
			Rules: []string{"Bash(gh pr merge *)"}, Targets: []string{"forge"}, Reason: "merge PRs",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	results := []protocol.PermissionApplyResult{{Target: res.Targets[0], Status: "applied", Added: []string{"Bash(gh pr merge *)"}}}
	if err := s.RecordPermissionResult(res.ID, results); err != nil {
		t.Fatal(err)
	}
	log, err := s.DeskLog(5)
	if err != nil || len(log) != 1 || len(log[0].Options) != 2 || log[0].Options[0] != protocol.OpPermissionUndo {
		t.Fatalf("log after the approval: %+v %v", log, err)
	}
	if err := s.MarkPermissionUndone(res.ID, []protocol.PermissionApplyResult{{Target: res.Targets[0], Status: "removed", Removed: []string{"Bash(gh pr merge *)"}}}); err != nil {
		t.Fatal(err)
	}
	log, err = s.DeskLog(5)
	if err != nil || len(log) != 1 || !strings.HasPrefix(log[0].Text, "Undone: ") || len(log[0].Options) != 0 {
		t.Fatalf("log after the undo: %+v %v", log, err)
	}
}

// Dismissing the ask rejects the proposal instead of leaving it pending.
func TestPermissionAskDismissRejects(t *testing.T) {
	s, _, session := permissionRig(t)
	res, err := s.ProposePermission(protocol.PermissionProposeFrame{
		SessionID: session,
		PermissionProposeRequest: protocol.PermissionProposeRequest{
			Rules: []string{"Bash(gh pr close *)"}, Targets: []string{"forge"}, Reason: "close PRs",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var askID string
	if err := s.db.QueryRow(`SELECT id FROM inbox_items WHERE source_ref=?`, permissionAskPrefix+res.ID).Scan(&askID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AnswerInbox(askID, "dismiss"); err != nil {
		t.Fatal(err)
	}
	if p, err := s.PermissionProposal(res.ID); err != nil || p.Status != "rejected" {
		t.Fatalf("dismissed proposal: %+v %v", p, err)
	}
}
