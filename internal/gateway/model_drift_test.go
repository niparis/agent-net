package gateway

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// sessions_find shows the model agent-net asked for next to the one a session
// runs when they differ, for a start the daemon reported and for a worker. A
// later report from a daemon that no longer holds the start keeps the ask.
func TestFindSessionsShowsModelDrift(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("research", "linux")
	if err != nil {
		t.Fatal(err)
	}
	merger := protocol.SessionInfo{Harness: "claude", NativeID: "merger", Cwd: "/w/garden", Model: "claude-fable-5-2", RequestedModel: "claude-sonnet-5-5"}
	if _, err := s.UpsertSession(node, merger); err != nil {
		t.Fatal(err)
	}
	merger.RequestedModel = ""
	if _, err := s.UpsertSession(node, merger); err != nil {
		t.Fatal(err)
	}
	worker := protocol.SessionInfo{Harness: "opencode", NativeID: "worker", Cwd: "/w/garden", Model: "deepseek/deepseek-v4",
		Worker: &protocol.WorkerInfo{ID: "w1", Owner: "owner", Model: "kimi-k3"}}
	if _, err := s.UpsertSession(node, worker); err != nil {
		t.Fatal(err)
	}
	steady := protocol.SessionInfo{Harness: "claude", NativeID: "steady", Cwd: "/w/garden", Model: "claude-opus-5-5", RequestedModel: "opus"}
	if _, err := s.UpsertSession(node, steady); err != nil {
		t.Fatal(err)
	}
	page, err := s.FindSessions(protocol.SessionsFindRequest{Online: "any"})
	if err != nil {
		t.Fatal(err)
	}
	checkOutput(t, "agent_sessions_find", page)
	want := map[string]string{
		"/w/garden claude-fable-5-2":     "asked claude-sonnet-5-5, runs claude-fable-5-2",
		"/w/garden deepseek/deepseek-v4": "asked kimi-k3, runs deepseek/deepseek-v4",
		"/w/garden claude-opus-5-5":      "",
	}
	if len(page.Items) != len(want) {
		t.Fatalf("rows = %+v", page.Items)
	}
	for _, row := range page.Items {
		got, ok := want["/w/garden "+row.Model]
		if !ok || row.ModelDrift != got {
			t.Fatalf("%s model %s: drift %q, want %q", row.ID, row.Model, row.ModelDrift, got)
		}
	}
}
