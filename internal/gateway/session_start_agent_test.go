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
	raw, _ := json.Marshal(protocol.SessionStartRelay{Owner: owner, SessionStartRequest: protocol.SessionStartRequest{Project: "repo", Harness: "pi", AgentID: "chosen"}})
	_, err = (&Gateway{store: s}).handleSessionRelay(context.Background(), &nodeConn{id: node}, protocol.FrameSessionStart, raw)
	if err == nil || !strings.Contains(err.Error(), "target capability negotiation") {
		t.Fatalf("remote selection: %v", err)
	}
}
