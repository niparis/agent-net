package gateway

import (
	"context"
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

// forgeGrant is a permission_apply request for the forge repo.
func forgeGrant(targets ...string) protocol.PermissionOwnerApplyRequest {
	return protocol.PermissionOwnerApplyRequest{Rules: []string{"Bash(gh pr merge *)"}, Targets: targets, OwnerWords: "let forge merge its PRs"}
}

// permission_apply grants nothing on words the daemon did not verify, and
// refuses an unregistered target by name. A refusal writes no log line.
func TestPermissionApplyRefusals(t *testing.T) {
	s, _, _ := permissionRig(t)
	g := &Gateway{store: s}
	ctx := context.Background()
	if _, err := g.ownerApplyPermission(ctx, protocol.PermissionOwnerApplyFrame{Request: forgeGrant("forge")}); err == nil || !strings.Contains(err.Error(), "not verified") {
		t.Fatalf("unverified words: %v", err)
	}
	if _, err := g.ownerApplyPermission(ctx, protocol.PermissionOwnerApplyFrame{Request: forgeGrant("forge", "missing-repo"), WordsVerified: true}); err == nil || !strings.Contains(err.Error(), "missing-repo") {
		t.Fatalf("unregistered target: %v", err)
	}
	if log, err := s.DeskLog(5); err != nil || len(log) != 0 {
		t.Fatalf("log after refusals: %+v %v", log, err)
	}
}

// A grant on the owner's verified words goes to each target machine at once
// and is logged with his words. A machine that is offline fails its target;
// nothing waits for later, so the log offers no undo.
func TestPermissionApplyLogsTheOwnerWords(t *testing.T) {
	s, node, _ := permissionRig(t)
	g := &Gateway{store: s}
	grant, err := g.ownerApplyPermission(context.Background(), protocol.PermissionOwnerApplyFrame{Request: forgeGrant("forge"), By: "cto", WordsVerified: true})
	if err != nil {
		t.Fatal(err)
	}
	if grant.Status != "failed" || grant.By != "cto" || len(grant.Results) != 1 ||
		grant.Results[0].Target.NodeID != node || grant.Results[0].Error != "machine is offline" {
		t.Fatalf("grant: %+v", grant)
	}
	log, err := s.DeskLog(5)
	if err != nil || len(log) != 1 || log[0].Source != "cto" || len(log[0].Options) != 0 ||
		!strings.Contains(log[0].Detail, `Owner's words: "let forge merge its PRs"`) || !strings.Contains(log[0].Detail, "machine is offline") {
		t.Fatalf("log: %+v %v", log, err)
	}
}

// A folder that needs trust is a plain request to the chief, sent once per
// machine and folder. It says how the CTO grants it and stores no item.
func TestTrustAskIsARequestToTheChiefOnce(t *testing.T) {
	s, node, session := permissionRig(t)
	chief, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "nc", Name: "chief"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{true, false} {
		created, err := s.RaiseTrustAsk(session, node, "box", "/tmp/forge", "forge", "", []string{"docs"})
		if err != nil || created != want {
			t.Fatalf("ask %d: created=%v err=%v", i, created, err)
		}
	}
	if n := inboxCount(t, s); n != 0 {
		t.Fatalf("%d items stored for the owner", n)
	}
	got := messagesTo(t, s, chief)
	if len(got) != 1 || got[0].from != session || got[0].typ != protocol.TypeRequest {
		t.Fatalf("messages to the chief: %+v", got)
	}
	for _, want := range []string{"/tmp/forge", "box", "forge-agent", "docs", "permission_apply (kind trust, machine box", "owner_words"} {
		if !strings.Contains(got[0].body, want) {
			t.Errorf("request does not name %q:\n%s", want, got[0].body)
		}
	}
}
