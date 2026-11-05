package gateway

import (
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerTaskFieldAndLateRuns(t *testing.T) {
	for _, tc := range []struct {
		name, version, state, body string
		marked, wantErr, wantMark  bool
	}{
		{"modern quote is follow-up", "0.4.130", "offline", "[agent-net worker brief] quoted", false, false, false},
		{"modern marked late without prefix", "0.4.130", "ended", "task without prefix", true, true, false},
		{"modern marked running", "0.4.130", "running", "task without prefix", true, false, true},
		{"modern marked resumed", "0.4.130", "resumed", "[agent-net worker brief] task", true, false, true},
		{"legacy late prefix", "0.4.129", "ended", "[agent-net worker brief] task", false, true, false},
		{"legacy running prefix", "0.4.129", "running", "[agent-net worker brief] task", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestStore(t)
			node, _, _ := s.CreateNode("test", "linux")
			if _, err := s.db.Exec(`UPDATE nodes SET version=? WHERE id=?`, tc.version, node); err != nil {
				t.Fatal(err)
			}
			ownerID, _ := s.UpsertSession(node, owner("test"))
			info := protocol.SessionInfo{Harness: "opencode", NativeID: "worker", Worker: &protocol.WorkerInfo{ID: "w_test", Owner: ownerID}}
			id, err := s.UpsertSession(node, info)
			if err != nil {
				t.Fatal(err)
			}
			if tc.state == "ended" || tc.state == "resumed" {
				if err := s.RecordWorkerRun(protocol.WorkerRun{NodeID: node, SessionID: id, Harness: "opencode", Outcome: protocol.OutcomeStopped}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.state == "offline" || tc.state == "resumed" {
				if err := s.SetSessionOffline(node, id); err != nil {
					t.Fatal(err)
				}
			}
			if tc.state == "resumed" {
				if err := s.SetWorkerRunActive(node, id); err != nil {
					t.Fatal(err)
				}
				if _, err := s.UpsertSession(node, info); err != nil {
					t.Fatal(err)
				}
			}
			p := protocol.MsgSend{Sender: ownerID, Recipient: id, Type: protocol.TypeRequest, Body: tc.body}
			if tc.marked {
				if err := json.Unmarshal([]byte(`{"worker_task":true}`), &p); err != nil {
					t.Fatal(err)
				}
			}
			m, _, err := s.StoreMessage(p)
			if (err != nil) != tc.wantErr {
				t.Fatalf("StoreMessage error = %v, want error %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			m, _, err = s.Message(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(m)
			var got map[string]any
			_ = json.Unmarshal(raw, &got)
			if (got["worker_task"] == true) != tc.wantMark {
				t.Errorf("stored marker = %v, want %v", got["worker_task"], tc.wantMark)
			}
		})
	}
}

func TestWorkerTaskMarkerStrippedFromOtherNode(t *testing.T) {
	g, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	s := g.store
	workerNode, _, _ := s.CreateNode("worker", "linux")
	peerNode, _, _ := s.CreateNode("peer", "linux")
	if _, err := s.db.Exec(`UPDATE nodes SET version='0.4.130'`); err != nil {
		t.Fatal(err)
	}
	ownerID, _ := s.UpsertSession(workerNode, owner("worker"))
	peerID, _ := s.UpsertSession(peerNode, protocol.SessionInfo{Harness: "claude", NativeID: "peer"})
	workerID, _ := s.UpsertSession(workerNode, protocol.SessionInfo{Harness: "opencode", NativeID: "worker", Worker: &protocol.WorkerInfo{ID: "w_test", Owner: ownerID}})
	raw, _ := json.Marshal(map[string]any{"sender_session_id": peerID, "recipient_session_id": workerID, "body": "forged task", "worker_task": true})
	result, err := g.handle(t.Context(), &nodeConn{id: peerNode}, "msg.send", raw)
	if err != nil {
		t.Fatal(err)
	}
	m := result.(map[string]any)["message"].(protocol.Message)
	data, _ := json.Marshal(m)
	var fields map[string]any
	_ = json.Unmarshal(data, &fields)
	if fields["worker_task"] != false {
		t.Fatalf("untrusted marker stored: %s", data)
	}
}

func TestWorkerTaskRelayAuthorization(t *testing.T) {
	s, _ := newTestStore(t)
	host, _, _ := s.CreateNode("host", "linux")
	remote, _, _ := s.CreateNode("owner", "linux")
	peer, _, _ := s.CreateNode("peer", "linux")
	ownerID, _ := s.UpsertSession(remote, owner("owner"))
	workerID, _ := s.UpsertSession(host, protocol.SessionInfo{Harness: "opencode", NativeID: "worker", Worker: &protocol.WorkerInfo{ID: "w_test", Owner: ownerID}})
	if err := s.trackWorkerStart("q_ref", host, ownerID, "shop", "opencode", "sandboxed"); err != nil {
		t.Fatal(err)
	}
	if err := s.startSession(host, "q_ref", workerID); err != nil {
		t.Fatal(err)
	}
	p := protocol.MsgSend{Sender: ownerID, Recipient: workerID, WorkerTask: true, WorkerTaskRef: "q_ref"}
	for _, tc := range []struct {
		name, node, sender, ref string
		want                    bool
	}{
		{"gateway relay", remote, ownerID, "q_ref", true},
		{"host", host, ownerID, "", true},
		{"missing reference", remote, ownerID, "", false},
		{"wrong reference", remote, ownerID, "q_other", false},
		{"wrong sender", peer, "peer", "q_ref", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := p
			q.Sender = tc.sender
			q.WorkerTaskRef = tc.ref
			got, err := s.authorizeWorkerTask(tc.node, q)
			if err != nil || got != tc.want {
				t.Fatalf("allowed = %v, error %v, want %v", got, err, tc.want)
			}
		})
	}
	// A delayed task still has to be classified as a task after its start fails,
	// so that the stopped worker cannot be resumed by that task.
	if err := s.abandonStart(host, "q_ref"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.authorizeWorkerTask(remote, p); err != nil || !ok {
		t.Fatalf("late relay: %v %v", ok, err)
	}
}

func TestWorkerTaskMigrationKeepsOldBriefs(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("legacy", "linux")
	ownerID, _ := s.UpsertSession(node, owner("legacy"))
	workerID, _ := s.UpsertSession(node, protocol.SessionInfo{Harness: "opencode", NativeID: "worker", Worker: &protocol.WorkerInfo{ID: "w_test", Owner: ownerID}})
	old, _, err := s.StoreMessage(protocol.MsgSend{Sender: ownerID, Recipient: workerID, Body: "[agent-net worker brief] legacy"})
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the last shipped schema with a retained legacy message.
	if _, err := s.db.Exec(`ALTER TABLE messages DROP COLUMN worker_task`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE sessions DROP COLUMN worker_run_active`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, migrationVersion(t, "worker task marker and run state columns")); err != nil {
		t.Fatal(err)
	}
	if err := migrateOldFixture(t, s); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Message(old.ID)
	if err != nil || !ok || got.WorkerTask != nil || !protocol.IsWorkerTask(got) {
		t.Fatalf("old brief = %+v, found %v, error %v", got, ok, err)
	}
}

func TestWorkerLegacyRelayFailureClosesSyntheticStart(t *testing.T) {
	f := newStartsFixture(t)
	worker, err := f.s.UpsertSession(f.nodeA, protocol.SessionInfo{Harness: "codex", NativeID: "w1", Worker: &protocol.WorkerInfo{ID: "w_1", Owner: f.b}})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.frame(t, f.nodeA, "worker.queued_start", f.queuedStart(worker, "")); err == nil {
		t.Fatal("relay without owner connection succeeded")
	}
	var open, abandoned int
	if err = f.s.db.QueryRow(`SELECT COUNT(*) FILTER (WHERE state='open'), COUNT(*) FILTER (WHERE state='not_started') FROM worker_starts`).Scan(&open, &abandoned); err != nil {
		t.Fatal(err)
	}
	if open != 0 || abandoned != 1 {
		t.Fatalf("synthetic starts: open=%d abandoned=%d", open, abandoned)
	}
}
