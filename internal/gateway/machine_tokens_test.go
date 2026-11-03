package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestMachineTokenRemovalAndRotation(t *testing.T) {
	s, _ := newTestStore(t)
	id, old, _ := s.CreateNode("laptop", "linux")
	sid, _ := s.UpsertSession(id, protocol.SessionInfo{Harness: "pi", NativeID: "test"})
	next := protocol.NewToken()
	if err := s.stageNodeToken(id, next); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.NodeByToken(old); !ok {
		t.Fatal("old token revoked before confirmation")
	}
	if err := s.confirmNodeToken(id, next); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.NodeByToken(old); ok {
		t.Fatal("old token still works")
	}
	if got, ok, err := s.NodeByToken(next); err != nil || !ok || got != id {
		t.Fatalf("new token: %s %v %v", got, ok, err)
	}
	if err := s.removeMachine(id); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.NodeByToken(next); ok {
		t.Fatal("removed token still works")
	}
	a, ok, err := s.Agent(sid)
	if err != nil || !ok || a.Online {
		t.Fatalf("history/offline: %+v %v %v", a, ok, err)
	}
	var hash string
	var removed int
	if err := s.db.QueryRow(`SELECT token_hash,removed FROM nodes WHERE id=?`, id).Scan(&hash, &removed); err != nil || hash != "removed:"+id || removed != 1 {
		t.Fatalf("removed: %q %d %v", hash, removed, err)
	}
	if err := s.confirmNodeToken(id, next); err == nil {
		t.Fatal("removed node revived")
	}
}

func TestMachineTokenPendingReconnectConfirms(t *testing.T) {
	s, _ := newTestStore(t)
	id, old, _ := s.CreateNode("laptop", "linux")
	next := protocol.NewToken()
	if err := s.stageNodeToken(id, next); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.NodeByToken(next); err != nil || !ok || got != id {
		t.Fatalf("saved token reconnect: %s %v %v", got, ok, err)
	}
	if _, ok, _ := s.NodeByToken(old); ok {
		t.Fatal("reconnect did not confirm rotation")
	}
}

func TestMachineActionsRequireOwnerAndOnlineRotation(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("desk", "linux")
	target, _, _ := s.CreateNode("laptop", "linux")
	ownerID, _ := s.UpsertSession(node, owner("desk"))
	agent, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "test"})
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	for _, method := range []string{"machine.remove", "machine.rotate"} {
		for _, who := range []string{"", agent} {
			raw, _ := json.Marshal(map[string]string{"node_id": target, "owner": who, "actor": "owner"})
			if _, err := g.handle(context.Background(), &nodeConn{id: node}, method, raw); err == nil || !strings.Contains(err.Error(), "owner authority") {
				t.Fatalf("%s agent gate: %v", method, err)
			}
		}
	}
	raw, _ := json.Marshal(map[string]string{"node_id": target, "owner": ownerID, "actor": "owner"})
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "machine.rotate", raw); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline rotate: %v", err)
	}
	self, _ := json.Marshal(map[string]string{"node_id": node, "owner": ownerID, "actor": "owner"})
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "machine.remove", self); err == nil || !strings.Contains(err.Error(), "cannot remove itself") {
		t.Fatalf("self removal: %v", err)
	}
}

func TestMachineRemovedSessionsCannotReappear(t *testing.T) {
	s, _ := newTestStore(t)
	id, _, _ := s.CreateNode("laptop", "linux")
	info := protocol.SessionInfo{Harness: "pi", NativeID: "test"}
	_, _ = s.UpsertSession(id, info)
	if err := s.removeMachine(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSession(id, info); err == nil {
		t.Fatal("removed session became online again")
	}
	if _, err := s.SyncSessions(id, []protocol.SessionInfo{info}); err == nil {
		t.Fatal("removed session sync accepted")
	}
}

func TestMachineTokenMigrationReplay(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version=?`, len(migrations)); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
}
