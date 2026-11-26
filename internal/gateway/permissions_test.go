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
