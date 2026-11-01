package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestDebugWorkersQuery(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	repo := newRepo(t, "shop")
	w.register(t, repo)
	owner := w.ownerID()
	const claudeID = "11111111-2222-3333-4444-555555555555"
	w.d.mu.Lock()
	w.d.sessions[key("claude", claudeID)] = &localSession{
		info: protocol.SessionInfo{Harness: "claude", NativeID: claudeID}, sessionID: owner}
	w.d.mu.Unlock()

	res := w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Task: "say ok\nand then more"})
	deadline := time.Now().Add(scaled(60 * time.Second))
	seen := false
	for time.Now().Before(deadline) {
		var r protocol.WorkersResult
		json.Unmarshal(w.cli.mustCall("workers", protocol.WorkersQuery{OwnerClaude: claudeID, Recent: true}), &r)
		if len(r.Workers) == 1 && r.Workers[0].Task == "say ok" {
			seen = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !seen {
		var agents protocol.AgentsResult
		json.Unmarshal(w.cli.mustCall("list", nil), &agents)
		for _, a := range agents.Agents {
			t.Logf("AGENT id=%s online=%v status=%s worker=%+v ownerSession=%s", a.SessionID, a.Online, a.Status, a.Worker, owner)
		}
		var all protocol.WorkersResult
		json.Unmarshal(w.cli.mustCall("workers", protocol.WorkersQuery{}), &all)
		t.Fatalf("worker did not appear; session=%s all=%+v", res.SessionID, all.Workers)
	}
	w.cli.mustCall("worker_stop", protocol.WorkerStopRequest{Session: res.SessionID})
	deadline = time.Now().Add(scaled(60 * time.Second))
	for time.Now().Before(deadline) {
		var r protocol.WorkersResult
		json.Unmarshal(w.cli.mustCall("workers", protocol.WorkersQuery{OwnerClaude: claudeID, Recent: true}), &r)
		if len(r.Workers) == 1 && r.Workers[0].SessionID == res.SessionID && r.Workers[0].EndStatus == protocol.StatusStopped {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	var r protocol.WorkersResult
	json.Unmarshal(w.cli.mustCall("workers", protocol.WorkersQuery{OwnerClaude: claudeID, Recent: true}), &r)
	t.Fatalf("ended worker not recent: %+v", r.Workers)
}
