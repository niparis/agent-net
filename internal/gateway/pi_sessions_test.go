package gateway

import (
	"github.com/niparis/agent-net/internal/protocol"
	"testing"
	"time"
)

func TestPiHelpersDoNotTakeForegroundNameOrRows(t *testing.T) {
	for _, offline := range []bool{false, true} {
		t.Run(map[bool]string{false: "online", true: "offline"}[offline], func(t *testing.T) {
			s, _ := newTestStore(t)
			node, _, _ := s.CreateNode("box", "linux")
			helper := protocol.SessionInfo{Harness: "pi", NativeID: "helper", Cwd: "/work/project", PID: 4242, Mode: protocol.ModeBackground}
			child, err := s.UpsertSession(node, helper)
			if err != nil {
				t.Fatal(err)
			}
			foreground := helper
			foreground.NativeID, foreground.Mode = "foreground", protocol.ModeForeground
			id, err := s.UpsertSession(node, foreground)
			if err != nil {
				t.Fatal(err)
			}
			if offline {
				if err := s.SetSessionOffline(node, child); err != nil {
					t.Fatal(err)
				}
			}
			a := agentOf(t, s, id)
			if a.Profile.Name != "project" {
				t.Errorf("foreground name %q, want project", a.Profile.Name)
			}
			for _, query := range []protocol.QueryAgents{{}, {OnlineOnly: true}, {Text: "project"}} {
				list, err := s.Agents(query)
				if err != nil {
					t.Fatal(err)
				}
				if len(list) != 1 || list[0].SessionID != id {
					t.Errorf("query %+v: %+v", query, list)
				}
			}
			// An existing numbered foreground row is repaired by its next report.
			if _, err := s.db.Exec(`UPDATE sessions SET name='project 2' WHERE id=?`, id); err != nil {
				t.Fatal(err)
			}
			if _, err := s.UpsertSession(node, foreground); err != nil {
				t.Fatal(err)
			}
			if a := agentOf(t, s, id); a.Profile.Name != "project" {
				t.Errorf("repaired name %q", a.Profile.Name)
			}
		})
	}
}

func TestPiStandaloneWorkersAndOtherProcessesRemainVisible(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	otherNode, _, _ := s.CreateNode("other", "linux")
	for _, in := range []protocol.SessionInfo{
		{Harness: "pi", NativeID: "foreground", PID: 4242, Mode: protocol.ModeForeground},
		{Harness: "pi", NativeID: "rpc", PID: 4243, Mode: protocol.ModeBackground},
		{Harness: "pi", NativeID: "worker", PID: 4242, Mode: protocol.ModeBackground, Worker: &protocol.WorkerInfo{ID: "w_test"}},
		{Harness: "pi", NativeID: "unknown-pid", Mode: protocol.ModeBackground},
		{Harness: "claude", NativeID: "other-harness", PID: 4242, Mode: protocol.ModeBackground},
	} {
		if _, err := s.UpsertSession(node, in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.UpsertSession(otherNode, protocol.SessionInfo{Harness: "pi", NativeID: "other-node", PID: 4242, Mode: protocol.ModeBackground}); err != nil {
		t.Fatal(err)
	}
	list, err := s.Agents(protocol.QueryAgents{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 6 {
		t.Fatalf("lost standalone sessions: %+v", list)
	}
}

func TestPiLegacyHelperLifetime(t *testing.T) {
	for _, overlap := range []bool{false, true} {
		t.Run(map[bool]string{false: "reused-pid", true: "ended-foreground"}[overlap], func(t *testing.T) {
			s, clock := newTestStore(t)
			node, _, _ := s.CreateNode("box", "linux")
			helper, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "helper", PID: 4242, Mode: protocol.ModeBackground})
			if err != nil {
				t.Fatal(err)
			}
			if !overlap {
				if err := s.SetSessionOffline(node, helper); err != nil {
					t.Fatal(err)
				}
			}
			clock.t = clock.t.Add(time.Minute)
			foreground, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "foreground", PID: 4242, Mode: protocol.ModeForeground})
			if err != nil {
				t.Fatal(err)
			}
			if overlap {
				if err := s.SetSessionOffline(node, helper); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.SetSessionOffline(node, foreground); err != nil {
				t.Fatal(err)
			}
			list, err := s.Agents(protocol.QueryAgents{})
			if err != nil {
				t.Fatal(err)
			}
			want := 2
			if overlap {
				want = 1
			}
			if len(list) != want {
				t.Fatalf("got %d sessions, want %d: %+v", len(list), want, list)
			}
		})
	}
}
