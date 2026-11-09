package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A Claude session's workers panel asks every 3 seconds. While nothing on the
// network changes, those calls must cost the gateway nothing: the daemon
// answers from the last gateway answer, which stays valid until the gateway
// pushes a change of that owner's rows. Writes of other sessions do not count.
func TestWorkersPanelPollsCostTheGatewayNothingWhileNothingChanges(t *testing.T) {
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
	first := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "say ok"})

	cliCalls := func(method string) int64 {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := g.gw.MachineGet(ctx, protocol.MachineGetRequest{Machine: w.node.d.hostName()})
		if err != nil || out.Requests == nil {
			t.Fatalf("gateway counters: %v", err)
		}
		for _, s := range out.Requests.Since.Top {
			if s.Method == method && s.Origin == protocol.ViaCLI {
				return s.Calls
			}
		}
		return 0
	}
	panel := func() protocol.WorkersResult {
		t.Helper()
		var r protocol.WorkersResult
		if err := json.Unmarshal(w.cli.mustCall("workers", protocol.WorkersQuery{OwnerClaude: claudeID, Recent: true}), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	ids := func(r protocol.WorkersResult) []string {
		var out []string
		for _, v := range r.Workers {
			out = append(out, v.SessionID)
		}
		return out
	}

	// The first call has no cached answer and may ask the gateway once.
	want := panel()
	if len(want.Workers) != 1 || want.Workers[0].SessionID != first.SessionID {
		t.Fatalf("the panel shows %+v", want.Workers)
	}
	wantJSON, _ := json.Marshal(want)
	// Let a change that the first call itself caused (the session record) settle.
	eventually(t, "the panel answer settles", func() bool {
		panel()
		before := cliCalls("query.agents")
		panel()
		return cliCalls("query.agents") == before
	})

	before := cliCalls("query.agents")
	for i := 0; i < 10; i++ {
		// Other sessions on the network write all the time. The agents topic
		// moves with each, and the owner's rows do not: no new gateway call.
		helloPi(t, w.node, fmt.Sprintf("bystander-%d", i), fmt.Sprintf("/other/%d", i), true)
		got, _ := json.Marshal(panel())
		if string(got) != string(wantJSON) {
			t.Fatalf("poll %d changed the rows:\nwant %s\ngot  %s", i, wantJSON, got)
		}
	}
	after := cliCalls("query.agents")
	t.Logf("gateway query.agents calls for 10 polls: before the cache 10, now %d", after-before)
	if after != before {
		t.Fatalf("10 polls made %d gateway query.agents calls, want 0", after-before)
	}

	// A change on the network shows in the next polls: a second worker.
	second := w.mustStart(t, protocol.WorkerStartRequest{Model: "fake/m1", Project: "shop", Harness: "opencode", Task: "say ok again"})
	eventually(t, "the panel shows the new worker", func() bool {
		got := ids(panel())
		return len(got) == 2
	})
	found := false
	for _, id := range ids(panel()) {
		found = found || id == second.SessionID
	}
	if !found {
		t.Fatalf("the panel lacks the new worker %s: %v", second.SessionID, ids(panel()))
	}
}
