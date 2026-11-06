package gateway

import (
	"context"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestSessionStartAgentReservation(t *testing.T) {
	for _, sync := range []bool{false, true} {
		t.Run(map[bool]string{false: "upsert", true: "sync"}[sync], func(t *testing.T) {
			s, _ := newTestStore(t)
			node, _, _ := s.CreateNode("test", "linux")
			owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "owner", NativeID: protocol.OwnerNativeID})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.AddProjects(node, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "repo", Path: "/code/repo"}, {Name: "chosen", Path: "/code/chosen"}}}); err != nil {
				t.Fatal(err)
			}
			for _, agent := range []string{"missing", "dead"} {
				if agent == "dead" {
					if _, err = s.db.Exec(`INSERT INTO agents VALUES('dead','Dead','',0,0,1)`); err != nil {
						t.Fatal(err)
					}
				}
				if err = s.ReserveSessionAgent(node, protocol.SessionAgentReservation{Owner: owner, AgentID: agent, Harness: "pi", NativeID: agent}); err == nil {
					t.Fatalf("accepted %s", agent)
				}
			}
			if err = s.ReserveSessionAgent(node, protocol.SessionAgentReservation{Owner: owner, AgentID: "chosen", Harness: "pi", NativeID: "new"}); err != nil {
				t.Fatal(err)
			}
			in := protocol.SessionInfo{Harness: "pi", NativeID: "new", Repo: protocol.RepoInfo{Project: "repo"}}
			var id string
			if sync {
				out, e := s.SyncSessions(node, []protocol.SessionInfo{in})
				err = e
				if len(out) > 0 {
					id = out[0].SessionID
				}
			} else {
				id, err = s.UpsertSession(node, in)
			}
			if err != nil {
				t.Fatal(err)
			}
			c, err := s.resolveSessionContext(id)
			if err != nil || c.Agent != "chosen" {
				t.Fatalf("first context: %+v %v", c, err)
			}
			if err = s.ReserveSessionAgent(node, protocol.SessionAgentReservation{Owner: owner, AgentID: "repo", Harness: "pi", NativeID: "new"}); err == nil {
				t.Fatal("reassigned registered identity")
			}
			id, err = s.UpsertSession(node, in)
			if err != nil {
				t.Fatal(err)
			}
			c, err = s.resolveSessionContext(id)
			if err != nil || c.Agent != "chosen" {
				t.Fatalf("sync replaced chosen: %+v %v", c, err)
			}
			in.NativeID = "old-daemon"
			id, err = s.UpsertSession(node, in)
			if err != nil {
				t.Fatal(err)
			}
			c, err = s.resolveSessionContext(id)
			if err != nil || c.Agent != "repo" {
				t.Fatalf("default: %+v %v", c, err)
			}
			if err = s.ReserveSessionAgent(node, protocol.SessionAgentReservation{Owner: id, AgentID: "chosen", Harness: "pi", NativeID: "bad"}); err == nil {
				t.Fatal("nonowner reserved")
			}
		})
	}
}

