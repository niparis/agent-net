package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/model"
	"github.com/niparis/agent-net/internal/protocol"
)

// A provider that a live session reported limited is a locked check of the
// gate: the automatic start moves to the next ranked model and the skip
// reason names the provider and the reset.
func TestChooseSkipsAProviderALiveSessionReportedLimited(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	nodeID, _, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSession(nodeID, protocol.SessionInfo{
		Harness:       "codex",
		NativeID:      "limited",
		Model:         "gpt-6.1-terra",
		LimitedUntil:  s.now().Add(time.Hour).Unix(),
		LimitedReason: "ChatGPT usage limit",
	}); err != nil {
		t.Fatal(err)
	}
	node := &protocol.NodeView{
		NodeID:   nodeID,
		Hostname: "box",
		Online:   true,
		Harnesses: []protocol.HarnessInfo{
			{Name: "codex", SignedIn: "yes", Models: []string{"gpt-6.1-terra"}},
			{Name: "opencode", SignedIn: "yes", Models: []string{"deepseek/deepseek-v4.1-flash"}},
		},
	}
	candidates := []protocol.WorkerModelCandidate{
		{Name: "terra", Harness: "codex", Model: "gpt-6.1-terra"},
		{Name: "deepseek", Harness: "opencode", Model: "deepseek/deepseek-v4.1-flash"},
	}
	scores := []protocol.ModelScore{
		{Match: "gpt-6.1-terra", Score: 51},
		{Match: "deepseek-v4.1-flash", Score: 51},
	}
	result, err := g.Choose(ChooseRequest{
		Purpose:    model.PurposeWorkerAutomatic,
		Node:       node,
		Candidates: candidates,
		Difficulty: "hard",
		D:          3.5,
		Scores:     scores,
		Persist:    true,
	})
	if err != nil {
		t.Fatalf("choose: %v", err)
	}
	if result.Choice.Name != "deepseek" {
		t.Fatalf("chose %q, want the provider without a reported limit", result.Choice.Name)
	}
	t.Logf("chosen %s, note: %s", result.Choice.Name, result.Note)
	for _, want := range []string{"terra", "limited until", "ChatGPT usage limit"} {
		if !strings.Contains(result.Note, want) {
			t.Errorf("note %q lacks %q", result.Note, want)
		}
	}
}
