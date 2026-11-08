package gateway

import (
	"reflect"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The inbox names its askers with one query. The names must be the ones a
// full agent view gives: the profile or session name, else the title.
func TestInboxAskerNamesMatchAgentView(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("box", "linux")
	named, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "named", Name: "Builder"})
	if err != nil {
		t.Fatal(err)
	}
	unnamed, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "unnamed"})
	if err != nil {
		t.Fatal(err)
	}
	names, err := s.agentNames()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{named, unnamed} {
		a, ok, err := s.Agent(id)
		if err != nil || !ok {
			t.Fatalf("agent %s: %v %v", id, ok, err)
		}
		want := a.Profile.Name
		if want == "" {
			want = a.Title
		}
		if names[id] != want {
			t.Fatalf("agentNames[%s] = %q, want %q", id, names[id], want)
		}
	}
	for _, session := range []string{named, "s_gone"} {
		if _, err := s.CreateInboxItem(protocol.InboxItem{Kind: "question", SessionID: session, Text: "q " + session}); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.ListInbox()
	if err != nil || len(items) != 2 {
		t.Fatalf("items: %+v %v", items, err)
	}
	got := map[string]string{}
	for _, it := range items {
		got[it.SessionID] = it.AskerName
	}
	if got[named] != "Builder" || got["s_gone"] != "an agent" {
		t.Fatalf("asker names: %v", got)
	}
}

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
