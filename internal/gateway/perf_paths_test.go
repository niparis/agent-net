package gateway

import (
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The preview counts running workers with a lean query. It must count the
// same sessions as the full agent list.
func TestOnlineWorkerCountsMatchAgents(t *testing.T) {
	s, _ := newTestStore(t)
	owner, peer := twoSessions(t, s)
	node, err := s.SessionNode(peer)
	if err != nil {
		t.Fatal(err)
	}
	for i, model := range []string{"gpt-5", "gpt-5", "claude-sonnet-5-5"} {
		harness := "opencode"
		if i == 2 {
			harness = "claude"
		}
		if _, err := s.UpsertSession(node, protocol.SessionInfo{
			Harness: harness, NativeID: "w" + string(rune('a'+i)), Name: "worker", Model: model,
			Worker: &protocol.WorkerInfo{ID: "w_" + string(rune('a'+i)), Owner: owner},
		}); err != nil {
			t.Fatal(err)
		}
	}
	agents, err := s.Agents(protocol.QueryAgents{OnlineOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]int{}
	for _, a := range agents {
		if a.Worker == nil {
			continue
		}
		if want[a.NodeID] == nil {
			want[a.NodeID] = map[string]int{}
		}
		want[a.NodeID][protocol.QuotaProviderForStart(a.Harness, a.Model)]++
	}
	got, err := s.onlineWorkerCounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("no online workers in the fixture")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("counts = %v, want %v", got, want)
	}
}
