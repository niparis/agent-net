package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestOwnerUseAndPinSnapshot(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	id := shopSession(t, s, node, "owner-use")
	if err := s.RecordOwnerUse(id); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionPin(id, true); err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.Agent(id)
	if err != nil || !ok || a.OwnerUses != 1 || a.OwnerUsedAt != c.t.Unix() || a.PinnedAt != c.t.Unix() {
		t.Fatalf("snapshot: %+v %v", a, err)
	}
	c.t = c.t.Add(8 * 24 * time.Hour)
	a, _, _ = s.Agent(id)
	if a.OwnerUses != 0 {
		t.Fatalf("score did not decay on read: %+v", a)
	}
	if err := s.RecordOwnerUse(id); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(id)
	if a.OwnerUses != 1 {
		t.Fatalf("count did not reset: %+v", a)
	}
	if err := s.SetSessionPin(id, false); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(id)
	if a.PinnedAt != 0 {
		t.Fatal("unpin failed")
	}
}

func TestOwnerUseMessages(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	owner, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: "owner"})
	peer := shopSession(t, s, node, "peer")
	target := shopSession(t, s, node, "target")
	for _, from := range []string{peer, owner} {
		if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: from, Recipient: target, Body: "hello", IdempotencyKey: from}); err != nil {
			t.Fatal(err)
		}
	}
	a, _, _ := s.Agent(target)
	if a.OwnerUses != 1 {
		t.Fatalf("peer counted or owner missed: %+v", a)
	}
	if _, _, err := s.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: target, Body: "hello", IdempotencyKey: owner}); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(target)
	if a.OwnerUses != 1 {
		t.Fatal("retry counted twice")
	}
}

func TestAutoArchiveOwnerUse(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	ids := map[string]string{}
	for _, name := range []string{"temp", "stale", "pinned", "recent", "active", "online", "merger", "lookalike", "cto", "owner", "bot", "temp-active"} {
		id := shopSession(t, s, node, name)
		ids[name] = id
		if name != "online" {
			if err := s.SetSessionOffline(node, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	old := c.t.Add(-8 * 24 * time.Hour).UnixNano()
	if _, err := s.db.Exec(`UPDATE sessions SET cwd='/code/project',last_active=?,last_seen=?,created_at=?`, old, old, old); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"temp", "pinned", "recent", "online", "merger", "cto", "owner", "bot", "temp-active"} {
		if _, err := s.db.Exec(`UPDATE sessions SET cwd='/tmp/example' WHERE id=?`, ids[name]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE sessions SET cwd='/tmp-other/example',last_active=? WHERE id=?`, c.t.UnixNano(), ids["lookalike"]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET last_active=? WHERE id=?`, c.t.UnixNano(), ids["active"]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE sessions SET last_active=? WHERE id=?`, c.t.UnixNano(), ids["temp-active"]); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"cto", "owner", "bot"} {
		if _, err := s.db.Exec(`UPDATE sessions SET harness=? WHERE id=?`, role, ids[role]); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordOwnerUse(ids["recent"]); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionPin(ids["pinned"], true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO project_mergers(project,node_id,session_id) VALUES('shop',?,?)`, node, ids["merger"]); err != nil {
		t.Fatal(err)
	}
	archived, err := s.AutoArchiveSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 3 {
		t.Fatalf("archive %v", archived)
	}
	for name, id := range ids {
		var stamp int64
		if err := s.db.QueryRow(`SELECT archived_at FROM sessions WHERE id=?`, id).Scan(&stamp); err != nil {
			t.Fatal(err)
		}
		want := name == "temp" || name == "stale" || name == "temp-active"
		if (stamp != 0) != want {
			t.Errorf("%s archived=%v", name, stamp != 0)
		}
	}
	if _, err = s.ArchiveSessions(protocol.SessionsArchiveRequest{IDs: []string{ids["temp"]}, Restore: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AutoArchiveSessions(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Agent(ids["temp"]); !ok {
		t.Fatal("restored session was immediately archived again")
	}
}

func TestOwnerUsePinGatewayAuthority(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	other, _, _ := s.CreateNode("other", "linux")
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: protocol.OwnerHarness, NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	peer := shopSession(t, s, node, "peer")
	target := shopSession(t, s, other, "target")
	g := &Gateway{store: s}
	for _, tc := range []struct {
		node, owner string
		allow       bool
	}{{node, peer, false}, {other, owner, false}, {node, owner, true}} {
		raw, _ := json.Marshal(map[string]any{"owner": tc.owner, "session_id": target, "pinned": true})
		_, err := g.handle(context.Background(), &nodeConn{id: tc.node}, "session.pin", raw)
		if (err == nil) != tc.allow {
			t.Errorf("owner %s: %v", tc.owner, err)
		}
	}
	a, _, _ := s.Agent(target)
	if a.PinnedAt == 0 {
		t.Fatal("owner pin absent")
	}
	raw, _ := json.Marshal(map[string]any{"owner": owner, "session_id": target, "pinned": false})
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "session.pin", raw); err != nil {
		t.Fatal(err)
	}
	a, _, _ = s.Agent(target)
	if a.PinnedAt != 0 {
		t.Fatal("owner unpin failed")
	}
}

func TestOwnerUseNonWorkerCleanupKeepsPinsAndRecentUse(t *testing.T) {
	s, c := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	for _, kind := range []string{"pinned", "recent"} {
		id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: kind, Cwd: "/workers/example"})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetSessionOffline(node, id); err != nil {
			t.Fatal(err)
		}
		if kind == "pinned" {
			err = s.SetSessionPin(id, true)
		} else {
			err = s.RecordOwnerUse(id)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(`UPDATE sessions SET last_seen=? WHERE id=?`, c.t.Add(-30*24*time.Hour).UnixNano(), id); err != nil {
			t.Fatal(err)
		}
		if _, err = s.CleanWorktreeSessions(node, "/workers"); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Prune(Retention{Sessions: 24 * time.Hour}); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := s.Agent(id); err != nil || !ok {
			t.Fatalf("%s session removed: %v", kind, err)
		}
	}
}

func TestOwnerUseDoesNotArchiveBeforeReconnect(t *testing.T) {
	g, err := New(Options{DataDir: seededDataDir(t), Retention: DefaultRetention()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	node, _, err := g.store.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	id, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "live", Cwd: "/tmp/live"})
	if err != nil {
		t.Fatal(err)
	}
	if err = g.store.SetAllOffline(); err != nil {
		t.Fatal(err)
	}
	stale, err := g.store.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "stale", Cwd: "/code/old"})
	if err != nil {
		t.Fatal(err)
	}
	if err = g.store.SetSessionOffline(node, stale); err != nil {
		t.Fatal(err)
	}
	old := g.store.now().Add(-40 * 24 * time.Hour).UnixNano()
	if _, err = g.store.db.Exec(`UPDATE sessions SET last_seen=?,last_active=?,created_at=? WHERE id=?`, old, old, old, stale); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g.RunExpiry(ctx)
	g.RunRetention(ctx)
	if _, ok, err := g.store.Agent(id); err != nil || !ok {
		t.Fatalf("session archived before it could reconnect: %v", err)
	}
	if _, ok, err := g.store.Agent(stale); err != nil || !ok {
		t.Fatalf("stale session deleted before archive: %v", err)
	}
	if _, err := g.store.AutoArchiveSessions(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := g.store.Agent(stale); ok {
		t.Fatal("stale session not archived")
	}

}
