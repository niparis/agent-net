package gateway

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSessionCloseRediscoveryAndActivity(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	in := protocol.SessionInfo{Harness: "opencode", NativeID: "saved", Status: "idle"}
	id, err := s.UpsertSession(node, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionClosed(node, id, true); err != nil {
		t.Fatal(err)
	}
	check := func(want int) {
		t.Helper()
		var online int
		if err := s.db.QueryRow(`SELECT online FROM sessions WHERE id=?`, id).Scan(&online); err != nil || online != want {
			t.Fatalf("online=%d want=%d err=%v", online, want, err)
		}
	}
	if _, err := s.UpsertSession(node, in); err != nil {
		t.Fatal(err)
	}
	check(0)
	in.OpenCodeActivity = time.Now().Add(time.Second).UnixMilli()
	if _, err := s.UpsertSession(node, in); err != nil {
		t.Fatal(err)
	}
	check(1)
	if err := s.SetSessionClosed(node, id, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionClosed(node, id, false); err != nil {
		t.Fatal(err)
	}
	in.OpenCodeActivity = 0
	if _, err := s.UpsertSession(node, in); err != nil {
		t.Fatal(err)
	}
	check(1)
}
