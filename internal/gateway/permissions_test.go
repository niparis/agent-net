package gateway

import (
	"context"
	"encoding/json"
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
	s, node, session := permissionRig(t)
	_, err := s.ProposePermission(protocol.PermissionProposeFrame{
		SessionID: session,
		PermissionProposeRequest: protocol.PermissionProposeRequest{
			Rules: []string{"Bash(gh pr merge *)"}, Targets: []string{"forge", "missing-repo"}, Reason: "merge PRs",
		},
	}, node)
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

// The approval log keeps its undo option until the undo settles it.
func TestPermissionResultLogSettlesOnUndo(t *testing.T) {
	s, node, session := permissionRig(t)
	res, err := s.ProposePermission(protocol.PermissionProposeFrame{
		SessionID: session,
		PermissionProposeRequest: protocol.PermissionProposeRequest{
			Rules: []string{"Bash(gh pr merge *)"}, Targets: []string{"forge"}, Reason: "merge PRs",
		},
	}, node)
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

// With no Desk (decision 0196) the owner's yes reaches the CTO in its chat,
// and the CTO finds the proposal to apply in the pending list. A decided
// proposal is not in it, and only the owner's machine may read it.
func TestPendingPermissionProposalsFrame(t *testing.T) {
	s, node, session := permissionRig(t)
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	propose := func(rule string) string {
		t.Helper()
		res, err := s.ProposePermission(protocol.PermissionProposeFrame{SessionID: session,
			PermissionProposeRequest: protocol.PermissionProposeRequest{Rules: []string{rule}, Targets: []string{"forge"}, Reason: "merge PRs"}}, node)
		if err != nil {
			t.Fatal(err)
		}
		return res.ID
	}
	decided, pending := propose("Bash(gh pr merge *)"), propose("Bash(gh pr view *)")
	if _, err := s.db.Exec(`UPDATE permission_proposals SET status='rejected' WHERE id=?`, decided); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s}
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, protocol.FramePermissionPending, []byte(`{}`)); err == nil {
		t.Fatal("pending proposals listed without the owner")
	}
	raw, _ := json.Marshal(protocol.PermissionGetFrame{Owner: owner})
	out, err := g.handle(context.Background(), &nodeConn{id: node}, protocol.FramePermissionPending, raw)
	got, ok := out.([]protocol.PermissionProposal)
	if err != nil || !ok || len(got) != 1 || got[0].ID != pending || got[0].Status != "pending" || got[0].Rules[0] != "Bash(gh pr view *)" {
		t.Fatalf("pending proposals: %#v %v", out, err)
	}
}
