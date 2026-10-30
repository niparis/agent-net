package gateway

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func nodeNamed(t *testing.T, s *Store, id string) protocol.NodeView {
	t.Helper()
	ns, err := s.Nodes()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range ns {
		if n.NodeID == id {
			return n
		}
	}
	t.Fatalf("node %s not listed", id)
	return protocol.NodeView{}
}

func TestDisplayNameIsKeptAcrossHellosAndShownEverywhere(t *testing.T) {
	s, _ := newTestStore(t)
	id, _, _ := s.CreateNode("Johns-MacBook-Pro-950", "darwin")
	s.SetNodeHello(id, "Johns-MacBook-Pro-950", "v1")
	sid, err := s.UpsertSession(id, protocol.SessionInfo{Harness: "claude", NativeID: "na", Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := s.SetNodeDisplayName(id, "  laptop-1 ")
	if err != nil || res.Hostname != "laptop-1" || res.DisplayName != "laptop-1" || res.Reported != "Johns-MacBook-Pro-950" {
		t.Fatalf("set: %+v %v", res, err)
	}
	n := nodeNamed(t, s, id)
	if n.Hostname != "laptop-1" || n.DisplayName != "laptop-1" || n.Reported != "Johns-MacBook-Pro-950" {
		t.Fatalf("node list: %+v", n)
	}
	if a := agentOf(t, s, sid); a.Hostname != "laptop-1" {
		t.Fatalf("agent list shows %q", a.Hostname)
	}
	if h := s.hostname(id); h != "laptop-1" {
		t.Fatalf("hostname(): %q", h)
	}
	if ids, _ := s.nodeIDs("laptop-1"); len(ids) != 1 || ids[0] != id {
		t.Fatalf("lookup by the display name: %v", ids)
	}

	// The daemon says hello again, with a new reported name: the display name
	// stays, the reported name is updated.
	s.SetNodeHello(id, "Johns-MacBook-Pro-951.local", "v2")
	n = nodeNamed(t, s, id)
	if n.Hostname != "laptop-1" || n.Reported != "Johns-MacBook-Pro-951.local" {
		t.Fatalf("after hello: %+v", n)
	}
	if a := agentOf(t, s, sid); a.Hostname != "laptop-1" {
		t.Fatalf("agent list after hello shows %q", a.Hostname)
	}

	// Clearing goes back to the reported name.
	res, err = s.SetNodeDisplayName(id, "")
	if err != nil || res.Hostname != "Johns-MacBook-Pro-951.local" || res.DisplayName != "" {
		t.Fatalf("clear: %+v %v", res, err)
	}
	if n = nodeNamed(t, s, id); n.Hostname != "Johns-MacBook-Pro-951.local" || n.DisplayName != "" {
		t.Fatalf("after clear: %+v", n)
	}
	s.SetNodeHello(id, "other-name", "v2")
	if n = nodeNamed(t, s, id); n.Hostname != "other-name" {
		t.Fatalf("a cleared name must follow the reported one: %+v", n)
	}
}

func TestDisplayNameIsUniqueAndChecked(t *testing.T) {
	s, _ := newTestStore(t)
	a, _, _ := s.CreateNode("box", "linux")
	b, _, _ := s.CreateNode("desk", "linux")
	if _, err := s.SetNodeDisplayName(a, "laptop-1"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"laptop-1", "LAPTOP-1"} {
		if _, err := s.SetNodeDisplayName(b, name); err == nil {
			t.Errorf("%q was accepted for a second machine", name)
		}
	}
	// A name the node already has is fine for it.
	if _, err := s.SetNodeDisplayName(a, "laptop-1"); err != nil {
		t.Errorf("same name again: %v", err)
	}
	// The name another machine reports is taken too.
	s.CreateNode("plain", "linux")
	if _, err := s.SetNodeDisplayName(b, "PLAIN"); err == nil {
		t.Error("the reported name of another machine was accepted")
	}

	bad := []string{strings.Repeat("a", 64), "-x", ".x", "_x", "a b", "a/b", "a@b", "a:b", "é", "a\nb", "a;b"}
	for _, name := range bad {
		_, err := s.SetNodeDisplayName(b, name)
		var en ErrNodeName
		if !errors.As(err, &en) {
			t.Errorf("%q: %v", name, err)
		}
	}
	for _, name := range []string{strings.Repeat("a", 63), "9", "a_b.c-d", "Mac1"} {
		if _, err := s.SetNodeDisplayName(b, name); err != nil {
			t.Errorf("%q refused: %v", name, err)
		}
	}
	if _, err := s.SetNodeDisplayName("n_nope", "x"); err == nil {
		t.Error("unknown node accepted")
	}
}

func TestMigrationBackfillsReportedHostname(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gw.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL)`)
	for i := 0; i < len(migrations)-1; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatal(err)
		}
		db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1)
	}
	db.Exec(`INSERT INTO nodes (id, hostname, os, token_hash, created_at, seen_at) VALUES ('n_1','old-host','linux','h',1,1)`)
	db.Close()
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n := nodeNamed(t, s, "n_1"); n.Reported != "old-host" || n.Hostname != "old-host" || n.DisplayName != "" {
		t.Fatalf("after migration: %+v", n)
	}
}
