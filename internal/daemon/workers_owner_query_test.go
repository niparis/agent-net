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
