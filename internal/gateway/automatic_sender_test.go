package gateway

import (
	"context"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestNativeRemovalUsesAgentNetSender(t *testing.T) {
	s, _ := newTestStore(t)
	_, b := twoSessions(t, s)
	owner := ownerSession(t, s)
	a, _, err := s.Agent(b)
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s}
	got := g.noticeNativeRemoved(context.Background(), protocol.MCPImportCandidate{NodeID: a.NodeID, Harness: a.Harness}, "example", "plan_test", owner)
	if len(got) != 1 {
		t.Fatalf("notified: %v", got)
	}
	var sender, kind string
	if err := s.db.QueryRow("SELECT sender,type FROM messages WHERE recipient=? AND idem_key LIKE 'native-remove:%'", b).Scan(&sender, &kind); err != nil {
		t.Fatal(err)
	}
	if sender != "agent-net" || kind != protocol.TypeNotification {
		t.Fatalf("sender=%s type=%s", sender, kind)
	}
}

func TestAutomaticMessagesKeepRealOwnerSender(t *testing.T) {
	s, _ := newTestStore(t)
	_, b := twoSessions(t, s)
	owner := ownerSession(t, s)
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: b, Body: "the owner's words", Type: protocol.TypeRequest})
	if err != nil || m.From != owner {
		t.Fatalf("real owner send: %+v %v", m, err)
	}
}

func TestAutomaticWorkerTaskHandbackToOwner(t *testing.T) {
	s, _ := newTestStore(t)
	_, worker := twoSessions(t, s)
	owner := ownerSession(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET worker=? WHERE id=?`, `{"owner":"`+owner+`"}`, worker); err != nil {
		t.Fatal(err)
	}
	task, _, err := s.storeAutomaticMessage(protocol.MsgSend{Recipient: worker, Type: protocol.TypeRequest, Body: "automatic phrasing task", WorkerTask: true})
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: worker, Recipient: owner, ParentID: task.ID, Body: "[agent-net worker notice] done", WorkerNotice: true})
	if err != nil || m.ParentID != task.ID {
		t.Fatalf("handback: %+v %v", m, err)
	}
}

func TestAutomaticWorkerTaskRejectsEndedWorker(t *testing.T) {
	s, _ := newTestStore(t)
	_, worker := twoSessions(t, s)
	owner := ownerSession(t, s)
	if _, err := s.db.Exec(`UPDATE sessions SET worker=? WHERE id=?`, `{"owner":"`+owner+`"}`, worker); err != nil {
		t.Fatal(err)
	}
	if err := s.SetWorkerSessionEnded(mustNode(t, s, worker), worker); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.storeAutomaticMessage(protocol.MsgSend{Recipient: worker, Type: protocol.TypeRequest, Body: "automatic task", WorkerTask: true}); err == nil {
		t.Fatal("stored task for ended worker")
	}
}

func TestSessionStartFailureNoticeKeepsMCPDetails(t *testing.T) {
	s, _ := newTestStore(t)
	_, requester := twoSessions(t, s)
	owner := ownerSession(t, s)
	detail := strings.Repeat("long launch path/", 20) + "tenebrae-library blockedjob mcp_servers"
	m, err := s.sessionStartFailureNotice(protocol.SessionStartFailure{Owner: owner, Requester: requester, StartID: "start_mcp", ErrorCode: protocol.StartErrorMCPApprovalNeeded, Error: detail})
	if err != nil {
		t.Fatal(err)
	}
	if m.From != "agent-net" || !strings.Contains(m.Body, "tenebrae-library blockedjob mcp_servers") {
		t.Fatalf("failure notice lost actionable details: %q", m.Body)
	}
}
