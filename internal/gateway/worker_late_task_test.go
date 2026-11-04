package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerLateTaskStore(t *testing.T) {
	for _, state := range []string{"running", "offline", "ended"} {
		t.Run(state, func(t *testing.T) {
			s, _ := newTestStore(t)
			node, _, _ := s.CreateNode("test", "linux")
			ownerID, _ := s.UpsertSession(node, owner("test"))
			id, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "worker", Worker: &protocol.WorkerInfo{ID: "w_test", Owner: ownerID}})
			if err != nil {
				t.Fatal(err)
			}
			if state == "offline" {
				if err := s.SetSessionOffline(node, id); err != nil {
					t.Fatal(err)
				}
			}
			if state == "ended" {
				if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: id, Harness: "opencode", Outcome: protocol.OutcomeStopped}); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err = s.StoreMessage(protocol.MsgSend{Sender: ownerID, Recipient: id, Type: protocol.TypeRequest, Body: "[agent-net worker brief] test task"})
			if (err == nil) != (state == "running") {
				t.Fatalf("state %s: error %v", state, err)
			}
		})
	}
}