func TestSessionStartAgentRemoteNeedsCapability(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("test", "linux")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "owner", NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	remote, _, err := s.CreateNode("remote", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetNodeHello(remote, "remote", "dev"); err != nil {
		t.Fatal(err)
	}
	target := &nodeConn{}
	raw, _ := json.Marshal(protocol.SessionStartRelay{Owner: owner, NodeID: remote, SessionStartRequest: protocol.SessionStartRequest{Project: "repo", Harness: "pi", AgentID: "chosen"}})
	_, err = (&Gateway{store: s, conns: map[string]*nodeConn{remote: target}}).handleSessionRelay(context.Background(), &nodeConn{id: node}, protocol.FrameSessionStart, raw)
	if err == nil || !strings.Contains(err.Error(), "does not support Agent selection") {
		t.Fatalf("remote selection: %v", err)
	}
}

func TestSessionStartAgentLaunchHold(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("test", "linux")
	owner, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "owner", NativeID: protocol.OwnerNativeID})
	_, err := s.AddProjects(node, protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "repo", Path: "/code/repo"}, {Name: "chosen", Path: "/code/chosen"}}})
	if err != nil {
		t.Fatal(err)
	}
	in := protocol.SessionInfo{Harness: "opencode", NativeID: "old", Cwd: "/code/repo", Repo: protocol.RepoInfo{Project: "repo"}}
	old, err := s.UpsertSession(node, in)
	if err != nil {
		t.Fatal(err)
	}
	p := protocol.SessionAgentReservation{Owner: owner, Harness: "opencode", AgentID: "chosen", Ref: "start_test", Cwd: in.Cwd}
	if err = s.ReserveSessionAgent(node, p); err != nil {
		t.Fatal(err)
	}
	if held, err := s.SessionAgentPending(old); err != nil || held {
		t.Fatalf("held old session: %v %v", held, err)
	}
	in.NativeID = "new"
	id, err := s.UpsertSession(node, in)
	if err != nil {
		t.Fatal(err)
	}
	if held, err := s.SessionAgentPending(id); err != nil || !held {
		t.Fatalf("new session escaped hold: %v %v", held, err)
	}
	var provisional string
	if err = s.db.QueryRow(`SELECT COALESCE(agent_id,'') FROM sessions WHERE id=?`, id).Scan(&provisional); err != nil || provisional != "" {
		t.Fatalf("pending session guessed Agent: %q %v", provisional, err)
	}
	if _, err = s.resolveSessionContext(id); err == nil {
		t.Fatal("pending identity exposed to resolver")
	}
	m, _, err := s.StoreMessage(protocol.MsgSend{Sender: owner, Recipient: id, Body: "first message", IdempotencyKey: "first"})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.Pending(node, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range pending {
		if msg.ID == m.ID {
			t.Fatal("first message escaped before binding")
		}
	}
	// A concurrent session is held without being guessed as the selected one.
	in.NativeID, in.Cwd = "concurrent", "/code/elsewhere"
	other, err := s.UpsertSession(node, in)
	if err != nil {
		t.Fatal(err)
	}
	if held, err := s.SessionAgentPending(other); err != nil || !held {
		t.Fatalf("concurrent candidate escaped: %v %v", held, err)
	}
	// Reopening the gateway must retain the hold.
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopened.now = s.now
	s = reopened
	if held, err := s.SessionAgentPending(id); err != nil || !held {
		t.Fatalf("restart lost hold: %v %v", held, err)
	}
	p.NativeID = "old"
	if err = s.ReserveSessionAgent(node, p); err == nil {
		t.Fatal("bound an existing session")
	}
	p.NativeID = "new"
	if _, err = s.db.Exec(`UPDATE agents SET tombstone=1 WHERE id='chosen'`); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveSessionAgent(node, p); err == nil {
		t.Fatal("bound a tombstoned Agent")
	}
	if held, err := s.SessionAgentPending(id); err != nil || !held {
		t.Fatalf("failed binding released hold: %v %v", held, err)
	}
	if _, err = s.db.Exec(`UPDATE agents SET tombstone=0 WHERE id='chosen'`); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveSessionAgent(node, p); err != nil {
		t.Fatal(err)
	}
	if held, err := s.SessionAgentPending(id); err != nil || held {
		t.Fatalf("bind did not release: %v %v", held, err)
	}
	c, err := s.resolveSessionContext(id)
	if err != nil || c.Agent != "chosen" {
		t.Fatalf("first delivery context: %+v %v", c, err)
	}
	c, err = s.resolveSessionContext(other)
	if err != nil || c.Agent != "repo" {
		t.Fatalf("concurrent default: %+v %v", c, err)
	}
	pending, err = s.Pending(node, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, msg := range pending {
		found = found || msg.ID == m.ID
	}
	if !found {
		t.Fatal("bound first message not eligible")
	}
}
