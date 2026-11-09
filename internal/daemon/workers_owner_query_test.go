package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The workers panel of a Claude session asks for its own workers every few
// seconds. The gateway must read only that owner's workers for it, not every
// agent on the network: the answer to the gateway is a few hundred bytes per
// worker, not the whole agent list.
func TestWorkersQueryOfOneOwnerDoesNotFetchEveryAgent(t *testing.T) {
	t.Parallel()
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
	res := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "say ok"})
	// Many other sessions on the network: the owner's panel must not read them.
	for i := 0; i < 40; i++ {
		helloPi(t, w.node, fmt.Sprintf("bystander-%d", i), fmt.Sprintf("/other/%d", i), true)
	}

	cliAgents := func(want int64) protocol.RequestStat {
		t.Helper()
		var got protocol.RequestStat
		eventually(t, "the gateway counted the query.agents call", func() bool {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := g.gw.MachineGet(ctx, protocol.MachineGetRequest{Machine: w.node.d.hostName()})
			if err != nil || out.Requests == nil {
				return false
			}
			got = protocol.RequestStat{}
			for _, s := range out.Requests.Since.Top {
				if s.Method == "query.agents" && s.Origin == protocol.ViaCLI {
					got = s
				}
			}
			return got.Calls >= want
		})
		return got
	}
	query := func(q protocol.WorkersQuery) protocol.WorkersResult {
		t.Helper()
		var r protocol.WorkersResult
		json.Unmarshal(w.cli.mustCall("workers", q), &r)
		return r
	}

	before := cliAgents(0)
	owned := query(protocol.WorkersQuery{OwnerClaude: claudeID, Recent: true})
	if len(owned.Workers) != 1 || owned.Workers[0].SessionID != res.SessionID || owned.Workers[0].OwnerName == "" {
		t.Fatalf("the owner's workers: %+v", owned.Workers)
	}
	afterOwned := cliAgents(before.Calls + 1)
	ownedBytes := afterOwned.Bytes - before.Bytes
	query(protocol.WorkersQuery{})
	afterAll := cliAgents(afterOwned.Calls + 1)
	allBytes := afterAll.Bytes - afterOwned.Bytes
	t.Logf("bytes per workers call: owner-filtered %d, full list %d", ownedBytes, allBytes)
	if ownedBytes*5 > allBytes {
		t.Fatalf("an owner-filtered workers query read %d bytes from the gateway, the full list is %d", ownedBytes, allBytes)
	}
}

// The gateway now leaves out the owner's workers that ended long ago. The
// panel must show exactly the same rows as before: the running worker and the
// one that ended in the last two minutes. Compare the workers result from the
// old answer (every worker) with the new one (the recent window only).
func TestWorkersViewKeepsTheSameRowsWhenTheGatewaySkipsOldEndedWorkers(t *testing.T) {
	const claudeID = "11111111-2222-3333-4444-555555555555"
	owner := "s_owner"
	worker := func(sid string, started int64, online bool, status string) protocol.AgentView {
		return protocol.AgentView{SessionID: sid, NodeID: "n1", Hostname: "host", Harness: "opencode", Model: "fake/m1",
			Cwd: "/work", Online: online, Status: status,
			Worker: &protocol.WorkerInfo{ID: sid, Owner: owner, Project: "shop", StartedAt: started}}
	}
	running := worker("s_running", 3, true, "busy")
	recent := worker("s_recent", 2, false, "ended")
	ownerView := protocol.AgentView{SessionID: owner, NodeID: "n1", Hostname: "host", Harness: "claude",
		Online: true, Status: "idle", Profile: protocol.Profile{Name: "owner"}}

	oldAgents := []protocol.AgentView{ownerView, running, recent}
	for i := 0; i < 20; i++ {
		oldAgents = append(oldAgents, worker(fmt.Sprintf("s_old%d", i), int64(i), false, "ended"))
	}
	newAgents := []protocol.AgentView{ownerView, running, recent}

	agents := oldAgents
	d := &Daemon{
		cfg: Config{testGatewayCall: func(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
			if typ != "query.agents" {
				return nil, false, nil
			}
			if q, ok := params.(protocol.QueryAgents); !ok || q.WorkersOwnedBy != owner {
				return nil, false, nil
			}
			out, err := json.Marshal(protocol.AgentsResult{Agents: agents})
			return out, true, err
		}},
		sessions: map[string]*localSession{key("claude", claudeID): {info: protocol.SessionInfo{Harness: "claude", NativeID: claudeID}, sessionID: owner}},
		recent: map[string]recentWorker{"s_recent": {
			view: protocol.WorkerView{SessionID: "s_recent", NodeID: "n1", Hostname: "host", Harness: "opencode", Model: "fake/m1",
				Project: "shop", Path: "/work", Owner: owner, OwnerName: "owner", StartedAt: 2, Status: "complete",
				EndedAt: time.Now().Add(-30 * time.Second).Unix(), EndStatus: "complete"},
			at: time.Now(),
		}},
	}
	query := func() []byte {
		t.Helper()
		res, err := d.workersView(context.Background(), protocol.WorkersQuery{OwnerClaude: claudeID, Recent: true})
		if err != nil {
			t.Fatal(err)
		}
		out, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := query()
	agents = newAgents
	after := query()
	if string(before) != string(after) {
		t.Fatalf("the panel result changed:\nbefore %s\nafter  %s", before, after)
	}
	var res protocol.WorkersResult
	if err := json.Unmarshal(after, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Workers) != 2 || res.Workers[0].SessionID != "s_running" || res.Workers[1].SessionID != "s_recent" {
		t.Fatalf("the panel shows %+v, want the running worker and the one that ended 30 s ago", res.Workers)
	}
	oldJSON, _ := json.Marshal(protocol.AgentsResult{Agents: oldAgents})
	newJSON, _ := json.Marshal(protocol.AgentsResult{Agents: newAgents})
	t.Logf("gateway answer for the panel: before %d bytes, after %d bytes", len(oldJSON), len(newJSON))
}
