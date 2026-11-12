package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCTONetworkSession(t *testing.T) {
	s, _ := newTestStore(t)
	agent, peer := twoSessions(t, s)
	node := mustNode(t, s, agent)
	if err := s.SetCTOTitle("Atlas"); err != nil {
		t.Fatal(err)
	}
	id, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.RegisterCTOSession(node)
	if err != nil || again != id {
		t.Fatalf("unstable ID: %s %s %v", id, again, err)
	}
	a, ok, err := s.Agent(id)
	if err != nil || !ok || a.Profile.Name != "CTO" || a.Harness != "cto" {
		t.Fatalf("directory: %+v %v", a, err)
	}
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	for _, sender := range []string{agent, peer, owner} {
		if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: sender, Recipient: peer, Body: "forged", FromCTO: true}); err == nil {
			t.Fatal("accepted other session flag")
		}
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: id, Recipient: peer, Body: "approved", FromCTO: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenameSession(id, "custom"); err == nil {
		t.Fatal("renamed CTO")
	}
	if _, err := s.RenameSession(agent, "CTO"); err == nil {
		t.Fatal("claimed reserved name")
	}
	if _, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "cto", NativeID: "forged"}); err == nil {
		t.Fatal("forged registration")
	}
	if _, err := s.SyncSessions(node, nil); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(id)
	if !a.Online {
		t.Fatal("sync removed CTO")
	}
}

func TestCTORegistrationRequiresOwnerAuthority(t *testing.T) {
	g, node := scopeGateway(t)
	owner, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	for _, caller := range []string{"", agent} {
		raw, _ := json.Marshal(map[string]string{"owner": caller})
		if _, err := g.handle(context.Background(), &nodeConn{id: node}, "cto.session.register", raw); err == nil {
			t.Fatalf("accepted caller %q", caller)
		}
	}
	raw, _ := json.Marshal(map[string]string{"owner": owner})
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "cto.session.register", raw); err != nil {
		t.Fatal(err)
	}
	other, _, err := g.store.CreateNode("other", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.handle(context.Background(), &nodeConn{id: other}, "cto.session.register", raw); err == nil {
		t.Fatal("accepted another node's owner")
	}
}

func TestCTOSessionMovesWithoutChangingIdentity(t *testing.T) {
	s, _ := newTestStore(t)
	agent, peer := twoSessions(t, s)
	node := mustNode(t, s, agent)
	id, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: id, Recipient: peer, Body: "before move"})
	if err != nil {
		t.Fatal(err)
	}
	other := mustNode(t, s, peer)
	if other == node {
		t.Fatal("fixture needs different nodes")
	}
	moved, err := s.RegisterCTOSession(other)
	if err != nil || moved != id {
		t.Fatalf("move: %s %v", moved, err)
	}
	a, _, err := s.Agent(id)
	if err != nil || a.NodeID != other || a.Profile.Name != "CTO" {
		t.Fatalf("moved directory: %+v %v", a, err)
	}
	saved, ok, err := s.Message(m.ID)
	if err != nil || !ok || saved.From != moved {
		t.Fatalf("lost conversation: %+v %v", saved, err)
	}
}

func TestCTOReservedNameUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	oldVersion := 0
	for i, migration := range migrations {
		if strings.Contains(migration, "-- Reserve CTO for the network session") {
			oldVersion = i
			break
		}
	}
	if oldVersion == 0 {
		t.Fatal("reserved CTO name migration missing")
	}
	// Avoid syncing each old-schema statement; OpenStore still runs the upgrade.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, migration := range migrations[:oldVersion] {
		if _, err := tx.Exec(migration); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_version(version INTEGER NOT NULL); INSERT INTO schema_version VALUES(?)`, oldVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO nodes(id,hostname,os,token_hash,created_at) VALUES('n_old','test','linux','hash',1); INSERT INTO sessions(id,node_id,harness,native_id,name,last_seen,online) VALUES('s_old','n_old','pi','native','CTO',1,1); INSERT INTO profiles(session_id,name,updated_at) VALUES('s_old','CTO',1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _, err := s.Agent("s_old")
	if err != nil {
		t.Fatal(err)
	}
	if a.Profile.Name == "CTO" {
		t.Fatal("an old session retained the reserved CTO name")
	}
}

func TestCTOLegacyReplyRoutesToRegisteredSession(t *testing.T) {
	s, _ := newTestStore(t)
	agent, peer := twoSessions(t, s)
	node := mustNode(t, s, agent)
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	root, _, err := s.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: peer, Body: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE owner_settings SET updated_at=? WHERE key='cto_legacy_route_start'`, s.now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE messages SET sender_cto=1,created_at=? WHERE id=?`, s.now().Add(-time.Second).UnixNano(), root.ID); err != nil {
		t.Fatal(err)
	}
	cto, err := s.RegisterCTOSession(node)
	if err != nil {
		t.Fatal(err)
	}
	reply, _, err := s.StoreMessage(protocol.MsgSend{Sender: peer, Recipient: owner, ParentID: root.ID, Body: "answer"})
	if err != nil {
		t.Fatal(err)
	}
	if reply.To != cto {
		t.Fatalf("reply went to %s, want CTO %s", reply.To, cto)
	}
	answer, _, err := s.StoreMessage(protocol.MsgSend{Sender: cto, Recipient: peer, ParentID: reply.ID, Body: "follow up"})
	if err != nil || answer.To != peer {
		t.Fatalf("CTO follow up: %+v %v", answer, err)
	}
	if _, err := s.db.Exec(`UPDATE owner_settings SET updated_at=? WHERE key='cto_legacy_route_start'`, s.now().Add(-31*24*time.Hour).UnixNano()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE messages SET created_at=? WHERE id=?`, s.now().Add(-32*24*time.Hour).UnixNano(), root.ID); err != nil {
		t.Fatal(err)
	}
	expired, _, err := s.StoreMessage(protocol.MsgSend{Sender: peer, Recipient: owner, ParentID: root.ID, Body: "late reply"})
	if err != nil || expired.To != owner {
		t.Fatalf("expired route: %+v %v", expired, err)
	}
}

func TestCTOLegacyWindowStartsOnGatewayUpgrade(t *testing.T) {
	s, _ := newTestStore(t)
	var started int64
	if err := s.db.QueryRow(`SELECT updated_at FROM owner_settings WHERE key='cto_legacy_route_start'`).Scan(&started); err != nil || started == 0 {
		t.Fatalf("upgrade timestamp: %d %v", started, err)
	}
}
