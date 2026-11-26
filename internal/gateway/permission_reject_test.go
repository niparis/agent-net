package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO rejects a pending permission proposal on the owner's quoted words.
// The reason is recorded and the proposal leaves the pending list.
func TestPermissionRejectOnOwnerWords(t *testing.T) {
	s, node, session := permissionRig(t)
	res, err := s.ProposePermission(protocol.PermissionProposeFrame{
		SessionID: session,
		PermissionProposeRequest: protocol.PermissionProposeRequest{
			Rules: []string{"Bash(gh pr merge *)"}, Targets: []string{"forge"}, Reason: "the forge repos need PR merges",
		},
	}, node)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingPermissionProposals()
	if err != nil || len(pending) != 1 || pending[0].ID != res.ID {
		t.Fatalf("pending before reject: %+v %v", pending, err)
	}
	words := "no, do not allow that"
	got, err := s.RejectPermissionOnOwnerWords(protocol.PermissionRejectFrame{
		Request:       protocol.PermissionRejectRequest{ID: res.ID, Reason: "the rule is too broad", OwnerWords: words},
		By:            "cto",
		WordsVerified: true,
	})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if got.Status != "rejected" || got.DecidedBy != "cto" || got.OwnerWords != words || got.DecidedReason != "the rule is too broad" {
		t.Fatalf("stored rejection: %+v", got)
	}
	pending, err = s.PendingPermissionProposals()
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after reject: %+v %v", pending, err)
	}
	if _, err := s.RejectPermissionOnOwnerWords(protocol.PermissionRejectFrame{
		Request: protocol.PermissionRejectRequest{ID: res.ID, Reason: "again", OwnerWords: words}, WordsVerified: true,
	}); err == nil || !strings.Contains(err.Error(), "not pending") {
		t.Fatalf("reject twice: %v", err)
	}
}

// A reject needs the owner's verified words and the pending proposal id.
func TestPermissionRejectNeedsVerifiedOwnerWords(t *testing.T) {
	s, node, session := permissionRig(t)
	res, err := s.ProposePermission(protocol.PermissionProposeFrame{
		SessionID:                session,
		PermissionProposeRequest: protocol.PermissionProposeRequest{Rules: []string{"Bash(make *)"}, Targets: []string{"forge"}, Reason: "make"},
	}, node)
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range []protocol.PermissionRejectFrame{
		{Request: protocol.PermissionRejectRequest{ID: res.ID}, WordsVerified: true},
		{Request: protocol.PermissionRejectRequest{ID: res.ID, OwnerWords: "no"}, WordsVerified: false},
		{Request: protocol.PermissionRejectRequest{ID: "pp_missing", OwnerWords: "no"}, WordsVerified: true},
	} {
		if _, err := s.RejectPermissionOnOwnerWords(frame); err == nil {
			t.Fatalf("reject was accepted: %+v", frame)
		}
	}
	if _, err := s.PendingPermissionProposals(); err != nil {
		t.Fatal(err)
	}
}
